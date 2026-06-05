package kms

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

var (
	ErrKeyNotFound          = errors.New("key not found")
	ErrKeyMaterialNotFound  = errors.New("key material not found")
	ErrKeyMaterialCorrupted = errors.New(
		"HMAC key material size mismatch: key was likely created before a version upgrade; recreate the key",
	)
	ErrAliasNotFound         = errors.New("alias not found")
	ErrAliasAlreadyExists    = errors.New("alias already exists")
	ErrAliasLimitExceeded    = errors.New("alias limit exceeded")
	ErrKeyDisabled           = errors.New("key is disabled")
	ErrKeyPendingDeletion    = errors.New("key is pending deletion")
	ErrInvalidKeyState       = errors.New("invalid key state for this operation")
	ErrUnsupportedOp         = errors.New("unsupported operation for this key type")
	ErrTagLimitExceeded      = errors.New("tag limit exceeded")
	ErrInvalidSignature      = errors.New("invalid signature")
	ErrOnDemandRotationLimit = errors.New("on-demand rotation limit exceeded (max 25)")
	ErrInvalidMarker         = errors.New("invalid pagination marker")
)

type errResponse struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(errResponse{Type: errType, Message: message}); err != nil {
		slog.Warn("failed to encode KMS error response", "err", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("failed to encode KMS response", "err", err)
	}
}

func writeEmpty(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
}
