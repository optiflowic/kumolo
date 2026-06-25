package cognito

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ──── AdminCreateUser ────────────────────────────────────────────────────────

func TestAdminCreateUser_WithTempPassword(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId":        poolID,
		"Username":          "admin-user",
		"TemporaryPassword": "TempPass1!",
		"UserAttributes":    []map[string]string{{"Name": "email", "Value": "admin@example.com"}},
	})
	w := doOp(t, ro, "AdminCreateUser", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		User struct {
			Username   string          `json:"Username"`
			Attributes []AttributeType `json:"Attributes"`
			UserStatus string          `json:"UserStatus"`
			Enabled    bool            `json:"Enabled"`
		} `json:"User"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "admin-user", resp.User.Username)
	assert.Equal(t, userStatusForceChangePasswd, resp.User.UserStatus)
	assert.True(t, resp.User.Enabled)
	require.NotEmpty(t, resp.User.Attributes)
	assert.Equal(t, "sub", resp.User.Attributes[0].Name)
}

func TestAdminCreateUser_NoPassword(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"Username":   "nopass-user",
	})
	w := doOp(t, ro, "AdminCreateUser", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		User struct {
			UserStatus string `json:"UserStatus"`
		} `json:"User"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, userStatusConfirmed, resp.User.UserStatus)
}

func TestAdminCreateUser_ValidationErrors(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	tests := []struct {
		name     string
		body     map[string]any
		wantCode int
		wantType string
	}{
		{
			name:     "missing UserPoolId",
			body:     map[string]any{"Username": "u"},
			wantCode: http.StatusBadRequest,
			wantType: ErrTypeInvalidParameterException,
		},
		{
			name:     "missing Username",
			body:     map[string]any{"UserPoolId": poolID},
			wantCode: http.StatusBadRequest,
			wantType: ErrTypeInvalidParameterException,
		},
		{
			name: "RESEND MessageAction not supported",
			body: map[string]any{
				"UserPoolId":    poolID,
				"Username":      "u",
				"MessageAction": "RESEND",
			},
			wantCode: http.StatusBadRequest,
			wantType: ErrTypeNotAuthorizedException,
		},
		{
			name: "password too short",
			body: map[string]any{
				"UserPoolId":        poolID,
				"Username":          "u",
				"TemporaryPassword": "short",
			},
			wantCode: http.StatusBadRequest,
			wantType: ErrTypeInvalidPasswordException,
		},
		{
			name:     "pool not found",
			body:     map[string]any{"UserPoolId": "us-east-1_UNKNOWN", "Username": "u"},
			wantCode: http.StatusBadRequest,
			wantType: ErrTypeResourceNotFoundException,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.body)
			w := doOp(t, ro, "AdminCreateUser", string(b))
			assert.Equal(t, tc.wantCode, w.Code)
			assertErrType(t, w, tc.wantType)
		})
	}
}

func TestAdminCreateUser_DuplicateUsername(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "Username": "dup"})
	require.Equal(t, http.StatusOK, doOp(t, ro, "AdminCreateUser", string(body)).Code)

	w := doOp(t, ro, "AdminCreateUser", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUsernameExistsException)
}

func TestAdminCreateUser_StorageError(t *testing.T) {
	// getErr=nil so GetUserPool succeeds; createUserErr causes CreateUser to fail.
	ro := &Router{
		storage: &mockStore{createUserErr: errors.New("disk full")},
	}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "us-east-1_X", "Username": "u"})
	w := doOp(t, ro, "AdminCreateUser", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

// ──── AdminGetUser ───────────────────────────────────────────────────────────

func TestAdminGetUser_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	createBody, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"Username":   "getme",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "getme@example.com"},
		},
	})
	require.Equal(t, http.StatusOK, doOp(t, ro, "AdminCreateUser", string(createBody)).Code)

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "Username": "getme"})
	w := doOp(t, ro, "AdminGetUser", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp adminGetUserResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "getme", resp.Username)
	assert.Equal(t, userStatusConfirmed, resp.UserStatus)
	assert.True(t, resp.Enabled)
	require.NotEmpty(t, resp.UserAttributes)
	assert.Equal(t, "sub", resp.UserAttributes[0].Name)
	assert.NotNil(t, resp.MFAOptions)
	assert.NotNil(t, resp.UserMFASettingList)
}

func TestAdminGetUser_ValidationErrors(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	tests := []struct {
		name     string
		body     map[string]any
		wantType string
	}{
		{
			name:     "missing UserPoolId",
			body:     map[string]any{"Username": "u"},
			wantType: ErrTypeInvalidParameterException,
		},
		{
			name:     "missing Username",
			body:     map[string]any{"UserPoolId": poolID},
			wantType: ErrTypeInvalidParameterException,
		},
		{
			name:     "pool not found",
			body:     map[string]any{"UserPoolId": "us-east-1_UNKNOWN", "Username": "u"},
			wantType: ErrTypeResourceNotFoundException,
		},
		{
			name:     "user not found",
			body:     map[string]any{"UserPoolId": poolID, "Username": "nobody"},
			wantType: ErrTypeUserNotFoundException,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.body)
			w := doOp(t, ro, "AdminGetUser", string(b))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, tc.wantType)
		})
	}
}

// ──── AdminSetUserPassword ───────────────────────────────────────────────────

func TestAdminSetUserPassword_Permanent(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "OldPass1!")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"Username":   "alice",
		"Password":   "NewPass99!",
		"Permanent":  true,
	})
	w := doOp(t, ro, "AdminSetUserPassword", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	user, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.Equal(t, userStatusConfirmed, user.Status)
}

func TestAdminSetUserPassword_Temporary(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "bob", "OldPass1!")
	confirmUser(t, ro, clientID, "bob")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"Username":   "bob",
		"Password":   "TempPass99!",
		"Permanent":  false,
	})
	w := doOp(t, ro, "AdminSetUserPassword", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	user, err := ro.storage.GetUser(poolID, "bob")
	require.NoError(t, err)
	assert.Equal(t, userStatusForceChangePasswd, user.Status)
}

func TestAdminSetUserPassword_ValidationErrors(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "carol", "OldPass1!")

	tests := []struct {
		name     string
		body     map[string]any
		wantType string
	}{
		{
			name:     "missing UserPoolId",
			body:     map[string]any{"Username": "carol", "Password": "NewPass1!"},
			wantType: ErrTypeInvalidParameterException,
		},
		{
			name:     "missing Username",
			body:     map[string]any{"UserPoolId": poolID, "Password": "NewPass1!"},
			wantType: ErrTypeInvalidParameterException,
		},
		{
			name:     "missing Password",
			body:     map[string]any{"UserPoolId": poolID, "Username": "carol"},
			wantType: ErrTypeInvalidParameterException,
		},
		{
			name: "password too short",
			body: map[string]any{
				"UserPoolId": poolID,
				"Username":   "carol",
				"Password":   "short",
			},
			wantType: ErrTypeInvalidPasswordException,
		},
		{
			name: "pool not found",
			body: map[string]any{
				"UserPoolId": "us-east-1_UNKNOWN",
				"Username":   "carol",
				"Password":   "NewPass1!",
			},
			wantType: ErrTypeResourceNotFoundException,
		},
		{
			name: "user not found",
			body: map[string]any{
				"UserPoolId": poolID,
				"Username":   "nobody",
				"Password":   "NewPass1!",
			},
			wantType: ErrTypeUserNotFoundException,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.body)
			w := doOp(t, ro, "AdminSetUserPassword", string(b))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, tc.wantType)
		})
	}
}

// ──── AdminConfirmSignUp ─────────────────────────────────────────────────────

func TestAdminConfirmSignUp_Unconfirmed(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "dave", "Pass1234!")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "Username": "dave"})
	w := doOp(t, ro, "AdminConfirmSignUp", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	user, err := ro.storage.GetUser(poolID, "dave")
	require.NoError(t, err)
	assert.Equal(t, userStatusConfirmed, user.Status)
}

func TestAdminConfirmSignUp_AlreadyConfirmed(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "eve", "Pass1234!")
	confirmUser(t, ro, clientID, "eve")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "Username": "eve"})
	w := doOp(t, ro, "AdminConfirmSignUp", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	user, err := ro.storage.GetUser(poolID, "eve")
	require.NoError(t, err)
	assert.Equal(t, userStatusConfirmed, user.Status)
}

func TestAdminConfirmSignUp_ValidationErrors(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "frank", "Pass1234!")

	tests := []struct {
		name     string
		body     map[string]any
		wantType string
	}{
		{
			name:     "missing UserPoolId",
			body:     map[string]any{"Username": "frank"},
			wantType: ErrTypeInvalidParameterException,
		},
		{
			name:     "missing Username",
			body:     map[string]any{"UserPoolId": poolID},
			wantType: ErrTypeInvalidParameterException,
		},
		{
			name:     "pool not found",
			body:     map[string]any{"UserPoolId": "us-east-1_UNKNOWN", "Username": "frank"},
			wantType: ErrTypeResourceNotFoundException,
		},
		{
			name:     "user not found",
			body:     map[string]any{"UserPoolId": poolID, "Username": "nobody"},
			wantType: ErrTypeUserNotFoundException,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.body)
			w := doOp(t, ro, "AdminConfirmSignUp", string(b))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, tc.wantType)
		})
	}
}

// ──── AdminDeleteUser ────────────────────────────────────────────────────────

func TestAdminDeleteUser_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "grace", "Pass1234!")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "Username": "grace"})
	w := doOp(t, ro, "AdminDeleteUser", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	_, err := ro.storage.GetUser(poolID, "grace")
	assert.ErrorIs(t, err, errUserNotFound)
}

func TestAdminDeleteUser_ValidationErrors(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "henry", "Pass1234!")

	tests := []struct {
		name     string
		body     map[string]any
		wantType string
	}{
		{
			name:     "missing UserPoolId",
			body:     map[string]any{"Username": "henry"},
			wantType: ErrTypeInvalidParameterException,
		},
		{
			name:     "missing Username",
			body:     map[string]any{"UserPoolId": poolID},
			wantType: ErrTypeInvalidParameterException,
		},
		{
			name:     "pool not found",
			body:     map[string]any{"UserPoolId": "us-east-1_UNKNOWN", "Username": "henry"},
			wantType: ErrTypeResourceNotFoundException,
		},
		{
			name:     "user not found",
			body:     map[string]any{"UserPoolId": poolID, "Username": "nobody"},
			wantType: ErrTypeUserNotFoundException,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.body)
			w := doOp(t, ro, "AdminDeleteUser", string(b))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, tc.wantType)
		})
	}
}

func TestAdminDeleteUser_StorageError(t *testing.T) {
	ro := &Router{
		storage: &mockStore{
			deleteUserErr: errors.New("disk full"),
		},
	}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "us-east-1_X", "Username": "u"})
	w := doOp(t, ro, "AdminDeleteUser", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

// ──── Invalid body (json.Unmarshal error paths) ──────────────────────────────

func TestAdminCreateUser_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "AdminCreateUser", "invalid-json")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestAdminGetUser_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "AdminGetUser", "invalid-json")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestAdminSetUserPassword_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "AdminSetUserPassword", "invalid-json")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestAdminConfirmSignUp_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "AdminConfirmSignUp", "invalid-json")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestAdminDeleteUser_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "AdminDeleteUser", "invalid-json")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

// ──── GetUserPool storage error (non-pool-not-found) ────────────────────────

func TestAdminCreateUser_GetPoolStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: errors.New("storage error")}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "us-east-1_X", "Username": "u"})
	w := doOp(t, ro, "AdminCreateUser", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestAdminGetUser_GetPoolStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: errors.New("storage error")}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "us-east-1_X", "Username": "u"})
	w := doOp(t, ro, "AdminGetUser", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestAdminSetUserPassword_GetPoolStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: errors.New("storage error")}}
	body, _ := json.Marshal(map[string]any{
		"UserPoolId": "us-east-1_X",
		"Username":   "u",
		"Password":   "ValidPass1!",
	})
	w := doOp(t, ro, "AdminSetUserPassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestAdminConfirmSignUp_GetPoolStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: errors.New("storage error")}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "us-east-1_X", "Username": "u"})
	w := doOp(t, ro, "AdminConfirmSignUp", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestAdminDeleteUser_GetPoolStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: errors.New("storage error")}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "us-east-1_X", "Username": "u"})
	w := doOp(t, ro, "AdminDeleteUser", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

// ──── GetUser / UpdateUser storage errors ────────────────────────────────────

func TestAdminGetUser_GetUserStorageError(t *testing.T) {
	ro := &Router{
		storage: &mockStore{
			getUserFn: func(string, string) (*UserMetadata, error) {
				return nil, errors.New("storage error")
			},
		},
	}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "us-east-1_X", "Username": "u"})
	w := doOp(t, ro, "AdminGetUser", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestAdminSetUserPassword_UpdateUserStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{updateUserErr: errors.New("disk full")}}
	body, _ := json.Marshal(map[string]any{
		"UserPoolId": "us-east-1_X",
		"Username":   "u",
		"Password":   "ValidPass1!",
	})
	w := doOp(t, ro, "AdminSetUserPassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestAdminConfirmSignUp_UpdateUserStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{updateUserErr: errors.New("disk full")}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "us-east-1_X", "Username": "u"})
	w := doOp(t, ro, "AdminConfirmSignUp", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}
