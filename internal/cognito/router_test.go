package cognito

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
