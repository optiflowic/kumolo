package cognito

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
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
	sub, jti, originJTI, exp, ok := validateAccessJWT(w, req.AccessToken, &privateKey.PublicKey)
	if !ok {
		return
	}
	if ok2 := ro.checkTokenNotRevoked(w, poolID, jti, originJTI); !ok2 {
		return
	}

	// Revoke the current access token JTI so it is rejected immediately.
	if err := ro.storage.RevokeAccessToken(poolID, jti, exp); err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to revoke access token")
		return
	}

	// Delete all refresh tokens for this user.
	if err := ro.storage.DeleteRefreshTokensBySub(poolID, sub); err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to revoke refresh tokens")
		return
	}

	writeEmpty(w)
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
