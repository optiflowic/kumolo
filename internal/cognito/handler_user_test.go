package cognito

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// doAuth signs up, confirms, and authenticates a user, returning the access token.
func doAuth(t *testing.T, ro *Router, clientID, username, password string) string {
	t.Helper()
	signUpUser(t, ro, clientID, username, password)
	confirmUser(t, ro, clientID, username)
	w := doInitAuth(t, ro, clientID, username, password)
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		AuthenticationResult struct {
			AccessToken string `json:"AccessToken"`
		} `json:"AuthenticationResult"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotEmpty(t, resp.AuthenticationResult.AccessToken)
	return resp.AuthenticationResult.AccessToken
}

// doGetUserDirect calls handleGetUser directly on a router, bypassing ServeHTTP.
func doGetUserDirect(t *testing.T, ro *Router, accessToken string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"AccessToken": accessToken})
	w := httptest.NewRecorder()
	ro.handleGetUser(w, body)
	return w
}

// ── GetUser ───────────────────────────────────────────────────────────────────

func TestGetUser_Success(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")

	w := doGetUserDirect(t, ro, token)

	require.Equal(t, http.StatusOK, w.Code)
	var resp getUserResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "alice", resp.Username)
	require.NotEmpty(t, resp.UserAttributes)
	assert.Equal(t, "sub", resp.UserAttributes[0].Name)
	assert.NotEmpty(t, resp.UserAttributes[0].Value)

	var email string
	for _, a := range resp.UserAttributes {
		if a.Name == "email" {
			email = a.Value
		}
	}
	assert.Equal(t, "alice@example.com", email)
}

func TestGetUser_MissingAccessToken(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "GetUser", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestGetUser_InvalidJSON(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "GetUser", `{invalid}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestGetUser_MalformedJWT_NotThreeParts(t *testing.T) {
	ro := newTestRouter(t)
	w := doGetUserDirect(t, ro, "only.two")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestGetUser_MalformedJWT_InvalidBase64(t *testing.T) {
	ro := newTestRouter(t)
	// payload part is not valid base64url
	w := doGetUserDirect(t, ro, "header.!!!notbase64!!!.sig")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestGetUser_MalformedJWT_PayloadNotJSON(t *testing.T) {
	ro := newTestRouter(t)
	// payload decodes to "not json"
	payload := b64url([]byte("not json"))
	w := doGetUserDirect(t, ro, "header."+payload+".sig")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestGetUser_NoIssInToken(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)

	poolID, err := ro.storage.GetPoolIDForClient(clientID)
	require.NoError(t, err)
	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)

	now := time.Now().Unix()
	token, err := buildJWT(privateKey, keys.KeyID, map[string]any{
		"sub":       "some-sub",
		"token_use": "access",
		"exp":       now + 3600,
		"iat":       now,
		// iss is intentionally omitted
	})
	require.NoError(t, err)

	w := doGetUserDirect(t, ro, token)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestGetUser_InvalidSignature(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")

	// Tamper the signature segment.
	dot := strings.LastIndexByte(token, '.')
	tampered := token[:dot+1] + "invalidsignature"

	w := doGetUserDirect(t, ro, tampered)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestGetUser_ExpiredToken(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	poolID, err := ro.storage.GetPoolIDForClient(clientID)
	require.NoError(t, err)
	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)
	user, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)

	now := time.Now().Unix()
	token, err := buildJWT(privateKey, keys.KeyID, map[string]any{
		"sub":       user.Sub,
		"iss":       issuerURL(poolID),
		"token_use": "access",
		"exp":       now - 1, // already expired
		"iat":       now - 3601,
	})
	require.NoError(t, err)

	w := doGetUserDirect(t, ro, token)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestGetUser_ExpiredToken_ExactNow(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	poolID, err := ro.storage.GetPoolIDForClient(clientID)
	require.NoError(t, err)
	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)
	user, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)

	now := time.Now().Unix()
	token, err := buildJWT(privateKey, keys.KeyID, map[string]any{
		"sub":       user.Sub,
		"iss":       issuerURL(poolID),
		"token_use": "access",
		"exp":       now, // exactly now — must be treated as expired
		"iat":       now - 3600,
	})
	require.NoError(t, err)

	w := doGetUserDirect(t, ro, token)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestGetUser_WrongTokenUse(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	poolID, err := ro.storage.GetPoolIDForClient(clientID)
	require.NoError(t, err)
	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)
	user, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)

	now := time.Now().Unix()
	token, err := buildJWT(privateKey, keys.KeyID, map[string]any{
		"sub":       user.Sub,
		"iss":       issuerURL(poolID),
		"token_use": "id", // wrong — must be "access"
		"exp":       now + 3600,
		"iat":       now,
	})
	require.NoError(t, err)

	w := doGetUserDirect(t, ro, token)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestGetUser_EmptySub(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)

	poolID, err := ro.storage.GetPoolIDForClient(clientID)
	require.NoError(t, err)
	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)

	now := time.Now().Unix()
	token, err := buildJWT(privateKey, keys.KeyID, map[string]any{
		// sub is intentionally omitted
		"iss":       issuerURL(poolID),
		"token_use": "access",
		"exp":       now + 3600,
		"iat":       now,
	})
	require.NoError(t, err)

	w := doGetUserDirect(t, ro, token)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestGetUser_UserNotFound(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)

	poolID, err := ro.storage.GetPoolIDForClient(clientID)
	require.NoError(t, err)
	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)

	now := time.Now().Unix()
	token, err := buildJWT(privateKey, keys.KeyID, map[string]any{
		"sub":       "00000000-0000-0000-0000-000000000000",
		"iss":       issuerURL(poolID),
		"token_use": "access",
		"exp":       now + 3600,
		"iat":       now,
	})
	require.NoError(t, err)

	w := doGetUserDirect(t, ro, token)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestGetUser_UnknownPool(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	poolID := "us-east-1_Unknown"
	now := time.Now().Unix()
	token, err := buildJWT(privKey, "kid", map[string]any{
		"sub":       "some-sub",
		"iss":       issuerURL(poolID),
		"token_use": "access",
		"exp":       now + 3600,
		"iat":       now,
	})
	require.NoError(t, err)

	ro := &Router{
		storage: &mockStore{
			getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
				return nil, nil, os.ErrNotExist
			},
		},
	}

	w := doGetUserDirect(t, ro, token)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestGetUser_KeysStorageError(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	poolID := "us-east-1_FakePool1"
	now := time.Now().Unix()
	token, err := buildJWT(privKey, "kid", map[string]any{
		"sub":       "some-sub",
		"iss":       issuerURL(poolID),
		"token_use": "access",
		"exp":       now + 3600,
		"iat":       now,
	})
	require.NoError(t, err)

	ro := &Router{
		storage: &mockStore{
			getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
				return nil, nil, errors.New("storage failure")
			},
		},
	}

	w := doGetUserDirect(t, ro, token)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestGetUser_GetUserBySubStorageError(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	poolID := "us-east-1_FakePool2"
	keyID := "k1"
	now := time.Now().Unix()
	token, err := buildJWT(privKey, keyID, map[string]any{
		"sub":       "some-sub",
		"iss":       issuerURL(poolID),
		"token_use": "access",
		"exp":       now + 3600,
		"iat":       now,
	})
	require.NoError(t, err)

	ro := &Router{
		storage: &mockStore{
			getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
				return &poolKeys{KeyID: keyID}, privKey, nil
			},
			getUserBySubFn: func(string, string) (*UserMetadata, error) {
				return nil, errors.New("storage failure")
			},
		},
	}

	w := doGetUserDirect(t, ro, token)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

// ── prependSub ────────────────────────────────────────────────────────────────

func TestPrependSub_AlreadyFirst(t *testing.T) {
	attrs := []AttributeType{
		{Name: "sub", Value: "uuid-1"},
		{Name: "email", Value: "a@example.com"},
	}
	result := prependSub(attrs, "uuid-1")
	require.Len(t, result, 2)
	assert.Equal(t, "sub", result[0].Name)
	assert.Equal(t, "uuid-1", result[0].Value)
	assert.Equal(t, "email", result[1].Name)
}

func TestPrependSub_OutOfOrder(t *testing.T) {
	attrs := []AttributeType{
		{Name: "email", Value: "a@example.com"},
		{Name: "sub", Value: "uuid-1"},
	}
	result := prependSub(attrs, "uuid-1")
	require.Len(t, result, 2)
	assert.Equal(t, "sub", result[0].Name)
	assert.Equal(t, "uuid-1", result[0].Value)
	assert.Equal(t, "email", result[1].Name)
}

func TestPrependSub_NotPresent(t *testing.T) {
	attrs := []AttributeType{{Name: "email", Value: "a@example.com"}}
	result := prependSub(attrs, "uuid-1")
	require.Len(t, result, 2)
	assert.Equal(t, "sub", result[0].Name)
	assert.Equal(t, "uuid-1", result[0].Value)
	assert.Equal(t, "email", result[1].Name)
}

func TestPrependSub_EmptyAttrs(t *testing.T) {
	result := prependSub(nil, "uuid-1")
	require.Len(t, result, 1)
	assert.Equal(t, "sub", result[0].Name)
}
