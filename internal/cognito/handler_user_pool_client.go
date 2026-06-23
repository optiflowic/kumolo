package cognito

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
)

const (
	clientIDLen       = 26
	clientIDChars     = "abcdefghijklmnopqrstuvwxyz0123456789"
	clientSecretLen   = 51
	clientSecretChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

var (
	reClientName   = regexp.MustCompile(`^[\w\s+=,.@-]{1,128}$`)
	reClientSecret = regexp.MustCompile(`^[\w+]+$`)
	reClientID     = regexp.MustCompile(`^[a-z0-9]{26}$`)
	reUserPoolID   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

func generateClientID() (string, error) {
	const n = len(clientIDChars)
	const limit = byte((256 / n) * n)
	b := make([]byte, clientIDLen)
	for i := range b {
		for {
			if _, err := rand.Read(b[i : i+1]); err != nil {
				// untestable: crypto/rand.Read only fails on OS-level entropy source errors
				return "", fmt.Errorf("read entropy: %w", err)
			}
			if b[i] < limit {
				b[i] = clientIDChars[b[i]%byte(n)]
				break
			}
		}
	}
	return string(b), nil
}

func generateClientSecret() (string, error) {
	const n = len(clientSecretChars)
	const limit = byte((256 / n) * n)
	b := make([]byte, clientSecretLen)
	for i := range b {
		for {
			if _, err := rand.Read(b[i : i+1]); err != nil {
				// untestable: crypto/rand.Read only fails on OS-level entropy source errors
				return "", fmt.Errorf("read entropy: %w", err)
			}
			if b[i] < limit {
				b[i] = clientSecretChars[b[i]%byte(n)]
				break
			}
		}
	}
	return string(b), nil
}

type createUserPoolClientRequest struct {
	UserPoolId                               string          `json:"UserPoolId"`
	ClientName                               string          `json:"ClientName"`
	GenerateSecret                           bool            `json:"GenerateSecret"`
	ClientSecret                             string          `json:"ClientSecret"`
	RefreshTokenValidity                     int             `json:"RefreshTokenValidity"`
	AccessTokenValidity                      int             `json:"AccessTokenValidity"`
	IdTokenValidity                          int             `json:"IdTokenValidity"`
	AuthSessionValidity                      int             `json:"AuthSessionValidity"`
	TokenValidityUnits                       json.RawMessage `json:"TokenValidityUnits"`
	ExplicitAuthFlows                        []string        `json:"ExplicitAuthFlows"`
	AllowedOAuthFlows                        []string        `json:"AllowedOAuthFlows"`
	AllowedOAuthScopes                       []string        `json:"AllowedOAuthScopes"`
	AllowedOAuthFlowsUserPoolClient          *bool           `json:"AllowedOAuthFlowsUserPoolClient"`
	CallbackURLs                             []string        `json:"CallbackURLs"`
	LogoutURLs                               []string        `json:"LogoutURLs"`
	DefaultRedirectURI                       string          `json:"DefaultRedirectURI"`
	SupportedIdentityProviders               []string        `json:"SupportedIdentityProviders"`
	ReadAttributes                           []string        `json:"ReadAttributes"`
	WriteAttributes                          []string        `json:"WriteAttributes"`
	PreventUserExistenceErrors               string          `json:"PreventUserExistenceErrors"`
	EnableTokenRevocation                    *bool           `json:"EnableTokenRevocation"`
	EnablePropagateAdditionalUserContextData *bool           `json:"EnablePropagateAdditionalUserContextData"`
	AnalyticsConfiguration                   json.RawMessage `json:"AnalyticsConfiguration"`
	RefreshTokenRotation                     json.RawMessage `json:"RefreshTokenRotation"`
}

type updateUserPoolClientRequest struct {
	UserPoolId                               string          `json:"UserPoolId"`
	ClientId                                 string          `json:"ClientId"`
	ClientName                               string          `json:"ClientName"`
	RefreshTokenValidity                     int             `json:"RefreshTokenValidity"`
	AccessTokenValidity                      int             `json:"AccessTokenValidity"`
	IdTokenValidity                          int             `json:"IdTokenValidity"`
	AuthSessionValidity                      int             `json:"AuthSessionValidity"`
	TokenValidityUnits                       json.RawMessage `json:"TokenValidityUnits"`
	ExplicitAuthFlows                        []string        `json:"ExplicitAuthFlows"`
	AllowedOAuthFlows                        []string        `json:"AllowedOAuthFlows"`
	AllowedOAuthScopes                       []string        `json:"AllowedOAuthScopes"`
	AllowedOAuthFlowsUserPoolClient          *bool           `json:"AllowedOAuthFlowsUserPoolClient"`
	CallbackURLs                             []string        `json:"CallbackURLs"`
	LogoutURLs                               []string        `json:"LogoutURLs"`
	DefaultRedirectURI                       string          `json:"DefaultRedirectURI"`
	SupportedIdentityProviders               []string        `json:"SupportedIdentityProviders"`
	ReadAttributes                           []string        `json:"ReadAttributes"`
	WriteAttributes                          []string        `json:"WriteAttributes"`
	PreventUserExistenceErrors               string          `json:"PreventUserExistenceErrors"`
	EnableTokenRevocation                    *bool           `json:"EnableTokenRevocation"`
	EnablePropagateAdditionalUserContextData *bool           `json:"EnablePropagateAdditionalUserContextData"`
	AnalyticsConfiguration                   json.RawMessage `json:"AnalyticsConfiguration"`
	RefreshTokenRotation                     json.RawMessage `json:"RefreshTokenRotation"`
}

func derefBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// validatePoolID writes a 400 error and returns false if poolID fails validation.
func validatePoolID(w http.ResponseWriter, poolID string) bool {
	if poolID == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"UserPoolId is required",
		)
		return false
	}
	if !reUserPoolID.MatchString(poolID) {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"UserPoolId contains invalid characters")
		return false
	}
	return true
}

// validateClientID writes a 400 error and returns false if clientID fails validation.
func validateClientID(w http.ResponseWriter, clientID string) bool {
	if clientID == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"ClientId is required",
		)
		return false
	}
	if !reClientID.MatchString(clientID) {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"ClientId contains invalid characters")
		return false
	}
	return true
}

// validateCreateUserPoolClientRequest validates create-specific fields.
// Returns false after writing a 400 error if any field is invalid.
func validateCreateUserPoolClientRequest(
	w http.ResponseWriter,
	req *createUserPoolClientRequest,
) bool {
	if req.ClientName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"ClientName is required",
		)
		return false
	}
	if !reClientName.MatchString(req.ClientName) {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"ClientName must be 1-128 characters and match pattern [\\w\\s+=,.@-]+")
		return false
	}
	if req.GenerateSecret && req.ClientSecret != "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"Cannot specify both GenerateSecret and ClientSecret")
		return false
	}
	if !req.GenerateSecret && req.ClientSecret != "" {
		if len(req.ClientSecret) < 24 || len(req.ClientSecret) > 64 {
			writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
				"ClientSecret length must be between 24 and 64 characters")
			return false
		}
		if !reClientSecret.MatchString(req.ClientSecret) {
			writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
				`ClientSecret must match pattern [\w+]+`)
			return false
		}
	}
	return true
}

// buildUserPoolClientMeta constructs UserPoolClientMetadata from a create request.
func buildUserPoolClientMeta(
	req *createUserPoolClientRequest,
	clientID, secret string,
) *UserPoolClientMetadata {
	ts := nowUnix()
	return &UserPoolClientMetadata{
		UserPoolID:           req.UserPoolId,
		ClientID:             clientID,
		ClientName:           req.ClientName,
		ClientSecret:         secret,
		CreationDate:         ts,
		LastModifiedDate:     ts,
		RefreshTokenValidity: req.RefreshTokenValidity,
		AccessTokenValidity:  req.AccessTokenValidity,
		IdTokenValidity:      req.IdTokenValidity,
		AuthSessionValidity:  req.AuthSessionValidity,
		TokenValidityUnits:   req.TokenValidityUnits,
		ExplicitAuthFlows:    req.ExplicitAuthFlows,
		AllowedOAuthFlows:    req.AllowedOAuthFlows,
		AllowedOAuthScopes:   req.AllowedOAuthScopes,
		AllowedOAuthFlowsUserPoolClient: derefBool(
			req.AllowedOAuthFlowsUserPoolClient,
			false,
		),
		CallbackURLs:               req.CallbackURLs,
		LogoutURLs:                 req.LogoutURLs,
		DefaultRedirectURI:         req.DefaultRedirectURI,
		SupportedIdentityProviders: req.SupportedIdentityProviders,
		ReadAttributes:             req.ReadAttributes,
		WriteAttributes:            req.WriteAttributes,
		PreventUserExistenceErrors: req.PreventUserExistenceErrors,
		EnableTokenRevocation:      derefBool(req.EnableTokenRevocation, true),
		EnablePropagateAdditionalUserContextData: derefBool(
			req.EnablePropagateAdditionalUserContextData,
			false,
		),
		AnalyticsConfiguration: req.AnalyticsConfiguration,
		RefreshTokenRotation:   req.RefreshTokenRotation,
	}
}

func (ro *Router) handleCreateUserPoolClient(w http.ResponseWriter, body []byte) {
	var req createUserPoolClientRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if !validatePoolID(w, req.UserPoolId) {
		return
	}
	if !validateCreateUserPoolClientRequest(w, &req) {
		return
	}

	clientID, err := generateClientID()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to generate client ID")
		return
	}

	var secret string
	if req.GenerateSecret {
		secret, err = generateClientSecret()
		if err != nil {
			// untestable: crypto/rand.Read only fails on OS-level entropy source errors
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to generate client secret")
			return
		}
	} else {
		secret = req.ClientSecret
	}

	meta := buildUserPoolClientMeta(&req, clientID, secret)
	if err := ro.storage.CreateUserPoolClient(meta); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"User pool not found.",
			)
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to create user pool client")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"UserPoolClient": meta})
}

func (ro *Router) handleDescribeUserPoolClient(w http.ResponseWriter, body []byte) {
	var req struct {
		UserPoolId string `json:"UserPoolId"`
		ClientId   string `json:"ClientId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if !validatePoolID(w, req.UserPoolId) {
		return
	}
	if !validateClientID(w, req.ClientId) {
		return
	}

	meta, err := ro.storage.GetUserPoolClient(req.UserPoolId, req.ClientId)
	if err != nil {
		if errors.Is(err, errUserPoolClientNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool client not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user pool client")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"UserPoolClient": meta})
}

func (ro *Router) handleUpdateUserPoolClient(w http.ResponseWriter, body []byte) {
	var req updateUserPoolClientRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if !validatePoolID(w, req.UserPoolId) {
		return
	}
	if !validateClientID(w, req.ClientId) {
		return
	}
	if req.ClientName != "" && !reClientName.MatchString(req.ClientName) {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"ClientName must be 1-128 characters and match pattern [\\w\\s+=,.@-]+")
		return
	}

	var updated *UserPoolClientMetadata
	err := ro.storage.UpdateUserPoolClient(req.UserPoolId, req.ClientId,
		func(meta *UserPoolClientMetadata) error {
			// Preserve immutable fields; replace all others with request values.
			if req.ClientName != "" {
				meta.ClientName = req.ClientName
			}
			meta.RefreshTokenValidity = req.RefreshTokenValidity
			meta.AccessTokenValidity = req.AccessTokenValidity
			meta.IdTokenValidity = req.IdTokenValidity
			meta.AuthSessionValidity = req.AuthSessionValidity
			meta.TokenValidityUnits = req.TokenValidityUnits
			meta.ExplicitAuthFlows = req.ExplicitAuthFlows
			meta.AllowedOAuthFlows = req.AllowedOAuthFlows
			meta.AllowedOAuthScopes = req.AllowedOAuthScopes
			meta.AllowedOAuthFlowsUserPoolClient = derefBool(
				req.AllowedOAuthFlowsUserPoolClient,
				false,
			)
			meta.CallbackURLs = req.CallbackURLs
			meta.LogoutURLs = req.LogoutURLs
			meta.DefaultRedirectURI = req.DefaultRedirectURI
			meta.SupportedIdentityProviders = req.SupportedIdentityProviders
			meta.ReadAttributes = req.ReadAttributes
			meta.WriteAttributes = req.WriteAttributes
			meta.PreventUserExistenceErrors = req.PreventUserExistenceErrors
			meta.EnableTokenRevocation = derefBool(req.EnableTokenRevocation, true)
			meta.EnablePropagateAdditionalUserContextData = derefBool(
				req.EnablePropagateAdditionalUserContextData,
				false,
			)
			meta.AnalyticsConfiguration = req.AnalyticsConfiguration
			meta.RefreshTokenRotation = req.RefreshTokenRotation
			updated = meta
			return nil
		},
	)
	if err != nil {
		if errors.Is(err, errUserPoolClientNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool client not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to update user pool client")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"UserPoolClient": updated})
}

func (ro *Router) handleDeleteUserPoolClient(w http.ResponseWriter, body []byte) {
	var req struct {
		UserPoolId string `json:"UserPoolId"`
		ClientId   string `json:"ClientId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if !validatePoolID(w, req.UserPoolId) {
		return
	}
	if !validateClientID(w, req.ClientId) {
		return
	}

	if err := ro.storage.DeleteUserPoolClient(req.UserPoolId, req.ClientId); err != nil {
		if errors.Is(err, errUserPoolClientNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException,
				"User pool client not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to delete user pool client")
		return
	}
	writeEmpty(w)
}

type userPoolClientDescription struct {
	ClientId   string `json:"ClientId"`
	ClientName string `json:"ClientName"`
	UserPoolId string `json:"UserPoolId"`
}

func (ro *Router) handleListUserPoolClients(w http.ResponseWriter, body []byte) {
	var req struct {
		UserPoolId string `json:"UserPoolId"`
		MaxResults *int   `json:"MaxResults"`
		NextToken  string `json:"NextToken"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if !validatePoolID(w, req.UserPoolId) {
		return
	}

	maxResults := 60
	if req.MaxResults != nil {
		maxResults = *req.MaxResults
		if maxResults < 1 || maxResults > 60 {
			writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
				"MaxResults must be between 1 and 60")
			return
		}
	}

	clients, nextToken, err := ro.storage.ListUserPoolClients(
		req.UserPoolId,
		maxResults,
		req.NextToken,
	)
	if err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"User pool not found.",
			)
			return
		}
		if errors.Is(err, errInvalidNextToken) {
			writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
				"Invalid pagination token.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to list user pool clients")
		return
	}

	descs := make([]userPoolClientDescription, 0, len(clients))
	for _, c := range clients {
		descs = append(descs, userPoolClientDescription{
			ClientId:   c.ClientID,
			ClientName: c.ClientName,
			UserPoolId: c.UserPoolID,
		})
	}

	resp := map[string]any{"UserPoolClients": descs}
	if nextToken != "" {
		resp["NextToken"] = nextToken
	}
	writeJSON(w, http.StatusOK, resp)
}
