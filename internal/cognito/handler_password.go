package cognito

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"

	"golang.org/x/crypto/bcrypt"
)

var errPreviousPassword = errors.New("previous password required or incorrect")

// hashPassword bcrypt-hashes password at the given cost.
func hashPassword(password string, cost int) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// ──── ForgotPassword ──────────────────────────────────────────────────────────

type forgotPasswordRequest struct {
	ClientID string `json:"ClientId"`
	Username string `json:"Username"`
}

type forgotPasswordResponse struct {
	CodeDeliveryDetails codeDeliveryDetails `json:"CodeDeliveryDetails"`
}

// recoveryMechanism mirrors AWS's RecoveryOptionType (Name/Priority) within an
// AccountRecoverySetting.RecoveryMechanisms array.
type recoveryMechanism struct {
	Name     string `json:"Name"`
	Priority int    `json:"Priority"`
}

// defaultRecoveryMechanisms matches AWS's documented default when a pool has no
// AccountRecoverySetting: phone first, falling back to email.
func defaultRecoveryMechanisms() []recoveryMechanism {
	return []recoveryMechanism{
		{Name: "verified_phone_number", Priority: 1},
		{Name: "verified_email", Priority: 2},
	}
}

// sortedRecoveryMechanisms decodes a pool's AccountRecoverySetting into its
// RecoveryMechanisms, sorted by ascending Priority, falling back to
// defaultRecoveryMechanisms when unset or unparseable.
func sortedRecoveryMechanisms(raw json.RawMessage) []recoveryMechanism {
	mechanisms := defaultRecoveryMechanisms()
	if len(raw) > 0 {
		var setting struct {
			RecoveryMechanisms []recoveryMechanism `json:"RecoveryMechanisms"`
		}
		if err := json.Unmarshal(raw, &setting); err == nil && len(setting.RecoveryMechanisms) > 0 {
			mechanisms = setting.RecoveryMechanisms
		}
	}
	sorted := make([]recoveryMechanism, len(mechanisms))
	copy(sorted, mechanisms)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })
	return sorted
}

// forgotPasswordDeliveryDetails returns delivery details for the user's verified
// contact attribute matching the first satisfied mechanism in the pool's
// AccountRecoverySetting priority order. admin_only mechanisms are skipped, since
// they disable self-service recovery.
func forgotPasswordDeliveryDetails(
	attrs []AttributeType,
	accountRecoverySetting json.RawMessage,
) (codeDeliveryDetails, bool) {
	for _, m := range sortedRecoveryMechanisms(accountRecoverySetting) {
		switch m.Name {
		case "verified_email":
			if v, _ := getAttr(attrs, attrEmail+"_verified"); v == "true" {
				email, _ := getAttr(attrs, attrEmail)
				return codeDeliveryDetails{
					AttributeName:  attrEmail,
					DeliveryMedium: deliveryEmail,
					Destination:    maskEmail(email),
				}, true
			}
		case "verified_phone_number":
			if v, _ := getAttr(attrs, attrPhoneNumber+"_verified"); v == "true" {
				phone, _ := getAttr(attrs, attrPhoneNumber)
				return codeDeliveryDetails{
					AttributeName:  attrPhoneNumber,
					DeliveryMedium: deliverySMS,
					Destination:    maskPhone(phone),
				}, true
			}
		}
	}
	return codeDeliveryDetails{}, false
}

func (ro *Router) handleForgotPassword(w http.ResponseWriter, body []byte) {
	var req forgotPasswordRequest
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

	pool, err := ro.storage.GetUserPool(poolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user pool")
		return
	}

	user, err := ro.storage.GetUser(poolID, req.Username)
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

	delivery, ok := forgotPasswordDeliveryDetails(user.Attributes, pool.AccountRecoverySetting)
	if !ok {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"Cannot reset password for the user as there is no registered/verified email "+
				"or phone_number")
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

	updateErr := ro.storage.UpdateUser(poolID, user.Username, func(u *UserMetadata) error {
		u.PasswordResetCode = code
		return nil
	})
	if updateErr != nil {
		if errors.Is(updateErr, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to store password reset code")
		return
	}

	slog.Info("ForgotPassword", "pool_id", poolID, "username", user.Username)
	slog.Debug("ForgotPassword", "pool_id", poolID, "username", user.Username, "code", code)

	writeJSON(w, http.StatusOK, forgotPasswordResponse{CodeDeliveryDetails: delivery})
}

// ──── ConfirmForgotPassword ───────────────────────────────────────────────────

type confirmForgotPasswordRequest struct {
	ClientID         string `json:"ClientId"`
	Username         string `json:"Username"`
	ConfirmationCode string `json:"ConfirmationCode"`
	Password         string `json:"Password"`
}

func (ro *Router) handleConfirmForgotPassword(w http.ResponseWriter, body []byte) {
	var req confirmForgotPasswordRequest
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
	if req.ConfirmationCode == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"ConfirmationCode is required")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"Password is required")
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

	pool, err := ro.storage.GetUserPool(poolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user pool")
		return
	}
	if msg, ok := validatePassword(passwordPolicyFromPool(pool), req.Password); !ok {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidPasswordException, msg)
		return
	}

	err = ro.storage.UpdateUser(poolID, req.Username, func(u *UserMetadata) error {
		if u.Status == userStatusUnconfirmed {
			return errNotUnconfirmed
		}
		if subtle.ConstantTimeCompare(
			[]byte(req.ConfirmationCode),
			[]byte(u.PasswordResetCode),
		) != 1 {
			return errCodeMismatch
		}
		hash, herr := hashPassword(req.Password, ro.bcryptCost)
		if herr != nil {
			return herr
		}
		salt, verifier, serr := srpVerifierFor(poolID, req.Username, req.Password)
		if serr != nil {
			return serr
		}
		u.PasswordHash = hash
		u.SRPSalt = salt
		u.SRPVerifier = verifier
		u.PasswordResetCode = ""
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errUserNotFound):
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
		case errors.Is(err, errNotUnconfirmed):
			writeError(w, http.StatusBadRequest, ErrTypeUserNotConfirmedException,
				"User is not confirmed.")
		case errors.Is(err, errCodeMismatch):
			writeError(w, http.StatusBadRequest, ErrTypeCodeMismatchException,
				"Invalid verification code provided, please try again.")
		default:
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to reset password")
		}
		return
	}

	writeEmpty(w)
}

// ──── ChangePassword ──────────────────────────────────────────────────────────

type changePasswordRequest struct {
	AccessToken      string `json:"AccessToken"`
	PreviousPassword string `json:"PreviousPassword"`
	ProposedPassword string `json:"ProposedPassword"`
}

func (ro *Router) handleChangePassword(w http.ResponseWriter, body []byte) {
	var req changePasswordRequest
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
	if req.ProposedPassword == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"ProposedPassword is required")
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

	pool, err := ro.storage.GetUserPool(poolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user pool")
		return
	}
	if msg, ok := validatePassword(passwordPolicyFromPool(pool), req.ProposedPassword); !ok {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidPasswordException, msg)
		return
	}

	updateErr := ro.storage.UpdateUser(poolID, user.Username, func(u *UserMetadata) error {
		if u.PasswordHash != "" {
			if req.PreviousPassword == "" {
				return errPreviousPassword
			}
			if bcrypt.CompareHashAndPassword(
				[]byte(u.PasswordHash), []byte(req.PreviousPassword),
			) != nil {
				return errPreviousPassword
			}
		}
		hash, herr := hashPassword(req.ProposedPassword, ro.bcryptCost)
		if herr != nil {
			return herr
		}
		salt, verifier, serr := srpVerifierFor(poolID, u.Username, req.ProposedPassword)
		if serr != nil {
			return serr
		}
		u.PasswordHash = hash
		u.SRPSalt = salt
		u.SRPVerifier = verifier
		return nil
	})
	if updateErr != nil {
		switch {
		case errors.Is(updateErr, errUserNotFound):
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
		case errors.Is(updateErr, errPreviousPassword):
			writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
				"Incorrect username or password.")
		default:
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to change password")
		}
		return
	}

	writeEmpty(w)
}
