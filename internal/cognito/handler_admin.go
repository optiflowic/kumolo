package cognito

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

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

func newUserTypeResponse(u *UserMetadata) userTypeResponse {
	return userTypeResponse{
		Username:             u.Username,
		Attributes:           prependSub(u.Attributes, u.Sub),
		UserCreateDate:       u.CreatedAt,
		UserLastModifiedDate: u.UpdatedAt,
		Enabled:              u.Enabled,
		UserStatus:           u.Status,
		MFAOptions:           []any{},
	}
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
		writeJSON(w, http.StatusOK, map[string]any{"User": newUserTypeResponse(user)})
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
		Enabled:      true,
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

	writeJSON(w, http.StatusOK, map[string]any{"User": newUserTypeResponse(user)})
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
		Enabled:              user.Enabled,
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

// ──── AdminUpdateUserAttributes ──────────────────────────────────────────────

type adminUpdateUserAttributesRequest struct {
	UserPoolID     string          `json:"UserPoolId"`
	Username       string          `json:"Username"`
	UserAttributes []AttributeType `json:"UserAttributes"`
}

// planAdminAttributeChanges computes attribute mutations for AdminUpdateUserAttributes.
// Unlike the self-service UpdateUserAttributes flow, an admin can bypass the
// verification-code step for email/phone_number by including the paired
// "_verified" attribute set to "true" in the same request.
func planAdminAttributeChanges(
	codeR io.Reader,
	current []AttributeType,
	requested []AttributeType,
) ([]attrChange, error) {
	bypass := map[string]bool{}
	requestedNames := map[string]bool{}
	for _, attr := range requested {
		requestedNames[attr.Name] = true
		switch attr.Name {
		case attrEmail + "_verified":
			bypass[attrEmail] = attr.Value == "true"
		case attrPhoneNumber + "_verified":
			bypass[attrPhoneNumber] = attr.Value == "true"
		}
	}

	var changes []attrChange
	for _, attr := range requested {
		if attr.Value == "" {
			changes = append(changes, attrChange{name: attr.Name, delete: true})
			continue
		}

		if attr.Name != attrEmail && attr.Name != attrPhoneNumber {
			changes = append(changes, attrChange{name: attr.Name, value: attr.Value})
			continue
		}

		if bypass[attr.Name] {
			changes = append(
				changes,
				attrChange{name: attr.Name, value: attr.Value, clearCode: true},
			)
			continue
		}

		oldValue, _ := getAttr(current, attr.Name)
		if oldValue == attr.Value {
			changes = append(changes, attrChange{name: attr.Name, value: attr.Value})
			continue
		}

		code, err := generateConfirmationCodeFrom(codeR)
		if err != nil {
			return nil, fmt.Errorf("generate verification code: %w", err)
		}
		changes = append(changes, attrChange{name: attr.Name, value: attr.Value, verifyCode: code})
	}

	// A bypass ("email_verified"/"phone_number_verified" = "true") may arrive
	// without a matching email/phone_number update in the same request — still
	// drop any pending verification code for that attribute.
	for _, name := range []string{attrEmail, attrPhoneNumber} {
		if bypass[name] && !requestedNames[name] {
			changes = append(changes, attrChange{name: name, clearCodeOnly: true})
		}
	}
	return changes, nil
}

func (ro *Router) handleAdminUpdateUserAttributes(w http.ResponseWriter, body []byte) {
	var req adminUpdateUserAttributesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"invalid request body")
		return
	}
	if req.UserPoolID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"UserPoolId is required")
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"Username is required")
		return
	}
	if len(req.UserAttributes) == 0 {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"UserAttributes is required")
		return
	}
	for _, attr := range req.UserAttributes {
		if attr.Name == "sub" {
			writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
				"Attribute modifications are not allowed for sub")
			return
		}
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

	codeR := ro.codeReader
	if codeR == nil {
		codeR = randReader
	}

	changes, err := planAdminAttributeChanges(codeR, user.Attributes, req.UserAttributes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to generate verification code")
		return
	}

	updateErr := ro.storage.UpdateUser(req.UserPoolID, req.Username, func(u *UserMetadata) error {
		applyAttributeChanges(u, changes)
		return nil
	})
	if updateErr != nil {
		if errors.Is(updateErr, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to update user attributes")
		return
	}

	writeEmpty(w)
}

// ──── ListUsers ──────────────────────────────────────────────────────────────

const listUsersDefaultLimit = 60

type listUsersRequest struct {
	UserPoolID      string `json:"UserPoolId"`
	Filter          string `json:"Filter"`
	Limit           *int   `json:"Limit"`
	PaginationToken string `json:"PaginationToken"`
}

// reUserFilter matches ListUsers' Filter grammar: AttributeName Op "Value",
// where Op is "=" (exact match) or "^=" (prefix match).
var reUserFilter = regexp.MustCompile(`^\s*([\w:]+)\s*(\^?=)\s*"(.*)"\s*$`)

// parseUserFilter compiles a ListUsers Filter string into a predicate over UserMetadata.
// A nil predicate (with nil error) means "match everything" (empty filter).
func parseUserFilter(filterStr string) (func(*UserMetadata) bool, error) {
	if filterStr == "" {
		return nil, nil
	}
	m := reUserFilter.FindStringSubmatch(filterStr)
	if m == nil {
		return nil, errors.New(
			`filter must have the form: AttributeName = "Value" or AttributeName ^= "Value"`,
		)
	}
	attrName, op, value := m[1], m[2], m[3]

	var extract func(*UserMetadata) (string, bool)
	switch attrName {
	case "username":
		extract = func(u *UserMetadata) (string, bool) { return u.Username, true }
	case "sub":
		extract = func(u *UserMetadata) (string, bool) { return u.Sub, true }
	case "cognito:user_status":
		value = strings.ToLower(value)
		extract = func(u *UserMetadata) (string, bool) { return strings.ToLower(u.Status), true }
	case "status":
		extract = func(u *UserMetadata) (string, bool) { return strconv.FormatBool(u.Enabled), true }
	case "email", "phone_number", "name", "given_name", "family_name", "preferred_username":
		extract = func(u *UserMetadata) (string, bool) {
			for _, a := range u.Attributes {
				if a.Name == attrName {
					return a.Value, true
				}
			}
			return "", false
		}
	default:
		return nil, fmt.Errorf("unsupported filter attribute: %s", attrName)
	}

	return func(u *UserMetadata) bool {
		v, ok := extract(u)
		if !ok {
			return false
		}
		if op == "^=" {
			return strings.HasPrefix(v, value)
		}
		return v == value
	}, nil
}

func (ro *Router) handleListUsers(w http.ResponseWriter, body []byte) {
	var req listUsersRequest
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

	limit := listUsersDefaultLimit
	if req.Limit != nil {
		limit = *req.Limit
		if limit < 1 || limit > listUsersDefaultLimit {
			writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
				"Limit must be between 1 and 60")
			return
		}
	}

	filter, err := parseUserFilter(req.Filter)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, err.Error())
		return
	}

	users, nextToken, err := ro.storage.ListUsers(
		req.UserPoolID,
		filter,
		limit,
		req.PaginationToken,
	)
	if err != nil {
		if errors.Is(err, errInvalidNextToken) {
			writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
				"Invalid pagination token.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to list users")
		return
	}

	userResps := make([]userTypeResponse, len(users))
	for i, u := range users {
		userResps[i] = newUserTypeResponse(u)
	}

	resp := map[string]any{"Users": userResps}
	if nextToken != "" {
		resp["PaginationToken"] = nextToken
	}
	writeJSON(w, http.StatusOK, resp)
}
