package cognito

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestWriteJSON_BrokenWriter(t *testing.T) {
	// exercises the slog.Warn path when json encoding fails due to a broken writer
	writeJSON(newFailWriter(), http.StatusOK, map[string]string{"key": "value"})
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

// mockStore is a minimal store implementation for testing handler error paths.
type mockStore struct {
	createErr error
	getErr    error
	updateErr error
	deleteErr error
	listErr   error
}

func (m *mockStore) CreateUserPool(*UserPoolMetadata) error { return m.createErr }
func (m *mockStore) GetUserPool(string) (*UserPoolMetadata, error) {
	return nil, m.getErr
}
func (m *mockStore) UpdateUserPool(_ string, _ func(*UserPoolMetadata) error) error {
	return m.updateErr
}
func (m *mockStore) DeleteUserPool(string) error { return m.deleteErr }
func (m *mockStore) ListUserPools(int, string) ([]*UserPoolMetadata, string, error) {
	return nil, "", m.listErr
}

func TestNewStorage_MkdirError(t *testing.T) {
	dir := t.TempDir()
	// Place a file where the "cognito" directory must be created, forcing MkdirAll to fail.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cognito"), []byte(""), 0o600))
	_, err := NewStorage(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create cognito pools dir")
}

func TestNewStorage_OpenRootError(t *testing.T) {
	_, err := newStorage(t.TempDir(), func(string) (*os.Root, error) {
		return nil, errors.New("open root failed")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open cognito storage root")
}

func TestStorage_WriteJSON_OpenFileError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	storage.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
		return nil, errors.New("open failed")
	}
	err = storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"})
	require.Error(t, err)
}

func TestStorage_ReadJSON_ReadAllError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	storage.readAll = func(io.Reader) ([]byte, error) {
		return nil, errors.New("read error")
	}
	_, err = storage.GetUserPool("us-east-1_Test12345")
	require.Error(t, err)
}

func TestStorage_ReadJSON_UnmarshalError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	storage.readAll = func(io.Reader) ([]byte, error) {
		return []byte("not json"), nil
	}
	_, err = storage.GetUserPool("us-east-1_Test12345")
	require.Error(t, err)
}

func TestStorage_CreateUserPool_MkdirError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	storage.mkdirFn = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}
	err = storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create pool dir")
}

func TestStorage_UpdateUserPool_FnError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	err = storage.UpdateUserPool("us-east-1_Test12345", func(*UserPoolMetadata) error {
		return errors.New("fn error")
	})
	require.Error(t, err)
}

func TestStorage_DeleteUserPool_RemoveMetaError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	storage.removeFile = func(string) error {
		return errors.New("permission denied")
	}
	err = storage.DeleteUserPool("us-east-1_Test12345")
	require.Error(t, err)
	assert.False(t, errors.Is(err, errUserPoolNotFound))
}

func TestStorage_DeleteUserPool_RemoveDirError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	calls := 0
	realRemove := storage.removeFile
	storage.removeFile = func(name string) error {
		calls++
		if calls == 1 {
			return realRemove(name) // remove meta.json: success
		}
		return errors.New("cannot remove dir")
	}
	err = storage.DeleteUserPool("us-east-1_Test12345")
	require.Error(t, err)
}

func TestStorage_ListUserPools_PoolsDirDeleted(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewStorage(dir)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "cognito", "pools")))
	pools, nextToken, err := storage.ListUserPools(10, "")
	require.NoError(t, err)
	assert.Empty(t, pools)
	assert.Empty(t, nextToken)
}

func TestStorage_ListUserPools_ListDirError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	storage.listDirFn = func(string) ([]os.DirEntry, error) {
		return nil, errors.New("permission denied")
	}
	_, _, err = storage.ListUserPools(10, "")
	require.Error(t, err)
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
