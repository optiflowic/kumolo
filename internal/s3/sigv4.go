package s3

import (
	"net/http"
	"strings"
)

// RequestContext holds parsed information from an AWS SigV4 signed request.
type RequestContext struct {
	AccessKeyID string
	Region      string
	Service     string
}

// ParseSigV4 extracts metadata from the Authorization header.
// Signature verification is intentionally skipped in local mode.
//
// Authorization header format:
// AWS4-HMAC-SHA256 Credential=<key>/<date>/<region>/<service>/aws4_request, ...
func ParseSigV4(r *http.Request) RequestContext {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		return RequestContext{}
	}

	var ctx RequestContext

	auth = strings.TrimPrefix(auth, "AWS4-HMAC-SHA256 ")
	for _, part := range strings.Split(auth, ", ") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "Credential=") {
			continue
		}
		credential := strings.TrimPrefix(part, "Credential=")
		fields := strings.SplitN(credential, "/", 5)
		if len(fields) < 4 {
			break
		}
		ctx.AccessKeyID = fields[0]
		ctx.Region = fields[2]
		ctx.Service = fields[3]
		break
	}

	return ctx
}
