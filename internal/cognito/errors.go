package cognito

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const (
	ErrTypeInvalidParameterException = "InvalidParameterException"
	ErrTypeResourceNotFoundException = "ResourceNotFoundException"
	ErrTypeUserNotFoundException     = "UserNotFoundException"
	ErrTypeUserNotConfirmedException = "UserNotConfirmedException"
	ErrTypeNotAuthorizedException    = "NotAuthorizedException"
	ErrTypeUsernameExistsException   = "UsernameExistsException"
	ErrTypeInternalErrorException    = "InternalErrorException"
	ErrTypeUnknownOperationException = "UnknownOperationException"
)

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

func emitRequestLog(op string, rec *responseRecorder, duration time.Duration) {
	status := rec.status
	attrs := []any{
		"service", "cognito",
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
		slog.Error(
			"request",
			attrs...) // #nosec G706 -- op comes from X-Amz-Target header; log injection risk accepted for a local dev emulator
	default:
		slog.Info(
			"request",
			attrs...) // #nosec G706 -- op comes from X-Amz-Target header; log injection risk accepted for a local dev emulator
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
		slog.Warn("failed to encode Cognito error response", "err", err)
	}
}
