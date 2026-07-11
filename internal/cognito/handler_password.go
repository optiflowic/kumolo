package cognito

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

var errPreviousPassword = errors.New("previous password required or incorrect")

// ──── ForgotPassword ──────────────────────────────────────────────────────────

type forgotPasswordRequest struct {
	ClientID string `json:"ClientId"`
	Username string `json:"Username"`
}

type forgotPasswordResponse struct {
	CodeDeliveryDetails codeDeliveryDetails `json:"CodeDeliveryDetails"`
}

// forgotPasswordDeliveryDetails returns delivery details for the user's verified
// contact attribute, requiring email_verified or phone_number_verified to be "true".
// Email takes precedence over phone when both are verified.
func forgotPasswordDeliveryDetails(attrs []AttributeType) (codeDeliveryDetails, bool) {
	if v, _ := getAttr(attrs, attrEmail+"_verified"); v == "true" {
		email, _ := getAttr(attrs, attrEmail)
		return codeDeliveryDetails{
			AttributeName:  attrEmail,
			DeliveryMedium: deliveryEmail,
			Destination:    maskEmail(email),
		}, true
	}
	if v, _ := getAttr(attrs, attrPhoneNumber+"_verified"); v == "true" {
		phone, _ := getAttr(attrs, attrPhoneNumber)
		return codeDeliveryDetails{
			AttributeName:  attrPhoneNumber,
			DeliveryMedium: deliverySMS,
			Destination:    maskPhone(phone),
		}, true
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

	delivery, ok := forgotPasswordDeliveryDetails(user.Attributes)
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

	slog.Info("ForgotPassword", "pool_id", poolID, "username", user.Username, "code", code)

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
		hash, herr := bcrypt.GenerateFromPassword([]byte(req.Password), ro.bcryptCost)
		if herr != nil {
			// untestable: bcrypt.GenerateFromPassword only fails on invalid cost (fixed) or OOM
			return herr
		}
		u.PasswordHash = string(hash)
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
		hash, herr := bcrypt.GenerateFromPassword([]byte(req.ProposedPassword), ro.bcryptCost)
		if herr != nil {
			// untestable: bcrypt.GenerateFromPassword only fails on invalid cost (fixed) or OOM
			return herr
		}
		u.PasswordHash = string(hash)
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
