package s3

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsePath(t *testing.T) {
	tests := []struct {
		path        string
		wantBucket  string
		wantKey     string
	}{
		{"/", "", ""},
		{"/my-bucket", "my-bucket", ""},
		{"/my-bucket/", "my-bucket", ""},
		{"/my-bucket/object.txt", "my-bucket", "object.txt"},
		{"/my-bucket/path/to/object.txt", "my-bucket", "path/to/object.txt"},
	}

	for _, tt := range tests {
		bucket, key := parsePath(tt.path)
		if bucket != tt.wantBucket || key != tt.wantKey {
			t.Errorf("parsePath(%q) = (%q, %q), want (%q, %q)",
				tt.path, bucket, key, tt.wantBucket, tt.wantKey)
		}
	}
}

func TestRouterReturnsXMLOnUnimplemented(t *testing.T) {
	ro := NewRouter()

	req := httptest.NewRequest(http.MethodGet, "/my-bucket", nil)
	w := httptest.NewRecorder()

	ro.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/xml" {
		t.Errorf("expected application/xml, got %s", ct)
	}
}

func TestParseSigV4(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20230101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=abc123")

	ctx := ParseSigV4(req)

	if ctx.AccessKeyID != "AKIAIOSFODNN7EXAMPLE" {
		t.Errorf("unexpected AccessKeyID: %s", ctx.AccessKeyID)
	}
	if ctx.Region != "us-east-1" {
		t.Errorf("unexpected Region: %s", ctx.Region)
	}
	if ctx.Service != "s3" {
		t.Errorf("unexpected Service: %s", ctx.Service)
	}
}
