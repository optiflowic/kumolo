package cognito

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// ──── GetUser ────────────────────────────────────────────────────────────────

type getUserRequest struct {
	AccessToken string `json:"AccessToken"`
}

type getUserResponse struct {
	Username       string          `json:"Username"`
	UserAttributes []AttributeType `json:"UserAttributes"`
}

func (ro *Router) handleGetUser(w http.ResponseWriter, body []byte) {
	token, ok := decodeGetUserToken(w, body)
	if !ok {
		return
	}
	poolID, ok := poolIDFromToken(w, token)
	if !ok {
		return
	}
	privateKey, ok := ro.poolKey(w, poolID)
	if !ok {
		return
	}
	sub, jti, originJTI, _, ok := validateAccessJWT(w, token, &privateKey.PublicKey)
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
	writeJSON(w, http.StatusOK, getUserResponse{
		Username:       user.Username,
		UserAttributes: prependSub(user.Attributes, user.Sub),
	})
}

func decodeGetUserToken(w http.ResponseWriter, body []byte) (string, bool) {
	var req getUserRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return "", false
	}
	if req.AccessToken == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"AccessToken is required",
		)
		return "", false
	}
	return req.AccessToken, true
}

func poolIDFromToken(w http.ResponseWriter, token string) (string, bool) {
	rawClaims, err := parseRawClaims(token)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return "", false
	}
	iss, _ := rawClaims[jwtClaimIssuer].(string)
	poolID := extractPoolID(iss)
	if poolID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return "", false
	}
	return poolID, true
}

func (ro *Router) poolKey(w http.ResponseWriter, poolID string) (*rsa.PrivateKey, bool) {
	_, privateKey, err := ro.storage.GetPoolKeys(poolID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeNotAuthorizedException,
				"Invalid access token.",
			)
		} else {
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to get pool keys")
		}
		return nil, false
	}
	return privateKey, true
}

func validateAccessJWT(
	w http.ResponseWriter,
	token string,
	publicKey *rsa.PublicKey,
) (sub, jti, originJTI string, exp float64, ok bool) {
	claims, err := verifyJWT(token, publicKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return "", "", "", 0, false
	}
	var expOK bool
	exp, expOK = claims[jwtClaimExp].(float64)
	if !expOK || int64(exp) <= time.Now().Unix() {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeNotAuthorizedException,
			"Access Token has expired",
		)
		return "", "", "", 0, false
	}
	if tokenUse, _ := claims[jwtClaimTokenUse].(string); tokenUse != jwtTokenUseAccess {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return "", "", "", 0, false
	}
	sub, _ = claims[jwtClaimSub].(string)
	if sub == "" {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return "", "", "", 0, false
	}
	jti, _ = claims[jwtClaimJTI].(string)
	if jti == "" {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return "", "", "", 0, false
	}
	originJTI, _ = claims[jwtClaimOriginJTI].(string)
	return sub, jti, originJTI, exp, true
}

func (ro *Router) lookupUser(w http.ResponseWriter, poolID, sub string) (*UserMetadata, bool) {
	user, err := ro.storage.GetUserBySub(poolID, sub)
	if err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeUserNotFoundException,
				"User does not exist.",
			)
		} else {
			writeError(
				w,
				http.StatusInternalServerError,
				ErrTypeInternalErrorException,
				"failed to get user",
			)
		}
		return nil, false
	}
	return user, true
}

// checkTokenNotRevoked returns false (and writes an error) if the token is revoked.
// It checks both the token's own JTI (revoked by GlobalSignOut) and its origin_jti
// (revoked by RevokeToken, which marks the entire token family as invalid).
func (ro *Router) checkTokenNotRevoked(w http.ResponseWriter, poolID, jti, originJTI string) bool {
	for _, key := range []string{jti, originJTI} {
		if key == "" {
			continue
		}
		revoked, err := ro.storage.IsAccessTokenRevoked(poolID, key)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to check token revocation")
			return false
		}
		if revoked {
			writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
				"Access Token has been revoked")
			return false
		}
	}
	return true
}

// ──── DeleteUser ────────────────────────────────────────────────────────────

type deleteUserRequest struct {
	AccessToken string `json:"AccessToken"`
}

func (ro *Router) handleDeleteUser(w http.ResponseWriter, body []byte) {
	var req deleteUserRequest
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

	if err := ro.storage.DeleteUser(poolID, user.Username); err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to delete user")
		return
	}

	writeEmpty(w)
}

// ──── GlobalSignOut ─────────────────────────────────────────────────────────

type globalSignOutRequest struct {
	AccessToken string `json:"AccessToken"`
}

func (ro *Router) handleGlobalSignOut(w http.ResponseWriter, body []byte) {
	var req globalSignOutRequest
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

	// Revoke the origin_jti of every refresh token for this user, blocking all
	// outstanding access tokens across every concurrent session.
	revokeExp := float64(nowUnix()) + float64(accessTokenExpiry)
	if err := ro.storage.RevokeOriginJTIsForSub(poolID, sub, revokeExp); err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to revoke all sessions")
		return
	}
	// Belt-and-suspenders: also revoke the presented token's origin_jti directly, covering
	// the edge case where its refresh token was already deleted before this call.
	if originJTI != "" {
		if err := ro.storage.RevokeAccessToken(poolID, originJTI, revokeExp); err != nil {
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to revoke access token")
			return
		}
	}

	// Delete all refresh tokens for this user.
	if err := ro.storage.DeleteRefreshTokensBySub(poolID, sub); err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to revoke refresh tokens")
		return
	}

	writeEmpty(w)
}

// ──── UpdateUserAttributes ───────────────────────────────────────────────────

type updateUserAttributesRequest struct {
	AccessToken    string          `json:"AccessToken"`
	UserAttributes []AttributeType `json:"UserAttributes"`
}

func (ro *Router) handleUpdateUserAttributes(w http.ResponseWriter, body []byte) {
	var req updateUserAttributesRequest
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

	codeR := ro.codeReader
	if codeR == nil {
		codeR = randReader
	}

	changes, deliveries, err := planAttributeChanges(codeR, user.Attributes, req.UserAttributes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to generate verification code")
		return
	}

	updateErr := ro.storage.UpdateUser(poolID, user.Username, func(u *UserMetadata) error {
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

	slog.Info("UpdateUserAttributes", "pool_id", poolID, "username", user.Username,
		"verified_count", len(deliveries))
	writeJSON(w, http.StatusOK, map[string]any{"CodeDeliveryDetailsList": deliveries})
}

// attrChange describes one attribute update or deletion to apply to a user.
type attrChange struct {
	name          string
	value         string // ignored when delete or clearCodeOnly is true
	delete        bool
	verifyCode    string // non-empty when this change also resets verification for name
	clearCode     bool   // true when applying value should also drop a pending code for name
	clearCodeOnly bool   // true when only a pending code should be dropped (no attribute touched)
}

// planAttributeChanges computes the attribute mutations and verification-code
// deliveries for an UpdateUserAttributes request, without mutating any state.
// Verification codes are generated here (fallible) so the storage mutation
// closure passed to UpdateUser stays infallible.
func planAttributeChanges(
	codeR io.Reader,
	current []AttributeType,
	requested []AttributeType,
) ([]attrChange, []codeDeliveryDetails, error) {
	var changes []attrChange
	var deliveries []codeDeliveryDetails

	for _, attr := range requested {
		if attr.Value == "" {
			changes = append(changes, attrChange{name: attr.Name, delete: true})
			continue
		}

		if attr.Name != attrEmail && attr.Name != attrPhoneNumber {
			changes = append(changes, attrChange{name: attr.Name, value: attr.Value})
			continue
		}

		oldValue, _ := getAttr(current, attr.Name)
		if oldValue == attr.Value {
			changes = append(changes, attrChange{name: attr.Name, value: attr.Value})
			continue
		}

		code, err := generateConfirmationCodeFrom(codeR)
		if err != nil {
			return nil, nil, fmt.Errorf("generate verification code: %w", err)
		}
		changes = append(changes, attrChange{name: attr.Name, value: attr.Value, verifyCode: code})

		medium, dest := deliveryEmail, maskEmail(attr.Value)
		if attr.Name == attrPhoneNumber {
			medium, dest = deliverySMS, maskPhone(attr.Value)
		}
		deliveries = append(deliveries, codeDeliveryDetails{
			AttributeName:  attr.Name,
			DeliveryMedium: medium,
			Destination:    dest,
		})
	}

	if deliveries == nil {
		deliveries = []codeDeliveryDetails{}
	}
	return changes, deliveries, nil
}

// applyAttributeChanges mutates u according to changes. It must not fail.
func applyAttributeChanges(u *UserMetadata, changes []attrChange) {
	for _, c := range changes {
		if c.delete {
			u.Attributes = deleteAttr(u.Attributes, c.name)
			if c.name == attrEmail || c.name == attrPhoneNumber {
				u.Attributes = deleteAttr(u.Attributes, c.name+"_verified")
				delete(u.VerificationCodes, c.name)
			}
			continue
		}
		if c.clearCodeOnly {
			delete(u.VerificationCodes, c.name)
			continue
		}

		u.Attributes = setAttr(u.Attributes, c.name, c.value)
		switch {
		case c.verifyCode != "":
			u.Attributes = setAttr(u.Attributes, c.name+"_verified", "false")
			if u.VerificationCodes == nil {
				u.VerificationCodes = map[string]string{}
			}
			u.VerificationCodes[c.name] = c.verifyCode
		case c.clearCode:
			delete(u.VerificationCodes, c.name)
		}
	}
}

// getAttr returns the value of the named attribute, if present.
func getAttr(attrs []AttributeType, name string) (string, bool) {
	for _, a := range attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// setAttr upserts name=value into attrs, replacing any existing entry.
func setAttr(attrs []AttributeType, name, value string) []AttributeType {
	for i, a := range attrs {
		if a.Name == name {
			attrs[i].Value = value
			return attrs
		}
	}
	return append(attrs, AttributeType{Name: name, Value: value})
}

// deleteAttr removes the named attribute from attrs, if present.
func deleteAttr(attrs []AttributeType, name string) []AttributeType {
	out := attrs[:0]
	for _, a := range attrs {
		if a.Name != name {
			out = append(out, a)
		}
	}
	return out
}

// prependSub ensures sub is always the first element of attrs.
// Any existing sub attribute is removed and replaced with the provided value at index 0.
func prependSub(attrs []AttributeType, sub string) []AttributeType {
	result := make([]AttributeType, 0, len(attrs)+1)
	result = append(result, AttributeType{Name: "sub", Value: sub})
	for _, a := range attrs {
		if a.Name != "sub" {
			result = append(result, a)
		}
	}
	return result
}
