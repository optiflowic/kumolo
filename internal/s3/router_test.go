package s3

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParsePath(t *testing.T) {
	tests := []struct {
		path       string
		wantBucket string
		wantKey    string
	}{
		{"/", "", ""},
		{"/my-bucket", "my-bucket", ""},
		{"/my-bucket/", "my-bucket", ""},
		{"/my-bucket/object.txt", "my-bucket", "object.txt"},
		{"/my-bucket/path/to/object.txt", "my-bucket", "path/to/object.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			bucket, key := parsePath(tt.path)
			assert.Equal(t, tt.wantBucket, bucket)
			assert.Equal(t, tt.wantKey, key)
		})
	}
}

func TestRouter(t *testing.T) {
	ro := NewRouter()

	t.Run("root path", func(t *testing.T) {
		t.Run("GET returns 501", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotImplemented, w.Code)
		})

		t.Run("non-GET returns 405", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	})

	t.Run("bucket path", func(t *testing.T) {
		methods := []struct {
			method string
			want   int
		}{
			{http.MethodGet, http.StatusNotImplemented},
			{http.MethodHead, http.StatusNotImplemented},
			{http.MethodPut, http.StatusNotImplemented},
			{http.MethodDelete, http.StatusNotImplemented},
			{http.MethodPost, http.StatusMethodNotAllowed},
		}
		for _, tt := range methods {
			t.Run(tt.method, func(t *testing.T) {
				req := httptest.NewRequest(tt.method, "/my-bucket", nil)
				w := httptest.NewRecorder()
				ro.ServeHTTP(w, req)
				assert.Equal(t, tt.want, w.Code)
				assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
			})
		}
	})

	t.Run("object path", func(t *testing.T) {
		methods := []struct {
			method string
			want   int
		}{
			{http.MethodGet, http.StatusNotImplemented},
			{http.MethodHead, http.StatusNotImplemented},
			{http.MethodPut, http.StatusNotImplemented},
			{http.MethodDelete, http.StatusNotImplemented},
			{http.MethodPost, http.StatusMethodNotAllowed},
		}
		for _, tt := range methods {
			t.Run(tt.method, func(t *testing.T) {
				req := httptest.NewRequest(tt.method, "/my-bucket/object.txt", nil)
				w := httptest.NewRecorder()
				ro.ServeHTTP(w, req)
				assert.Equal(t, tt.want, w.Code)
			})
		}
	})
}

func TestParseSigV4(t *testing.T) {
	t.Run("extracts credentials from valid header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(
			"Authorization",
			"AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20230101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=abc123",
		)

		ctx := ParseSigV4(req)
		assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", ctx.AccessKeyID)
		assert.Equal(t, "us-east-1", ctx.Region)
		assert.Equal(t, "s3", ctx.Service)
	})

	t.Run("returns empty context when Authorization header is missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := ParseSigV4(req)
		assert.Empty(t, ctx.AccessKeyID)
		assert.Empty(t, ctx.Region)
		assert.Empty(t, ctx.Service)
	})

	t.Run("returns empty context when header is not SigV4", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		ctx := ParseSigV4(req)
		assert.Empty(t, ctx.AccessKeyID)
		assert.Empty(t, ctx.Region)
		assert.Empty(t, ctx.Service)
	})

	t.Run("returns empty context when Credential part is missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 SignedHeaders=host, Signature=abc123")
		ctx := ParseSigV4(req)
		assert.Empty(t, ctx.AccessKeyID)
		assert.Empty(t, ctx.Region)
	})

	t.Run("returns empty context when Credential has too few fields", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(
			"Authorization",
			"AWS4-HMAC-SHA256 Credential=KEYID/20230101, SignedHeaders=host, Signature=abc",
		)
		ctx := ParseSigV4(req)
		assert.Empty(t, ctx.AccessKeyID)
		assert.Empty(t, ctx.Region)
	})
}
