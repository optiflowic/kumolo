package sts

import (
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failWriter is an http.ResponseWriter whose Write fails after failAfter successful calls.
type failWriter struct {
	header     http.Header
	failAfter  int
	writeCount int
}

func newFailWriter(failAfter int) *failWriter {
	return &failWriter{header: http.Header{}, failAfter: failAfter}
}

func (w *failWriter) Header() http.Header { return w.header }
func (w *failWriter) WriteHeader(int)     {}
func (w *failWriter) Write(b []byte) (int, error) {
	w.writeCount++
	if w.writeCount > w.failAfter {
		return 0, errors.New("write error")
	}
	return len(b), nil
}

func stsRequest(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, req)
	return w
}

func stsAction(t *testing.T, action string) *httptest.ResponseRecorder {
	t.Helper()
	return stsRequest(t, "Action="+action+"&Version=2011-06-15")
}

func TestHandleGetCallerIdentity(t *testing.T) {
	w := stsAction(t, "GetCallerIdentity")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/xml")

	var resp getCallerIdentityResponse
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, fixedAccount, resp.GetCallerIdentityResult.Account)
	assert.Equal(t, fixedARN, resp.GetCallerIdentityResult.Arn)
	assert.Equal(t, fixedUserID, resp.GetCallerIdentityResult.UserID)
	assert.Equal(t, requestID, resp.ResponseMetadata.RequestID)
}

func TestHandleAssumeRole(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantARN     string
		wantRoleID  string
		wantErrCode string
	}{
		{
			name:       "valid request",
			body:       "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role&RoleSessionName=my-session",
			wantStatus: http.StatusOK,
			wantARN:    "arn:aws:sts::000000000000:assumed-role/my-role/my-session",
			wantRoleID: fixedRoleIDPrefix + ":my-session",
		},
		{
			name:        "missing RoleArn",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleSessionName=my-session",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "missing RoleSessionName",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := stsRequest(t, tt.body)
			assert.Equal(t, tt.wantStatus, w.Code)
			if tt.wantErrCode != "" {
				var errResp errorResponse
				require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &errResp))
				assert.Equal(t, tt.wantErrCode, errResp.Error.Code)
				return
			}
			var resp assumeRoleResponse
			require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &resp))
			creds := resp.AssumeRoleResult.Credentials
			assert.Equal(t, fixedAccessKeyID, creds.AccessKeyID)
			assert.Equal(t, fixedSecretKey, creds.SecretAccessKey)
			assert.Equal(t, fixedSessionToken, creds.SessionToken)
			assert.Equal(t, fixedExpiration, creds.Expiration)
			assert.Equal(t, tt.wantARN, resp.AssumeRoleResult.AssumedRoleUser.Arn)
			assert.Equal(t, tt.wantRoleID, resp.AssumeRoleResult.AssumedRoleUser.AssumedRoleID)
		})
	}
}

func TestHandleGetSessionToken(t *testing.T) {
	w := stsAction(t, "GetSessionToken")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp getSessionTokenResponse
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &resp))
	creds := resp.GetSessionTokenResult.Credentials
	assert.Equal(t, fixedAccessKeyID, creds.AccessKeyID)
	assert.Equal(t, fixedSecretKey, creds.SecretAccessKey)
	assert.Equal(t, fixedSessionToken, creds.SessionToken)
	assert.Equal(t, fixedExpiration, creds.Expiration)
}

func TestParseFormError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("%invalid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWriteXMLErrors(t *testing.T) {
	t.Run("write header fails", func(t *testing.T) {
		writeXML(newFailWriter(0), http.StatusOK, getCallerIdentityResponse{})
	})
	t.Run("encode fails", func(t *testing.T) {
		writeXML(newFailWriter(1), http.StatusOK, getCallerIdentityResponse{})
	})
}

func TestUnknownAction(t *testing.T) {
	w := stsAction(t, "UnknownAction")
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errorResponse
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Sender", resp.Error.Type)
	assert.Equal(t, "InvalidAction", resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "UnknownAction")
}
