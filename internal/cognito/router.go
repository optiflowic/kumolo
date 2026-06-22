package cognito

import (
	"io"
	"net/http"
	"strings"
	"time"
)

// store is the storage interface used by Router.
// Methods are added incrementally as operations are implemented.
type store interface{}

// Router handles Cognito User Pools API requests dispatched via the X-Amz-Target header.
type Router struct {
	storage store
}

func NewRouter(storage *Storage) *Router {
	return &Router{storage: storage}
}

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := newResponseRecorder(w)
	start := time.Now()
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AWSCognitoIdentityProviderService.")
	ro.serveHTTP(rec, r, op)
	emitRequestLog(op, rec, time.Since(start))
}

func (ro *Router) serveHTTP(w http.ResponseWriter, r *http.Request, op string) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"failed to read request body",
		)
		return
	}
	_ = body

	switch op {
	default:
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeUnknownOperationException,
			"Operation not supported: "+op,
		)
	}
}
