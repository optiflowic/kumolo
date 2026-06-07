package s3

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogObjectKey(t *testing.T) {
	t.Run("no prefix", func(t *testing.T) {
		ts := time.Date(2026, 6, 7, 12, 34, 56, 123456789, time.UTC)
		key := logObjectKey("", ts, [4]byte{0xAB, 0xCD, 0xEF, 0x01})
		assert.Equal(t, "2026-06-07-12-34-56-123456789-abcdef01", key)
	})
	t.Run("with prefix", func(t *testing.T) {
		ts := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
		key := logObjectKey("logs/", ts, [4]byte{})
		assert.Equal(t, "logs/2026-06-07-00-00-00-000000000-00000000", key)
	})
	t.Run("different nonces produce different keys", func(t *testing.T) {
		ts := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
		k1 := logObjectKey("", ts, [4]byte{0x01, 0x02, 0x03, 0x04})
		k2 := logObjectKey("", ts, [4]byte{0x05, 0x06, 0x07, 0x08})
		assert.NotEqual(t, k1, k2)
	})
}

func TestLogOperationName(t *testing.T) {
	tests := []struct {
		method   string
		isObject bool
		want     string
	}{
		{"GET", true, "REST.GET.OBJECT"},
		{"PUT", true, "REST.PUT.OBJECT"},
		{"DELETE", true, "REST.DELETE.OBJECT"},
		{"HEAD", true, "REST.HEAD.OBJECT"},
		{"GET", false, "REST.GET.BUCKET"},
		{"PUT", false, "REST.PUT.BUCKET"},
		{"DELETE", false, "REST.DELETE.BUCKET"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, logOperationName(tt.method, tt.isObject))
		})
	}
}

func TestFormatLogEntry(t *testing.T) {
	ts := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/my-bucket/my-key", nil)
	req.RemoteAddr = "192.168.1.1:54321"
	req.Header.Set("User-Agent", "aws-sdk-go/1.0")
	req.Header.Set("Referer", "https://example.com")

	rec := newResponseRecorder(httptest.NewRecorder())
	rec.status = http.StatusOK
	rec.bytesWritten = 42

	entry := formatLogEntry("my-bucket", "my-key", req, rec, ts)

	assert.Contains(t, entry, "my-bucket")
	assert.Contains(t, entry, "[07/Jun/2026:12:00:00 +0000]")
	assert.Contains(t, entry, "192.168.1.1")
	assert.Contains(t, entry, "REST.GET.OBJECT")
	assert.Contains(t, entry, "my-key")
	assert.Contains(t, entry, "200")
	assert.Contains(t, entry, "42")
	assert.Contains(t, entry, "aws-sdk-go/1.0")
	assert.Contains(t, entry, "https://example.com")
}

func TestFormatLogEntry_defaults(t *testing.T) {
	ts := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/my-bucket", nil)
	req.RemoteAddr = "127.0.0.1:8080"

	rec := newResponseRecorder(httptest.NewRecorder())
	rec.status = http.StatusOK
	rec.bytesWritten = 0

	entry := formatLogEntry("my-bucket", "", req, rec, ts)

	assert.Contains(t, entry, "REST.GET.BUCKET")
	// No key → dash
	assert.Contains(t, entry, " - ")
	// No bytes → dash
	assert.Contains(t, entry, ` - - - "-" "-"`)
}

func TestResponseRecorder(t *testing.T) {
	t.Run("captures status and bytes", func(t *testing.T) {
		w := httptest.NewRecorder()
		rec := newResponseRecorder(w)

		rec.WriteHeader(http.StatusNotFound)
		n, err := rec.Write([]byte("hello"))

		require.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, http.StatusNotFound, rec.status)
		assert.Equal(t, int64(5), rec.bytesWritten)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "hello", w.Body.String())
	})
	t.Run("defaults to 200", func(t *testing.T) {
		rec := newResponseRecorder(httptest.NewRecorder())
		assert.Equal(t, http.StatusOK, rec.status)
		assert.Equal(t, int64(0), rec.bytesWritten)
	})
	t.Run("flush forwarded", func(t *testing.T) {
		w := httptest.NewRecorder()
		rec := newResponseRecorder(w)
		assert.NotPanics(t, func() { rec.Flush() })
	})
}

func TestWriteAccessLog(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateBucket("logs", "us-east-1", false))

	t.Run("writes readable object", func(t *testing.T) {
		err := s.WriteAccessLog("logs", "prefix/2026-06-07-record", "log line content")
		require.NoError(t, err)

		f, meta, err := s.GetObject("logs", "prefix/2026-06-07-record")
		require.NoError(t, err)
		t.Cleanup(func() { _ = f.Close() })

		body, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, "log line content", string(body))
		assert.Equal(t, "text/plain", meta.ContentType)
	})

	t.Run("returns error for missing target bucket", func(t *testing.T) {
		err := s.WriteAccessLog("no-such-bucket", "key", "content")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})
}

func TestAppendAccessLog(t *testing.T) {
	ro := newTestRouter(t)

	// Create source and target buckets.
	req := httptest.NewRequest(http.MethodPut, "/src", nil)
	ro.ServeHTTP(httptest.NewRecorder(), req)
	req = httptest.NewRequest(http.MethodPut, "/logs", nil)
	ro.ServeHTTP(httptest.NewRecorder(), req)

	// Enable logging on src bucket, targeting logs bucket.
	loggingXML := `<BucketLoggingStatus xmlns="http://doc.s3.amazonaws.com/2006-03-01">
	<LoggingEnabled>
		<TargetBucket>logs</TargetBucket>
		<TargetPrefix>access/</TargetPrefix>
	</LoggingEnabled>
</BucketLoggingStatus>`
	req = httptest.NewRequest(http.MethodPut, "/src?logging", strings.NewReader(loggingXML))
	ro.ServeHTTP(httptest.NewRecorder(), req)

	// Make a request to src bucket; this should produce a log object.
	req = httptest.NewRequest(http.MethodGet, "/src", nil)
	w := httptest.NewRecorder()
	ro.ServeHTTP(w, req)

	// Verify at least one log object was written to the logs bucket.
	// Note: the PUT ?logging request itself is also logged (AWS behaviour),
	// so we may see more than one entry.
	objects, err := ro.storage.ListObjects("logs")
	require.NoError(t, err)
	require.NotEmpty(t, objects, "expected at least one access log object")

	for _, obj := range objects {
		assert.True(t, strings.HasPrefix(obj.Key, "access/"),
			"log key %q should start with configured prefix", obj.Key)
	}

	// Verify the GET /src request was logged.
	var found bool
	for _, obj := range objects {
		f, _, err := ro.storage.GetObject("logs", obj.Key)
		require.NoError(t, err)
		body, err := io.ReadAll(f)
		_ = f.Close()
		require.NoError(t, err)
		if strings.Contains(string(body), "REST.GET.BUCKET") {
			found = true
			assert.Contains(t, string(body), "src")
		}
	}
	assert.True(t, found, "expected a log entry for the GET /src request")
}

func TestAppendAccessLog_noLoggingConfigured(t *testing.T) {
	ro := newTestRouter(t)

	req := httptest.NewRequest(http.MethodPut, "/my-bucket", nil)
	ro.ServeHTTP(httptest.NewRecorder(), req)

	req = httptest.NewRequest(http.MethodGet, "/my-bucket", nil)
	ro.ServeHTTP(httptest.NewRecorder(), req)

	// No log bucket exists — verify no objects were written anywhere.
	objects, err := ro.storage.ListObjects("my-bucket")
	require.NoError(t, err)
	assert.Empty(t, objects)
}

func TestAppendAccessLog_rootRequest(t *testing.T) {
	ro := newTestRouter(t)
	// Root-level requests (no bucket) must not trigger access logging.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	assert.NotPanics(t, func() { ro.ServeHTTP(w, req) })
}

func TestAppendAccessLog_missingTargetBucket(t *testing.T) {
	ro := newTestRouter(t)

	req := httptest.NewRequest(http.MethodPut, "/src", nil)
	ro.ServeHTTP(httptest.NewRecorder(), req)

	// Point logging to a non-existent bucket.
	loggingXML := `<BucketLoggingStatus xmlns="http://doc.s3.amazonaws.com/2006-03-01">
	<LoggingEnabled>
		<TargetBucket>ghost-bucket</TargetBucket>
		<TargetPrefix></TargetPrefix>
	</LoggingEnabled>
</BucketLoggingStatus>`
	req = httptest.NewRequest(http.MethodPut, "/src?logging", strings.NewReader(loggingXML))
	ro.ServeHTTP(httptest.NewRecorder(), req)

	// Request must succeed even though the log target does not exist.
	req = httptest.NewRequest(http.MethodGet, "/src", nil)
	w := httptest.NewRecorder()
	assert.NotPanics(t, func() { ro.ServeHTTP(w, req) })
	assert.Equal(t, http.StatusOK, w.Code)
}
