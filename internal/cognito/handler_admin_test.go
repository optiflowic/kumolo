package cognito

import (
	"encoding/json"
	"errors"
	"fmt"
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
			name: "RESEND user not found",
			body: map[string]any{
				"UserPoolId":    poolID,
				"Username":      "nonexistent",
				"MessageAction": "RESEND",
			},
			wantCode: http.StatusBadRequest,
			wantType: ErrTypeUserNotFoundException,
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

func TestAdminCreateUser_RESEND_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	createBody, _ := json.Marshal(map[string]any{
		"UserPoolId":        poolID,
		"Username":          "resend-user",
		"TemporaryPassword": "TempPass1!",
	})
	require.Equal(t, http.StatusOK, doOp(t, ro, "AdminCreateUser", string(createBody)).Code)

	body, _ := json.Marshal(map[string]any{
		"UserPoolId":    poolID,
		"Username":      "resend-user",
		"MessageAction": "RESEND",
	})
	w := doOp(t, ro, "AdminCreateUser", string(body))
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		User struct {
			Username   string `json:"Username"`
			UserStatus string `json:"UserStatus"`
		} `json:"User"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "resend-user", resp.User.Username)
	assert.Equal(t, userStatusForceChangePasswd, resp.User.UserStatus)
}

func TestAdminCreateUser_RESEND_GetUserStorageError(t *testing.T) {
	ro := &Router{
		storage: &mockStore{
			getUserFn: func(string, string) (*UserMetadata, error) {
				return nil, errors.New("storage error")
			},
		},
	}
	body, _ := json.Marshal(map[string]any{
		"UserPoolId":    "us-east-1_X",
		"Username":      "u",
		"MessageAction": "RESEND",
	})
	w := doOp(t, ro, "AdminCreateUser", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
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

func TestAdminOps_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	for _, op := range []string{
		"AdminCreateUser", "AdminGetUser", "AdminSetUserPassword",
		"AdminConfirmSignUp", "AdminDeleteUser",
	} {
		t.Run(op, func(t *testing.T) {
			w := doOp(t, ro, op, "invalid-json")
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, ErrTypeInvalidParameterException)
		})
	}
}

// ──── GetUserPool storage error (non-pool-not-found) ────────────────────────

func TestAdminOps_GetPoolStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: errors.New("storage error")}}
	tests := []struct {
		op   string
		body map[string]any
	}{
		{"AdminCreateUser", map[string]any{"UserPoolId": "us-east-1_X", "Username": "u"}},
		{"AdminGetUser", map[string]any{"UserPoolId": "us-east-1_X", "Username": "u"}},
		{"AdminSetUserPassword", map[string]any{
			"UserPoolId": "us-east-1_X", "Username": "u", "Password": "ValidPass1!",
		}},
		{"AdminConfirmSignUp", map[string]any{"UserPoolId": "us-east-1_X", "Username": "u"}},
		{"AdminDeleteUser", map[string]any{"UserPoolId": "us-east-1_X", "Username": "u"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			b, _ := json.Marshal(tc.body)
			w := doOp(t, ro, tc.op, string(b))
			assert.Equal(t, http.StatusInternalServerError, w.Code)
			assertErrType(t, w, ErrTypeInternalErrorException)
		})
	}
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

// ──── ListUsers ──────────────────────────────────────────────────────────────

func TestListUsers_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createAdminUser(t, ro, poolID, "alice")
	createAdminUser(t, ro, poolID, "bob")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID})
	w := doOp(t, ro, "ListUsers", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Users []struct {
			Username   string          `json:"Username"`
			Attributes []AttributeType `json:"Attributes"`
			Enabled    bool            `json:"Enabled"`
			UserStatus string          `json:"UserStatus"`
		} `json:"Users"`
		PaginationToken string `json:"PaginationToken"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Users, 2)
	assert.Equal(t, "alice", resp.Users[0].Username)
	assert.Equal(t, "bob", resp.Users[1].Username)
	assert.True(t, resp.Users[0].Enabled)
	assert.Equal(t, "sub", resp.Users[0].Attributes[0].Name)
	assert.Empty(t, resp.PaginationToken)
}

func TestListUsers_Pagination(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	for _, u := range []string{"charlie", "alice", "bob"} {
		createAdminUser(t, ro, poolID, u)
	}

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "Limit": 2})
	w := doOp(t, ro, "ListUsers", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Users           []struct{ Username string } `json:"Users"`
		PaginationToken string                      `json:"PaginationToken"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Users, 2)
	assert.Equal(t, "alice", resp.Users[0].Username)
	assert.Equal(t, "bob", resp.Users[1].Username)
	require.NotEmpty(t, resp.PaginationToken)

	body2, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "Limit": 2, "PaginationToken": resp.PaginationToken,
	})
	w2 := doOp(t, ro, "ListUsers", string(body2))
	require.Equal(t, http.StatusOK, w2.Code)
	var resp2 struct {
		Users           []struct{ Username string } `json:"Users"`
		PaginationToken string                      `json:"PaginationToken"`
	}
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp2))
	require.Len(t, resp2.Users, 1)
	assert.Equal(t, "charlie", resp2.Users[0].Username)
	assert.Empty(t, resp2.PaginationToken)
}

func TestListUsers_FilterExactMatch(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createAdminUser(t, ro, poolID, "alice")
	createAdminUser(t, ro, poolID, "bob")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "Filter": `username = "alice"`,
	})
	w := doOp(t, ro, "ListUsers", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Users []struct{ Username string } `json:"Users"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Users, 1)
	assert.Equal(t, "alice", resp.Users[0].Username)
}

func TestListUsers_FilterPrefixMatch(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createAdminUser(t, ro, poolID, "alice")
	createAdminUser(t, ro, poolID, "alicia")
	createAdminUser(t, ro, poolID, "bob")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "Filter": `username ^= "ali"`,
	})
	w := doOp(t, ro, "ListUsers", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Users []struct{ Username string } `json:"Users"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Users, 2)
}

func TestListUsers_FilterByEmailAttribute(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	body, _ := json.Marshal(map[string]any{
		"UserPoolId":     poolID,
		"Username":       "alice",
		"UserAttributes": []map[string]string{{"Name": "email", "Value": "alice@example.com"}},
	})
	require.Equal(t, http.StatusOK, doOp(t, ro, "AdminCreateUser", string(body)).Code)
	createAdminUser(t, ro, poolID, "bob")

	listBody, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "Filter": `email = "alice@example.com"`,
	})
	w := doOp(t, ro, "ListUsers", string(listBody))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Users []struct{ Username string } `json:"Users"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Users, 1)
	assert.Equal(t, "alice", resp.Users[0].Username)
}

func TestListUsers_FilterCognitoUserStatus(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createAdminUser(t, ro, poolID, "alice") // CONFIRMED (no temp password)

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "Filter": `cognito:user_status = "confirmed"`,
	})
	w := doOp(t, ro, "ListUsers", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Users []struct{ Username string } `json:"Users"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Users, 1)
}

func TestListUsers_FilterBySub(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createAdminUser(t, ro, poolID, "alice")

	getBody, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "Username": "alice"})
	gw := doOp(t, ro, "AdminGetUser", string(getBody))
	require.Equal(t, http.StatusOK, gw.Code)
	var getResp struct {
		UserAttributes []AttributeType `json:"UserAttributes"`
	}
	require.NoError(t, json.NewDecoder(gw.Body).Decode(&getResp))
	sub := getResp.UserAttributes[0].Value
	require.NotEmpty(t, sub)

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "Filter": fmt.Sprintf(`sub = "%s"`, sub),
	})
	w := doOp(t, ro, "ListUsers", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Users []struct{ Username string } `json:"Users"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Users, 1)
	assert.Equal(t, "alice", resp.Users[0].Username)
}

func TestListUsers_FilterByStatusEnabled(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createAdminUser(t, ro, poolID, "alice")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "Filter": `status = "true"`,
	})
	w := doOp(t, ro, "ListUsers", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Users []struct{ Username string } `json:"Users"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Users, 1)
}

func TestListUsers_FilterNoMatch(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createAdminUser(t, ro, poolID, "alice")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "Filter": `email = "nobody@example.com"`,
	})
	w := doOp(t, ro, "ListUsers", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Users []struct{ Username string } `json:"Users"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Empty(t, resp.Users)
}

func TestListUsers_ValidationErrors(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	tests := []struct {
		name string
		body map[string]any
	}{
		{"missing UserPoolId", map[string]any{}},
		{"limit too low", map[string]any{"UserPoolId": poolID, "Limit": 0}},
		{"limit too high", map[string]any{"UserPoolId": poolID, "Limit": 61}},
		{"malformed filter", map[string]any{"UserPoolId": poolID, "Filter": "not a filter"}},
		{
			"unsupported filter attribute",
			map[string]any{"UserPoolId": poolID, "Filter": `custom:foo = "bar"`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.body)
			w := doOp(t, ro, "ListUsers", string(b))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, ErrTypeInvalidParameterException)
		})
	}
}

func TestListUsers_InvalidPaginationToken(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createAdminUser(t, ro, poolID, "alice")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "PaginationToken": "bogus",
	})
	w := doOp(t, ro, "ListUsers", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestListUsers_UserPoolNotFound(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]any{"UserPoolId": "us-east-1_nonexistent"})
	w := doOp(t, ro, "ListUsers", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeResourceNotFoundException)
}

func TestListUsers_GetUserPoolStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: errors.New("storage error")}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "us-east-1_X"})
	w := doOp(t, ro, "ListUsers", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestListUsers_ListUsersStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{listUsersErr: errors.New("storage error")}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "us-east-1_X"})
	w := doOp(t, ro, "ListUsers", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}
