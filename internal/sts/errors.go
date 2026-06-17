package sts

import (
	"log/slog"
	"net/http"
	"time"
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
	if !rr.headerWritten {
		rr.status = status
		rr.headerWritten = true
	}
	rr.ResponseWriter.WriteHeader(status)
}

func (rr *responseRecorder) Flush() {
	if f, ok := rr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// emitRequestLog writes one structured log line per STS request.
// Level rules: 5xx → Error, everything else → Info.
func emitRequestLog(action string, rec *responseRecorder, duration time.Duration) {
	status := rec.status
	attrs := []any{
		"op", action,
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
		slog.Error( // #nosec G706 -- action comes from the Action query parameter; log injection risk accepted for a local dev emulator
			"request",
			attrs...)
	default:
		slog.Info( // #nosec G706 -- action comes from the Action query parameter; log injection risk accepted for a local dev emulator
			"request",
			attrs...)
	}
}
