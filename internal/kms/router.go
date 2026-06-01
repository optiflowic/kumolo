package kms

import (
	"crypto/rand"
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
	GetKeyMaterial(keyID string) (KeyMaterial, error)
	CreateAlias(aliasName, targetKeyID string) error
	DeleteAlias(aliasName string) error
	UpdateAlias(aliasName, targetKeyID string) error
	ListAliases(filterKeyID string) ([]AliasEntry, error)
	ResolveAlias(aliasName string) (string, error)
	EnableKey(keyID string) error
	DisableKey(keyID string) error
	ScheduleKeyDeletion(keyID string, pendingWindowInDays int) (KeyMetadata, error)
	CancelKeyDeletion(keyID string) (KeyMetadata, error)
	EnableKeyRotation(keyID string, rotationPeriodInDays int) error
	DisableKeyRotation(keyID string) error
	GetKeyRotationStatus(keyID string) (KeyMetadata, KeyRotationConfig, error)
}

// Router handles KMS API requests dispatched via the X-Amz-Target header.
type Router struct {
	storage  store
	randRead func([]byte) (int, error)
}

func NewRouter(s store) *Router {
	return newRouterWithRand(s, rand.Read)
}

func newRouterWithRand(s store, randRead func([]byte) (int, error)) *Router {
	return &Router{storage: s, randRead: randRead}
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
	case "Encrypt":
		ro.handleEncrypt(w, body)
	case "Decrypt":
		ro.handleDecrypt(w, body)
	case "GenerateDataKey":
		ro.handleGenerateDataKey(w, body)
	case "GenerateDataKeyWithoutPlaintext":
		ro.handleGenerateDataKeyWithoutPlaintext(w, body)
	case "CreateAlias":
		ro.handleCreateAlias(w, body)
	case "DeleteAlias":
		ro.handleDeleteAlias(w, body)
	case "UpdateAlias":
		ro.handleUpdateAlias(w, body)
	case "ListAliases":
		ro.handleListAliases(w, body)
	case "EnableKey":
		ro.handleEnableKey(w, body)
	case "DisableKey":
		ro.handleDisableKey(w, body)
	case "ScheduleKeyDeletion":
		ro.handleScheduleKeyDeletion(w, body)
	case "CancelKeyDeletion":
		ro.handleCancelKeyDeletion(w, body)
	case "EnableKeyRotation":
		ro.handleEnableKeyRotation(w, body)
	case "DisableKeyRotation":
		ro.handleDisableKeyRotation(w, body)
	case "GetKeyRotationStatus":
		ro.handleGetKeyRotationStatus(w, body)
	case "ListResourceTags":
		ro.handleListResourceTags(w, body)
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
