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
	sub, ok := validateAccessJWT(w, token, &privateKey.PublicKey)
	if !ok {
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
) (string, bool) {
	claims, err := verifyJWT(token, publicKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return "", false
	}
	exp, ok := claims[jwtClaimExp].(float64)
	if !ok || int64(exp) <= time.Now().Unix() {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeNotAuthorizedException,
			"Access Token has expired",
		)
		return "", false
	}
	if tokenUse, _ := claims[jwtClaimTokenUse].(string); tokenUse != jwtTokenUseAccess {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return "", false
	}
	sub, _ := claims[jwtClaimSub].(string)
	if sub == "" {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return "", false
	}
	return sub, true
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
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user")
		}
		return nil, false
	}
	return user, true
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
