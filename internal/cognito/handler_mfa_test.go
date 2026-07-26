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
	"golang.org/x/crypto/bcrypt"
)

// ── MFA test helpers ────────────────────────────────────────────────────────

// testTOTPSecret is an arbitrary base32 TOTP secret used to build session/user fixtures
// directly (bypassing generateTOTPSecret) in tests that don't exercise the code's own value.
const testTOTPSecret = "JBSWY3DPEHPK3PXP" //nolint:gosec // G101 false positive: test fixture, not a credential

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

func TestAssociateSoftwareToken_RequestValidation(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		errType string
	}{
		{"neither AccessToken nor Session", `{}`, ErrTypeInvalidParameterException},
		{
			"both AccessToken and Session",
			`{"AccessToken":"tok","Session":"some-session-id-that-is-long-enough"}`,
			ErrTypeInvalidParameterException,
		},
		{
			"malformed Session",
			`{"Session":"some-session-id-that-is-long-enough"}`,
			ErrTypeNotAuthorizedException,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ro := newTestRouter(t)
			w := doOp(t, ro, "AssociateSoftwareToken", tt.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, tt.errType)
		})
	}
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

// TestSetUserMFAPreference_ClearsPreferredWhenNoLongerPreferred ensures that a
// request with Enabled: true, PreferredMfa: false clears a previously-set
// SOFTWARE_TOKEN_MFA preference rather than leaving it stale, while TOTP stays enabled.
func TestSetUserMFAPreference_ClearsPreferredWhenNoLongerPreferred(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	enrollTOTP(t, ro, token)

	w := doSetUserMFAPreference(t, ro, token, map[string]any{
		"Enabled": true, "PreferredMfa": true,
	})
	require.Equal(t, http.StatusOK, w.Code)

	w = doSetUserMFAPreference(t, ro, token, map[string]any{
		"Enabled": true, "PreferredMfa": false,
	})
	require.Equal(t, http.StatusOK, w.Code)

	u, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.True(t, u.SoftwareTokenMFAEnabled)
	assert.Empty(t, u.PreferredMfaSetting)
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

func TestSetUserMFAPreference_GetUserPoolStorageError(t *testing.T) {
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
		getErr: errors.New("storage failure"),
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

// TestSoftwareTokenMFA_RespondClientIDMismatch ensures a SOFTWARE_TOKEN_MFA
// session issued for one app client cannot be redeemed by another client in the
// same pool.
func TestSoftwareTokenMFA_RespondClientIDMismatch(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	otherClientID := createClient(t, ro, poolID, "other-client")
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	secret := enableSoftwareTokenMFA(t, ro, token)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	body, _ := json.Marshal(map[string]any{
		"ClientId":      otherClientID,
		"ChallengeName": "SOFTWARE_TOKEN_MFA",
		"Session":       session,
		"ChallengeResponses": map[string]string{
			"USERNAME":                "alice",
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
	session, err := buildSessionToken(key, keyID, "pool-1", "c", "ghost", "SOFTWARE_TOKEN_MFA", nil)
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

func TestSoftwareTokenMFA_RespondMFADisabledSinceChallengeIssued(t *testing.T) {
	key := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(key, keyID, "pool-1", "c", "alice", "SOFTWARE_TOKEN_MFA", nil)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		getUserFn: func(string, string) (*UserMetadata, error) {
			// Disabling MFA preserves the registered authenticator; only the enabled
			// flag changes (see TestSetUserMFAPreference_DisableClearsPreferred).
			return &UserMetadata{ //nolint:gosec // G101 false positive: test fixture, not a credential
				Username:                "alice",
				TOTPSecret:              testTOTPSecret,
				SoftwareTokenMFAEnabled: false,
			}, nil
		},
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "ChallengeName": "SOFTWARE_TOKEN_MFA", "Session": session,
		"ChallengeResponses": map[string]string{
			"USERNAME": "alice", "SOFTWARE_TOKEN_MFA_CODE": "123456",
		},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestSoftwareTokenMFA_RespondGetUserPoolStorageError(t *testing.T) {
	key := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(key, keyID, "pool-1", "c", "alice", "SOFTWARE_TOKEN_MFA", nil)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		getUserFn: func(string, string) (*UserMetadata, error) {
			return &UserMetadata{ //nolint:gosec // G101 false positive: test fixture, not a credential
				Username:                "alice",
				Enabled:                 true,
				TOTPSecret:              testTOTPSecret,
				SoftwareTokenMFAEnabled: false,
			}, nil
		},
		getErr: errors.New("storage failure"),
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

// TestSoftwareTokenMFA_RespondSelfHealFailures covers RespondToAuthChallenge(SOFTWARE_TOKEN_MFA)
// for a user with a verified TOTPSecret whose SoftwareTokenMFAEnabled is false in a pool with
// MfaConfiguration "ON" (see #502): the challenge is allowed, but the self-heal UpdateUser call
// that re-activates SoftwareTokenMFAEnabled can still race a deletion or hit a storage error.
func TestSoftwareTokenMFA_RespondSelfHealFailures(t *testing.T) {
	tests := []struct {
		name          string
		updateUserFn  func(string, string, func(*UserMetadata) error) error
		updateUserErr error
		wantStatus    int
		wantErrType   string
	}{
		{
			name: "self-heal update races user deletion",
			updateUserFn: func(string, string, func(*UserMetadata) error) error {
				return errUserNotFound
			},
			wantStatus:  http.StatusBadRequest,
			wantErrType: ErrTypeUserNotFoundException,
		},
		{
			name:          "self-heal update fails with storage error",
			updateUserErr: errors.New("storage failure"),
			wantStatus:    http.StatusInternalServerError,
			wantErrType:   ErrTypeInternalErrorException,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := testRSAKey(t)
			keyID, _ := generateTokenID()
			session, err := buildSessionToken(
				key, keyID, "pool-1", "c", "alice", "SOFTWARE_TOKEN_MFA", nil,
			)
			require.NoError(t, err)

			ro := &Router{storage: &mockStore{
				getPoolForClient: func(string) (string, error) { return "pool-1", nil },
				getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
					return &poolKeys{KeyID: keyID}, key, nil
				},
				getUserFn: func(string, string) (*UserMetadata, error) {
					return &UserMetadata{ //nolint:gosec // G101 false positive: test fixture, not a credential
						Username:                "alice",
						Enabled:                 true,
						TOTPSecret:              testTOTPSecret,
						SoftwareTokenMFAEnabled: false,
					}, nil
				},
				getUserPoolFn: func(string) (*UserPoolMetadata, error) {
					return &UserPoolMetadata{MfaConfiguration: "ON"}, nil
				},
				updateUserFn:  tt.updateUserFn,
				updateUserErr: tt.updateUserErr,
			}}
			body, _ := json.Marshal(map[string]any{
				"ClientId": "c", "ChallengeName": "SOFTWARE_TOKEN_MFA", "Session": session,
				"ChallengeResponses": map[string]string{
					"USERNAME":                "alice",
					"SOFTWARE_TOKEN_MFA_CODE": currentTOTPCode(t, testTOTPSecret),
				},
			})
			w := doOp(t, ro, "RespondToAuthChallenge", string(body))
			assert.Equal(t, tt.wantStatus, w.Code)
			assertErrType(t, w, tt.wantErrType)
		})
	}
}

// TestSoftwareTokenMFA_RespondUserDisabledSinceChallengeIssued ensures a user disabled
// (AdminDisableUser) between InitiateAuth issuing the SOFTWARE_TOKEN_MFA challenge and
// RespondToAuthChallenge cannot still complete authentication and receive tokens.
func TestSoftwareTokenMFA_RespondUserDisabledSinceChallengeIssued(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	secret := enableSoftwareTokenMFA(t, ro, token)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	disableBody, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "Username": "alice"})
	require.Equal(t, http.StatusOK, doOp(t, ro, "AdminDisableUser", string(disableBody)).Code)

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
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeNotAuthorizedException)
}

func TestSoftwareTokenMFA_RespondGetUserStorageError(t *testing.T) {
	key := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(key, keyID, "pool-1", "c", "alice", "SOFTWARE_TOKEN_MFA", nil)
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

// ── Forced MFA_SETUP enrollment challenge ───────────────────────────────────

// createPoolWithMFA creates a pool with the given MfaConfiguration ("ON"/"OPTIONAL"/"OFF").
func createPoolWithMFA(t *testing.T, ro *Router, name, mfaConfiguration string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"PoolName":         name,
		"MfaConfiguration": mfaConfiguration,
	})
	w := doOp(t, ro, "CreateUserPool", string(body))
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		UserPool struct {
			Id string `json:"Id"`
		} `json:"UserPool"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotEmpty(t, resp.UserPool.Id)
	return resp.UserPool.Id
}

// setupMFARequiredPool creates a pool with MfaConfiguration ON, a client, and a confirmed
// user with no MFA enrolled — the preconditions for a forced MFA_SETUP challenge.
func setupMFARequiredPool(t *testing.T, ro *Router) (clientID string) {
	t.Helper()
	poolID := createPoolWithMFA(t, ro, "mfa-required-pool", "ON")
	clientID = createClient(t, ro, poolID, "mfa-required-client")
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")
	return clientID
}

func TestMFASetup_InitiateAuthReturnsChallenge(t *testing.T) {
	ro := newTestRouter(t)
	clientID := setupMFARequiredPool(t, ro)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "MFA_SETUP", resp["ChallengeName"])
	assert.NotEmpty(t, resp["Session"])
	params := resp["ChallengeParameters"].(map[string]any)
	assert.Equal(t, "alice", params["USER_ID_FOR_SRP"])
	assert.Equal(t, `["SOFTWARE_TOKEN_MFA"]`, params["MFAS_CAN_SETUP"])
	_, hasResult := resp["AuthenticationResult"]
	assert.False(t, hasResult)
}

func TestMFASetup_NotTriggeredWhenOptional(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPoolWithMFA(t, ro, "mfa-optional-pool", "OPTIONAL")
	clientID := createClient(t, ro, poolID, "mfa-optional-client")
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	_, hasResult := resp["AuthenticationResult"]
	assert.True(t, hasResult)
}

func TestMFASetup_NotTriggeredWhenAlreadyEnrolled(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPoolWithMFA(t, ro, "mfa-pool", "OPTIONAL")
	clientID := createClient(t, ro, poolID, "mfa-client")
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	enableSoftwareTokenMFA(t, ro, token)

	// Now flip the pool to require MFA; the already-enrolled user must get
	// SOFTWARE_TOKEN_MFA, not MFA_SETUP.
	body, _ := json.Marshal(map[string]any{
		"UserPoolId":       poolID,
		"MfaConfiguration": "ON",
	})
	wSet := doOp(t, ro, "SetUserPoolMfaConfig", string(body))
	require.Equal(t, http.StatusOK, wSet.Code)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "SOFTWARE_TOKEN_MFA", resp["ChallengeName"])
}

// TestMFASetup_NotForcedForDisabledButRegisteredUser guards against the regression in #502: a
// user who enrolled and then disabled SOFTWARE_TOKEN_MFA while the pool still allowed it
// (permitted per TestSetUserMFAPreference_DisableClearsPreferred) must not be forced through
// MFA_SETUP re-enrollment once the pool later switches to "ON" — their existing verified TOTP
// secret must be reused via a SOFTWARE_TOKEN_MFA challenge instead, and completing it must
// self-heal SoftwareTokenMFAEnabled back to true without generating a new secret.
func TestMFASetup_NotForcedForDisabledButRegisteredUser(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPoolWithMFA(t, ro, "mfa-pool", "OPTIONAL")
	clientID := createClient(t, ro, poolID, "mfa-client")
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	secret := enableSoftwareTokenMFA(t, ro, token)

	w := doSetUserMFAPreference(t, ro, token, map[string]any{"Enabled": false})
	require.Equal(t, http.StatusOK, w.Code)

	body, _ := json.Marshal(map[string]any{
		"UserPoolId":       poolID,
		"MfaConfiguration": "ON",
	})
	wSet := doOp(t, ro, "SetUserPoolMfaConfig", string(body))
	require.Equal(t, http.StatusOK, wSet.Code)

	wInit := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, wInit.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(wInit.Body).Decode(&initResp))
	require.Equal(t, "SOFTWARE_TOKEN_MFA", initResp["ChallengeName"])
	session := initResp["Session"].(string)

	respBody, _ := json.Marshal(map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "SOFTWARE_TOKEN_MFA",
		"Session":       session,
		"ChallengeResponses": map[string]string{
			"USERNAME":                "alice",
			"SOFTWARE_TOKEN_MFA_CODE": currentTOTPCode(t, secret),
		},
	})
	wResp := doOp(t, ro, "RespondToAuthChallenge", string(respBody))
	require.Equal(t, http.StatusOK, wResp.Code)
	var authResp map[string]any
	require.NoError(t, json.NewDecoder(wResp.Body).Decode(&authResp))
	result := authResp["AuthenticationResult"].(map[string]any)
	assert.NotEmpty(t, result["AccessToken"])

	u, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.Equal(t, secret, u.TOTPSecret, "existing verified secret must not be replaced")
	assert.True(
		t,
		u.SoftwareTokenMFAEnabled,
		"successful sign-in must self-heal the disabled preference",
	)
	assert.Equal(t, mfaSettingSoftwareToken, u.PreferredMfaSetting)
}

// TestSetUserMFAPreference_DisableRejectedInMFARequiredPool guards against a regression where
// an already-enrolled user could call SetUserMFAPreference to disable SOFTWARE_TOKEN_MFA in a
// pool with MfaConfiguration "ON", which forced them back through MFA_SETUP on their next
// sign-in even though they still had a registered TOTP secret. Real AWS doesn't let users
// disable an MFA method once the pool enforces MFA (see docs/aws-spec/cognito/initiate_auth.md),
// so the disable attempt itself must be rejected up front.
func TestSetUserMFAPreference_DisableRejectedInMFARequiredPool(t *testing.T) {
	ro := newTestRouter(t)
	clientID := setupMFARequiredPool(t, ro)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	assocBody, _ := json.Marshal(map[string]string{"Session": session})
	w2 := doOp(t, ro, "AssociateSoftwareToken", string(assocBody))
	require.Equal(t, http.StatusOK, w2.Code)
	var assocResp associateSoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&assocResp))

	verifyBody, _ := json.Marshal(map[string]string{
		"Session":  assocResp.Session,
		"UserCode": currentTOTPCode(t, assocResp.SecretCode),
	})
	w3 := doOp(t, ro, "VerifySoftwareToken", string(verifyBody))
	require.Equal(t, http.StatusOK, w3.Code)
	var verifyResp verifySoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w3.Body).Decode(&verifyResp))

	respBody, _ := json.Marshal(map[string]any{
		"ClientId":           clientID,
		"ChallengeName":      "MFA_SETUP",
		"Session":            verifyResp.Session,
		"ChallengeResponses": map[string]string{"USERNAME": "alice"},
	})
	w4 := doOp(t, ro, "RespondToAuthChallenge", string(respBody))
	require.Equal(t, http.StatusOK, w4.Code)
	var authResp map[string]any
	require.NoError(t, json.NewDecoder(w4.Body).Decode(&authResp))
	token := authResp["AuthenticationResult"].(map[string]any)["AccessToken"].(string)

	wDisable := doSetUserMFAPreference(t, ro, token, map[string]any{"Enabled": false})
	assert.Equal(t, http.StatusBadRequest, wDisable.Code)
	assertErrType(t, wDisable, ErrTypeInvalidParameterException)

	poolID, err := ro.storage.GetPoolIDForClient(clientID)
	require.NoError(t, err)
	u, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.True(t, u.SoftwareTokenMFAEnabled)
	assert.NotEmpty(t, u.TOTPSecret)

	// A later sign-in must still present SOFTWARE_TOKEN_MFA, not MFA_SETUP again.
	wInit := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, wInit.Code)
	var laterResp map[string]any
	require.NoError(t, json.NewDecoder(wInit.Body).Decode(&laterResp))
	assert.Equal(t, "SOFTWARE_TOKEN_MFA", laterResp["ChallengeName"])
}

func TestMFASetup_FullFlowSuccess(t *testing.T) {
	ro := newTestRouter(t)
	clientID := setupMFARequiredPool(t, ro)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	require.Equal(t, "MFA_SETUP", initResp["ChallengeName"])
	session := initResp["Session"].(string)

	// AssociateSoftwareToken with the MFA_SETUP session.
	assocBody, _ := json.Marshal(map[string]string{"Session": session})
	w2 := doOp(t, ro, "AssociateSoftwareToken", string(assocBody))
	require.Equal(t, http.StatusOK, w2.Code)
	var assocResp associateSoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&assocResp))
	require.NotEmpty(t, assocResp.SecretCode)
	require.NotEmpty(t, assocResp.Session)

	// VerifySoftwareToken with the session AssociateSoftwareToken returned.
	verifyBody, _ := json.Marshal(map[string]string{
		"Session":  assocResp.Session,
		"UserCode": currentTOTPCode(t, assocResp.SecretCode),
	})
	w3 := doOp(t, ro, "VerifySoftwareToken", string(verifyBody))
	require.Equal(t, http.StatusOK, w3.Code)
	var verifyResp verifySoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w3.Body).Decode(&verifyResp))
	assert.Equal(t, "SUCCESS", verifyResp.Status)
	require.NotEmpty(t, verifyResp.Session)

	// RespondToAuthChallenge with the session VerifySoftwareToken returned completes sign-in.
	respBody, _ := json.Marshal(map[string]any{
		"ClientId":           clientID,
		"ChallengeName":      "MFA_SETUP",
		"Session":            verifyResp.Session,
		"ChallengeResponses": map[string]string{"USERNAME": "alice"},
	})
	w4 := doOp(t, ro, "RespondToAuthChallenge", string(respBody))
	require.Equal(t, http.StatusOK, w4.Code)
	var authResp map[string]any
	require.NoError(t, json.NewDecoder(w4.Body).Decode(&authResp))
	result := authResp["AuthenticationResult"].(map[string]any)
	assert.NotEmpty(t, result["AccessToken"])
	assert.NotEmpty(t, result["RefreshToken"])

	// A later sign-in must present SOFTWARE_TOKEN_MFA, not MFA_SETUP again.
	w5 := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w5.Code)
	var laterResp map[string]any
	require.NoError(t, json.NewDecoder(w5.Body).Decode(&laterResp))
	assert.Equal(t, "SOFTWARE_TOKEN_MFA", laterResp["ChallengeName"])
}

func TestMFASetup_GetUserPoolInternalError(t *testing.T) {
	hash, err := hashPassword("Password123!", bcrypt.MinCost)
	require.NoError(t, err)
	key := testRSAKey(t)
	keyID, _ := generateTokenID()

	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getUserFn: func(string, string) (*UserMetadata, error) {
			return &UserMetadata{
				Username:     "alice",
				Status:       userStatusConfirmed,
				Enabled:      true,
				PasswordHash: hash,
			}, nil
		},
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		getErr: errors.New("boom"),
	}}

	w := doInitAuth(t, ro, "client-1", "alice", "Password123!")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestAssociateSoftwareTokenSession_InvalidJWT(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]string{"Session": "not-a-valid-jwt"})
	w := doOp(t, ro, "AssociateSoftwareToken", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestAssociateSoftwareTokenSession_UnknownPool(t *testing.T) {
	key := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(
		key,
		keyID,
		"pool-does-not-exist",
		"c",
		"alice",
		"MFA_SETUP",
		nil,
	)
	require.NoError(t, err)

	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]string{"Session": session})
	w := doOp(t, ro, "AssociateSoftwareToken", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestAssociateSoftwareTokenSession_WrongChallengeInSession(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	storage := ro.storage.(*Storage)
	insertFCPUser(t, storage, poolID, "charlie", "charlie-sub", "TempPass123!")

	w := doInitAuth(t, ro, clientID, "charlie", "TempPass123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string) // a NEW_PASSWORD_REQUIRED session

	body, _ := json.Marshal(map[string]string{"Session": session})
	w2 := doOp(t, ro, "AssociateSoftwareToken", string(body))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeNotAuthorizedException)
}

func TestAssociateSoftwareTokenSession_UserNotFound(t *testing.T) {
	key := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(key, keyID, "pool-1", "c", "ghost", "MFA_SETUP", nil)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		getUserFn: func(string, string) (*UserMetadata, error) {
			return nil, errUserNotFound
		},
	}}
	body, _ := json.Marshal(map[string]string{"Session": session})
	w := doOp(t, ro, "AssociateSoftwareToken", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestAssociateSoftwareTokenSession_EmptyPoolIDClaim(t *testing.T) {
	key := testRSAKey(t)
	token, err := buildJWT(
		key,
		"kid",
		map[string]any{"challenge": "MFA_SETUP", "username": "alice"},
	)
	require.NoError(t, err)

	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]string{"Session": token})
	w := doOp(t, ro, "AssociateSoftwareToken", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestAssociateSoftwareTokenSession_SignatureMismatch(t *testing.T) {
	ro := newTestRouter(t)
	poolID, _ := setupPool(t, ro)
	storage := ro.storage.(*Storage)
	_, _, err := storage.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)

	foreignKey := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(foreignKey, keyID, poolID, "c", "alice", "MFA_SETUP", nil)
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{"Session": session})
	w := doOp(t, ro, "AssociateSoftwareToken", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestAssociateSoftwareTokenSession_GetUserInternalError(t *testing.T) {
	key := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(key, keyID, "pool-1", "c", "alice", "MFA_SETUP", nil)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		getUserFn: func(string, string) (*UserMetadata, error) {
			return nil, errors.New("boom")
		},
	}}
	body, _ := json.Marshal(map[string]string{"Session": session})
	w := doOp(t, ro, "AssociateSoftwareToken", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestAssociateSoftwareTokenSession_PoolKeysInternalError(t *testing.T) {
	key := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(key, keyID, "pool-1", "c", "alice", "MFA_SETUP", nil)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return nil, nil, errors.New("boom")
		},
	}}
	body, _ := json.Marshal(map[string]string{"Session": session})
	w := doOp(t, ro, "AssociateSoftwareToken", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestVerifySoftwareTokenSession_BothAccessTokenAndSessionRejected(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]string{
		"AccessToken": "tok",
		"Session":     "some-session-id-that-is-long-enough",
		"UserCode":    "123456",
	})
	w := doOp(t, ro, "VerifySoftwareToken", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestVerifySoftwareTokenSession_NeitherAccessTokenNorSessionRejected(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]string{"UserCode": "123456"})
	w := doOp(t, ro, "VerifySoftwareToken", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestVerifySoftwareTokenSession_InvalidSession(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]string{"Session": "not-a-valid-jwt", "UserCode": "123456"})
	w := doOp(t, ro, "VerifySoftwareToken", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestVerifySoftwareTokenSession_GetUserInternalError(t *testing.T) {
	key := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(
		key,
		keyID,
		"pool-1",
		"c",
		"alice",
		"MFA_SETUP",
		map[string]any{"pending_totp_secret": testTOTPSecret},
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		getUserFn: func(string, string) (*UserMetadata, error) {
			return nil, errors.New("boom")
		},
	}}
	body, _ := json.Marshal(map[string]string{"Session": session, "UserCode": "123456"})
	w := doOp(t, ro, "VerifySoftwareToken", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestVerifySoftwareTokenSession_NoPendingSecret(t *testing.T) {
	ro := newTestRouter(t)
	clientID := setupMFARequiredPool(t, ro)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string) // MFA_SETUP session with no pending secret yet

	body, _ := json.Marshal(map[string]string{"Session": session, "UserCode": "123456"})
	w2 := doOp(t, ro, "VerifySoftwareToken", string(body))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeSoftwareTokenMFANotFoundException)
}

func TestVerifySoftwareTokenSession_WrongCode(t *testing.T) {
	ro := newTestRouter(t)
	clientID := setupMFARequiredPool(t, ro)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	assocBody, _ := json.Marshal(map[string]string{"Session": session})
	w2 := doOp(t, ro, "AssociateSoftwareToken", string(assocBody))
	require.Equal(t, http.StatusOK, w2.Code)
	var assocResp associateSoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&assocResp))

	verifyBody, _ := json.Marshal(map[string]string{
		"Session": assocResp.Session, "UserCode": "000000",
	})
	w3 := doOp(t, ro, "VerifySoftwareToken", string(verifyBody))
	assert.Equal(t, http.StatusBadRequest, w3.Code)
	assertErrType(t, w3, ErrTypeCodeMismatchException)
}

func TestVerifySoftwareTokenSession_UserNotFound(t *testing.T) {
	key := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(
		key,
		keyID,
		"pool-1",
		"c",
		"ghost",
		"MFA_SETUP",
		map[string]any{"pending_totp_secret": testTOTPSecret},
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		getUserFn: func(string, string) (*UserMetadata, error) {
			return nil, errUserNotFound
		},
	}}
	body, _ := json.Marshal(map[string]string{"Session": session, "UserCode": "123456"})
	w := doOp(t, ro, "VerifySoftwareToken", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestMFASetupChallenge_PoolKeysInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return nil, nil, errors.New("boom")
		},
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "ChallengeName": "MFA_SETUP", "Session": "irrelevant",
		"ChallengeResponses": map[string]string{"USERNAME": "alice"},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestMFASetupChallenge_GetUserInternalError(t *testing.T) {
	key := testRSAKey(t)
	keyID, _ := generateTokenID()
	session, err := buildSessionToken(
		key,
		keyID,
		"pool-1",
		"c",
		"alice",
		"MFA_SETUP",
		map[string]any{"verified_totp_secret": testTOTPSecret},
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		getUserFn: func(string, string) (*UserMetadata, error) {
			return nil, errors.New("boom")
		},
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "ChallengeName": "MFA_SETUP", "Session": session,
		"ChallengeResponses": map[string]string{"USERNAME": "alice"},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestMFASetupChallenge_RespondUsernameFromSession(t *testing.T) {
	ro := newTestRouter(t)
	clientID := setupMFARequiredPool(t, ro)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	assocBody, _ := json.Marshal(map[string]string{"Session": session})
	w2 := doOp(t, ro, "AssociateSoftwareToken", string(assocBody))
	require.Equal(t, http.StatusOK, w2.Code)
	var assocResp associateSoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&assocResp))

	verifyBody, _ := json.Marshal(map[string]string{
		"Session": assocResp.Session, "UserCode": currentTOTPCode(t, assocResp.SecretCode),
	})
	w3 := doOp(t, ro, "VerifySoftwareToken", string(verifyBody))
	require.Equal(t, http.StatusOK, w3.Code)
	var verifyResp verifySoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w3.Body).Decode(&verifyResp))

	respBody, _ := json.Marshal(map[string]any{
		"ClientId":           clientID,
		"ChallengeName":      "MFA_SETUP",
		"Session":            verifyResp.Session,
		"ChallengeResponses": map[string]string{},
	})
	w4 := doOp(t, ro, "RespondToAuthChallenge", string(respBody))
	assert.Equal(t, http.StatusOK, w4.Code)
}

func TestMFASetupChallenge_MissingVerifiedSecret(t *testing.T) {
	ro := newTestRouter(t)
	clientID := setupMFARequiredPool(t, ro)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	// Skip AssociateSoftwareToken/VerifySoftwareToken: try to complete the challenge
	// with the raw InitiateAuth session, which carries no verified_totp_secret.
	body, _ := json.Marshal(map[string]any{
		"ClientId":           clientID,
		"ChallengeName":      "MFA_SETUP",
		"Session":            session,
		"ChallengeResponses": map[string]string{"USERNAME": "alice"},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeNotAuthorizedException)
}

func TestMFASetupChallenge_InvalidSession(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]any{
		"ClientId":           clientID,
		"ChallengeName":      "MFA_SETUP",
		"Session":            "not-a-valid-jwt",
		"ChallengeResponses": map[string]string{"USERNAME": "alice"},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestMFASetupChallenge_ClientIDMismatch(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPoolWithMFA(t, ro, "mfa-pool", "ON")
	clientID := createClient(t, ro, poolID, "mfa-client")
	otherClientID := createClient(t, ro, poolID, "other-client")
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	assocBody, _ := json.Marshal(map[string]string{"Session": session})
	w2 := doOp(t, ro, "AssociateSoftwareToken", string(assocBody))
	require.Equal(t, http.StatusOK, w2.Code)
	var assocResp associateSoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&assocResp))

	verifyBody, _ := json.Marshal(map[string]string{
		"Session": assocResp.Session, "UserCode": currentTOTPCode(t, assocResp.SecretCode),
	})
	w3 := doOp(t, ro, "VerifySoftwareToken", string(verifyBody))
	require.Equal(t, http.StatusOK, w3.Code)
	var verifyResp verifySoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w3.Body).Decode(&verifyResp))

	respBody, _ := json.Marshal(map[string]any{
		"ClientId":           otherClientID,
		"ChallengeName":      "MFA_SETUP",
		"Session":            verifyResp.Session,
		"ChallengeResponses": map[string]string{"USERNAME": "alice"},
	})
	w4 := doOp(t, ro, "RespondToAuthChallenge", string(respBody))
	assert.Equal(t, http.StatusBadRequest, w4.Code)
	assertErrType(t, w4, ErrTypeNotAuthorizedException)
}

func TestMFASetupChallenge_UsernameMismatch(t *testing.T) {
	ro := newTestRouter(t)
	clientID := setupMFARequiredPool(t, ro)

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	assocBody, _ := json.Marshal(map[string]string{"Session": session})
	w2 := doOp(t, ro, "AssociateSoftwareToken", string(assocBody))
	require.Equal(t, http.StatusOK, w2.Code)
	var assocResp associateSoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&assocResp))

	verifyBody, _ := json.Marshal(map[string]string{
		"Session": assocResp.Session, "UserCode": currentTOTPCode(t, assocResp.SecretCode),
	})
	w3 := doOp(t, ro, "VerifySoftwareToken", string(verifyBody))
	require.Equal(t, http.StatusOK, w3.Code)
	var verifyResp verifySoftwareTokenResponse
	require.NoError(t, json.NewDecoder(w3.Body).Decode(&verifyResp))

	respBody, _ := json.Marshal(map[string]any{
		"ClientId":           clientID,
		"ChallengeName":      "MFA_SETUP",
		"Session":            verifyResp.Session,
		"ChallengeResponses": map[string]string{"USERNAME": "someone-else"},
	})
	w4 := doOp(t, ro, "RespondToAuthChallenge", string(respBody))
	assert.Equal(t, http.StatusBadRequest, w4.Code)
	assertErrType(t, w4, ErrTypeNotAuthorizedException)
}

// TestMFASetupChallenge_UserAndUpdateFailures covers RespondToAuthChallenge(MFA_SETUP)
// failures that surface after the Session has already been validated: the user record
// vanishing or being disabled between challenge issuance and response, and the subsequent
// UpdateUser call racing a deletion or hitting a storage error.
func TestMFASetupChallenge_UserAndUpdateFailures(t *testing.T) {
	tests := []struct {
		name          string
		username      string
		getUserFn     func(string, string) (*UserMetadata, error)
		updateUserFn  func(string, string, func(*UserMetadata) error) error
		updateUserErr error
		wantStatus    int
		wantErrType   string
	}{
		{
			name:     "user deleted after challenge issued",
			username: "ghost",
			getUserFn: func(string, string) (*UserMetadata, error) {
				return nil, errUserNotFound
			},
			wantStatus:  http.StatusBadRequest,
			wantErrType: ErrTypeUserNotFoundException,
		},
		{
			name:     "user disabled since challenge issued",
			username: "alice",
			getUserFn: func(string, string) (*UserMetadata, error) {
				return &UserMetadata{Username: "alice", Enabled: false}, nil
			},
			wantStatus:  http.StatusBadRequest,
			wantErrType: ErrTypeNotAuthorizedException,
		},
		{
			name:     "update races user deletion",
			username: "alice",
			getUserFn: func(string, string) (*UserMetadata, error) {
				return &UserMetadata{Username: "alice", Enabled: true}, nil
			},
			updateUserFn: func(string, string, func(*UserMetadata) error) error {
				return errUserNotFound
			},
			wantStatus:  http.StatusBadRequest,
			wantErrType: ErrTypeUserNotFoundException,
		},
		{
			name:     "update fails with storage error",
			username: "alice",
			getUserFn: func(string, string) (*UserMetadata, error) {
				return &UserMetadata{Username: "alice", Enabled: true}, nil
			},
			updateUserErr: errors.New("storage failure"),
			wantStatus:    http.StatusInternalServerError,
			wantErrType:   ErrTypeInternalErrorException,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := testRSAKey(t)
			keyID, _ := generateTokenID()
			session, err := buildSessionToken(
				key, keyID, "pool-1", "c", tt.username, challengeMFASetup,
				map[string]any{"verified_totp_secret": testTOTPSecret},
			)
			require.NoError(t, err)

			ro := &Router{storage: &mockStore{
				getPoolForClient: func(string) (string, error) { return "pool-1", nil },
				getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
					return &poolKeys{KeyID: keyID}, key, nil
				},
				getUserFn:     tt.getUserFn,
				updateUserFn:  tt.updateUserFn,
				updateUserErr: tt.updateUserErr,
			}}
			body, _ := json.Marshal(map[string]any{
				"ClientId": "c", "ChallengeName": "MFA_SETUP", "Session": session,
				"ChallengeResponses": map[string]string{"USERNAME": tt.username},
			})
			w := doOp(t, ro, "RespondToAuthChallenge", string(body))
			assert.Equal(t, tt.wantStatus, w.Code)
			assertErrType(t, w, tt.wantErrType)
		})
	}
}
