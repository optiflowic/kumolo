package cognito

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// ──── AdminCreateUser ────────────────────────────────────────────────────────

type adminCreateUserRequest struct {
	UserPoolID        string          `json:"UserPoolId"`
	Username          string          `json:"Username"`
	TemporaryPassword string          `json:"TemporaryPassword"`
	UserAttributes    []AttributeType `json:"UserAttributes"`
	MessageAction     string          `json:"MessageAction"`
}

type userTypeResponse struct {
	Username             string          `json:"Username"`
	Attributes           []AttributeType `json:"Attributes"`
	UserCreateDate       float64         `json:"UserCreateDate"`
	UserLastModifiedDate float64         `json:"UserLastModifiedDate"`
	Enabled              bool            `json:"Enabled"`
	UserStatus           string          `json:"UserStatus"`
	MFAOptions           []any           `json:"MFAOptions"`
}

func (ro *Router) handleAdminCreateUser(w http.ResponseWriter, body []byte) {
	var req adminCreateUserRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.UserPoolID == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"UserPoolId is required",
		)
		return
	}
	if req.Username == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"Username is required",
		)
		return
	}
	if req.TemporaryPassword != "" && len(req.TemporaryPassword) < minPasswordLen {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidPasswordException,
			fmt.Sprintf("Password must be at least %d characters", minPasswordLen))
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user pool")
		return
	}

	if req.MessageAction == "RESEND" {
		// RESEND requires the user to already exist; kumolo returns the existing
		// record without resending any message (no message delivery support).
		user, err := ro.storage.GetUser(req.UserPoolID, req.Username)
		if err != nil {
			if errors.Is(err, errUserNotFound) {
				writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
					"User does not exist.")
				return
			}
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to get user")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"User": userTypeResponse{
				Username:             user.Username,
				Attributes:           prependSub(user.Attributes, user.Sub),
				UserCreateDate:       user.CreatedAt,
				UserLastModifiedDate: user.UpdatedAt,
				Enabled:              true,
				UserStatus:           user.Status,
				MFAOptions:           []any{},
			},
		})
		return
	}

	sub, err := generateTokenID()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to generate user ID")
		return
	}

	var passwordHash string
	status := userStatusConfirmed
	if req.TemporaryPassword != "" {
		hash, herr := bcrypt.GenerateFromPassword([]byte(req.TemporaryPassword), bcrypt.DefaultCost)
		if herr != nil {
			// untestable: bcrypt.GenerateFromPassword only fails on invalid cost or OOM
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to hash password")
			return
		}
		passwordHash = string(hash)
		status = userStatusForceChangePasswd
	}

	ts := nowUnix()
	user := &UserMetadata{
		Username:     req.Username,
		Sub:          sub,
		Status:       status,
		PasswordHash: passwordHash,
		Attributes:   req.UserAttributes,
		CreatedAt:    ts,
		UpdatedAt:    ts,
	}

	if err := ro.storage.CreateUser(req.UserPoolID, user); err != nil {
		if errors.Is(err, errUsernameExists) {
			writeError(w, http.StatusBadRequest, ErrTypeUsernameExistsException,
				"An account with the given username already exists.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to create user")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"User": userTypeResponse{
			Username:             user.Username,
			Attributes:           prependSub(user.Attributes, user.Sub),
			UserCreateDate:       user.CreatedAt,
			UserLastModifiedDate: user.UpdatedAt,
			Enabled:              true,
			UserStatus:           user.Status,
			MFAOptions:           []any{},
		},
	})
}

// ──── AdminGetUser ───────────────────────────────────────────────────────────

type adminGetUserRequest struct {
	UserPoolID string `json:"UserPoolId"`
	Username   string `json:"Username"`
}

type adminGetUserResponse struct {
	Username             string          `json:"Username"`
	UserAttributes       []AttributeType `json:"UserAttributes"`
	UserCreateDate       float64         `json:"UserCreateDate"`
	UserLastModifiedDate float64         `json:"UserLastModifiedDate"`
	Enabled              bool            `json:"Enabled"`
	UserStatus           string          `json:"UserStatus"`
	MFAOptions           []any           `json:"MFAOptions"`
	UserMFASettingList   []string        `json:"UserMFASettingList"`
	PreferredMfaSetting  string          `json:"PreferredMfaSetting"`
}

func (ro *Router) handleAdminGetUser(w http.ResponseWriter, body []byte) {
	var req adminGetUserRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.UserPoolID == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"UserPoolId is required",
		)
		return
	}
	if req.Username == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"Username is required",
		)
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user pool")
		return
	}

	user, err := ro.storage.GetUser(req.UserPoolID, req.Username)
	if err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user")
		return
	}

	writeJSON(w, http.StatusOK, adminGetUserResponse{
		Username:             user.Username,
		UserAttributes:       prependSub(user.Attributes, user.Sub),
		UserCreateDate:       user.CreatedAt,
		UserLastModifiedDate: user.UpdatedAt,
		Enabled:              true,
		UserStatus:           user.Status,
		MFAOptions:           []any{},
		UserMFASettingList:   []string{},
		PreferredMfaSetting:  "",
	})
}

// ──── AdminSetUserPassword ───────────────────────────────────────────────────

type adminSetUserPasswordRequest struct {
	UserPoolID string `json:"UserPoolId"`
	Username   string `json:"Username"`
	Password   string `json:"Password"`
	Permanent  bool   `json:"Permanent"`
}

func (ro *Router) handleAdminSetUserPassword(w http.ResponseWriter, body []byte) {
	var req adminSetUserPasswordRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.UserPoolID == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"UserPoolId is required",
		)
		return
	}
	if req.Username == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"Username is required",
		)
		return
	}
	if req.Password == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"Password is required",
		)
		return
	}
	if len(req.Password) < minPasswordLen {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidPasswordException,
			fmt.Sprintf("Password must be at least %d characters", minPasswordLen))
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user pool")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		// untestable: bcrypt.GenerateFromPassword only fails on invalid cost or OOM
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to hash password")
		return
	}
	newHash := string(hash)

	newStatus := userStatusForceChangePasswd
	if req.Permanent {
		newStatus = userStatusConfirmed
	}

	err = ro.storage.UpdateUser(req.UserPoolID, req.Username, func(u *UserMetadata) error {
		u.PasswordHash = newHash
		u.Status = newStatus
		return nil
	})
	if err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to update user")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

// ──── AdminConfirmSignUp ─────────────────────────────────────────────────────

type adminConfirmSignUpRequest struct {
	UserPoolID string `json:"UserPoolId"`
	Username   string `json:"Username"`
}

func (ro *Router) handleAdminConfirmSignUp(w http.ResponseWriter, body []byte) {
	var req adminConfirmSignUpRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.UserPoolID == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"UserPoolId is required",
		)
		return
	}
	if req.Username == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"Username is required",
		)
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user pool")
		return
	}

	err := ro.storage.UpdateUser(req.UserPoolID, req.Username, func(u *UserMetadata) error {
		if u.Status == userStatusUnconfirmed {
			u.Status = userStatusConfirmed
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to confirm user")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

// ──── AdminDeleteUser ────────────────────────────────────────────────────────

type adminDeleteUserRequest struct {
	UserPoolID string `json:"UserPoolId"`
	Username   string `json:"Username"`
}

func (ro *Router) handleAdminDeleteUser(w http.ResponseWriter, body []byte) {
	var req adminDeleteUserRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.UserPoolID == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"UserPoolId is required",
		)
		return
	}
	if req.Username == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"Username is required",
		)
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user pool")
		return
	}

	if err := ro.storage.DeleteUser(req.UserPoolID, req.Username); err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to delete user")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}
