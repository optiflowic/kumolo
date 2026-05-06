package dynamodb

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

var (
	ErrTableNotFound          = errors.New("table not found")
	ErrTableAlreadyExists     = errors.New("table already exists")
	ErrValidationException    = errors.New("validation error")
	ErrConditionalCheckFailed = errors.New("conditional check failed")
)

type CancellationReason struct {
	Code    string         `json:"Code"`
	Message string         `json:"Message,omitempty"`
	Item    map[string]any `json:"Item,omitempty"`
}

type TransactionCanceledError struct {
	Reasons []CancellationReason
}

func (e *TransactionCanceledError) Error() string { return "transaction canceled" }

type errResponse struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(errResponse{Type: errType, Message: message}); err != nil {
		slog.Warn("failed to encode DynamoDB error response", "err", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("failed to encode DynamoDB response", "err", err)
	}
}
