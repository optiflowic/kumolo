package kms

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
)

func (ro *Router) handleCreateKey(w http.ResponseWriter, body []byte) {
	var req struct {
		Description                    string `json:"Description"`
		KeySpec                        string `json:"KeySpec"`
		CustomerMasterKeySpec          string `json:"CustomerMasterKeySpec"`
		KeyUsage                       string `json:"KeyUsage"`
		MultiRegion                    bool   `json:"MultiRegion"`
		Origin                         string `json:"Origin"`
		Policy                         string `json:"Policy"`
		BypassPolicyLockoutSafetyCheck bool   `json:"BypassPolicyLockoutSafetyCheck"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}

	// CustomerMasterKeySpec is a deprecated alias for KeySpec.
	if req.KeySpec == "" && req.CustomerMasterKeySpec != "" {
		req.KeySpec = req.CustomerMasterKeySpec
	}
	if req.KeySpec == "" {
		req.KeySpec = "SYMMETRIC_DEFAULT"
	}
	if !validKeySpecs[req.KeySpec] {
		writeError(w, http.StatusBadRequest, "ValidationException",
			fmt.Sprintf("Invalid KeySpec: %s", req.KeySpec))
		return
	}

	if req.KeyUsage == "" {
		req.KeyUsage = defaultKeyUsage[req.KeySpec]
	}
	if !isValidKeyUsageForSpec(req.KeySpec, req.KeyUsage) {
		writeError(w, http.StatusBadRequest, "ValidationException",
			fmt.Sprintf("KeyUsage %s is not compatible with KeySpec %s", req.KeyUsage, req.KeySpec))
		return
	}

	origin := req.Origin
	if origin == "" {
		origin = "AWS_KMS"
	}
	if origin != "AWS_KMS" {
		writeError(w, http.StatusBadRequest, "UnsupportedOperationException",
			fmt.Sprintf("Origin %s is not supported by kumolo", origin))
		return
	}

	meta, err := ro.storage.CreateKey(CreateKeyInput{
		Description: req.Description,
		KeySpec:     req.KeySpec,
		KeyUsage:    req.KeyUsage,
		MultiRegion: req.MultiRegion,
		Origin:      origin,
		Policy:      req.Policy,
	})
	if err != nil {
		slog.Error("CreateKey storage failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	slog.Info("KMS CreateKey", "keyID", meta.KeyID, "spec", meta.KeySpec)
	writeJSON(w, http.StatusOK, map[string]any{"KeyMetadata": meta})
}

// resolveKeyRef resolves a key reference (key ID, key ARN, alias name, or alias ARN)
// to a plain key UUID. Writes an HTTP error response and returns ("", false) on failure.
func (ro *Router) resolveKeyRef(w http.ResponseWriter, keyRef string) (string, bool) {
	if id, ok := resolveKeyID(keyRef); ok {
		return id, true
	}
	if isAliasRef(keyRef) {
		aliasName, nameOK := normalizeAliasRef(keyRef)
		if !nameOK {
			writeError(w, http.StatusBadRequest, "InvalidArnException",
				fmt.Sprintf("Invalid alias ARN: %s", keyRef))
			return "", false
		}
		id, err := ro.storage.ResolveAlias(aliasName)
		if errors.Is(err, ErrAliasNotFound) {
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Invalid keyId %s", keyRef))
			return "", false
		}
		if err != nil {
			slog.Error("KMS: ResolveAlias failure", "err", err)
			writeError(
				w,
				http.StatusInternalServerError,
				"KMSInternalException",
				"internal server error",
			)
			return "", false
		}
		return id, true
	}
	writeError(w, http.StatusBadRequest, "InvalidArnException",
		fmt.Sprintf("Invalid key ARN: %s", keyRef))
	return "", false
}

func (ro *Router) handleDescribeKey(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId string `json:"KeyId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException",
			"KeyId is required")
		return
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	meta, err := ro.storage.GetKeyMetadata(keyID)
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			slog.Debug("KMS DescribeKey: key not found", "keyID", keyID)
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Invalid keyId %s", keyID))
			return
		}
		slog.Error("DescribeKey storage failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	slog.Debug("KMS DescribeKey", "keyID", keyID)
	writeJSON(w, http.StatusOK, map[string]any{"KeyMetadata": meta})
}

type keyListEntry struct {
	KeyID  string `json:"KeyId"`
	KeyArn string `json:"KeyArn"`
}

func (ro *Router) handleListKeys(w http.ResponseWriter, body []byte) {
	var req struct {
		Limit  *int   `json:"Limit"`
		Marker string `json:"Marker"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}

	limit := 100
	if req.Limit != nil {
		if *req.Limit < 1 || *req.Limit > 1000 {
			writeError(
				w,
				http.StatusBadRequest,
				"ValidationException",
				fmt.Sprintf(
					"Value %d at 'limit' failed to satisfy constraint: Member must have value between 1 and 1000, inclusive",
					*req.Limit,
				),
			)
			return
		}
		limit = *req.Limit
	}

	ids, err := ro.storage.ListKeyIDs()
	if err != nil {
		slog.Error("ListKeys storage failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}
	if ids == nil {
		ids = []string{}
	}

	// Apply Marker pagination cursor.
	if req.Marker != "" {
		if !looksLikeUUID(req.Marker) {
			writeError(w, http.StatusBadRequest, "InvalidMarkerException",
				fmt.Sprintf("The marker %s is not valid", req.Marker))
			return
		}
		start := -1
		for i, id := range ids {
			if id == req.Marker {
				start = i + 1
				break
			}
		}
		if start == -1 {
			// Stale marker (valid UUID but key was deleted) — advance silently.
			start = sort.SearchStrings(ids, req.Marker)
			if start < len(ids) && ids[start] < req.Marker {
				// unreachable: sort.SearchStrings guarantees ids[start] >= marker
				start++
			}
		}
		ids = ids[start:]
	}

	truncated := len(ids) > limit
	if truncated {
		ids = ids[:limit]
	}

	entries := make([]keyListEntry, len(ids))
	for i, id := range ids {
		entries[i] = keyListEntry{KeyID: id, KeyArn: keyARN(id)}
	}

	resp := map[string]any{
		"Keys":      entries,
		"Truncated": truncated,
	}
	if truncated && len(ids) > 0 {
		resp["NextMarker"] = ids[len(ids)-1]
	}

	slog.Debug("KMS ListKeys", "count", len(entries))
	writeJSON(w, http.StatusOK, resp)
}

// resolveKeyMeta fetches key metadata and validates the key is not in
// PendingDeletion state. It writes the appropriate error response and returns
// false if the caller should stop processing.
func (ro *Router) resolveKeyMeta(w http.ResponseWriter, keyID string) (KeyMetadata, bool) {
	meta, err := ro.storage.GetKeyMetadata(keyID)
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Invalid keyId %s", keyID))
			return KeyMetadata{}, false
		}
		slog.Error("GetKeyMetadata failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return KeyMetadata{}, false
	}
	if meta.KeyState == keyStatePendingDeletion {
		writeError(w, http.StatusBadRequest, "KMSInvalidStateException",
			fmt.Sprintf("KMS key %s is pending deletion", keyID))
		return KeyMetadata{}, false
	}
	return meta, true
}

func (ro *Router) handleGetKeyPolicy(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId      string `json:"KeyId"`
		PolicyName string `json:"PolicyName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}
	if req.PolicyName != "" && req.PolicyName != "default" {
		writeError(w, http.StatusBadRequest, "ValidationException",
			fmt.Sprintf("PolicyName %s is not supported; only 'default' is valid", req.PolicyName))
		return
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	if _, ok := ro.resolveKeyMeta(w, keyID); !ok {
		return
	}

	policy, err := ro.storage.GetKeyPolicy(keyID)
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			// unreachable: GetKeyMetadata succeeded above
			slog.Debug("KMS GetKeyPolicy: key not found", "keyID", keyID)
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Invalid keyId %s", keyID))
			return
		}
		slog.Error("GetKeyPolicy storage failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	slog.Debug("KMS GetKeyPolicy", "keyID", keyID)
	writeJSON(w, http.StatusOK, map[string]any{
		"Policy":     policy,
		"PolicyName": "default",
	})
}

func (ro *Router) handlePutKeyPolicy(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId                          string `json:"KeyId"`
		Policy                         string `json:"Policy"`
		PolicyName                     string `json:"PolicyName"`
		BypassPolicyLockoutSafetyCheck bool   `json:"BypassPolicyLockoutSafetyCheck"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}
	if req.Policy == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "Policy is required")
		return
	}
	if req.PolicyName != "" && req.PolicyName != "default" {
		writeError(w, http.StatusBadRequest, "ValidationException",
			fmt.Sprintf("PolicyName %s is not supported; only 'default' is valid", req.PolicyName))
		return
	}
	if len(req.Policy) > 32768 {
		writeError(w, http.StatusBadRequest, "LimitExceededException",
			"Policy exceeds the maximum allowed size of 32768 bytes")
		return
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	if _, ok := ro.resolveKeyMeta(w, keyID); !ok {
		return
	}

	if err := ro.storage.PutKeyPolicy(keyID, req.Policy); err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			// unreachable: GetKeyMetadata succeeded above
			slog.Debug("KMS PutKeyPolicy: key not found", "keyID", keyID)
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Invalid keyId %s", keyID))
			return
		}
		slog.Error("PutKeyPolicy storage failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	slog.Info("KMS PutKeyPolicy", "keyID", keyID)
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
}
