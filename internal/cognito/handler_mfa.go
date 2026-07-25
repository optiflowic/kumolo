package cognito

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"
)

var errNoPendingTOTPSecret = errors.New("no pending TOTP secret")

// challengeMFASetup and challengeSoftwareTokenMFA are the "challenge" claim values minted
// into Session JWTs by handler_auth.go and validated against here; mfaSettingSoftwareToken
// is the corresponding UserMetadata.PreferredMfaSetting value. All three cross the
// handler_auth.go/handler_mfa.go boundary as a string contract, so a typo in any one
// spelling would otherwise fail open into a generic error with no compile-time signal.
const (
	challengeMFASetup         = "MFA_SETUP"
	challengeSoftwareTokenMFA = "SOFTWARE_TOKEN_MFA"
	mfaSettingSoftwareToken   = "SOFTWARE_TOKEN_MFA"
)

// ──── AssociateSoftwareToken ──────────────────────────────────────────────────

type associateSoftwareTokenRequest struct {
	AccessToken string `json:"AccessToken"`
	Session     string `json:"Session"`
}

type associateSoftwareTokenResponse struct {
	SecretCode string `json:"SecretCode"`
	Session    string `json:"Session,omitempty"`
}

func (ro *Router) handleAssociateSoftwareToken(w http.ResponseWriter, body []byte) {
	var req associateSoftwareTokenRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"invalid request body")
		return
	}
	if req.AccessToken != "" && req.Session != "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"Only one of AccessToken or Session may be provided")
		return
	}
	if req.Session != "" {
		ro.handleAssociateSoftwareTokenSession(w, req.Session)
		return
	}
	if req.AccessToken == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"AccessToken or Session is required",
		)
		return
	}

	poolID, ok := poolIDFromToken(w, req.AccessToken)
	if !ok {
		return
	}
	privateKey, ok := ro.poolKey(w, poolID)
	if !ok {
		return
	}
	sub, jti, originJTI, _, ok := validateAccessJWT(w, req.AccessToken, &privateKey.PublicKey)
	if !ok {
		return
	}
	if ok2 := ro.checkTokenNotRevoked(w, poolID, jti, originJTI); !ok2 {
		return
	}
	user, ok := ro.lookupUser(w, poolID, sub)
	if !ok {
		return
	}

	secret, err := generateTOTPSecret()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to generate TOTP secret")
		return
	}

	updateErr := ro.storage.UpdateUser(poolID, user.Username, func(u *UserMetadata) error {
		u.PendingTOTPSecret = secret
		return nil
	})
	if updateErr != nil {
		if errors.Is(updateErr, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to store pending TOTP secret")
		return
	}

	writeJSON(w, http.StatusOK, associateSoftwareTokenResponse{SecretCode: secret})
}

// ──── VerifySoftwareToken ───────────────────────────────────────────────────

type verifySoftwareTokenRequest struct {
	AccessToken        string `json:"AccessToken"`
	Session            string `json:"Session"`
	UserCode           string `json:"UserCode"`
	FriendlyDeviceName string `json:"FriendlyDeviceName"`
}

type verifySoftwareTokenResponse struct {
	Status  string `json:"Status"`
	Session string `json:"Session,omitempty"`
}

func (ro *Router) handleVerifySoftwareToken(w http.ResponseWriter, body []byte) {
	var req verifySoftwareTokenRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"invalid request body")
		return
	}
	if req.UserCode == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"UserCode is required")
		return
	}
	if req.AccessToken != "" && req.Session != "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"Only one of AccessToken or Session may be provided")
		return
	}
	if req.Session != "" {
		ro.handleVerifySoftwareTokenSession(w, req.Session, req.UserCode)
		return
	}
	if req.AccessToken == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"AccessToken or Session is required")
		return
	}

	poolID, ok := poolIDFromToken(w, req.AccessToken)
	if !ok {
		return
	}
	privateKey, ok := ro.poolKey(w, poolID)
	if !ok {
		return
	}
	sub, jti, originJTI, _, ok := validateAccessJWT(w, req.AccessToken, &privateKey.PublicKey)
	if !ok {
		return
	}
	if ok2 := ro.checkTokenNotRevoked(w, poolID, jti, originJTI); !ok2 {
		return
	}
	user, ok := ro.lookupUser(w, poolID, sub)
	if !ok {
		return
	}

	err := ro.storage.UpdateUser(poolID, user.Username, func(u *UserMetadata) error {
		if u.PendingTOTPSecret == "" {
			return errNoPendingTOTPSecret
		}
		if !verifyTOTP(u.PendingTOTPSecret, req.UserCode, time.Now().Unix()) {
			return errCodeMismatch
		}
		u.TOTPSecret = u.PendingTOTPSecret
		u.PendingTOTPSecret = ""
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errUserNotFound):
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
		case errors.Is(err, errNoPendingTOTPSecret):
			writeError(w, http.StatusBadRequest, ErrTypeSoftwareTokenMFANotFoundException,
				"Software token MFA has not been associated for this user.")
		case errors.Is(err, errCodeMismatch):
			writeError(w, http.StatusBadRequest, ErrTypeCodeMismatchException,
				"Invalid verification code provided, please try again.")
		default:
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to verify software token")
		}
		return
	}

	writeJSON(w, http.StatusOK, verifySoftwareTokenResponse{Status: "SUCCESS"})
}

// ──── Session-authenticated AssociateSoftwareToken/VerifySoftwareToken ────────
//
// Satisfies the forced MFA_SETUP challenge: InitiateAuth/RespondToAuthChallenge issue a
// Session with challenge "MFA_SETUP" when the pool requires TOTP MFA (MfaConfiguration:
// "ON") and the user has none enrolled. The pending/verified secret travels in the Session
// JWT itself rather than in user storage (mirroring PASSWORD_VERIFIER's session-carried SRP
// state), so nothing is persisted to the user record until RespondToAuthChallenge commits it.

// resolveMFASetupSession validates a Session JWT for the forced MFA_SETUP flow: it must be a
// signed, unexpired session issued for challenge "MFA_SETUP". Returns the pool's signing key
// and the verified claims.
func (ro *Router) resolveMFASetupSession(
	w http.ResponseWriter,
	sessionToken string,
) (poolID string, keys *poolKeys, privateKey *rsa.PrivateKey, claims map[string]any, ok bool) {
	rawClaims, err := parseRawClaims(sessionToken)
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeNotAuthorizedException,
			"Invalid or expired session.",
		)
		return "", nil, nil, nil, false
	}
	poolID, _ = rawClaims["pool_id"].(string)
	if poolID == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeNotAuthorizedException,
			"Invalid or expired session.",
		)
		return "", nil, nil, nil, false
	}

	keys, privateKey, err = ro.storage.GetPoolKeys(poolID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeNotAuthorizedException,
				"Invalid or expired session.",
			)
		} else {
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to get pool keys")
		}
		return "", nil, nil, nil, false
	}

	claims, err = parseSessionToken(sessionToken, &privateKey.PublicKey)
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeNotAuthorizedException,
			"Invalid or expired session.",
		)
		return "", nil, nil, nil, false
	}
	if challenge, _ := claims["challenge"].(string); challenge != challengeMFASetup {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid session.")
		return "", nil, nil, nil, false
	}

	return poolID, keys, privateKey, claims, true
}

func (ro *Router) handleAssociateSoftwareTokenSession(w http.ResponseWriter, sessionToken string) {
	poolID, keys, privateKey, claims, ok := ro.resolveMFASetupSession(w, sessionToken)
	if !ok {
		return
	}
	username, _ := claims["username"].(string)
	clientID, _ := claims["client_id"].(string)

	if _, err := ro.storage.GetUser(poolID, username); err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeUserNotFoundException,
				"User does not exist.",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to get user",
		)
		return
	}

	secret, err := generateTOTPSecret()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to generate TOTP secret")
		return
	}

	newSession, err := buildSessionToken(
		privateKey, keys.KeyID, poolID, clientID, username, challengeMFASetup,
		map[string]any{"pending_totp_secret": secret},
	)
	if err != nil {
		// unreachable: buildJWT fails only if claims contain non-serializable types (all primitives here)
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to build session token")
		return
	}

	writeJSON(
		w,
		http.StatusOK,
		associateSoftwareTokenResponse{SecretCode: secret, Session: newSession},
	)
}

func (ro *Router) handleVerifySoftwareTokenSession(
	w http.ResponseWriter,
	sessionToken, userCode string,
) {
	poolID, keys, privateKey, claims, ok := ro.resolveMFASetupSession(w, sessionToken)
	if !ok {
		return
	}
	username, _ := claims["username"].(string)
	clientID, _ := claims["client_id"].(string)
	pendingSecret, _ := claims["pending_totp_secret"].(string)

	if _, err := ro.storage.GetUser(poolID, username); err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeUserNotFoundException,
				"User does not exist.",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to get user",
		)
		return
	}
	if pendingSecret == "" {
		writeError(w, http.StatusBadRequest, ErrTypeSoftwareTokenMFANotFoundException,
			"Software token MFA has not been associated for this user.")
		return
	}
	if !verifyTOTP(pendingSecret, userCode, time.Now().Unix()) {
		writeError(w, http.StatusBadRequest, ErrTypeCodeMismatchException,
			"Invalid verification code provided, please try again.")
		return
	}

	newSession, err := buildSessionToken(
		privateKey, keys.KeyID, poolID, clientID, username, challengeMFASetup,
		map[string]any{"verified_totp_secret": pendingSecret},
	)
	if err != nil {
		// unreachable: buildJWT fails only if claims contain non-serializable types (all primitives here)
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to build session token")
		return
	}

	writeJSON(w, http.StatusOK, verifySoftwareTokenResponse{Status: "SUCCESS", Session: newSession})
}

// ──── RespondToAuthChallenge: MFA_SETUP ────────────────────────────────────────

// validateMFASetupChallengeClaims checks the Session JWT's claims against the pool/client
// from the RespondToAuthChallenge request and reconciles the ChallengeResponses USERNAME (if
// provided) against the session's username, mirroring resolveMFASetupSession's validation for
// the RespondToAuthChallenge entry point. Writes the appropriate error response and returns
// ok=false on any mismatch.
func (ro *Router) validateMFASetupChallengeClaims(
	w http.ResponseWriter,
	claims map[string]any,
	poolID, clientID string,
	responses map[string]string,
) (username, verifiedSecret string, ok bool) {
	claimPoolID, _ := claims["pool_id"].(string)
	claimClientID, _ := claims["client_id"].(string)
	claimChallenge, _ := claims["challenge"].(string)
	claimUsername, _ := claims["username"].(string)
	verifiedSecret, _ = claims["verified_totp_secret"].(string)

	if claimPoolID != poolID || claimClientID != clientID ||
		claimChallenge != challengeMFASetup || verifiedSecret == "" {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid session.")
		return "", "", false
	}

	username = responses["USERNAME"]
	if username == "" {
		username = claimUsername
	} else if username != claimUsername {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid session.")
		return "", "", false
	}

	return username, verifiedSecret, true
}

// handleMFASetupChallenge completes the forced MFA_SETUP challenge using the Session
// returned by handleVerifySoftwareTokenSession. It commits the verified TOTP secret to the
// user record, activates SOFTWARE_TOKEN_MFA as the user's sign-in requirement (so later
// sign-ins get a SOFTWARE_TOKEN_MFA challenge instead of MFA_SETUP again), and issues tokens.
func (ro *Router) handleMFASetupChallenge(
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

	username, verifiedSecret, ok := ro.validateMFASetupChallengeClaims(
		w, claims, poolID, clientID, responses,
	)
	if !ok {
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
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to get user",
		)
		return
	}
	if !user.Enabled {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "User is disabled.")
		return
	}

	var updatedUser *UserMetadata
	updateErr := ro.storage.UpdateUser(poolID, username, func(u *UserMetadata) error {
		u.TOTPSecret = verifiedSecret
		u.SoftwareTokenMFAEnabled = true
		u.PreferredMfaSetting = mfaSettingSoftwareToken
		updatedUser = u
		return nil
	})
	if updateErr != nil {
		if errors.Is(updateErr, errUserNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeUserNotFoundException,
				"User does not exist.",
			)
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to activate software token MFA")
		return
	}

	ro.writeAuthResult(w, poolID, clientID, updatedUser, privateKey, keys.KeyID, true, "")
}

// ──── SetUserMFAPreference ──────────────────────────────────────────────────

type mfaSettingsType struct {
	Enabled      bool `json:"Enabled"`
	PreferredMfa bool `json:"PreferredMfa"`
}

type setUserMFAPreferenceRequest struct {
	AccessToken              string           `json:"AccessToken"`
	SoftwareTokenMfaSettings *mfaSettingsType `json:"SoftwareTokenMfaSettings"`
}

var errTOTPNotVerified = errors.New("TOTP not verified")

func (ro *Router) handleSetUserMFAPreference(w http.ResponseWriter, body []byte) {
	var req setUserMFAPreferenceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"invalid request body")
		return
	}
	if req.AccessToken == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"AccessToken is required")
		return
	}

	poolID, ok := poolIDFromToken(w, req.AccessToken)
	if !ok {
		return
	}
	privateKey, ok := ro.poolKey(w, poolID)
	if !ok {
		return
	}
	sub, jti, originJTI, _, ok := validateAccessJWT(w, req.AccessToken, &privateKey.PublicKey)
	if !ok {
		return
	}
	if ok2 := ro.checkTokenNotRevoked(w, poolID, jti, originJTI); !ok2 {
		return
	}
	user, ok := ro.lookupUser(w, poolID, sub)
	if !ok {
		return
	}

	err := ro.storage.UpdateUser(poolID, user.Username, func(u *UserMetadata) error {
		settings := req.SoftwareTokenMfaSettings
		if settings == nil {
			return nil
		}
		if settings.Enabled && u.TOTPSecret == "" {
			return errTOTPNotVerified
		}
		u.SoftwareTokenMFAEnabled = settings.Enabled
		switch {
		case !u.SoftwareTokenMFAEnabled || !settings.PreferredMfa:
			if u.PreferredMfaSetting == mfaSettingSoftwareToken {
				u.PreferredMfaSetting = ""
			}
		default:
			u.PreferredMfaSetting = mfaSettingSoftwareToken
		}
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errUserNotFound):
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
		case errors.Is(err, errTOTPNotVerified):
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeInvalidParameterException,
				"Cannot enable SOFTWARE_TOKEN_MFA before verifying a software token via VerifySoftwareToken.",
			)
		default:
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to update MFA preference")
		}
		return
	}

	writeEmpty(w)
}

// ──── RespondToAuthChallenge: SOFTWARE_TOKEN_MFA ───────────────────────────────

func (ro *Router) handleSoftwareTokenMFAChallenge(
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
	claimClientID, _ := claims["client_id"].(string)
	claimChallenge, _ := claims["challenge"].(string)
	claimUsername, _ := claims["username"].(string)

	if claimPoolID != poolID || claimClientID != clientID ||
		claimChallenge != challengeSoftwareTokenMFA {
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

	code := responses["SOFTWARE_TOKEN_MFA_CODE"]
	if code == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"SOFTWARE_TOKEN_MFA_CODE is required in ChallengeResponses")
		return
	}

	user, err := ro.storage.GetUser(poolID, username)
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
	if user.TOTPSecret == "" || !user.SoftwareTokenMFAEnabled {
		// Reached only if SetUserMFAPreference disables MFA between challenge
		// issuance and response (this challenge is otherwise only issued, via
		// completeAuth, for a user with SoftwareTokenMFAEnabled).
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid session.")
		return
	}
	if !user.Enabled {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "User is disabled.")
		return
	}

	if !verifyTOTP(user.TOTPSecret, code, time.Now().Unix()) {
		writeError(w, http.StatusBadRequest, ErrTypeCodeMismatchException,
			"Invalid verification code provided, please try again.")
		return
	}

	ro.writeAuthResult(w, poolID, clientID, user, privateKey, keys.KeyID, true, "")
}
