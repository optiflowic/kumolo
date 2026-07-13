package cognito

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// verifyAttr marks attrName as verified for username, bypassing the code flow.
func verifyAttr(t *testing.T, ro *Router, poolID, username, attrName, value string) {
	t.Helper()
	err := ro.storage.UpdateUser(poolID, username, func(u *UserMetadata) error {
		u.Attributes = setAttr(u.Attributes, attrName, value)
		u.Attributes = setAttr(u.Attributes, attrName+"_verified", "true")
		return nil
	})
	require.NoError(t, err)
}

// ── ForgotPassword ───────────────────────────────────────────────────────────

func TestForgotPassword_Success_EmailVerified(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")
	verifyAttr(t, ro, poolID, "alice", attrEmail, "alice@example.com")

	body, _ := json.Marshal(map[string]string{"ClientId": clientID, "Username": "alice"})
	w := doOp(t, ro, "ForgotPassword", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp forgotPasswordResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, attrEmail, resp.CodeDeliveryDetails.AttributeName)
	assert.Equal(t, deliveryEmail, resp.CodeDeliveryDetails.DeliveryMedium)
	assert.Equal(t, "a***@example.com", resp.CodeDeliveryDetails.Destination)

	user, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.NotEmpty(t, user.PasswordResetCode)
}

func TestForgotPassword_Success_PhoneVerifiedOnly(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")
	verifyAttr(t, ro, poolID, "alice", attrPhoneNumber, "+15551234567")

	body, _ := json.Marshal(map[string]string{"ClientId": clientID, "Username": "alice"})
	w := doOp(t, ro, "ForgotPassword", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp forgotPasswordResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, attrPhoneNumber, resp.CodeDeliveryDetails.AttributeName)
	assert.Equal(t, deliverySMS, resp.CodeDeliveryDetails.DeliveryMedium)
}

func TestForgotPassword_DefaultPrefersPhoneOverEmail(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")
	verifyAttr(t, ro, poolID, "alice", attrPhoneNumber, "+15551234567")
	verifyAttr(t, ro, poolID, "alice", attrEmail, "alice@example.com")

	body, _ := json.Marshal(map[string]string{"ClientId": clientID, "Username": "alice"})
	w := doOp(t, ro, "ForgotPassword", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp forgotPasswordResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, attrPhoneNumber, resp.CodeDeliveryDetails.AttributeName)
}

// TestForgotPassword_RecoveryMechanismSelection covers pool-configured
// AccountRecoverySetting priority ordering: honoring a custom order, falling back to
// the next mechanism when the preferred one is unverified, and rejecting recovery
// entirely when only admin_only is configured.
func TestForgotPassword_RecoveryMechanismSelection(t *testing.T) {
	customOrder := json.RawMessage(
		`{"RecoveryMechanisms":[{"Name":"verified_email","Priority":1},` +
			`{"Name":"verified_phone_number","Priority":2}]}`,
	)
	tests := []struct {
		name                   string
		accountRecoverySetting json.RawMessage
		verifyAttrs            func(t *testing.T, ro *Router, poolID string)
		wantStatus             int
		wantAttributeName      string
	}{
		{
			name:                   "honors custom priority order",
			accountRecoverySetting: customOrder,
			verifyAttrs: func(t *testing.T, ro *Router, poolID string) {
				verifyAttr(t, ro, poolID, "alice", attrPhoneNumber, "+15551234567")
				verifyAttr(t, ro, poolID, "alice", attrEmail, "alice@example.com")
			},
			wantStatus:        http.StatusOK,
			wantAttributeName: attrEmail,
		},
		{
			name:                   "falls back to secondary mechanism",
			accountRecoverySetting: customOrder,
			verifyAttrs: func(t *testing.T, ro *Router, poolID string) {
				verifyAttr(t, ro, poolID, "alice", attrPhoneNumber, "+15551234567")
			},
			wantStatus:        http.StatusOK,
			wantAttributeName: attrPhoneNumber,
		},
		{
			name: "admin_only rejects self-service recovery",
			accountRecoverySetting: json.RawMessage(
				`{"RecoveryMechanisms":[{"Name":"admin_only","Priority":1}]}`,
			),
			verifyAttrs: func(t *testing.T, ro *Router, poolID string) {
				verifyAttr(t, ro, poolID, "alice", attrEmail, "alice@example.com")
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ro := newTestRouter(t)
			poolID, clientID := setupPool(t, ro)
			require.NoError(t, ro.storage.UpdateUserPool(poolID, func(m *UserPoolMetadata) error {
				m.AccountRecoverySetting = tc.accountRecoverySetting
				return nil
			}))
			signUpUser(t, ro, clientID, "alice", "Password123!")
			confirmUser(t, ro, clientID, "alice")
			tc.verifyAttrs(t, ro, poolID)

			body, _ := json.Marshal(map[string]string{"ClientId": clientID, "Username": "alice"})
			w := doOp(t, ro, "ForgotPassword", string(body))

			require.Equal(t, tc.wantStatus, w.Code)
			if tc.wantStatus == http.StatusOK {
				var resp forgotPasswordResponse
				require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
				assert.Equal(t, tc.wantAttributeName, resp.CodeDeliveryDetails.AttributeName)
			} else {
				assertErrType(t, w, ErrTypeInvalidParameterException)
			}
		})
	}
}

// TestSortedRecoveryMechanisms_MalformedJSONFallsBackToDefault is a regression test:
// an unparseable AccountRecoverySetting must not error, it must fall back to the same
// default order used when the setting is absent.
func TestSortedRecoveryMechanisms_MalformedJSONFallsBackToDefault(t *testing.T) {
	got := sortedRecoveryMechanisms(json.RawMessage(`{not valid json`))
	assert.Equal(t, defaultRecoveryMechanisms(), got)
}

func TestForgotPassword_NoVerifiedContact(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	body, _ := json.Marshal(map[string]string{"ClientId": clientID, "Username": "alice"})
	w := doOp(t, ro, "ForgotPassword", string(body))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestForgotPassword_MissingClientId(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ForgotPassword", `{"Username":"alice"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestForgotPassword_MissingUsername(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]string{"ClientId": clientID})
	w := doOp(t, ro, "ForgotPassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestForgotPassword_InvalidJSON(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ForgotPassword", `{invalid}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestForgotPassword_ClientNotFound(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]string{"ClientId": "nonexistent", "Username": "alice"})
	w := doOp(t, ro, "ForgotPassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeResourceNotFoundException)
}

func TestForgotPassword_UserNotFound(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]string{"ClientId": clientID, "Username": "ghost"})
	w := doOp(t, ro, "ForgotPassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestForgotPassword_ClientLookupStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "", errors.New("storage error") },
	}}
	body, _ := json.Marshal(map[string]string{"ClientId": "c", "Username": "alice"})
	w := doOp(t, ro, "ForgotPassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestForgotPassword_GetUserPoolStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getErr:           errors.New("storage error"),
	}}
	body, _ := json.Marshal(map[string]string{"ClientId": "c", "Username": "alice"})
	w := doOp(t, ro, "ForgotPassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestForgotPassword_GetUserStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getUserFn: func(string, string) (*UserMetadata, error) {
			return nil, errors.New("storage error")
		},
	}}
	body, _ := json.Marshal(map[string]string{"ClientId": "c", "Username": "alice"})
	w := doOp(t, ro, "ForgotPassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestForgotPassword_CodeGenerationError(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")
	verifyAttr(t, ro, poolID, "alice", attrEmail, "alice@example.com")
	ro.codeReader = &errorReader{err: errors.New("entropy failed")}

	body, _ := json.Marshal(map[string]string{"ClientId": clientID, "Username": "alice"})
	w := doOp(t, ro, "ForgotPassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestForgotPassword_UpdateUserStorageError(t *testing.T) {
	verifiedUser := &UserMetadata{
		Username: "alice",
		Attributes: []AttributeType{
			{Name: "email", Value: "alice@example.com"},
			{Name: "email_verified", Value: "true"},
		},
	}
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getUserFn: func(string, string) (*UserMetadata, error) {
			return verifiedUser, nil
		},
		updateUserErr: errors.New("storage error"),
	}}
	body, _ := json.Marshal(map[string]string{"ClientId": "c", "Username": "alice"})
	w := doOp(t, ro, "ForgotPassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestForgotPassword_UpdateUserNotFound(t *testing.T) {
	verifiedUser := &UserMetadata{
		Username: "alice",
		Attributes: []AttributeType{
			{Name: "email", Value: "alice@example.com"},
			{Name: "email_verified", Value: "true"},
		},
	}
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getUserFn: func(string, string) (*UserMetadata, error) {
			return verifiedUser, nil
		},
		updateUserErr: errUserNotFound,
	}}
	body, _ := json.Marshal(map[string]string{"ClientId": "c", "Username": "alice"})
	w := doOp(t, ro, "ForgotPassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

// ── ConfirmForgotPassword ────────────────────────────────────────────────────

// requestPasswordReset calls ForgotPassword and returns the stored reset code.
func requestPasswordReset(t *testing.T, ro *Router, poolID, clientID, username string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"ClientId": clientID, "Username": username})
	w := doOp(t, ro, "ForgotPassword", string(body))
	require.Equal(t, http.StatusOK, w.Code)
	user, err := ro.storage.GetUser(poolID, username)
	require.NoError(t, err)
	return user.PasswordResetCode
}

func TestConfirmForgotPassword_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")
	verifyAttr(t, ro, poolID, "alice", attrEmail, "alice@example.com")
	code := requestPasswordReset(t, ro, poolID, clientID, "alice")

	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Username": "alice",
		"ConfirmationCode": code, "Password": "NewPassword456!",
	})
	w := doOp(t, ro, "ConfirmForgotPassword", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	user, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.Empty(t, user.PasswordResetCode)

	// New password works, old one no longer does.
	authW := doInitAuth(t, ro, clientID, "alice", "NewPassword456!")
	assert.Equal(t, http.StatusOK, authW.Code)
	oldW := doInitAuth(t, ro, clientID, "alice", "Password123!")
	assertErrType(t, oldW, ErrTypeNotAuthorizedException)
}

func TestConfirmForgotPassword_MissingFields(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	tests := []struct {
		name string
		body map[string]string
	}{
		{"missing ClientId", map[string]string{
			"Username": "alice", "ConfirmationCode": "123456", "Password": "NewPassword456!",
		}},
		{"missing Username", map[string]string{
			"ClientId": clientID, "ConfirmationCode": "123456", "Password": "NewPassword456!",
		}},
		{"missing ConfirmationCode", map[string]string{
			"ClientId": clientID, "Username": "alice", "Password": "NewPassword456!",
		}},
		{"missing Password", map[string]string{
			"ClientId": clientID, "Username": "alice", "ConfirmationCode": "123456",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			w := doOp(t, ro, "ConfirmForgotPassword", string(body))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, ErrTypeInvalidParameterException)
		})
	}
}

func TestConfirmForgotPassword_InvalidJSON(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ConfirmForgotPassword", `{invalid}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestConfirmForgotPassword_ClientNotFound(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]string{
		"ClientId": "nonexistent", "Username": "alice",
		"ConfirmationCode": "123456", "Password": "NewPassword456!",
	})
	w := doOp(t, ro, "ConfirmForgotPassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeResourceNotFoundException)
}

func TestConfirmForgotPassword_InvalidPassword(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")
	verifyAttr(t, ro, poolID, "alice", attrEmail, "alice@example.com")
	code := requestPasswordReset(t, ro, poolID, clientID, "alice")

	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Username": "alice",
		"ConfirmationCode": code, "Password": "short",
	})
	w := doOp(t, ro, "ConfirmForgotPassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidPasswordException)

	// The pending code must still be usable after a rejected password.
	user, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.Equal(t, code, user.PasswordResetCode)
}

func TestConfirmForgotPassword_UserNotFound(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Username": "ghost",
		"ConfirmationCode": "123456", "Password": "NewPassword456!",
	})
	w := doOp(t, ro, "ConfirmForgotPassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestConfirmForgotPassword_UserNotConfirmed(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")

	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Username": "alice",
		"ConfirmationCode": "123456", "Password": "NewPassword456!",
	})
	w := doOp(t, ro, "ConfirmForgotPassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotConfirmedException)
}

func TestConfirmForgotPassword_CodeMismatch(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")
	verifyAttr(t, ro, poolID, "alice", attrEmail, "alice@example.com")
	requestPasswordReset(t, ro, poolID, clientID, "alice")

	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Username": "alice",
		"ConfirmationCode": "000000", "Password": "NewPassword456!",
	})
	w := doOp(t, ro, "ConfirmForgotPassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeCodeMismatchException)
}

func TestConfirmForgotPassword_NoCodePending(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	body, _ := json.Marshal(map[string]string{
		"ClientId": clientID, "Username": "alice",
		"ConfirmationCode": "123456", "Password": "NewPassword456!",
	})
	w := doOp(t, ro, "ConfirmForgotPassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeCodeMismatchException)
}

func TestConfirmForgotPassword_ClientLookupStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "", errors.New("storage error") },
	}}
	body, _ := json.Marshal(map[string]string{
		"ClientId": "c", "Username": "alice",
		"ConfirmationCode": "123456", "Password": "NewPassword456!",
	})
	w := doOp(t, ro, "ConfirmForgotPassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestConfirmForgotPassword_GetUserPoolStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		getErr:           errors.New("storage error"),
	}}
	body, _ := json.Marshal(map[string]string{
		"ClientId": "c", "Username": "alice",
		"ConfirmationCode": "123456", "Password": "NewPassword456!",
	})
	w := doOp(t, ro, "ConfirmForgotPassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestConfirmForgotPassword_UpdateUserStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "pool-1", nil },
		updateUserErr:    errors.New("storage error"),
	}}
	body, _ := json.Marshal(map[string]string{
		"ClientId": "c", "Username": "alice",
		"ConfirmationCode": "123456", "Password": "NewPassword456!",
	})
	w := doOp(t, ro, "ConfirmForgotPassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

// TestConfirmForgotPassword_HashError covers hashPassword's error path via an
// invalid bcrypt cost (injectable through Router.bcryptCost).
func TestConfirmForgotPassword_HashError(t *testing.T) {
	ro := &Router{
		bcryptCost: bcrypt.MaxCost + 1,
		storage: &mockStore{
			getPoolForClient: func(string) (string, error) { return "pool-1", nil },
			updateUserFn: func(_, _ string, fn func(*UserMetadata) error) error {
				u := &UserMetadata{Status: userStatusConfirmed, PasswordResetCode: "123456"}
				return fn(u)
			},
		},
	}
	body, _ := json.Marshal(map[string]string{
		"ClientId": "c", "Username": "alice",
		"ConfirmationCode": "123456", "Password": "NewPassword456!",
	})
	w := doOp(t, ro, "ConfirmForgotPassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

// ── ChangePassword ───────────────────────────────────────────────────────────

func TestChangePassword_Success(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")

	body, _ := json.Marshal(map[string]string{
		"AccessToken":      token,
		"PreviousPassword": "Password123!",
		"ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	newW := doInitAuth(t, ro, clientID, "alice", "NewPassword456!")
	assert.Equal(t, http.StatusOK, newW.Code)
	oldW := doInitAuth(t, ro, clientID, "alice", "Password123!")
	assertErrType(t, oldW, ErrTypeNotAuthorizedException)
}

func TestChangePassword_MissingAccessToken(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ChangePassword", `{"ProposedPassword":"NewPassword456!"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestChangePassword_MissingProposedPassword(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "PreviousPassword": "Password123!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestChangePassword_InvalidJSON(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ChangePassword", `{invalid}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestChangePassword_InvalidAccessToken(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]string{
		"AccessToken": "only.two", "ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestChangePassword_MissingPreviousPassword(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

// TestChangePassword_PasswordlessUser_Success covers a user created via
// AdminCreateUser without a TemporaryPassword (empty PasswordHash): ChangePassword
// must succeed without a PreviousPassword, and the new password must authenticate.
func TestChangePassword_PasswordlessUser_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	createAdminUser(t, ro, poolID, "alice")

	user, err := ro.storage.GetUser(poolID, "alice")
	require.NoError(t, err)
	require.Empty(t, user.PasswordHash)

	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)
	token, _, _, _, _, err := issueTokens(
		privateKey,
		keys.KeyID,
		poolID,
		clientID,
		user,
		nil,
		"",
		accessTokenExpiry,
		accessTokenExpiry,
	)
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	require.Equal(t, http.StatusOK, w.Code, "changePassword failed: %s", w.Body.String())

	newW := doInitAuth(t, ro, clientID, "alice", "NewPassword456!")
	assert.Equal(t, http.StatusOK, newW.Code)
}

func TestChangePassword_WrongPreviousPassword(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "PreviousPassword": "WrongPassword1!",
		"ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestChangePassword_InvalidProposedPassword(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	token := doAuth(t, ro, clientID, "alice", "Password123!")
	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "PreviousPassword": "Password123!", "ProposedPassword": "short",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidPasswordException)
}

func TestChangePassword_KeysStorageError(t *testing.T) {
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
	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestChangePassword_WrongTokenUse(t *testing.T) {
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
		"sub": user.Sub, "iss": issuerURL(poolID), "token_use": "id",
		"exp": now + 3600, "iat": now,
	})
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestChangePassword_RevokedToken(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key,
		"kid",
		poolID,
		"client-1",
		user,
		nil,
		"",
		accessTokenExpiry,
		accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		isRevokedFn: func(string, string) (bool, error) { return true, nil },
	}}
	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestChangePassword_UserNotFound(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key,
		"kid",
		poolID,
		"client-1",
		user,
		nil,
		"",
		accessTokenExpiry,
		accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
	}}
	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestChangePassword_GetUserPoolStorageError(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key,
		"kid",
		poolID,
		"client-1",
		user,
		nil,
		"",
		accessTokenExpiry,
		accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		getUserBySubFn: func(string, string) (*UserMetadata, error) { return user, nil },
		getErr:         errors.New("storage error"),
	}}
	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestChangePassword_UpdateUserStorageError(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key,
		"kid",
		poolID,
		"client-1",
		user,
		nil,
		"",
		accessTokenExpiry,
		accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		getUserBySubFn: func(string, string) (*UserMetadata, error) { return user, nil },
		updateUserErr:  errors.New("storage error"),
	}}
	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

// TestChangePassword_UpdateUserNotFound covers the TOCTOU race where lookupUser
// (keyed by sub) succeeds but UpdateUser (keyed by username) no longer finds the
// user, e.g. because it was deleted in between.
func TestChangePassword_UpdateUserNotFound(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key,
		"kid",
		poolID,
		"client-1",
		user,
		nil,
		"",
		accessTokenExpiry,
		accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{storage: &mockStore{
		getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return &poolKeys{KeyID: "kid"}, key, nil
		},
		getUserBySubFn: func(string, string) (*UserMetadata, error) { return user, nil },
		updateUserErr:  errUserNotFound,
	}}
	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

// TestChangePassword_HashError covers hashPassword's error path via an invalid
// bcrypt cost (injectable through Router.bcryptCost).
func TestChangePassword_HashError(t *testing.T) {
	key := testRSAKey(t)
	poolID := "us-east-1_TestPool"
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	token, _, _, _, _, err := issueTokens(
		key,
		"kid",
		poolID,
		"client-1",
		user,
		nil,
		"",
		accessTokenExpiry,
		accessTokenExpiry,
	)
	require.NoError(t, err)

	ro := &Router{
		bcryptCost: bcrypt.MaxCost + 1,
		storage: &mockStore{
			getPoolKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
				return &poolKeys{KeyID: "kid"}, key, nil
			},
			getUserBySubFn: func(string, string) (*UserMetadata, error) { return user, nil },
			updateUserFn: func(_, _ string, fn func(*UserMetadata) error) error {
				return fn(user)
			},
		},
	}
	body, _ := json.Marshal(map[string]string{
		"AccessToken": token, "ProposedPassword": "NewPassword456!",
	})
	w := doOp(t, ro, "ChangePassword", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}
