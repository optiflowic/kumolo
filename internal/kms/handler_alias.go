package kms

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

var (
	// ^alias/[a-zA-Z0-9/_-]+$ — AWS CreateAlias / UpdateAlias pattern
	reAliasName = regexp.MustCompile(`^alias/[a-zA-Z0-9/_-]+$`)
)

func validateAliasName(aliasName string) error {
	if aliasName == "" {
		return fmt.Errorf("AliasName is required")
	}
	if !reAliasName.MatchString(aliasName) {
		return fmt.Errorf("invalid alias name %q: must match ^alias/[a-zA-Z0-9/_-]+$", aliasName)
	}
	return nil
}

func (ro *Router) handleCreateAlias(w http.ResponseWriter, body []byte) {
	var req struct {
		AliasName   string `json:"AliasName"`
		TargetKeyId string `json:"TargetKeyId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}

	if err := validateAliasName(req.AliasName); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidAliasNameException", err.Error())
		return
	}
	// alias/aws/ prefix is reserved for AWS managed keys
	if strings.HasPrefix(req.AliasName, "alias/aws/") {
		writeError(w, http.StatusBadRequest, "InvalidAliasNameException",
			"alias names beginning with alias/aws/ are reserved for AWS managed keys")
		return
	}
	if req.TargetKeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "TargetKeyId is required")
		return
	}

	targetKeyID, ok := resolveKeyID(req.TargetKeyId)
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidArnException",
			fmt.Sprintf("Invalid key ARN: %s", req.TargetKeyId))
		return
	}

	if err := ro.storage.CreateAlias(req.AliasName, targetKeyID); err != nil {
		switch {
		case errors.Is(err, ErrAliasAlreadyExists):
			writeError(w, http.StatusBadRequest, "AlreadyExistsException",
				fmt.Sprintf("An alias with the name %s already exists", req.AliasName))
		case errors.Is(err, ErrAliasLimitExceeded):
			writeError(
				w,
				http.StatusBadRequest,
				"LimitExceededException",
				fmt.Sprintf(
					"Key %s already has the maximum number of aliases (%d)",
					targetKeyID,
					maxAliasesPerKey,
				),
			)
		case errors.Is(err, ErrKeyNotFound):
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Invalid keyId %s", targetKeyID))
		default:
			slog.Error("KMS CreateAlias storage failure", "err", err)
			writeError(
				w,
				http.StatusInternalServerError,
				"KMSInternalException",
				"internal server error",
			)
		}
		return
	}

	slog.Info("KMS CreateAlias", "aliasName", req.AliasName, "targetKeyID", targetKeyID)
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleDeleteAlias(w http.ResponseWriter, body []byte) {
	var req struct {
		AliasName string `json:"AliasName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.AliasName == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "AliasName is required")
		return
	}

	if err := ro.storage.DeleteAlias(req.AliasName); err != nil {
		if errors.Is(err, ErrAliasNotFound) {
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Alias %s not found", req.AliasName))
			return
		}
		slog.Error("KMS DeleteAlias storage failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	slog.Info("KMS DeleteAlias", "aliasName", req.AliasName)
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleUpdateAlias(w http.ResponseWriter, body []byte) {
	var req struct {
		AliasName   string `json:"AliasName"`
		TargetKeyId string `json:"TargetKeyId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}

	if err := validateAliasName(req.AliasName); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", err.Error())
		return
	}
	if strings.HasPrefix(req.AliasName, "alias/aws/") {
		writeError(w, http.StatusBadRequest, "InvalidAliasNameException",
			"alias names beginning with alias/aws/ are reserved for AWS managed keys")
		return
	}
	if req.TargetKeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "TargetKeyId is required")
		return
	}

	newKeyID, ok := resolveKeyID(req.TargetKeyId)
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidArnException",
			fmt.Sprintf("Invalid key ARN: %s", req.TargetKeyId))
		return
	}

	// Look up current alias target to enforce same KeySpec/KeyUsage constraint.
	oldKeyID, err := ro.storage.ResolveAlias(req.AliasName)
	if err != nil {
		if errors.Is(err, ErrAliasNotFound) {
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Alias %s not found", req.AliasName))
			return
		}
		slog.Error("KMS UpdateAlias: ResolveAlias failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	oldMeta, err := ro.storage.GetKeyMetadata(oldKeyID)
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Invalid keyId %s", oldKeyID))
			return
		}
		slog.Error("KMS UpdateAlias: GetKeyMetadata (old) failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	newMeta, err := ro.storage.GetKeyMetadata(newKeyID)
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Invalid keyId %s", newKeyID))
			return
		}
		slog.Error("KMS UpdateAlias: GetKeyMetadata (new) failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	if oldMeta.KeySpec != newMeta.KeySpec || oldMeta.KeyUsage != newMeta.KeyUsage {
		writeError(w, http.StatusBadRequest, "KMSInvalidStateException",
			fmt.Sprintf(
				"The new target key (spec=%s, usage=%s) is not compatible with the current key (spec=%s, usage=%s)",
				newMeta.KeySpec,
				newMeta.KeyUsage,
				oldMeta.KeySpec,
				oldMeta.KeyUsage,
			))
		return
	}

	if err := ro.storage.UpdateAlias(req.AliasName, newKeyID); err != nil {
		if errors.Is(err, ErrAliasNotFound) {
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Alias %s not found", req.AliasName))
			return
		}
		if errors.Is(err, ErrKeyNotFound) {
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Invalid keyId %s", newKeyID))
			return
		}
		slog.Error("KMS UpdateAlias storage failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	slog.Info("KMS UpdateAlias", "aliasName", req.AliasName, "newKeyID", newKeyID)
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleListAliases(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId  string `json:"KeyId"`
		Limit  *int   `json:"Limit"`
		Marker string `json:"Marker"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}

	limit := 50
	if req.Limit != nil {
		if *req.Limit < 1 || *req.Limit > 100 {
			writeError(w, http.StatusBadRequest, "ValidationException",
				fmt.Sprintf(
					"Value %d at 'limit' failed to satisfy constraint: Member must have value between 1 and 100, inclusive",
					*req.Limit,
				))
			return
		}
		limit = *req.Limit
	}

	// Resolve optional KeyId filter.
	var filterKeyID string
	if req.KeyId != "" {
		resolved, ok := resolveKeyID(req.KeyId)
		if !ok {
			writeError(w, http.StatusBadRequest, "InvalidArnException",
				fmt.Sprintf("Invalid key ARN: %s", req.KeyId))
			return
		}
		// Verify the key exists.
		if _, err := ro.storage.GetKeyMetadata(resolved); err != nil {
			if errors.Is(err, ErrKeyNotFound) {
				writeError(w, http.StatusBadRequest, "NotFoundException",
					fmt.Sprintf("Invalid keyId %s", resolved))
				return
			}
			slog.Error("KMS ListAliases: GetKeyMetadata failure", "err", err)
			writeError(
				w,
				http.StatusInternalServerError,
				"KMSInternalException",
				"internal server error",
			)
			return
		}
		filterKeyID = resolved
	}

	aliases, err := ro.storage.ListAliases(filterKeyID)
	if err != nil {
		slog.Error("KMS ListAliases storage failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}
	if aliases == nil {
		aliases = []AliasEntry{}
	}

	// Apply Marker pagination.
	if req.Marker != "" {
		start := -1
		for i, a := range aliases {
			if a.AliasName == req.Marker {
				start = i + 1
				break
			}
		}
		if start == -1 {
			// Marker not found by exact match — position to the next alias lexicographically.
			// kumolo deviation: stale markers (e.g. alias deleted between pages) silently advance
			// rather than returning InvalidMarkerException.
			start = sort.Search(len(aliases), func(i int) bool {
				return aliases[i].AliasName >= req.Marker
			})
		}
		aliases = aliases[start:]
	}

	truncated := len(aliases) > limit
	if truncated {
		aliases = aliases[:limit]
	}

	resp := map[string]any{
		"Aliases":   aliases,
		"Truncated": truncated,
	}
	if truncated && len(aliases) > 0 {
		resp["NextMarker"] = aliases[len(aliases)-1].AliasName
	}

	slog.Debug("KMS ListAliases", "count", len(aliases))
	writeJSON(w, http.StatusOK, resp)
}
