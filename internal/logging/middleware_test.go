package logging

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddleware(t *testing.T) {
	t.Run("logs method, path, status, and duration", func(t *testing.T) {
		var buf testBuffer
		slog.SetDefault(slog.New(NewBracketHandler(&buf, slog.LevelInfo)))

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
		slog.SetDefault(slog.New(NewBracketHandler(&buf, slog.LevelInfo)))

		handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte("ok"))
			require.NoError(t, err)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		assert.Contains(t, buf.String(), "status=200")
	})
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
