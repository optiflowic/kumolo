package kms

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
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
	ErrGrantNotFound         = errors.New("grant not found")
	ErrGrantLimitExceeded    = errors.New("grant limit exceeded (max 50000)")
)

// responseRecorder wraps http.ResponseWriter to capture the HTTP status and
// KMS error type set by writeError.
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
	if rr.headerWritten {
		return
	}
	rr.status = status
	rr.headerWritten = true
	rr.ResponseWriter.WriteHeader(status)
}

func (rr *responseRecorder) Flush() {
	if f, ok := rr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// emitRequestLog writes one structured log line per KMS request.
// Level rules: 5xx → Error, everything else → Info.
func emitRequestLog(op string, rec *responseRecorder, duration time.Duration) {
	status := rec.status
	attrs := []any{
		"service", "kms",
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
		slog.Error( // #nosec G706 -- op comes from X-Amz-Target header; log injection risk accepted for a local dev emulator
			"request",
			attrs...)
	default:
		slog.Info( // #nosec G706 -- op comes from X-Amz-Target header; log injection risk accepted for a local dev emulator
			"request",
			attrs...)
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
