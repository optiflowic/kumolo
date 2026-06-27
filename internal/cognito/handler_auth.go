package cognito

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	minPasswordLen = 8

	userStatusUnconfirmed       = "UNCONFIRMED"
	userStatusConfirmed         = "CONFIRMED"
	userStatusForceChangePasswd = "FORCE_CHANGE_PASSWORD"
)

// randReader is the default entropy source; overridden in tests via Router.codeReader.
var randReader = rand.Reader

func generateConfirmationCodeFrom(r io.Reader) (string, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("read entropy: %w", err)
	}
	n := binary.BigEndian.Uint32(b[:]) % 1_000_000
	return fmt.Sprintf("%06d", n), nil
}

var (
	errNotUnconfirmed       = errors.New("user is not UNCONFIRMED")
	errCodeMismatch         = errors.New("confirmation code mismatch")
	errWrongChallengeStatus = errors.New("user is not in FORCE_CHANGE_PASSWORD state")
)

// ──── SignUp ────────────────────────────────────────────────────────────────

type signUpRequest struct {
	ClientID       string          `json:"ClientId"`
	Username       string          `json:"Username"`
	Password       string          `json:"Password"`
	UserAttributes []AttributeType `json:"UserAttributes"`
}

type codeDeliveryDetails struct {
	AttributeName  string `json:"AttributeName"`
	DeliveryMedium string `json:"DeliveryMedium"`
	Destination    string `json:"Destination"`
}

type signUpResponse struct {
	UserSub             string              `json:"UserSub"`
	UserConfirmed       bool                `json:"UserConfirmed"`
	CodeDeliveryDetails codeDeliveryDetails `json:"CodeDeliveryDetails"`
}

func (ro *Router) handleSignUp(w http.ResponseWriter, body []byte) {
	var req signUpRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.ClientID == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"ClientId is required",
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
	if len(req.Password) < minPasswordLen {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidPasswordException,
			fmt.Sprintf("Password must be at least %d characters", minPasswordLen))
		return
	}

	poolID, err := ro.storage.GetPoolIDForClient(req.ClientID)
	if err != nil {
		if errors.Is(err, errUserPoolClientNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool client not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to resolve client")
		return
	}

	sub, err := generateTokenID()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to generate user ID")
		return
	}

	codeR := ro.codeReader
	if codeR == nil {
		codeR = randReader
	}
	code, err := generateConfirmationCodeFrom(codeR)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to generate confirmation code")
		return
	}

	var passwordHash string
	if req.Password != "" {
		hash, herr := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if herr != nil {
			// untestable: bcrypt.GenerateFromPassword only fails on invalid cost (fixed) or OOM
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to hash password")
			return
		}
		passwordHash = string(hash)
	}

	ts := nowUnix()
	user := &UserMetadata{
		Username:         req.Username,
		Sub:              sub,
		Status:           userStatusUnconfirmed,
		PasswordHash:     passwordHash,
		Attributes:       req.UserAttributes,
		ConfirmationCode: code,
		CreatedAt:        ts,
		UpdatedAt:        ts,
	}

	if err := ro.storage.CreateUser(poolID, user); err != nil {
		if errors.Is(err, errUsernameExists) {
			writeError(w, http.StatusBadRequest, ErrTypeUsernameExistsException,
				"An account with the given email already exists.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to create user")
		return
	}
	slog.Info("SignUp confirmation code", "pool_id", poolID, "username", req.Username, "code", code)

	dest := "***"
	for _, attr := range req.UserAttributes {
		if attr.Name == "email" {
			dest = maskEmail(attr.Value)
			break
		}
	}

	writeJSON(w, http.StatusOK, signUpResponse{
		UserSub:       sub,
		UserConfirmed: false,
		CodeDeliveryDetails: codeDeliveryDetails{
			AttributeName:  "email",
			DeliveryMedium: "EMAIL",
			Destination:    dest,
		},
	})
}

func maskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return "***"
	}
	return email[:1] + "***" + email[at:]
}

// ──── ConfirmSignUp ─────────────────────────────────────────────────────────

type confirmSignUpRequest struct {
	ClientID         string `json:"ClientId"`
	Username         string `json:"Username"`
	ConfirmationCode string `json:"ConfirmationCode"`
}

func (ro *Router) handleConfirmSignUp(w http.ResponseWriter, body []byte) {
	var req confirmSignUpRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.ClientID == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"ClientId is required",
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
	if req.ConfirmationCode == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"ConfirmationCode is required")
		return
	}

	poolID, err := ro.storage.GetPoolIDForClient(req.ClientID)
	if err != nil {
		if errors.Is(err, errUserPoolClientNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool client not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to resolve client")
		return
	}

	err = ro.storage.UpdateUser(poolID, req.Username, func(u *UserMetadata) error {
		if u.Status != userStatusUnconfirmed {
			return errNotUnconfirmed
		}
		if subtle.ConstantTimeCompare(
			[]byte(req.ConfirmationCode),
			[]byte(u.ConfirmationCode),
		) != 1 {
			return errCodeMismatch
		}
		u.Status = userStatusConfirmed
		u.ConfirmationCode = ""
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errUserNotFound):
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeUserNotFoundException,
				"User does not exist.",
			)
		case errors.Is(err, errNotUnconfirmed):
			writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
				"User cannot be confirmed. Current status is CONFIRMED.")
		case errors.Is(err, errCodeMismatch):
			writeError(w, http.StatusBadRequest, ErrTypeCodeMismatchException,
				"Invalid verification code provided, please try again.")
		default:
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to confirm user")
		}
		return
	}

	writeEmpty(w)
}

// ──── InitiateAuth ──────────────────────────────────────────────────────────

type initiateAuthRequest struct {
	ClientID       string            `json:"ClientId"`
	AuthFlow       string            `json:"AuthFlow"`
	AuthParameters map[string]string `json:"AuthParameters"`
}

type authResult struct {
	AccessToken  string `json:"AccessToken"`
	ExpiresIn    int    `json:"ExpiresIn"`
	IdToken      string `json:"IdToken"`
	RefreshToken string `json:"RefreshToken,omitempty"`
	TokenType    string `json:"TokenType"`
}

func (ro *Router) handleInitiateAuth(w http.ResponseWriter, body []byte) {
	var req initiateAuthRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.ClientID == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"ClientId is required",
		)
		return
	}
	if req.AuthFlow == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"AuthFlow is required",
		)
		return
	}

	poolID, err := ro.storage.GetPoolIDForClient(req.ClientID)
	if err != nil {
		if errors.Is(err, errUserPoolClientNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool client not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to resolve client")
		return
	}

	switch req.AuthFlow {
	case "USER_PASSWORD_AUTH":
		ro.handleUserPasswordAuth(w, poolID, req.ClientID, req.AuthParameters)
	case "REFRESH_TOKEN_AUTH", "REFRESH_TOKEN":
		ro.handleRefreshTokenAuth(w, poolID, req.ClientID, req.AuthParameters)
	default:
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"Unsupported AuthFlow: "+req.AuthFlow)
	}
}

func (ro *Router) handleUserPasswordAuth(
	w http.ResponseWriter,
	poolID, clientID string,
	params map[string]string,
) {
	username := params["USERNAME"]
	password := params["PASSWORD"]
	if username == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"USERNAME is required in AuthParameters")
		return
	}
	if password == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"PASSWORD is required in AuthParameters")
		return
	}

	user, err := ro.storage.GetUser(poolID, username)
	if err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeUserNotFoundException,
				"User does not exist.",
			)
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user")
		return
	}

	if user.Status == userStatusUnconfirmed {
		writeError(w, http.StatusBadRequest, ErrTypeUserNotConfirmedException,
			"User is not confirmed.")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
			"Incorrect username or password.")
		return
	}

	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get pool keys")
		return
	}

	if user.Status == userStatusForceChangePasswd {
		sessionToken, serr := buildSessionToken(
			privateKey, keys.KeyID, poolID, username, "NEW_PASSWORD_REQUIRED",
		)
		if serr != nil {
			// unreachable: buildJWT fails only if claims contain non-serializable types (all primitives here)
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to build session token")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ChallengeName": "NEW_PASSWORD_REQUIRED",
			"ChallengeParameters": map[string]string{
				"USER_ID_FOR_SRP":    username,
				"requiredAttributes": "[]",
				"userAttributes":     "{}",
			},
			"Session": sessionToken,
		})
		return
	}

	ro.writeAuthResult(w, poolID, clientID, user, privateKey, keys.KeyID, true)
}

func (ro *Router) handleRefreshTokenAuth(
	w http.ResponseWriter,
	poolID, clientID string,
	params map[string]string,
) {
	token := params["REFRESH_TOKEN"]
	if token == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"REFRESH_TOKEN is required in AuthParameters")
		return
	}

	rt, err := ro.storage.GetRefreshToken(poolID, token)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid Refresh Token")
		return
	}

	if rt.ClientID != clientID {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid Refresh Token")
		return
	}

	user, err := ro.storage.GetUserBySub(poolID, rt.Sub)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException, "User does not exist.")
		return
	}

	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get pool keys")
		return
	}

	// Refresh token flow: new access/ID tokens only; no new refresh token.
	ro.writeAuthResult(w, poolID, clientID, user, privateKey, keys.KeyID, false)
}

// writeAuthResult issues tokens and writes the AuthenticationResult JSON response.
func (ro *Router) writeAuthResult(
	w http.ResponseWriter,
	poolID, clientID string,
	user *UserMetadata,
	privateKey *rsa.PrivateKey,
	keyID string,
	includeRefreshToken bool,
) {
	accessToken, idToken, rt, err := issueTokens(privateKey, keyID, poolID, clientID, user)
	if err != nil {
		// untestable: issueTokens only fails on crypto/rand.Read OS-level failures
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to issue tokens")
		return
	}

	if includeRefreshToken {
		rtData := &refreshTokenData{
			Token:    rt,
			PoolID:   poolID,
			ClientID: clientID,
			Username: user.Username,
			Sub:      user.Sub,
			IssuedAt: nowUnix(),
		}
		if err := ro.storage.CreateRefreshToken(rtData); err != nil {
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to store refresh token")
			return
		}
	}

	result := authResult{
		AccessToken: accessToken,
		ExpiresIn:   accessTokenExpiry,
		IdToken:     idToken,
		TokenType:   "Bearer",
	}
	if includeRefreshToken {
		result.RefreshToken = rt
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"AuthenticationResult": result,
		"ChallengeParameters":  map[string]string{},
	})
}

// ──── RespondToAuthChallenge ─────────────────────────────────────────────────

type respondToAuthChallengeRequest struct {
	ClientID           string            `json:"ClientId"`
	ChallengeName      string            `json:"ChallengeName"`
	Session            string            `json:"Session"`
	ChallengeResponses map[string]string `json:"ChallengeResponses"`
}

func (ro *Router) handleRespondToAuthChallenge(w http.ResponseWriter, body []byte) {
	var req respondToAuthChallengeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.ClientID == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"ClientId is required",
		)
		return
	}
	if req.ChallengeName == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"ChallengeName is required")
		return
	}
	if req.Session == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"Session is required",
		)
		return
	}

	poolID, err := ro.storage.GetPoolIDForClient(req.ClientID)
	if err != nil {
		if errors.Is(err, errUserPoolClientNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool client not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to resolve client")
		return
	}

	switch req.ChallengeName {
	case "NEW_PASSWORD_REQUIRED":
		ro.handleNewPasswordRequired(w, poolID, req.ClientID, req.Session, req.ChallengeResponses)
	default:
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"Unsupported ChallengeName: "+req.ChallengeName)
	}
}

func (ro *Router) handleNewPasswordRequired(
	w http.ResponseWriter,
	poolID, clientID, sessionToken string,
	responses map[string]string,
) {
	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get pool keys")
		return
	}

	claims, err := parseSessionToken(sessionToken, &privateKey.PublicKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
			"Invalid or expired session.")
		return
	}

	claimPoolID, _ := claims["pool_id"].(string)
	claimChallenge, _ := claims["challenge"].(string)
	claimUsername, _ := claims["username"].(string)

	if claimPoolID != poolID || claimChallenge != "NEW_PASSWORD_REQUIRED" {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid session.")
		return
	}

	username := responses["USERNAME"]
	if username == "" {
		username = claimUsername
	} else if username != claimUsername {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid session.")
		return
	}

	newPassword := responses["NEW_PASSWORD"]
	if newPassword == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"NEW_PASSWORD is required in ChallengeResponses")
		return
	}
	if len(newPassword) < minPasswordLen {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidPasswordException,
			fmt.Sprintf("Password must be at least %d characters", minPasswordLen))
		return
	}

	var updatedUser *UserMetadata
	err = ro.storage.UpdateUser(poolID, username, func(u *UserMetadata) error {
		if u.Status != userStatusForceChangePasswd {
			return errWrongChallengeStatus
		}
		hash, herr := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if herr != nil {
			// untestable: bcrypt.GenerateFromPassword only fails on invalid cost (fixed) or OOM
			return fmt.Errorf("hash password: %w", herr)
		}
		u.PasswordHash = string(hash)
		u.Status = userStatusConfirmed
		updatedUser = u
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errUserNotFound):
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeUserNotFoundException,
				"User does not exist.",
			)
		case errors.Is(err, errWrongChallengeStatus):
			writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
				"User is not in FORCE_CHANGE_PASSWORD state.")
		default:
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to update user password")
		}
		return
	}

	ro.writeAuthResult(w, poolID, clientID, updatedUser, privateKey, keys.KeyID, true)
}

// ──── ResendConfirmationCode ─────────────────────────────────────────────────

type resendConfirmationCodeRequest struct {
	ClientID string `json:"ClientId"`
	Username string `json:"Username"`
}

type resendConfirmationCodeResponse struct {
	CodeDeliveryDetails codeDeliveryDetails `json:"CodeDeliveryDetails"`
}

func (ro *Router) handleResendConfirmationCode(w http.ResponseWriter, body []byte) {
	var req resendConfirmationCodeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"invalid request body")
		return
	}
	if req.ClientID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"ClientId is required")
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"Username is required")
		return
	}

	poolID, err := ro.storage.GetPoolIDForClient(req.ClientID)
	if err != nil {
		if errors.Is(err, errUserPoolClientNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool client not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to resolve client")
		return
	}

	codeR := ro.codeReader
	if codeR == nil {
		codeR = randReader
	}
	code, err := generateConfirmationCodeFrom(codeR)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to generate confirmation code")
		return
	}

	var dest, actualStatus string
	err = ro.storage.UpdateUser(poolID, req.Username, func(u *UserMetadata) error {
		if u.Status != userStatusUnconfirmed {
			actualStatus = u.Status
			return errNotUnconfirmed
		}
		u.ConfirmationCode = code
		for _, attr := range u.Attributes {
			if attr.Name == "email" {
				dest = maskEmail(attr.Value)
				break
			}
		}
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errUserNotFound):
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
		case errors.Is(err, errNotUnconfirmed):
			writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
				fmt.Sprintf("User cannot be confirmed. Current status is %s.", actualStatus))
		default:
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to update user")
		}
		return
	}

	slog.Info("ResendConfirmationCode", "pool_id", poolID, "username", req.Username, "code", code)

	if dest == "" {
		dest = "***"
	}
	writeJSON(w, http.StatusOK, resendConfirmationCodeResponse{
		CodeDeliveryDetails: codeDeliveryDetails{
			AttributeName:  "email",
			DeliveryMedium: "EMAIL",
			Destination:    dest,
		},
	})
}

// ──── JWKS ──────────────────────────────────────────────────────────────────

func (ro *Router) handleJWKS(w http.ResponseWriter, r *http.Request) {
	// Path format: /{poolID}/.well-known/jwks.json
	path := strings.TrimPrefix(r.URL.Path, "/")
	poolID := strings.SplitN(path, "/", 2)[0]
	if poolID == "" {
		// unreachable: the router only dispatches here when the path ends with "/{poolID}/.well-known/jwks.json"
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"pool ID missing in path")
		return
	}

	if _, err := ro.storage.GetUserPool(poolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			http.NotFound(w, r)
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user pool")
		return
	}

	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get pool keys")
		return
	}

	jwks := buildJWKS(&privateKey.PublicKey, keys.KeyID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(jwks); err != nil {
		slog.Warn("failed to encode JWKS response", "err", err)
	}
}
