package cognito

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── MFA test helpers ────────────────────────────────────────────────────────

func doAssociateSoftwareToken(
	t *testing.T,
	ro *Router,
	token string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"AccessToken": token})
	return doOp(t, ro, "AssociateSoftwareToken", string(body))
}

func doVerifySoftwareToken(
	t *testing.T,
	ro *Router,
	token, code string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"AccessToken": token, "UserCode": code})
	return doOp(t, ro, "VerifySoftwareToken", string(body))
}

func doSetUserMFAPreference(
	t *testing.T,
	ro *Router,
	token string,
	settings map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()
	req := map[string]any{"AccessToken": token}
	if settings != nil {
		req["SoftwareTokenMfaSettings"] = settings
	}
	body, _ := json.Marshal(req)
	return doOp(t, ro, "SetUserMFAPreference", string(body))
}

func currentTOTPCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totpCodeAt(secret, totpStep(time.Now().Unix()))
	require.NoError(t, err)
	return code
}

// enrollTOTP associates and verifies a TOTP secret for the user behind token, without
// enabling it as a sign-in requirement. Returns the verified secret.
func enrollTOTP(t *testing.T, ro *Router, token string) string {
	t.Helper()
	w := doAssociateSoftwareToken(t, ro, token)
	require.Equal(t, http.StatusOK, w.Code)
	var resp associateSoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotEmpty(t, resp.SecretCode)

	w2 := doVerifySoftwareToken(t, ro, token, currentTOTPCode(t, resp.SecretCode))
	require.Equal(t, http.StatusOK, w2.Code)
	return resp.SecretCode
}

// enableSoftwareTokenMFA enrolls and activates SOFTWARE_TOKEN_MFA as the user's preferred
// sign-in factor. Returns the verified secret.
func enableSoftwareTokenMFA(t *testing.T, ro *Router, token string) string {
	t.Helper()
	secret := enrollTOTP(t, ro, token)
	w := doSetUserMFAPreference(t, ro, token, map[string]any{
		"Enabled": true, "PreferredMfa": true,
	})
	require.Equal(t, http.StatusOK, w.Code)
	return secret
}

// ── AssociateSoftwareToken ──────────────────────────────────────────────────

func TestAssociateSoftwareToken_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")

	w := doAssociateSoftwareToken(t, ro, token)
	require.Equal(t, http.StatusOK, w.Code)
	var resp associateSoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotEmpty(t, resp.SecretCode)

	u, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.Equal(t, resp.SecretCode, u.PendingTOTPSecret)
	assert.Empty(t, u.TOTPSecret)
}

func TestAssociateSoftwareToken_OverwritesPreviousPending(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")

	w1 := doAssociateSoftwareToken(t, ro, token)
	require.Equal(t, http.StatusOK, w1.Code)
	var resp1 associateSoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w1.Body).Decode(&resp1))

	w2 := doAssociateSoftwareToken(t, ro, token)
	require.Equal(t, http.StatusOK, w2.Code)
	var resp2 associateSoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp2))

	assert.NotEqual(t, resp1.SecretCode, resp2.SecretCode)

	u, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.Equal(t, resp2.SecretCode, u.PendingTOTPSecret)
}

func TestAssociateSoftwareToken_MissingAccessToken(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "AssociateSoftwareToken", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestAssociateSoftwareToken_SessionOnlyRejected(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "AssociateSoftwareToken", `{"Session":"some-session-id-that-is-long-enough"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestAssociateSoftwareToken_InvalidJSON(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "AssociateSoftwareToken", `not json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestAssociateSoftwareToken_InvalidToken(t *testing.T) {
	ro := newTestRouter(t)
	w := doAssociateSoftwareToken(t, ro, "not-a-jwt")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestAssociateSoftwareToken_KeysStorageError(t *testing.T) {
	privKey := testRSAKey(t)
	poolID := "us-east-1_FakePool1"
	now := time.Now().Unix()
	token, err := buildJWT(privKey, "kid", map[string]any{
		"sub": "some-sub", "iss": issuerURL(poolID), "token_use": "access",
		"exp": now + 3600, "iat": now,
	})
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return nil, nil, errors.New("storage failure")
		},
	}}
	w := doAssociateSoftwareToken(t, ro, token)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestAssociateSoftwareToken_UserNotFound(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	now := time.Now().Unix()
	token, err := buildJWT(key, "kid", map[string]any{
		"sub": "missing-sub", "iss": issuerURL(poolID), "token_use": "access",
		"exp": now + 3600, "iat": now, "jti": "jti-1",
	})
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		getUserBySubFn: func(string, string) (*UserMetadata, error) {
			return nil, errUserNotFound
		},
	}}
	w := doAssociateSoftwareToken(t, ro, token)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestAssociateSoftwareToken_StorageUpdateError(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key, "kid", poolID, "client-1", user, nil, "", accessTokenExpiry, accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		getUserBySubFn: func(string, string) (*UserMetadata, error) {
			return user, nil
		},
		updateUserErr: errors.New("disk error"),
	}}
	w := doAssociateSoftwareToken(t, ro, token)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestAssociateSoftwareToken_WrongTokenUse(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	now := time.Now().Unix()
	token, err := buildJWT(key, "kid", map[string]any{
		"sub": "some-sub", "iss": issuerURL(poolID), "token_use": "id",
		"exp": now + 3600, "iat": now,
	})
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
	}}
	w := doAssociateSoftwareToken(t, ro, token)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestAssociateSoftwareToken_TokenRevoked(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key, "kid", poolID, "client-1", user, nil, "", accessTokenExpiry, accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		isRevokedFn: func(string, string) (bool, error) { return true, nil },
	}}
	w := doAssociateSoftwareToken(t, ro, token)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestAssociateSoftwareToken_UpdateUserRaceNotFound(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key, "kid", poolID, "client-1", user, nil, "", accessTokenExpiry, accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		getUserBySubFn: func(string, string) (*UserMetadata, error) {
			return user, nil
		},
		updateUserErr: errUserNotFound, // simulates the user being deleted between lookup and update
	}}
	w := doAssociateSoftwareToken(t, ro, token)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

// ── VerifySoftwareToken ──────────────────────────────────────────────────────

func TestVerifySoftwareToken_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")

	assocResp := doAssociateSoftwareToken(t, ro, token)
	require.Equal(t, http.StatusOK, assocResp.Code)
	var assoc associateSoftwareTokenResponse
	require.NoError(t, json.NewDecoder(assocResp.Body).Decode(&assoc))

	w := doVerifySoftwareToken(t, ro, token, currentTOTPCode(t, assoc.SecretCode))
	require.Equal(t, http.StatusOK, w.Code)
	var resp verifySoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "SUCCESS", resp.Status)

	u, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.Equal(t, assoc.SecretCode, u.TOTPSecret)
	assert.Empty(t, u.PendingTOTPSecret)
}

func TestVerifySoftwareToken_ClockSkewTolerated(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")

	assocResp := doAssociateSoftwareToken(t, ro, token)
	var assoc associateSoftwareTokenResponse
	require.NoError(t, json.NewDecoder(assocResp.Body).Decode(&assoc))

	prevStep := totpStep(time.Now().Unix()) - 1
	code, err := totpCodeAt(assoc.SecretCode, prevStep)
	require.NoError(t, err)

	w := doVerifySoftwareToken(t, ro, token, code)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestVerifySoftwareToken_InvalidJSON(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "VerifySoftwareToken", `not json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestVerifySoftwareToken_MissingAccessToken(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "VerifySoftwareToken", `{"UserCode":"123456"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestVerifySoftwareToken_MissingUserCode(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")

	w := doVerifySoftwareToken(t, ro, token, "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestVerifySoftwareToken_NoPendingSecret(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")

	w := doVerifySoftwareToken(t, ro, token, "123456")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeSoftwareTokenMFANotFoundException)
}

func TestVerifySoftwareToken_WrongCode(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")

	require.Equal(t, http.StatusOK, doAssociateSoftwareToken(t, ro, token).Code)

	w := doVerifySoftwareToken(t, ro, token, "000000")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeCodeMismatchException)

	u, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.Empty(t, u.TOTPSecret)
	assert.NotEmpty(t, u.PendingTOTPSecret)
}

func TestVerifySoftwareToken_InvalidToken(t *testing.T) {
	ro := newTestRouter(t)
	w := doVerifySoftwareToken(t, ro, "not-a-jwt", "123456")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestVerifySoftwareToken_UserNotFound(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	now := time.Now().Unix()
	token, err := buildJWT(key, "kid", map[string]any{
		"sub": "missing-sub", "iss": issuerURL(poolID), "token_use": "access",
		"exp": now + 3600, "iat": now, "jti": "jti-1",
	})
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		getUserBySubFn: func(string, string) (*UserMetadata, error) {
			return nil, errUserNotFound
		},
	}}
	w := doVerifySoftwareToken(t, ro, token, "123456")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestVerifySoftwareToken_StorageUpdateError(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key, "kid", poolID, "client-1", user, nil, "", accessTokenExpiry, accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		getUserBySubFn: func(string, string) (*UserMetadata, error) {
			return user, nil
		},
		updateUserErr: errors.New("disk error"),
	}}
	w := doVerifySoftwareToken(t, ro, token, "123456")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestVerifySoftwareToken_KeysStorageError(t *testing.T) {
	privKey := testRSAKey(t)
	poolID := "us-east-1_FakePool1"
	now := time.Now().Unix()
	token, err := buildJWT(privKey, "kid", map[string]any{
		"sub": "some-sub", "iss": issuerURL(poolID), "token_use": "access",
		"exp": now + 3600, "iat": now,
	})
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return nil, nil, errors.New("storage failure")
		},
	}}
	w := doVerifySoftwareToken(t, ro, token, "123456")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestVerifySoftwareToken_WrongTokenUse(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	now := time.Now().Unix()
	token, err := buildJWT(key, "kid", map[string]any{
		"sub": "some-sub", "iss": issuerURL(poolID), "token_use": "id",
		"exp": now + 3600, "iat": now,
	})
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
	}}
	w := doVerifySoftwareToken(t, ro, token, "123456")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestVerifySoftwareToken_TokenRevoked(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key, "kid", poolID, "client-1", user, nil, "", accessTokenExpiry, accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		isRevokedFn: func(string, string) (bool, error) { return true, nil },
	}}
	w := doVerifySoftwareToken(t, ro, token, "123456")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestVerifySoftwareToken_UpdateUserRaceNotFound(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key, "kid", poolID, "client-1", user, nil, "", accessTokenExpiry, accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		getUserBySubFn: func(string, string) (*UserMetadata, error) {
			return user, nil
		},
		updateUserErr: errUserNotFound,
	}}
	w := doVerifySoftwareToken(t, ro, token, "123456")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

// ── SetUserMFAPreference ─────────────────────────────────────────────────────

func TestSetUserMFAPreference_EnableAfterVerify(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	enrollTOTP(t, ro, token)

	w := doSetUserMFAPreference(t, ro, token, map[string]any{
		"Enabled": true, "PreferredMfa": true,
	})
	require.Equal(t, http.StatusOK, w.Code)

	u, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.True(t, u.SoftwareTokenMFAEnabled)
	assert.Equal(t, "SOFTWARE_TOKEN_MFA", u.PreferredMfaSetting)
}

func TestSetUserMFAPreference_EnableWithoutVerifiedTOTP(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")

	w := doSetUserMFAPreference(t, ro, token, map[string]any{"Enabled": true})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)

	u, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.False(t, u.SoftwareTokenMFAEnabled)
}

func TestSetUserMFAPreference_DisableClearsPreferred(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	enableSoftwareTokenMFA(t, ro, token)

	w := doSetUserMFAPreference(t, ro, token, map[string]any{"Enabled": false})
	require.Equal(t, http.StatusOK, w.Code)

	u, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.False(t, u.SoftwareTokenMFAEnabled)
	assert.Empty(t, u.PreferredMfaSetting)
	// Disabling does not forget the registered authenticator.
	assert.NotEmpty(t, u.TOTPSecret)
}

func TestSetUserMFAPreference_NilSettingsIsNoOp(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")

	w := doSetUserMFAPreference(t, ro, token, nil)
	require.Equal(t, http.StatusOK, w.Code)

	u, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.False(t, u.SoftwareTokenMFAEnabled)
}

func TestSetUserMFAPreference_MissingAccessToken(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "SetUserMFAPreference", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestSetUserMFAPreference_InvalidJSON(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "SetUserMFAPreference", `not json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestSetUserMFAPreference_InvalidToken(t *testing.T) {
	ro := newTestRouter(t)
	w := doSetUserMFAPreference(t, ro, "not-a-jwt", map[string]any{"Enabled": true})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestSetUserMFAPreference_UserNotFound(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	now := time.Now().Unix()
	token, err := buildJWT(key, "kid", map[string]any{
		"sub": "missing-sub", "iss": issuerURL(poolID), "token_use": "access",
		"exp": now + 3600, "iat": now, "jti": "jti-1",
	})
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		getUserBySubFn: func(string, string) (*UserMetadata, error) {
			return nil, errUserNotFound
		},
	}}
	w := doSetUserMFAPreference(t, ro, token, map[string]any{"Enabled": false})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestSetUserMFAPreference_StorageUpdateError(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key, "kid", poolID, "client-1", user, nil, "", accessTokenExpiry, accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		getUserBySubFn: func(string, string) (*UserMetadata, error) {
			return user, nil
		},
		updateUserErr: errors.New("disk error"),
	}}
	w := doSetUserMFAPreference(t, ro, token, map[string]any{"Enabled": false})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestSetUserMFAPreference_KeysStorageError(t *testing.T) {
	privKey := testRSAKey(t)
	poolID := "us-east-1_FakePool1"
	now := time.Now().Unix()
	token, err := buildJWT(privKey, "kid", map[string]any{
		"sub": "some-sub", "iss": issuerURL(poolID), "token_use": "access",
		"exp": now + 3600, "iat": now,
	})
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return nil, nil, errors.New("storage failure")
		},
	}}
	w := doSetUserMFAPreference(t, ro, token, map[string]any{"Enabled": false})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestSetUserMFAPreference_WrongTokenUse(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	now := time.Now().Unix()
	token, err := buildJWT(key, "kid", map[string]any{
		"sub": "some-sub", "iss": issuerURL(poolID), "token_use": "id",
		"exp": now + 3600, "iat": now,
	})
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
	}}
	w := doSetUserMFAPreference(t, ro, token, map[string]any{"Enabled": false})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestSetUserMFAPreference_TokenRevoked(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key, "kid", poolID, "client-1", user, nil, "", accessTokenExpiry, accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		isRevokedFn: func(string, string) (bool, error) { return true, nil },
	}}
	w := doSetUserMFAPreference(t, ro, token, map[string]any{"Enabled": false})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestSetUserMFAPreference_UpdateUserRaceNotFound(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key, "kid", poolID, "client-1", user, nil, "", accessTokenExpiry, accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		getUserBySubFn: func(string, string) (*UserMetadata, error) {
			return user, nil
		},
		updateUserErr: errUserNotFound,
	}}
	w := doSetUserMFAPreference(t, ro, token, map[string]any{"Enabled": false})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

// ── RespondToAuthChallenge: SOFTWARE_TOKEN_MFA ──────────────────────────────

func TestSoftwareTokenMFA_UserPasswordAuth_ChallengeIssued(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	secret := enableSoftwareTokenMFA(t, ro, token)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "SOFTWARE_TOKEN_MFA", resp["ChallengeName"])
	assert.NotEmpty(t, resp["Session"])
	_, hasResult := resp["AuthenticationResult"]
	assert.False(t, hasResult)

	// The secret must still be usable for the follow-up challenge.
	assert.NotEmpty(t, secret)
}

func TestSoftwareTokenMFA_RespondSuccess(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	secret := enableSoftwareTokenMFA(t, ro, token)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	body, _ := json.Marshal(map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "SOFTWARE_TOKEN_MFA",
		"Session":       session,
		"ChallengeResponses": map[string]string{
			"USERNAME":                "alice",
			"SOFTWARE_TOKEN_MFA_CODE": currentTOTPCode(t, secret),
		},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(body))
	require.Equal(t, http.StatusOK, w2.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	result := resp["AuthenticationResult"].(map[string]any)
	assert.NotEmpty(t, result["AccessToken"])
	assert.NotEmpty(t, result["RefreshToken"])
}

func TestSoftwareTokenMFA_RespondUsernameFromSession(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	secret := enableSoftwareTokenMFA(t, ro, token)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	body, _ := json.Marshal(map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "SOFTWARE_TOKEN_MFA",
		"Session":       session,
		"ChallengeResponses": map[string]string{
			"SOFTWARE_TOKEN_MFA_CODE": currentTOTPCode(t, secret),
		},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusOK, w2.Code)
}

func TestSoftwareTokenMFA_RespondUsernameMismatch(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	secret := enableSoftwareTokenMFA(t, ro, token)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	body, _ := json.Marshal(map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "SOFTWARE_TOKEN_MFA",
		"Session":       session,
		"ChallengeResponses": map[string]string{
			"USERNAME":                "someone-else",
			"SOFTWARE_TOKEN_MFA_CODE": currentTOTPCode(t, secret),
		},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeNotAuthorizedException)
}

func TestSoftwareTokenMFA_RespondMissingCode(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	enableSoftwareTokenMFA(t, ro, token)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	body, _ := json.Marshal(map[string]any{
		"ClientId":           clientID,
		"ChallengeName":      "SOFTWARE_TOKEN_MFA",
		"Session":            session,
		"ChallengeResponses": map[string]string{"USERNAME": "alice"},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeInvalidParameterException)
}

func TestSoftwareTokenMFA_RespondWrongCode(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	enableSoftwareTokenMFA(t, ro, token)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	body, _ := json.Marshal(map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "SOFTWARE_TOKEN_MFA",
		"Session":       session,
		"ChallengeResponses": map[string]string{
			"USERNAME": "alice", "SOFTWARE_TOKEN_MFA_CODE": "000000",
		},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeCodeMismatchException)
}

func TestSoftwareTokenMFA_RespondWrongChallengeInSession(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	storage := ro.storage.(*Storage)
	insertFCPUser(t, storage, poolID, "charlie", "charlie-sub", "TempPass123!")

	w := doInitAuth(t, ro, clientID, "charlie", "TempPass123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string) // a NEW_PASSWORD_REQUIRED session, not SOFTWARE_TOKEN_MFA

	body, _ := json.Marshal(map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "SOFTWARE_TOKEN_MFA",
		"Session":       session,
		"ChallengeResponses": map[string]string{
			"USERNAME": "charlie", "SOFTWARE_TOKEN_MFA_CODE": "123456",
		},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeNotAuthorizedException)
}

func TestSoftwareTokenMFA_RespondInvalidSession(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "SOFTWARE_TOKEN_MFA",
		"Session":       "not-a-valid-jwt",
		"ChallengeResponses": map[string]string{
			"USERNAME": "alice", "SOFTWARE_TOKEN_MFA_CODE": "123456",
		},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestSoftwareTokenMFA_RespondUserNotFound(t *testing.T) {
	key := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(key, keyID, "pool-1", "ghost", "SOFTWARE_TOKEN_MFA", nil)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		getUserFn: func(string, string) (*UserMetadata, error) {
			return nil, errUserNotFound
		},
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "ChallengeName": "SOFTWARE_TOKEN_MFA", "Session": session,
		"ChallengeResponses": map[string]string{
			"USERNAME": "ghost", "SOFTWARE_TOKEN_MFA_CODE": "123456",
		},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestSoftwareTokenMFA_RespondGetUserStorageError(t *testing.T) {
	key := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(key, keyID, "pool-1", "alice", "SOFTWARE_TOKEN_MFA", nil)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		getUserFn: func(string, string) (*UserMetadata, error) {
			return nil, errors.New("disk error")
		},
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "ChallengeName": "SOFTWARE_TOKEN_MFA", "Session": session,
		"ChallengeResponses": map[string]string{
			"USERNAME": "alice", "SOFTWARE_TOKEN_MFA_CODE": "123456",
		},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestSoftwareTokenMFA_GetPoolKeysStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return nil, nil, errors.New("disk error")
		},
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "ChallengeName": "SOFTWARE_TOKEN_MFA", "Session": "s",
		"ChallengeResponses": map[string]string{
			"USERNAME": "u", "SOFTWARE_TOKEN_MFA_CODE": "123456",
		},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestSoftwareTokenMFA_SRPPasswordVerifier_ChallengeIssued(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	enableSoftwareTokenMFA(t, ro, token)

	w := srpLogin(t, ro, poolID, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "SOFTWARE_TOKEN_MFA", resp["ChallengeName"])
	_, hasResult := resp["AuthenticationResult"]
	assert.False(t, hasResult)
}

func TestSoftwareTokenMFA_NewPasswordRequired_ChainsIntoChallenge(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	enableSoftwareTokenMFA(t, ro, token)

	// Force alice back into FORCE_CHANGE_PASSWORD while keeping SOFTWARE_TOKEN_MFA enabled.
	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "Username": "alice", "Password": "TempPass123!", "Permanent": false,
	})
	require.Equal(t, http.StatusOK, doOp(t, ro, "AdminSetUserPassword", string(body)).Code)

	w := doInitAuth(t, ro, clientID, "alice", "TempPass123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	require.Equal(t, "NEW_PASSWORD_REQUIRED", initResp["ChallengeName"])
	session := initResp["Session"].(string)

	npBody, _ := json.Marshal(map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "NEW_PASSWORD_REQUIRED",
		"Session":       session,
		"ChallengeResponses": map[string]string{
			"USERNAME": "alice", "NEW_PASSWORD": "NewSecurePass123!",
		},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(npBody))
	require.Equal(t, http.StatusOK, w2.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	assert.Equal(t, "SOFTWARE_TOKEN_MFA", resp["ChallengeName"])
	_, hasResult := resp["AuthenticationResult"]
	assert.False(t, hasResult)
}

func TestSoftwareTokenMFA_RefreshTokenAuthBypassesChallenge(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	secret := enableSoftwareTokenMFA(t, ro, token)

	// Complete the SOFTWARE_TOKEN_MFA challenge once to obtain a refresh token.
	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	respBody, _ := json.Marshal(map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "SOFTWARE_TOKEN_MFA",
		"Session":       session,
		"ChallengeResponses": map[string]string{
			"USERNAME": "alice", "SOFTWARE_TOKEN_MFA_CODE": currentTOTPCode(t, secret),
		},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(respBody))
	require.Equal(t, http.StatusOK, w2.Code)
	var authResp map[string]any
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&authResp))
	refreshToken := authResp["AuthenticationResult"].(map[string]any)["RefreshToken"].(string)

	// Refreshing must not re-trigger the MFA challenge.
	refreshBody, _ := json.Marshal(map[string]any{
		"ClientId": clientID,
		"AuthFlow": "REFRESH_TOKEN_AUTH",
		"AuthParameters": map[string]string{
			"REFRESH_TOKEN": refreshToken,
		},
	})
	w3 := doOp(t, ro, "InitiateAuth", string(refreshBody))
	require.Equal(t, http.StatusOK, w3.Code)
	var refreshResp map[string]any
	require.NoError(t, json.NewDecoder(w3.Body).Decode(&refreshResp))
	result, hasResult := refreshResp["AuthenticationResult"].(map[string]any)
	require.True(t, hasResult)
	assert.NotEmpty(t, result["AccessToken"])
}
