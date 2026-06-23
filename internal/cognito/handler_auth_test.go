package cognito

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// ── test helpers ──────────────────────────────────────────────────────────────

// setupPool creates a pool and client, returning (poolID, clientID).
func setupPool(t *testing.T, ro *Router) (string, string) {
	t.Helper()
	poolID := createPool(t, ro, "test-pool")
	clientID := createClient(t, ro, poolID, "test-client")
	return poolID, clientID
}

// signUpUser registers a new user and returns the UserSub.
func signUpUser(t *testing.T, ro *Router, clientID, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID,
		"Username": username,
		"Password": password,
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": username + "@example.com"},
		},
	})
	w := doOp(t, ro, "SignUp", string(body))
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		UserSub string `json:"UserSub"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp.UserSub
}

// confirmUser confirms a registered user with the fixed code.
func confirmUser(t *testing.T, ro *Router, clientID, username string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"ClientId":         clientID,
		"Username":         username,
		"ConfirmationCode": confirmationCode,
	})
	w := doOp(t, ro, "ConfirmSignUp", string(body))
	require.Equal(t, http.StatusOK, w.Code)
}

// doInitAuth calls InitiateAuth with USER_PASSWORD_AUTH.
func doInitAuth(
	t *testing.T,
	ro *Router,
	clientID, username, password string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID,
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": username,
			"PASSWORD": password,
		},
	})
	return doOp(t, ro, "InitiateAuth", string(body))
}

// insertFCPUser inserts a FORCE_CHANGE_PASSWORD user directly into storage.
func insertFCPUser(
	t *testing.T,
	storage *Storage,
	poolID, username, sub, tempPassword string,
) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(tempPassword), bcrypt.DefaultCost)
	require.NoError(t, err)
	ts := nowUnix()
	user := &UserMetadata{
		Username:         username,
		Sub:              sub,
		Status:           userStatusForceChangePasswd,
		PasswordHash:     string(hash),
		Attributes:       nil,
		ConfirmationCode: "",
		CreatedAt:        ts,
		UpdatedAt:        ts,
	}
	require.NoError(t, storage.CreateUser(poolID, user))
}

// assertErrType decodes the response body and asserts the __type field.
func assertErrType(t *testing.T, w *httptest.ResponseRecorder, expected string) {
	t.Helper()
	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, expected, resp.Type)
}

// ── SignUp ────────────────────────────────────────────────────────────────────

func TestSignUp_Success(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)

	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID,
		"Username": "alice",
		"Password": "Password123!",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "alice@example.com"},
		},
	})
	w := doOp(t, ro, "SignUp", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp signUpResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotEmpty(t, resp.UserSub)
	assert.False(t, resp.UserConfirmed)
	assert.Equal(t, "EMAIL", resp.CodeDeliveryDetails.DeliveryMedium)
	assert.Contains(t, resp.CodeDeliveryDetails.Destination, "@example.com")
}

func TestSignUp_NoEmail_MasksDestination(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Username": "alice", "Password": "Password123!",
	})
	w := doOp(t, ro, "SignUp", string(body))
	require.Equal(t, http.StatusOK, w.Code)
	var resp signUpResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "***", resp.CodeDeliveryDetails.Destination)
}

func TestSignUp_MissingClientId(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "SignUp", `{"Username":"alice","Password":"Password123!"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestSignUp_MissingUsername(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Password": "Password123!",
	})
	w := doOp(t, ro, "SignUp", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestSignUp_ShortPassword(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Username": "alice", "Password": "short",
	})
	w := doOp(t, ro, "SignUp", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidPasswordException)
}

func TestSignUp_InvalidClientId(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]string{
		"ClientId": "nonexistent", "Username": "alice", "Password": "Password123!",
	})
	w := doOp(t, ro, "SignUp", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeResourceNotFoundException)
}

func TestSignUp_DuplicateUsername(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")

	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Username": "alice", "Password": "Password123!",
	})
	w := doOp(t, ro, "SignUp", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUsernameExistsException)
}

func TestSignUp_GetPoolError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "", errors.New("db error") },
	}}
	body, _ := json.Marshal(map[string]string{
		"ClientId": "c", "Username": "u", "Password": "Password123!",
	})
	w := doOp(t, ro, "SignUp", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestSignUp_CreateUserError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		createUserErr:    errors.New("disk full"),
	}}
	body, _ := json.Marshal(map[string]string{
		"ClientId": "c", "Username": "u", "Password": "Password123!",
	})
	w := doOp(t, ro, "SignUp", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── ConfirmSignUp ─────────────────────────────────────────────────────────────

func TestConfirmSignUp_Success(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")
}

func TestConfirmSignUp_WrongCode(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")

	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Username": "alice", "ConfirmationCode": "000000",
	})
	w := doOp(t, ro, "ConfirmSignUp", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeCodeMismatchException)
}

func TestConfirmSignUp_AlreadyConfirmed(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Username": "alice", "ConfirmationCode": confirmationCode,
	})
	w := doOp(t, ro, "ConfirmSignUp", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestConfirmSignUp_UserNotFound(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Username": "nobody", "ConfirmationCode": confirmationCode,
	})
	w := doOp(t, ro, "ConfirmSignUp", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestConfirmSignUp_MissingClientId(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ConfirmSignUp", `{"Username":"alice","ConfirmationCode":"123456"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestConfirmSignUp_MissingUsername(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ConfirmSignUp", `{"ClientId":"x","ConfirmationCode":"123456"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestConfirmSignUp_MissingCode(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ConfirmSignUp", `{"ClientId":"x","Username":"alice"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestConfirmSignUp_InvalidClientId(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]string{
		"ClientId": "nonexistent", "Username": "alice", "ConfirmationCode": confirmationCode,
	})
	w := doOp(t, ro, "ConfirmSignUp", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeResourceNotFoundException)
}

func TestConfirmSignUp_GetPoolError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "", errors.New("db error") },
	}}
	body, _ := json.Marshal(map[string]string{
		"ClientId": "c", "Username": "u", "ConfirmationCode": "123456",
	})
	w := doOp(t, ro, "ConfirmSignUp", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestConfirmSignUp_UpdateStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		updateUserErr:    errors.New("storage error"),
	}}
	body, _ := json.Marshal(map[string]string{
		"ClientId": "c", "Username": "u", "ConfirmationCode": "123456",
	})
	w := doOp(t, ro, "ConfirmSignUp", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── InitiateAuth ──────────────────────────────────────────────────────────────

func TestInitiateAuth_UserPasswordAuth_Success(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	result, ok := resp["AuthenticationResult"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, result["AccessToken"])
	assert.NotEmpty(t, result["IdToken"])
	assert.NotEmpty(t, result["RefreshToken"])
	assert.Equal(t, "Bearer", result["TokenType"])
	assert.Equal(t, float64(accessTokenExpiry), result["ExpiresIn"])
}

func TestInitiateAuth_WrongPassword(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	w := doInitAuth(t, ro, clientID, "alice", "WrongPass123!")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestInitiateAuth_UnconfirmedUser(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotConfirmedException)
}

func TestInitiateAuth_UserNotFound(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)

	w := doInitAuth(t, ro, clientID, "nobody", "Password123!")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestInitiateAuth_MissingClientId(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "InitiateAuth", `{"AuthFlow":"USER_PASSWORD_AUTH"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestInitiateAuth_MissingAuthFlow(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "InitiateAuth", `{"ClientId":"x"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestInitiateAuth_InvalidClientId(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]any{
		"ClientId": "nonexistent",
		"AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{
			"USERNAME": "alice", "PASSWORD": "Password123!",
		},
	})
	w := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeResourceNotFoundException)
}

func TestInitiateAuth_UnsupportedAuthFlow(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "AuthFlow": "USER_SRP_AUTH",
	})
	w := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestInitiateAuth_MissingUsername(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"PASSWORD": "Password123!"},
	})
	w := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestInitiateAuth_MissingPassword(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID, "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "alice"},
	})
	w := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestInitiateAuth_ForceChangePassword_ReturnsChallenge(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	storage := ro.storage.(*Storage)

	insertFCPUser(t, storage, poolID, "bob", "bob-sub", "TempPass123!")

	w := doInitAuth(t, ro, clientID, "bob", "TempPass123!")
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "NEW_PASSWORD_REQUIRED", resp["ChallengeName"])
	assert.NotEmpty(t, resp["Session"])
	params, ok := resp["ChallengeParameters"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "bob", params["USER_ID_FOR_SRP"])
}

func TestInitiateAuth_GetPoolKeysError(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("Password123!"), bcrypt.DefaultCost)
	confirmedUser := &UserMetadata{
		Username: "u", Sub: "sub-u", Status: userStatusConfirmed,
		PasswordHash: string(hash), Attributes: nil,
	}
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getUserFn:        func(string, string) (*UserMetadata, error) { return confirmedUser, nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return nil, nil, errors.New("key error")
		},
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "u", "PASSWORD": "Password123!"},
	})
	w := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestInitiateAuth_GetUserError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getUserFn: func(string, string) (*UserMetadata, error) {
			return nil, errors.New("storage error")
		},
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "u", "PASSWORD": "Password123!"},
	})
	w := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestInitiateAuth_GetPoolError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "", errors.New("db error") },
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "AuthFlow": "USER_PASSWORD_AUTH",
	})
	w := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── RefreshTokenAuth ──────────────────────────────────────────────────────────

func TestInitiateAuth_RefreshTokenAuth_Success(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	// Sign in to get a refresh token.
	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var firstResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&firstResp))
	result := firstResp["AuthenticationResult"].(map[string]any)
	refreshToken := result["RefreshToken"].(string)

	// Use the refresh token.
	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID,
		"AuthFlow": "REFRESH_TOKEN_AUTH",
		"AuthParameters": map[string]string{
			"REFRESH_TOKEN": refreshToken,
		},
	})
	w2 := doOp(t, ro, "InitiateAuth", string(body))
	require.Equal(t, http.StatusOK, w2.Code)
	var secondResp map[string]any
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&secondResp))
	result2 := secondResp["AuthenticationResult"].(map[string]any)
	assert.NotEmpty(t, result2["AccessToken"])
	assert.NotEmpty(t, result2["IdToken"])
	_, hasRefresh := result2["RefreshToken"]
	assert.False(t, hasRefresh, "refresh token flow must not issue a new refresh token")
}

func TestInitiateAuth_RefreshToken_AliasFlow(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	w := doInitAuth(t, ro, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)
	var firstResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&firstResp))
	rt := firstResp["AuthenticationResult"].(map[string]any)["RefreshToken"].(string)

	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID, "AuthFlow": "REFRESH_TOKEN",
		"AuthParameters": map[string]string{"REFRESH_TOKEN": rt},
	})
	w2 := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusOK, w2.Code)
}

func TestInitiateAuth_RefreshTokenAuth_InvalidToken(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID, "AuthFlow": "REFRESH_TOKEN",
		"AuthParameters": map[string]string{"REFRESH_TOKEN": "invalid-token"},
	})
	w := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestInitiateAuth_RefreshToken_ClientIDMismatch(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID1 := setupPool(t, ro)

	// Sign up and confirm a user, then obtain a refresh token for clientID1.
	signUpUser(t, ro, clientID1, "dave", "Password1!")
	confirmUser(t, ro, clientID1, "dave")
	w1 := doInitAuth(t, ro, clientID1, "dave", "Password1!")
	require.Equal(t, http.StatusOK, w1.Code)
	var firstResp map[string]any
	require.NoError(t, json.NewDecoder(w1.Body).Decode(&firstResp))
	rt := firstResp["AuthenticationResult"].(map[string]any)["RefreshToken"].(string)

	// Create a second client in the same pool.
	clientID2 := createClient(t, ro, poolID, "test-client-2")

	// Presenting the first client's refresh token to the second client must fail.
	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID2, "AuthFlow": "REFRESH_TOKEN_AUTH",
		"AuthParameters": map[string]string{"REFRESH_TOKEN": rt},
	})
	w2 := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeNotAuthorizedException)
}

func TestInitiateAuth_RefreshTokenAuth_MissingToken(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID, "AuthFlow": "REFRESH_TOKEN_AUTH",
		"AuthParameters": map[string]string{},
	})
	w := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestInitiateAuth_RefreshToken_GetUserBySubError(t *testing.T) {
	rt := &refreshTokenData{Token: "tok", PoolID: "p", ClientID: "c", Username: "u", Sub: "s"}
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getRefreshFn:     func(string, string) (*refreshTokenData, error) { return rt, nil },
		getUserBySubFn: func(string, string) (*UserMetadata, error) {
			return nil, errors.New("not found")
		},
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "AuthFlow": "REFRESH_TOKEN",
		"AuthParameters": map[string]string{"REFRESH_TOKEN": "tok"},
	})
	w := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── RespondToAuthChallenge ────────────────────────────────────────────────────

func TestRespondToAuthChallenge_NewPasswordRequired_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	storage := ro.storage.(*Storage)

	insertFCPUser(t, storage, poolID, "charlie", "charlie-sub", "TempPass123!")

	w := doInitAuth(t, ro, clientID, "charlie", "TempPass123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	body, _ := json.Marshal(map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "NEW_PASSWORD_REQUIRED",
		"Session":       session,
		"ChallengeResponses": map[string]string{
			"USERNAME":     "charlie",
			"NEW_PASSWORD": "NewSecurePass123!",
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

func TestRespondToAuthChallenge_UsernameFromSession(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	storage := ro.storage.(*Storage)

	insertFCPUser(t, storage, poolID, "dave", "dave-sub", "TempPass123!")

	w := doInitAuth(t, ro, clientID, "dave", "TempPass123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	// Omit USERNAME in ChallengeResponses — must be taken from session.
	body, _ := json.Marshal(map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "NEW_PASSWORD_REQUIRED",
		"Session":       session,
		"ChallengeResponses": map[string]string{
			"NEW_PASSWORD": "NewSecurePass123!",
		},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(body))
	require.Equal(t, http.StatusOK, w2.Code)
}

func TestRespondToAuthChallenge_InvalidSession(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID, "ChallengeName": "NEW_PASSWORD_REQUIRED",
		"Session":            "not-a-valid-jwt",
		"ChallengeResponses": map[string]string{"USERNAME": "u", "NEW_PASSWORD": "NewPass123!"},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestRespondToAuthChallenge_ShortNewPassword(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	storage := ro.storage.(*Storage)

	insertFCPUser(t, storage, poolID, "eve", "eve-sub", "TempPass123!")

	w := doInitAuth(t, ro, clientID, "eve", "TempPass123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID, "ChallengeName": "NEW_PASSWORD_REQUIRED", "Session": session,
		"ChallengeResponses": map[string]string{"USERNAME": "eve", "NEW_PASSWORD": "short"},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeInvalidPasswordException)
}

func TestRespondToAuthChallenge_MissingNewPassword(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	storage := ro.storage.(*Storage)

	insertFCPUser(t, storage, poolID, "frank", "frank-sub", "TempPass123!")

	w := doInitAuth(t, ro, clientID, "frank", "TempPass123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID, "ChallengeName": "NEW_PASSWORD_REQUIRED", "Session": session,
		"ChallengeResponses": map[string]string{"USERNAME": "frank"},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeInvalidParameterException)
}

func TestRespondToAuthChallenge_MissingClientId(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "RespondToAuthChallenge",
		`{"ChallengeName":"NEW_PASSWORD_REQUIRED","Session":"x"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestRespondToAuthChallenge_MissingChallengeName(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "RespondToAuthChallenge", `{"ClientId":"x","Session":"x"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestRespondToAuthChallenge_MissingSession(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "RespondToAuthChallenge",
		`{"ClientId":"x","ChallengeName":"NEW_PASSWORD_REQUIRED"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestRespondToAuthChallenge_UnsupportedChallenge(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "ChallengeName": "SMS_MFA", "Session": "x",
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestRespondToAuthChallenge_InvalidClientId(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]string{
		"ClientId": "nonexistent", "ChallengeName": "NEW_PASSWORD_REQUIRED", "Session": "x",
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeResourceNotFoundException)
}

func TestRespondToAuthChallenge_GetPoolError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "", errors.New("db error") },
	}}
	body, _ := json.Marshal(map[string]string{
		"ClientId": "c", "ChallengeName": "NEW_PASSWORD_REQUIRED", "Session": "s",
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── JWKS ──────────────────────────────────────────────────────────────────────

func TestJWKS_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID, _ := setupPool(t, ro)

	req := httptest.NewRequest(http.MethodGet, "/"+poolID+"/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	ro.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	keys, ok := resp["keys"].([]any)
	require.True(t, ok)
	require.Len(t, keys, 1)
	key := keys[0].(map[string]any)
	assert.Equal(t, "RS256", key["alg"])
	assert.Equal(t, "RSA", key["kty"])
	assert.Equal(t, "sig", key["use"])
	assert.NotEmpty(t, key["n"])
	assert.NotEmpty(t, key["e"])
}

func TestJWKS_SameKeyOnSubsequentCalls(t *testing.T) {
	ro := newTestRouter(t)
	poolID, _ := setupPool(t, ro)

	req1 := httptest.NewRequest(http.MethodGet, "/"+poolID+"/.well-known/jwks.json", nil)
	w1 := httptest.NewRecorder()
	ro.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code)

	req2 := httptest.NewRequest(http.MethodGet, "/"+poolID+"/.well-known/jwks.json", nil)
	w2 := httptest.NewRecorder()
	ro.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	assert.Equal(t, w1.Body.String(), w2.Body.String(), "JWKS must be stable across calls")
}

func TestJWKS_PoolNotFound(t *testing.T) {
	ro := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/us-east-1_UNKNOWN/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	ro.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeResourceNotFoundException)
}

func TestJWKS_MissingPoolID(t *testing.T) {
	ro := newTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	ro.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── maskEmail ─────────────────────────────────────────────────────────────────

func TestMaskEmail_AtStart(t *testing.T) {
	assert.Equal(t, "***", maskEmail("@example.com"))
}

func TestMaskEmail_NoAt(t *testing.T) {
	assert.Equal(t, "***", maskEmail("noatsign"))
}

// ── writeAuthResult error paths ───────────────────────────────────────────────

func TestWriteAuthResult_CreateRefreshTokenError(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyID, _ := generateTokenID()

	hash, _ := bcrypt.GenerateFromPassword([]byte("Password123!"), bcrypt.DefaultCost)
	confirmedUser := &UserMetadata{
		Username: "u", Sub: "sub-u", Status: userStatusConfirmed,
		PasswordHash: string(hash),
	}
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getUserFn:        func(string, string) (*UserMetadata, error) { return confirmedUser, nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		createRefreshErr: errors.New("disk full"),
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "AuthFlow": "USER_PASSWORD_AUTH",
		"AuthParameters": map[string]string{"USERNAME": "u", "PASSWORD": "Password123!"},
	})
	w := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── handleRefreshTokenAuth error paths ────────────────────────────────────────

func TestInitiateAuth_RefreshToken_GetOrCreateKeysError(t *testing.T) {
	rt := &refreshTokenData{
		Token: "tok", PoolID: "pool-1", ClientID: "c", Username: "u", Sub: "sub-u",
	}
	user := &UserMetadata{Username: "u", Sub: "sub-u", Status: userStatusConfirmed}
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getRefreshFn:     func(string, string) (*refreshTokenData, error) { return rt, nil },
		getUserBySubFn:   func(string, string) (*UserMetadata, error) { return user, nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return nil, nil, errors.New("key error")
		},
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "AuthFlow": "REFRESH_TOKEN",
		"AuthParameters": map[string]string{"REFRESH_TOKEN": "tok"},
	})
	w := doOp(t, ro, "InitiateAuth", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── handleNewPasswordRequired error paths ─────────────────────────────────────

func TestRespondToAuthChallenge_GetOrCreateKeysError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return nil, nil, errors.New("key error")
		},
	}}
	body, _ := json.Marshal(map[string]string{
		"ClientId": "c", "ChallengeName": "NEW_PASSWORD_REQUIRED", "Session": "s",
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRespondToAuthChallenge_WrongSessionPool(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	storage := ro.storage.(*Storage)

	insertFCPUser(t, storage, poolID, "grace", "grace-sub", "TempPass123!")

	// Get a session token for pool A by initiating auth.
	w := doInitAuth(t, ro, clientID, "grace", "TempPass123!")
	require.Equal(t, http.StatusOK, w.Code)
	var initResp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&initResp))
	session := initResp["Session"].(string)

	// Create a second pool with its own client.
	poolID2 := createPool(t, ro, "other-pool")
	clientID2 := createClient(t, ro, poolID2, "other-client")

	// Use session from pool1 to call RespondToAuthChallenge for pool2.
	// Pool2 has a different RSA key, so parseSessionToken will fail.
	body, _ := json.Marshal(map[string]any{
		"ClientId":      clientID2,
		"ChallengeName": "NEW_PASSWORD_REQUIRED",
		"Session":       session,
		"ChallengeResponses": map[string]string{
			"USERNAME": "grace", "NEW_PASSWORD": "NewPass123!",
		},
	})
	w2 := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeNotAuthorizedException)
}

func TestRespondToAuthChallenge_UpdateUserNotFound(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyID, _ := generateTokenID()

	// Build a valid session token.
	session, err := buildSessionToken(key, keyID, "pool-1", "u", "NEW_PASSWORD_REQUIRED")
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		updateUserErr: errUserNotFound,
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "ChallengeName": "NEW_PASSWORD_REQUIRED", "Session": session,
		"ChallengeResponses": map[string]string{"USERNAME": "u", "NEW_PASSWORD": "NewPass123!"},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestRespondToAuthChallenge_UpdateUserStorageError(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyID, _ := generateTokenID()

	session, err := buildSessionToken(key, keyID, "pool-1", "u", "NEW_PASSWORD_REQUIRED")
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		updateUserErr: errors.New("storage error"),
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "ChallengeName": "NEW_PASSWORD_REQUIRED", "Session": session,
		"ChallengeResponses": map[string]string{"USERNAME": "u", "NEW_PASSWORD": "NewPass123!"},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── handleJWKS error paths ────────────────────────────────────────────────────

func TestJWKS_GetUserPoolStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getErr: errors.New("storage error"),
	}}
	req := httptest.NewRequest(http.MethodGet, "/us-east-1_Pool1/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	ro.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestJWKS_GetOrCreateKeysError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		// getErr=nil means GetUserPool returns (nil, nil) = "pool exists"
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return nil, nil, errors.New("key error")
		},
	}}
	req := httptest.NewRequest(http.MethodGet, "/us-east-1_Pool1/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	ro.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── Invalid JSON body tests ───────────────────────────────────────────────────

func TestSignUp_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "SignUp", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestConfirmSignUp_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ConfirmSignUp", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestInitiateAuth_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "InitiateAuth", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestRespondToAuthChallenge_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "RespondToAuthChallenge", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

// ── handleNewPasswordRequired: wrong challenge claim ──────────────────────────

func TestRespondToAuthChallenge_WrongChallengeName(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyID, _ := generateTokenID()

	// Build a session token with a different challenge name.
	session, err := buildSessionToken(key, keyID, "pool-1", "u", "SOME_OTHER_CHALLENGE")
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "ChallengeName": "NEW_PASSWORD_REQUIRED", "Session": session,
		"ChallengeResponses": map[string]string{"USERNAME": "u", "NEW_PASSWORD": "NewPass123!"},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

// ── handleNewPasswordRequired: errWrongChallengeStatus ────────────────────────

func TestRespondToAuthChallenge_WrongChallengeStatus(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyID, _ := generateTokenID()

	session, err := buildSessionToken(key, keyID, "pool-1", "u", "NEW_PASSWORD_REQUIRED")
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: keyID}, key, nil
		},
		updateUserErr: errWrongChallengeStatus,
	}}
	body, _ := json.Marshal(map[string]any{
		"ClientId": "c", "ChallengeName": "NEW_PASSWORD_REQUIRED", "Session": session,
		"ChallengeResponses": map[string]string{"USERNAME": "u", "NEW_PASSWORD": "NewPass123!"},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

// TestRespondToAuthChallenge_UserAlreadyConfirmed exercises the errWrongChallengeStatus
// path inside the UpdateUser callback when the user is already in CONFIRMED status.
func TestRespondToAuthChallenge_UserAlreadyConfirmed(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	storage := ro.storage.(*Storage)

	// Sign up and confirm a regular user (status = CONFIRMED, not FORCE_CHANGE_PASSWORD).
	signUpUser(t, ro, clientID, "henry", "Password123!")
	confirmUser(t, ro, clientID, "henry")

	// Build a valid session token for this pool using the pool's actual key.
	keys, privateKey, err := storage.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)
	session, err := buildSessionToken(
		privateKey,
		keys.KeyID,
		poolID,
		"henry",
		"NEW_PASSWORD_REQUIRED",
	)
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID, "ChallengeName": "NEW_PASSWORD_REQUIRED", "Session": session,
		"ChallengeResponses": map[string]string{"USERNAME": "henry", "NEW_PASSWORD": "NewPass123!"},
	})
	w := doOp(t, ro, "RespondToAuthChallenge", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}
