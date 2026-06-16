package dynamodb

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
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
	ErrTagLimitExceeded           = errors.New("tag limit exceeded")
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

// responseRecorder wraps http.ResponseWriter to capture the HTTP status and
// DynamoDB error type set by writeError.
type responseRecorder struct {
	http.ResponseWriter
	status        int
	headerWritten bool
	errCode       string
	errMsg        string
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (rr *responseRecorder) WriteHeader(status int) {
	if !rr.headerWritten {
		rr.status = status
		rr.headerWritten = true
	}
	rr.ResponseWriter.WriteHeader(status)
}

// emitRequestLog writes one structured log line per DynamoDB request.
// Level rules: 5xx → Error, 4xx → Debug, 2xx → Info.
func emitRequestLog(op string, rec *responseRecorder, duration time.Duration) {
	status := rec.status
	attrs := []any{
		"op", op,
		"status", status,
	}
	if rec.errCode != "" {
		attrs = append(attrs, "code", rec.errCode)
	}
	if status >= 500 && rec.errMsg != "" {
		attrs = append(attrs, "err", rec.errMsg)
	}
	attrs = append(attrs, "duration", duration.Round(time.Microsecond))

	switch {
	case status >= 500:
		slog.Error("request", attrs...)
	case status >= 400:
		slog.Debug("request", attrs...)
	default:
		slog.Info("request", attrs...)
	}
}

type errResponse struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, errType, message string) {
	if rec, ok := w.(*responseRecorder); ok {
		rec.errCode = errType
		rec.errMsg = message
	}
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
