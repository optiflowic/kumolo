package sts

import (
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
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

func stsRequest(t *testing.T, action string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/?Action="+action+"&Version=2011-06-15", nil)
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, req)
	return w
}

func TestHandleGetCallerIdentity(t *testing.T) {
	w := stsRequest(t, "GetCallerIdentity")
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
	w := stsRequest(t, "AssumeRole")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp assumeRoleResponse
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &resp))
	creds := resp.AssumeRoleResult.Credentials
	assert.Equal(t, fixedAccessKeyID, creds.AccessKeyID)
	assert.Equal(t, fixedSecretKey, creds.SecretAccessKey)
	assert.Equal(t, fixedSessionToken, creds.SessionToken)
	assert.Equal(t, fixedExpiration, creds.Expiration)
	assert.Equal(t, fixedRoleARN, resp.AssumeRoleResult.AssumedRoleUser.Arn)
	assert.Equal(t, fixedRoleID, resp.AssumeRoleResult.AssumedRoleUser.AssumedRoleID)
}

func TestHandleGetSessionToken(t *testing.T) {
	w := stsRequest(t, "GetSessionToken")
	assert.Equal(t, http.StatusOK, w.Code)

	var resp getSessionTokenResponse
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &resp))
	creds := resp.GetSessionTokenResult.Credentials
	assert.Equal(t, fixedAccessKeyID, creds.AccessKeyID)
	assert.Equal(t, fixedSecretKey, creds.SecretAccessKey)
	assert.Equal(t, fixedSessionToken, creds.SessionToken)
	assert.Equal(t, fixedExpiration, creds.Expiration)
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
	w := stsRequest(t, "UnknownAction")
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errorResponse
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Sender", resp.Error.Type)
	assert.Equal(t, "InvalidAction", resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "UnknownAction")
}
