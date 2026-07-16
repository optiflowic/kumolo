package cognito

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

var errNoPendingTOTPSecret = errors.New("no pending TOTP secret")

// ──── AssociateSoftwareToken ──────────────────────────────────────────────────

type associateSoftwareTokenRequest struct {
	AccessToken string `json:"AccessToken"`
	Session     string `json:"Session"`
}

type associateSoftwareTokenResponse struct {
	SecretCode string `json:"SecretCode"`
}

func (ro *Router) handleAssociateSoftwareToken(w http.ResponseWriter, body []byte) {
	var req associateSoftwareTokenRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"invalid request body")
		return
	}
	if req.AccessToken == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"AccessToken is required; the Session-based AssociateSoftwareToken flow is not supported",
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
	Status string `json:"Status"`
}

func (ro *Router) handleVerifySoftwareToken(w http.ResponseWriter, body []byte) {
	var req verifySoftwareTokenRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"invalid request body")
		return
	}
	if req.AccessToken == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"AccessToken is required; the Session-based VerifySoftwareToken flow is not supported")
		return
	}
	if req.UserCode == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"UserCode is required")
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
		case !u.SoftwareTokenMFAEnabled:
			if u.PreferredMfaSetting == "SOFTWARE_TOKEN_MFA" {
				u.PreferredMfaSetting = ""
			}
		case settings.PreferredMfa:
			u.PreferredMfaSetting = "SOFTWARE_TOKEN_MFA"
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
	claimChallenge, _ := claims["challenge"].(string)
	claimUsername, _ := claims["username"].(string)

	if claimPoolID != poolID || claimChallenge != "SOFTWARE_TOKEN_MFA" {
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
		// unreachable in normal operation: this challenge is only ever issued (via completeAuth)
		// for a user with SoftwareTokenMFAEnabled, which SetUserMFAPreference only allows once
		// TOTPSecret is set. A concurrent SetUserMFAPreference disabling MFA between challenge
		// issuance and response is the only way to reach this.
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid session.")
		return
	}

	if !verifyTOTP(user.TOTPSecret, code, time.Now().Unix()) {
		writeError(w, http.StatusBadRequest, ErrTypeCodeMismatchException,
			"Invalid verification code provided, please try again.")
		return
	}

	ro.writeAuthResult(w, poolID, clientID, user, privateKey, keys.KeyID, true, "")
}
