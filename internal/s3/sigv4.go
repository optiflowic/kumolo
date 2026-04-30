package s3

import (
	"net/http"
	"strings"
)

type RequestContext struct {
	AccessKeyID string
	Region      string
	Service     string
}

// ParseSigV4 extracts AccessKeyID, Region, and Service from the Authorization header
// or from query parameters for presigned requests.
// Signature verification is intentionally skipped in local mode.
//
// Header format:
// AWS4-HMAC-SHA256 Credential=<key>/<date>/<region>/<service>/aws4_request, ...
//
// Presigned URL query parameter format:
// X-Amz-Credential=<key>/<date>/<region>/<service>/aws4_request
func ParseSigV4(r *http.Request) RequestContext {
	// Presigned URL: credentials are in the query string.
	if cred := r.URL.Query().Get(amzQCredential); cred != "" {
		return parseCredential(cred)
	}

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		return RequestContext{}
	}

	auth = strings.TrimPrefix(auth, "AWS4-HMAC-SHA256 ")
	for _, part := range strings.Split(auth, ", ") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "Credential=") {
			continue
		}
		return parseCredential(strings.TrimPrefix(part, "Credential="))
	}

	return RequestContext{}
}

func parseCredential(credential string) RequestContext {
	fields := strings.SplitN(credential, "/", 5)
	if len(fields) < 4 {
		return RequestContext{}
	}
	return RequestContext{
		AccessKeyID: fields[0],
		Region:      fields[2],
		Service:     fields[3],
	}
}
