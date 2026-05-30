package kms

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// store is the storage interface used by Router.
type store interface {
	CreateKey(in CreateKeyInput) (KeyMetadata, error)
	GetKeyMetadata(keyID string) (KeyMetadata, error)
	ListKeyIDs() ([]string, error)
	GetKeyPolicy(keyID string) (string, error)
	PutKeyPolicy(keyID, policy string) error
}

// Router handles KMS API requests dispatched via the X-Amz-Target header.
type Router struct {
	storage store
}

func NewRouter(storage *Storage) *Router {
	return &Router{storage: storage}
}

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	op := strings.TrimPrefix(target, "TrentService.")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "failed to read request body")
		return
	}

	switch op {
	case "CreateKey":
		ro.handleCreateKey(w, body)
	case "DescribeKey":
		ro.handleDescribeKey(w, body)
	case "ListKeys":
		ro.handleListKeys(w, body)
	case "GetKeyPolicy":
		ro.handleGetKeyPolicy(w, body)
	case "PutKeyPolicy":
		ro.handlePutKeyPolicy(w, body)
	default:
		slog.Debug( // #nosec G706 -- target comes from the X-Amz-Target header; log injection risk accepted for a local dev emulator
			"KMS operation not implemented",
			"target",
			target,
		)
		writeError(
			w,
			http.StatusNotImplemented,
			"UnsupportedOperationException",
			"Operation not implemented: "+op,
		)
	}
}
