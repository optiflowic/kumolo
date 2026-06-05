package kms

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
)

var reGrantName = regexp.MustCompile(`^[a-zA-Z0-9:/_-]+$`)

const (
	maxGrantNameLen           = 256
	defaultListGrantsLimit    = 50
	maxListGrantsLimit        = 100
	defaultListRetirableLimit = 50
	maxListRetirableLimit     = 100
)

func (ro *Router) handleCreateGrant(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId             string            `json:"KeyId"`
		GranteePrincipal  string            `json:"GranteePrincipal"`
		Operations        []string          `json:"Operations"`
		RetiringPrincipal string            `json:"RetiringPrincipal"`
		Constraints       *GrantConstraints `json:"Constraints"`
		Name              string            `json:"Name"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}
	if req.GranteePrincipal == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "GranteePrincipal is required")
		return
	}
	if len(req.Operations) == 0 {
		writeError(w, http.StatusBadRequest, "ValidationException", "Operations is required")
		return
	}
	for _, op := range req.Operations {
		if !validGrantOperations[op] {
			writeError(w, http.StatusBadRequest, "ValidationException",
				fmt.Sprintf("invalid grant operation: %s", op))
			return
		}
	}
	if req.Name != "" {
		if len(req.Name) > maxGrantNameLen {
			writeError(w, http.StatusBadRequest, "ValidationException",
				fmt.Sprintf("Name must be at most %d characters", maxGrantNameLen))
			return
		}
		if !reGrantName.MatchString(req.Name) {
			writeError(w, http.StatusBadRequest, "ValidationException",
				"Name must match ^[a-zA-Z0-9:/_-]+$")
			return
		}
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	g, err := ro.storage.CreateGrant(keyID, CreateGrantInput{
		GranteePrincipal:  req.GranteePrincipal,
		Operations:        req.Operations,
		RetiringPrincipal: req.RetiringPrincipal,
		Constraints:       req.Constraints,
		Name:              req.Name,
	})
	if err != nil {
		writeGrantCreateError(w, keyID, err)
		return
	}

	slog.Info("KMS CreateGrant", "keyID", keyID, "grantID", g.GrantId)
	writeJSON(w, http.StatusOK, map[string]any{
		"GrantId":    g.GrantId,
		"GrantToken": g.GrantToken,
	})
}

func writeGrantCreateError(w http.ResponseWriter, keyID string, err error) {
	switch {
	case errors.Is(err, ErrKeyNotFound):
		slog.Debug("KMS CreateGrant: key not found", "keyID", keyID)
		writeError(w, http.StatusBadRequest, "NotFoundException",
			fmt.Sprintf("Invalid keyId %s", keyID))
	case errors.Is(err, ErrKeyDisabled):
		slog.Debug("KMS CreateGrant: key disabled", "keyID", keyID)
		writeError(w, http.StatusBadRequest, "DisabledException",
			fmt.Sprintf("KMS key %s is disabled", keyID))
	case errors.Is(err, ErrInvalidKeyState):
		slog.Debug("KMS CreateGrant: invalid key state", "keyID", keyID)
		writeError(
			w,
			http.StatusBadRequest,
			"KMSInvalidStateException",
			fmt.Sprintf(
				"KMS key %s is in a state that is not compatible with this operation",
				keyID,
			),
		)
	default:
		slog.Error("KMS CreateGrant storage failure", "keyID", keyID, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
	}
}

func (ro *Router) handleListGrants(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId            string `json:"KeyId"`
		GrantId          string `json:"GrantId"`
		GranteePrincipal string `json:"GranteePrincipal"`
		Limit            *int   `json:"Limit"`
		Marker           string `json:"Marker"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}
	if !validateLimit(w, req.Limit, maxListGrantsLimit) {
		return
	}

	limit := defaultListGrantsLimit
	if req.Limit != nil {
		limit = *req.Limit
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	grants, nextMarker, err := ro.storage.ListGrants(
		keyID,
		req.GrantId,
		req.GranteePrincipal,
		limit,
		req.Marker,
	)
	if err != nil {
		writeGrantListError(w, keyID, "ListGrants", err)
		return
	}

	entries := make([]grantListEntry, len(grants))
	for i, g := range grants {
		entries[i] = toGrantListEntry(g)
	}

	resp := map[string]any{
		"Grants":    entries,
		"Truncated": nextMarker != "",
	}
	if nextMarker != "" {
		resp["NextMarker"] = nextMarker
	}

	slog.Debug("KMS ListGrants", "keyID", keyID, "count", len(entries))
	writeJSON(w, http.StatusOK, resp)
}

func (ro *Router) handleRevokeGrant(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId   string `json:"KeyId"`
		GrantId string `json:"GrantId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}
	if req.GrantId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "GrantId is required")
		return
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	if err := ro.storage.RevokeGrant(keyID, req.GrantId); err != nil {
		writeGrantMutateError(w, keyID, "RevokeGrant", err)
		return
	}

	slog.Info("KMS RevokeGrant", "keyID", keyID, "grantID", req.GrantId)
	writeEmpty(w)
}

func (ro *Router) handleRetireGrant(w http.ResponseWriter, body []byte) {
	var req struct {
		GrantToken string `json:"GrantToken"`
		KeyId      string `json:"KeyId"`
		GrantId    string `json:"GrantId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}

	if req.GrantToken == "" && (req.KeyId == "" || req.GrantId == "") {
		writeError(w, http.StatusBadRequest, "ValidationException",
			"provide GrantToken, or both KeyId and GrantId")
		return
	}
	if req.GrantId != "" && req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException",
			"KeyId is required when GrantId is provided")
		return
	}

	if req.GrantToken != "" {
		if err := ro.storage.RetireGrantByToken(req.GrantToken); err != nil {
			if errors.Is(err, ErrGrantNotFound) {
				slog.Debug("KMS RetireGrant: grant not found by token")
				writeError(w, http.StatusBadRequest, "NotFoundException",
					"grant not found for the provided GrantToken")
				return
			}
			slog.Error("KMS RetireGrant storage failure", "err", err)
			writeError(
				w,
				http.StatusInternalServerError,
				"KMSInternalException",
				"internal server error",
			)
			return
		}
		slog.Info("KMS RetireGrant by token")
		writeEmpty(w)
		return
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	if err := ro.storage.RetireGrantByID(keyID, req.GrantId); err != nil {
		writeGrantMutateError(w, keyID, "RetireGrant", err)
		return
	}

	slog.Info("KMS RetireGrant", "keyID", keyID, "grantID", req.GrantId)
	writeEmpty(w)
}

func (ro *Router) handleListRetirableGrants(w http.ResponseWriter, body []byte) {
	var req struct {
		RetiringPrincipal string `json:"RetiringPrincipal"`
		Limit             *int   `json:"Limit"`
		Marker            string `json:"Marker"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.RetiringPrincipal == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "RetiringPrincipal is required")
		return
	}
	if !validateLimit(w, req.Limit, maxListRetirableLimit) {
		return
	}

	limit := defaultListRetirableLimit
	if req.Limit != nil {
		limit = *req.Limit
	}

	grants, nextMarker, err := ro.storage.ListRetirableGrants(
		req.RetiringPrincipal,
		limit,
		req.Marker,
	)
	if err != nil {
		slog.Error("KMS ListRetirableGrants storage failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	entries := make([]grantListEntry, len(grants))
	for i, g := range grants {
		entries[i] = toGrantListEntry(g)
	}

	resp := map[string]any{
		"Grants":    entries,
		"Truncated": nextMarker != "",
	}
	if nextMarker != "" {
		resp["NextMarker"] = nextMarker
	}

	slog.Debug(
		"KMS ListRetirableGrants",
		"retiringPrincipal",
		req.RetiringPrincipal,
		"count",
		len(entries),
	)
	writeJSON(w, http.StatusOK, resp)
}

// writeGrantListError handles errors for ListGrants.
func writeGrantListError(w http.ResponseWriter, keyID, op string, err error) {
	switch {
	case errors.Is(err, ErrKeyNotFound):
		slog.Debug("KMS "+op+": key not found", "keyID", keyID)
		writeError(w, http.StatusBadRequest, "NotFoundException",
			fmt.Sprintf("Invalid keyId %s", keyID))
	case errors.Is(err, ErrInvalidKeyState):
		slog.Debug("KMS "+op+": invalid key state", "keyID", keyID)
		writeError(
			w,
			http.StatusBadRequest,
			"KMSInvalidStateException",
			fmt.Sprintf(
				"KMS key %s is in a state that is not compatible with this operation",
				keyID,
			),
		)
	default:
		slog.Error("KMS "+op+" storage failure", "keyID", keyID, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
	}
}

// writeGrantMutateError handles errors for RevokeGrant and RetireGrant (ID-based).
func writeGrantMutateError(w http.ResponseWriter, keyID, op string, err error) {
	switch {
	case errors.Is(err, ErrKeyNotFound):
		slog.Debug("KMS "+op+": key not found", "keyID", keyID)
		writeError(w, http.StatusBadRequest, "NotFoundException",
			fmt.Sprintf("Invalid keyId %s", keyID))
	case errors.Is(err, ErrGrantNotFound):
		slog.Debug("KMS "+op+": grant not found", "keyID", keyID)
		writeError(w, http.StatusBadRequest, "NotFoundException",
			fmt.Sprintf("Grant not found for key %s", keyID))
	case errors.Is(err, ErrInvalidKeyState):
		slog.Debug("KMS "+op+": invalid key state", "keyID", keyID)
		writeError(
			w,
			http.StatusBadRequest,
			"KMSInvalidStateException",
			fmt.Sprintf(
				"KMS key %s is in a state that is not compatible with this operation",
				keyID,
			),
		)
	default:
		slog.Error("KMS "+op+" storage failure", "keyID", keyID, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
	}
}
