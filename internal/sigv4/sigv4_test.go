package sigv4

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

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

	t.Run("parses presigned URL credential from query param", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		q := req.URL.Query()
		q.Set("X-Amz-Credential", "AKIAIOSFODNN7EXAMPLE/20231001/ap-northeast-1/s3/aws4_request")
		req.URL.RawQuery = q.Encode()
		ctx := ParseSigV4(req)
		assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", ctx.AccessKeyID)
		assert.Equal(t, "ap-northeast-1", ctx.Region)
		assert.Equal(t, "s3", ctx.Service)
	})

	t.Run("query param credential takes precedence over Authorization header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(
			"Authorization",
			"AWS4-HMAC-SHA256 Credential=HEADERKEY/20231001/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=abc",
		)
		q := req.URL.Query()
		q.Set("X-Amz-Credential", "QUERYKEY/20231001/eu-west-1/s3/aws4_request")
		req.URL.RawQuery = q.Encode()
		ctx := ParseSigV4(req)
		assert.Equal(t, "QUERYKEY", ctx.AccessKeyID)
		assert.Equal(t, "eu-west-1", ctx.Region)
	})

	t.Run("presigned credential with too few fields returns empty context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		q := req.URL.Query()
		q.Set("X-Amz-Credential", "KEY/20231001")
		req.URL.RawQuery = q.Encode()
		ctx := ParseSigV4(req)
		assert.Empty(t, ctx.AccessKeyID)
		assert.Empty(t, ctx.Region)
	})
}
