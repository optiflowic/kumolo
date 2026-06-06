package dynamodb

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// AWS DynamoDB error type strings used in __type response fields.
const (
	ErrTypeValidationException             = "com.amazonaws.dynamodb.v20120810#ValidationException"
	ErrTypeResourceNotFoundException       = "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException"
	ErrTypeConditionalCheckFailedException = "com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException"
	ErrTypeTransactionCanceledException    = "com.amazonaws.dynamodb.v20120810#TransactionCanceledException"
	ErrTypeInternalServerError             = "com.amazonaws.dynamodb.v20120810#InternalServerError"
	ErrTypeResourceInUseException          = "com.amazonaws.dynamodb.v20120810#ResourceInUseException"
	ErrTypeTableNotFoundException          = "com.amazonaws.dynamodb.v20120810#TableNotFoundException"
	ErrTypeLimitExceededException          = "com.amazonaws.dynamodb.v20120810#LimitExceededException"
	ErrTypeNotImplemented                  = "com.amazonaws.dynamodb.v20120810#NotImplemented"
)

var (
	ErrTableNotFound              = errors.New("table not found")
	ErrTableAlreadyExists         = errors.New("table already exists")
	ErrValidationException        = errors.New("validation error")
	ErrConditionalCheckFailed     = errors.New("conditional check failed")
	ErrKinesisLimitExceeded       = errors.New("kinesis destinations limit exceeded")
	ErrKinesisDestinationNotFound = errors.New("kinesis destination not found")
)

// ConditionalCheckFailedError is returned by write operations when a ConditionExpression
// fails. It carries the item state at check time so handlers can include it in the
// ConditionalCheckFailedException response when ReturnValuesOnConditionCheckFailure=ALL_OLD.
type ConditionalCheckFailedError struct {
	Item map[string]any // nil when the item did not exist at check time
}

func (e *ConditionalCheckFailedError) Error() string { return ErrConditionalCheckFailed.Error() }
func (e *ConditionalCheckFailedError) Is(target error) bool {
	return target == ErrConditionalCheckFailed
}

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
