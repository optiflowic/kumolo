package logging

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setLogger(t *testing.T, buf *testBuffer) {
	t.Helper()
	orig := slog.Default()
	slog.SetDefault(slog.New(NewBracketHandler(buf, slog.LevelInfo)))
	t.Cleanup(func() { slog.SetDefault(orig) })
}

func TestMiddleware(t *testing.T) {
	t.Run("logs method, path, status, and duration", func(t *testing.T) {
		var buf testBuffer
		setLogger(t, &buf)

		handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		}))

		req := httptest.NewRequest(http.MethodPut, "/my-bucket", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		line := buf.String()
		assert.Contains(t, line, "request")
		assert.Contains(t, line, "method=PUT")
		assert.Contains(t, line, "path=/my-bucket")
		assert.Contains(t, line, "status=201")
		assert.Contains(t, line, "duration=")
	})

	t.Run("defaults to 200 when handler does not call WriteHeader", func(t *testing.T) {
		var buf testBuffer
		setLogger(t, &buf)

		handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte("ok"))
			require.NoError(t, err)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		assert.Contains(t, buf.String(), "status=200")
	})
}

func TestLogLevelForStatus(t *testing.T) {
	tests := []struct {
		status    int
		wantLevel slog.Level
	}{
		{http.StatusOK, slog.LevelInfo},
		{http.StatusCreated, slog.LevelInfo},
		{http.StatusNoContent, slog.LevelInfo},
		{http.StatusBadRequest, slog.LevelWarn},
		{http.StatusNotFound, slog.LevelWarn},
		{http.StatusForbidden, slog.LevelWarn},
		{http.StatusInternalServerError, slog.LevelError},
		{http.StatusNotImplemented, slog.LevelError},
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.wantLevel, logLevelForStatus(tt.status))
		})
	}
}

func TestMiddlewareLogLevel(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantLevel string
	}{
		{"2xx logs at Info", http.StatusOK, "[INFO]"},
		{"4xx logs at Warn", http.StatusNotFound, "[WARN]"},
		{"5xx logs at Error", http.StatusInternalServerError, "[ERROR]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf testBuffer
			setLogger(t, &buf)

			handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

			assert.Contains(t, buf.String(), tt.wantLevel)
		})
	}
}

// testBuffer captures log output in tests.
type testBuffer struct {
	buf []byte
}

func (b *testBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *testBuffer) String() string {
	return string(b.buf)
}
