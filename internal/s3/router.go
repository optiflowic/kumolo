package s3

import (
	"net/http"
	"strings"
)

// Router handles S3 API requests using path-style URLs: /<bucket>/<key>
type Router struct{}

func NewRouter() *Router {
	return &Router{}
}

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket, key := parsePath(r.URL.Path)

	if bucket == "" {
		if r.Method == http.MethodGet {
			writeNotImplemented(w, r)
			return
		}
		writeError(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed.")
		return
	}

	if key == "" {
		ro.routeBucket(w, r, bucket)
		return
	}

	ro.routeObject(w, r, bucket, key)
}

func (ro *Router) routeBucket(w http.ResponseWriter, r *http.Request, _ string) {
	switch r.Method {
	case http.MethodPut:
		writeNotImplemented(w, r)
	case http.MethodDelete:
		writeNotImplemented(w, r)
	case http.MethodGet, http.MethodHead:
		writeNotImplemented(w, r)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed.")
	}
}

func (ro *Router) routeObject(w http.ResponseWriter, r *http.Request, _, _ string) {
	switch r.Method {
	case http.MethodPut:
		writeNotImplemented(w, r)
	case http.MethodGet:
		writeNotImplemented(w, r)
	case http.MethodDelete:
		writeNotImplemented(w, r)
	case http.MethodHead:
		writeNotImplemented(w, r)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed.")
	}
}

// parsePath splits a path-style S3 URL into bucket and key.
// e.g. "/my-bucket/path/to/object" → ("my-bucket", "path/to/object")
func parsePath(path string) (bucket, key string) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", ""
	}
	parts := strings.SplitN(path, "/", 2)
	bucket = parts[0]
	if len(parts) == 2 {
		key = parts[1]
	}
	return bucket, key
}
