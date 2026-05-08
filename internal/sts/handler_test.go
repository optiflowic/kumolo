package sts

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestUnknownAction(t *testing.T) {
	w := stsRequest(t, "UnknownAction")
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errorResponse
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "InvalidAction", resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "UnknownAction")
}
