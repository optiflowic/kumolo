package kms

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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
