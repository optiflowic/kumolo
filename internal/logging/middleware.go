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
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info( // #nosec G706 -- method and path are from the HTTP request; log injection risk is accepted for a local development emulator
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
