package cognito

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failWriter is an http.ResponseWriter whose Write always returns an error.
type failWriter struct{ header http.Header }

func newFailWriter() *failWriter                { return &failWriter{header: http.Header{}} }
func (f *failWriter) Header() http.Header       { return f.header }
func (f *failWriter) WriteHeader(int)           {}
func (f *failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func newTestRouter(t *testing.T) *Router {
	t.Helper()
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	return NewRouter(storage)
}

func TestRouter_UnknownOperation(t *testing.T) {
	tests := []struct {
		name   string
		target string
		wantOp string
	}{
		{
			name:   "unrecognized operation",
			target: "AWSCognitoIdentityProviderService.DoesNotExist",
			wantOp: "DoesNotExist",
		},
		{
			name:   "empty target header",
			target: "",
			wantOp: "",
		},
		{
			name:   "wrong service prefix",
			target: "DynamoDB_20120810.ListTables",
			wantOp: "DynamoDB_20120810.ListTables",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ro := newTestRouter(t)
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
			if tt.target != "" {
				req.Header.Set("X-Amz-Target", tt.target)
			}
			w := httptest.NewRecorder()

			ro.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, "application/x-amz-json-1.1", w.Header().Get("Content-Type"))

			var resp errResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, ErrTypeUnknownOperationException, resp.Type)
			assert.Contains(t, resp.Message, tt.wantOp)
		})
	}
}

func TestResponseRecorder_WriteHeaderIdempotent(t *testing.T) {
	w := httptest.NewRecorder()
	rec := newResponseRecorder(w)
	rec.WriteHeader(http.StatusOK)
	rec.WriteHeader(http.StatusBadRequest) // second call must be ignored
	assert.Equal(t, http.StatusOK, rec.status)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestResponseRecorder_Flush(t *testing.T) {
	w := httptest.NewRecorder()
	rec := newResponseRecorder(w)
	rec.Flush()
	assert.True(t, w.Flushed)
}

func TestEmitRequestLog_5xx(t *testing.T) {
	w := httptest.NewRecorder()
	rec := newResponseRecorder(w)
	rec.WriteHeader(http.StatusInternalServerError)
	rec.errCode = ErrTypeInternalErrorException
	rec.errMsg = "something went wrong"
	emitRequestLog("SomeOp", rec, time.Millisecond) // exercises status>=500 logging branch
}

func TestWriteError_BrokenWriter(t *testing.T) {
	// exercises the slog.Warn path when json encoding fails due to a broken writer
	writeError(newFailWriter(), http.StatusBadRequest, ErrTypeInvalidParameterException, "test")
}

func TestRouter_ReadBodyError(t *testing.T) {
	ro := newTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/", iotest.ErrReader(errors.New("read error")))
	req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService.InitiateAuth")
	w := httptest.NewRecorder()

	ro.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestStorage_Close(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	assert.NoError(t, storage.Close())
}

func TestRouter_ErrorResponseFormat(t *testing.T) {
	ro := newTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService.UnknownOp")
	w := httptest.NewRecorder()

	ro.ServeHTTP(w, req)

	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Contains(t, body, "__type", "error response must contain __type field")
	assert.Contains(t, body, "message", "error response must contain message field")
	_, hasCode := body["code"]
	assert.False(t, hasCode, "Cognito errors use __type, not code")
}
