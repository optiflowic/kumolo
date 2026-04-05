package logging

import (
	"log/slog"
	"net/http"
	"time"
)

// responseRecorder wraps http.ResponseWriter to capture the status code.
type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Middleware logs each incoming request and its response status and duration.
// Log level is chosen by status class: 2xx → Info, 4xx → Warn, 5xx → Error.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Log( // #nosec G706 -- method and path are from the HTTP request; log injection risk is accepted for a local development emulator
			r.Context(),
			logLevelForStatus(rec.status),
			"incoming request",
			"method",
			r.Method,
			"path",
			r.URL.Path,
			"status",
			rec.status,
			"duration",
			time.Since(start),
		)
	})
}

func logLevelForStatus(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
