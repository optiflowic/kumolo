package kms

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

const (
	defaultPendingWindowDays  = 30
	minPendingWindowDays      = 7
	maxPendingWindowDays      = 30
	defaultRotationPeriodDays = 365
	minRotationPeriodDays     = 90
	maxRotationPeriodDays     = 2560
)

func (ro *Router) handleEnableKey(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId string `json:"KeyId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	if err := ro.storage.EnableKey(keyID); err != nil {
		writeLifecycleError(w, keyID, "EnableKey", err)
		return
	}

	slog.Info("KMS EnableKey", "keyID", keyID)
	writeEmpty(w)
}

func (ro *Router) handleDisableKey(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId string `json:"KeyId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	if err := ro.storage.DisableKey(keyID); err != nil {
		writeLifecycleError(w, keyID, "DisableKey", err)
		return
	}

	slog.Info("KMS DisableKey", "keyID", keyID)
	writeEmpty(w)
}

func (ro *Router) handleScheduleKeyDeletion(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId               string `json:"KeyId"`
		PendingWindowInDays *int   `json:"PendingWindowInDays"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}

	pendingDays := defaultPendingWindowDays
	if req.PendingWindowInDays != nil {
		pendingDays = *req.PendingWindowInDays
		if pendingDays < minPendingWindowDays || pendingDays > maxPendingWindowDays {
			writeError(w, http.StatusBadRequest, "ValidationException",
				fmt.Sprintf("PendingWindowInDays must be between %d and %d, got %d",
					minPendingWindowDays, maxPendingWindowDays, pendingDays))
			return
		}
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	meta, err := ro.storage.ScheduleKeyDeletion(keyID, pendingDays)
	if err != nil {
		writeLifecycleError(w, keyID, "ScheduleKeyDeletion", err)
		return
	}

	slog.Info("KMS ScheduleKeyDeletion", "keyID", keyID, "pendingDays", pendingDays)
	writeJSON(w, http.StatusOK, map[string]any{
		"KeyId":               meta.Arn,
		"KeyState":            meta.KeyState,
		"DeletionDate":        meta.DeletionDate,
		"PendingWindowInDays": pendingDays,
	})
}

func (ro *Router) handleCancelKeyDeletion(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId string `json:"KeyId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	meta, err := ro.storage.CancelKeyDeletion(keyID)
	if err != nil {
		writeLifecycleError(w, keyID, "CancelKeyDeletion", err)
		return
	}

	slog.Info("KMS CancelKeyDeletion", "keyID", keyID)
	writeJSON(w, http.StatusOK, map[string]any{
		"KeyId": meta.Arn,
	})
}

func (ro *Router) handleEnableKeyRotation(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId                string `json:"KeyId"`
		RotationPeriodInDays *int   `json:"RotationPeriodInDays"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}

	rotationDays := defaultRotationPeriodDays
	if req.RotationPeriodInDays != nil {
		rotationDays = *req.RotationPeriodInDays
		if rotationDays < minRotationPeriodDays || rotationDays > maxRotationPeriodDays {
			writeError(
				w,
				http.StatusBadRequest,
				"ValidationException",
				fmt.Sprintf(
					"RotationPeriodInDays must be between %d and %d, got %d",
					minRotationPeriodDays, maxRotationPeriodDays, rotationDays,
				),
			)
			return
		}
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	if err := ro.storage.EnableKeyRotation(keyID, rotationDays); err != nil {
		writeRotationError(w, keyID, "EnableKeyRotation", err)
		return
	}

	slog.Info("KMS EnableKeyRotation", "keyID", keyID, "rotationDays", rotationDays)
	writeEmpty(w)
}

func (ro *Router) handleDisableKeyRotation(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId string `json:"KeyId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	if err := ro.storage.DisableKeyRotation(keyID); err != nil {
		writeRotationError(w, keyID, "DisableKeyRotation", err)
		return
	}

	slog.Info("KMS DisableKeyRotation", "keyID", keyID)
	writeEmpty(w)
}

func (ro *Router) handleGetKeyRotationStatus(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId string `json:"KeyId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	meta, cfg, err := ro.storage.GetKeyRotationStatus(keyID)
	if err != nil {
		writeRotationError(w, keyID, "GetKeyRotationStatus", err)
		return
	}

	resp := map[string]any{
		"KeyId":              meta.Arn,
		"KeyRotationEnabled": cfg.Enabled,
	}
	if cfg.Enabled {
		resp["RotationPeriodInDays"] = cfg.RotationPeriodInDays
		resp["NextRotationDate"] = cfg.NextRotationDate
	}

	slog.Debug("KMS GetKeyRotationStatus", "keyID", keyID, "enabled", cfg.Enabled)
	writeJSON(w, http.StatusOK, resp)
}

const (
	maxTagKeyLen   = 128
	maxTagValueLen = 256
)

func (ro *Router) handleTagResource(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId string     `json:"KeyId"`
		Tags  []TagEntry `json:"Tags"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}
	if len(req.Tags) == 0 {
		writeError(w, http.StatusBadRequest, "ValidationException", "Tags is required")
		return
	}
	for _, t := range req.Tags {
		if strings.HasPrefix(t.TagKey, "aws:") {
			writeError(w, http.StatusBadRequest, "TagException",
				fmt.Sprintf("Tag key %q is reserved for AWS use", t.TagKey))
			return
		}
		if len(t.TagKey) < 1 || len(t.TagKey) > maxTagKeyLen {
			writeError(w, http.StatusBadRequest, "TagException",
				fmt.Sprintf("Tag key must be between 1 and %d characters", maxTagKeyLen))
			return
		}
		if len(t.TagValue) > maxTagValueLen {
			writeError(w, http.StatusBadRequest, "TagException",
				fmt.Sprintf("Tag value must be at most %d characters", maxTagValueLen))
			return
		}
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	if err := ro.storage.TagResource(keyID, req.Tags); err != nil {
		writeTagError(w, keyID, "TagResource", err)
		return
	}

	slog.Info("KMS TagResource", "keyID", keyID, "count", len(req.Tags))
	writeEmpty(w)
}

func (ro *Router) handleUntagResource(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId   string   `json:"KeyId"`
		TagKeys []string `json:"TagKeys"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}
	if len(req.TagKeys) == 0 {
		writeError(w, http.StatusBadRequest, "ValidationException", "TagKeys is required")
		return
	}
	for _, k := range req.TagKeys {
		if strings.HasPrefix(k, "aws:") {
			writeError(w, http.StatusBadRequest, "TagException",
				fmt.Sprintf("Tag key %q is reserved for AWS use", k))
			return
		}
		if len(k) < 1 || len(k) > maxTagKeyLen {
			writeError(w, http.StatusBadRequest, "TagException",
				fmt.Sprintf("Tag key must be between 1 and %d characters", maxTagKeyLen))
			return
		}
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	if err := ro.storage.UntagResource(keyID, req.TagKeys); err != nil {
		writeTagError(w, keyID, "UntagResource", err)
		return
	}

	slog.Info("KMS UntagResource", "keyID", keyID, "count", len(req.TagKeys))
	writeEmpty(w)
}

// writeTagError handles errors common to TagResource and UntagResource.
func writeTagError(w http.ResponseWriter, keyID, op string, err error) {
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
	case errors.Is(err, ErrTagLimitExceeded):
		slog.Debug("KMS "+op+": tag limit exceeded", "keyID", keyID)
		writeError(w, http.StatusBadRequest, "LimitExceededException",
			fmt.Sprintf("Key %s has reached the maximum number of tags (%d)", keyID, maxTagsPerKey))
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

// writeLifecycleError handles errors common to EnableKey, DisableKey,
// ScheduleKeyDeletion, and CancelKeyDeletion.
func writeLifecycleError(w http.ResponseWriter, keyID, op string, err error) {
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

func (ro *Router) handleRotateKeyOnDemand(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId string `json:"KeyId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	meta, err := ro.storage.RotateKeyOnDemand(keyID)
	if err != nil {
		writeOnDemandRotationError(w, keyID, err)
		return
	}

	slog.Info("KMS RotateKeyOnDemand", "keyID", keyID)
	writeJSON(w, http.StatusOK, map[string]any{
		"KeyId": meta.Arn,
	})
}

const (
	defaultListKeyRotationsLimit = 100
	maxListKeyRotationsLimit     = 1000
)

func (ro *Router) handleListKeyRotations(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId  string `json:"KeyId"`
		Limit  *int   `json:"Limit"`
		Marker string `json:"Marker"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}
	if !validateLimit(w, req.Limit, maxListKeyRotationsLimit) {
		return
	}

	limit := defaultListKeyRotationsLimit
	if req.Limit != nil {
		limit = *req.Limit
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	records, nextMarker, err := ro.storage.ListKeyRotations(keyID, limit, req.Marker)
	if err != nil {
		writeListKeyRotationsError(w, keyID, req.Marker, err)
		return
	}

	if records == nil {
		records = []RotationRecord{}
	}

	resp := map[string]any{
		"Rotations": records,
		"Truncated": nextMarker != "",
	}
	if nextMarker != "" {
		resp["NextMarker"] = nextMarker
	}

	slog.Debug("KMS ListKeyRotations", "keyID", keyID, "count", len(records))
	writeJSON(w, http.StatusOK, resp)
}

// writeListKeyRotationsError handles errors specific to ListKeyRotations.
func writeListKeyRotationsError(w http.ResponseWriter, keyID, marker string, err error) {
	switch {
	case errors.Is(err, ErrKeyNotFound):
		slog.Debug("KMS ListKeyRotations: key not found", "keyID", keyID)
		writeError(w, http.StatusBadRequest, "NotFoundException",
			fmt.Sprintf("Invalid keyId %s", keyID))
	case errors.Is(err, ErrInvalidKeyState):
		slog.Debug("KMS ListKeyRotations: invalid key state", "keyID", keyID)
		writeError(
			w,
			http.StatusBadRequest,
			"KMSInvalidStateException",
			fmt.Sprintf(
				"KMS key %s is in a state that is not compatible with this operation",
				keyID,
			),
		)
	case errors.Is(err, ErrUnsupportedOp):
		slog.Debug("KMS ListKeyRotations: unsupported operation", "keyID", keyID)
		writeError(
			w,
			http.StatusBadRequest,
			"UnsupportedOperationException",
			fmt.Sprintf(
				"Key %s does not support this operation; only SYMMETRIC_DEFAULT keys support key rotation",
				keyID,
			),
		)
	case errors.Is(err, ErrInvalidMarker):
		slog.Debug("KMS ListKeyRotations: invalid marker", "keyID", keyID)
		writeError(w, http.StatusBadRequest, "InvalidMarkerException",
			fmt.Sprintf("The marker %s is not valid", marker))
	default:
		slog.Error("KMS ListKeyRotations storage failure", "keyID", keyID, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
	}
}

// writeOnDemandRotationError handles errors specific to RotateKeyOnDemand.
func writeOnDemandRotationError(w http.ResponseWriter, keyID string, err error) {
	switch {
	case errors.Is(err, ErrKeyNotFound):
		slog.Debug("KMS RotateKeyOnDemand: key not found", "keyID", keyID)
		writeError(w, http.StatusBadRequest, "NotFoundException",
			fmt.Sprintf("Invalid keyId %s", keyID))
	case errors.Is(err, ErrKeyDisabled):
		slog.Debug("KMS RotateKeyOnDemand: key disabled", "keyID", keyID)
		writeError(w, http.StatusBadRequest, "DisabledException",
			fmt.Sprintf("KMS key %s is disabled", keyID))
	case errors.Is(err, ErrInvalidKeyState):
		slog.Debug("KMS RotateKeyOnDemand: invalid key state", "keyID", keyID)
		writeError(
			w,
			http.StatusBadRequest,
			"KMSInvalidStateException",
			fmt.Sprintf(
				"KMS key %s is in a state that is not compatible with this operation",
				keyID,
			),
		)
	case errors.Is(err, ErrUnsupportedOp):
		slog.Debug("KMS RotateKeyOnDemand: unsupported operation", "keyID", keyID)
		writeError(
			w,
			http.StatusBadRequest,
			"UnsupportedOperationException",
			fmt.Sprintf(
				"Key %s does not support this operation; only SYMMETRIC_DEFAULT keys support key rotation",
				keyID,
			),
		)
	case errors.Is(err, ErrOnDemandRotationLimit):
		slog.Debug("KMS RotateKeyOnDemand: limit exceeded", "keyID", keyID)
		writeError(
			w,
			http.StatusBadRequest,
			"LimitExceededException",
			fmt.Sprintf(
				"Key %s has reached the maximum number of on-demand rotations (%d)",
				keyID,
				maxOnDemandRotations,
			),
		)
	default:
		slog.Error("KMS RotateKeyOnDemand storage failure", "keyID", keyID, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
	}
}

// writeRotationError handles errors common to rotation operations and
// GetKeyRotationStatus, which add DisabledException and UnsupportedOperationException.
func writeRotationError(w http.ResponseWriter, keyID, op string, err error) {
	switch {
	case errors.Is(err, ErrKeyNotFound):
		slog.Debug("KMS "+op+": key not found", "keyID", keyID)
		writeError(w, http.StatusBadRequest, "NotFoundException",
			fmt.Sprintf("Invalid keyId %s", keyID))
	case errors.Is(err, ErrKeyDisabled):
		slog.Debug("KMS "+op+": key disabled", "keyID", keyID)
		writeError(w, http.StatusBadRequest, "DisabledException",
			fmt.Sprintf("KMS key %s is disabled", keyID))
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
	case errors.Is(err, ErrUnsupportedOp):
		slog.Debug("KMS "+op+": unsupported operation", "keyID", keyID)
		writeError(
			w,
			http.StatusBadRequest,
			"UnsupportedOperationException",
			fmt.Sprintf(
				"Key %s does not support this operation; only SYMMETRIC_DEFAULT keys support key rotation",
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
