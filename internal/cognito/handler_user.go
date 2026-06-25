package cognito

import (
	"encoding/json"
	"errors"
	"net/http"
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
	var req getUserRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.AccessToken == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"AccessToken is required",
		)
		return
	}

	// Parse claims without signature verification to extract pool ID from iss.
	rawClaims, err := parseRawClaims(req.AccessToken)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return
	}

	iss, _ := rawClaims["iss"].(string)
	poolID := extractPoolID(iss)
	if poolID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return
	}

	_, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to get pool keys",
		)
		return
	}

	claims, err := verifyJWT(req.AccessToken, &privateKey.PublicKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return
	}

	exp, ok := claims["exp"].(float64)
	if !ok || int64(exp) < time.Now().Unix() {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeNotAuthorizedException,
			"Access Token has expired",
		)
		return
	}

	if tokenUse, _ := claims["token_use"].(string); tokenUse != "access" {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid access token.")
		return
	}

	user, err := ro.storage.GetUserBySub(poolID, sub)
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

	attrs := prependSub(user.Attributes, user.Sub)

	writeJSON(w, http.StatusOK, getUserResponse{
		Username:       user.Username,
		UserAttributes: attrs,
	})
}

// prependSub ensures sub is the first element of attrs.
func prependSub(attrs []AttributeType, sub string) []AttributeType {
	for _, a := range attrs {
		if a.Name == "sub" {
			return attrs
		}
	}
	result := make([]AttributeType, 0, len(attrs)+1)
	result = append(result, AttributeType{Name: "sub", Value: sub})
	return append(result, attrs...)
}
