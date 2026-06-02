package kms

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"log/slog"
	"net/http"
)

// hmacHashForAlgorithm returns the hash constructor for the given MAC algorithm.
// Returns nil if the algorithm is not supported.
func hmacHashForAlgorithm(algo string) func() hash.Hash {
	switch algo {
	case "HMAC_SHA_224":
		return sha256.New224
	case "HMAC_SHA_256":
		return sha256.New
	case "HMAC_SHA_384":
		return sha512.New384
	case "HMAC_SHA_512":
		return sha512.New
	}
	return nil
}

// resolveAndValidateMACKey resolves keyID and validates that it is enabled
// and has GENERATE_VERIFY_MAC usage.
func (ro *Router) resolveAndValidateMACKey(
	w http.ResponseWriter,
	keyIDParam string,
	macAlgorithm string,
) (KeyMetadata, KeyMaterial, bool) {
	if keyIDParam == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return KeyMetadata{}, KeyMaterial{}, false
	}
	keyID, ok := ro.resolveKeyRef(w, keyIDParam)
	if !ok {
		return KeyMetadata{}, KeyMaterial{}, false
	}

	meta, err := ro.storage.GetKeyMetadata(keyID)
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			slog.Debug("KMS: key not found", "keyID", keyID)
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Invalid keyId %s", keyID))
			return KeyMetadata{}, KeyMaterial{}, false
		}
		slog.Error("KMS: GetKeyMetadata failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return KeyMetadata{}, KeyMaterial{}, false
	}

	if meta.KeyState == keyStatePendingDeletion {
		writeError(w, http.StatusBadRequest, "KMSInvalidStateException",
			fmt.Sprintf("KMS key %s is pending deletion", keyID))
		return KeyMetadata{}, KeyMaterial{}, false
	}
	if !meta.Enabled || meta.KeyState != keyStateEnabled {
		writeError(w, http.StatusBadRequest, "DisabledException",
			fmt.Sprintf("KMS key %s is disabled", keyID))
		return KeyMetadata{}, KeyMaterial{}, false
	}
	if meta.KeyUsage != "GENERATE_VERIFY_MAC" {
		writeError(w, http.StatusBadRequest, "InvalidKeyUsageException",
			fmt.Sprintf("Key %s has KeyUsage %s; GENERATE_VERIFY_MAC is required",
				keyID, meta.KeyUsage))
		return KeyMetadata{}, KeyMaterial{}, false
	}

	// Verify the algorithm is compatible with the key spec.
	supported := false
	for _, a := range meta.MacAlgorithms {
		if a == macAlgorithm {
			supported = true
			break
		}
	}
	if !supported {
		writeError(w, http.StatusBadRequest, "InvalidKeyUsageException",
			fmt.Sprintf("MacAlgorithm %s is not supported by key %s", macAlgorithm, keyID))
		return KeyMetadata{}, KeyMaterial{}, false
	}

	mat, err := ro.storage.GetKeyMaterial(keyID)
	if err != nil {
		if errors.Is(err, ErrKeyMaterialNotFound) {
			writeError(w, http.StatusBadRequest, "KMSInvalidStateException",
				fmt.Sprintf("Key material not available for key %s", keyID))
			return KeyMetadata{}, KeyMaterial{}, false
		}
		slog.Error("KMS: GetKeyMaterial failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return KeyMetadata{}, KeyMaterial{}, false
	}

	return meta, mat, true
}

// ---- GenerateMac -----------------------------------------------------------

func (ro *Router) handleGenerateMac(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyID        string `json:"KeyId"`
		MacAlgorithm string `json:"MacAlgorithm"`
		Message      []byte `json:"Message"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if len(req.Message) == 0 {
		writeError(w, http.StatusBadRequest, "ValidationException", "Message is required")
		return
	}
	if len(req.Message) > 4096 {
		writeError(w, http.StatusBadRequest, "ValidationException",
			fmt.Sprintf("Message length %d exceeds maximum of 4096 bytes", len(req.Message)))
		return
	}
	if req.MacAlgorithm == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "MacAlgorithm is required")
		return
	}
	newHash := hmacHashForAlgorithm(req.MacAlgorithm)
	if newHash == nil {
		writeError(w, http.StatusBadRequest, "ValidationException",
			fmt.Sprintf("Invalid MacAlgorithm: %s", req.MacAlgorithm))
		return
	}

	meta, mat, ok := ro.resolveAndValidateMACKey(w, req.KeyID, req.MacAlgorithm)
	if !ok {
		return
	}

	mac := hmac.New(newHash, mat.KeyBytes)
	mac.Write(req.Message)
	tag := mac.Sum(nil)

	slog.Debug("KMS GenerateMac", "keyID", meta.KeyID, "algorithm", req.MacAlgorithm)
	writeJSON(w, http.StatusOK, map[string]any{
		"KeyId":        meta.Arn,
		"Mac":          tag,
		"MacAlgorithm": req.MacAlgorithm,
	})
}

// ---- VerifyMac -------------------------------------------------------------

func (ro *Router) handleVerifyMac(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyID        string `json:"KeyId"`
		MacAlgorithm string `json:"MacAlgorithm"`
		Message      []byte `json:"Message"`
		Mac          []byte `json:"Mac"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if len(req.Message) == 0 {
		writeError(w, http.StatusBadRequest, "ValidationException", "Message is required")
		return
	}
	if len(req.Message) > 4096 {
		writeError(w, http.StatusBadRequest, "ValidationException",
			fmt.Sprintf("Message length %d exceeds maximum of 4096 bytes", len(req.Message)))
		return
	}
	if req.MacAlgorithm == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "MacAlgorithm is required")
		return
	}
	if len(req.Mac) == 0 {
		writeError(w, http.StatusBadRequest, "ValidationException", "Mac is required")
		return
	}
	newHash := hmacHashForAlgorithm(req.MacAlgorithm)
	if newHash == nil {
		writeError(w, http.StatusBadRequest, "ValidationException",
			fmt.Sprintf("Invalid MacAlgorithm: %s", req.MacAlgorithm))
		return
	}

	meta, mat, ok := ro.resolveAndValidateMACKey(w, req.KeyID, req.MacAlgorithm)
	if !ok {
		return
	}

	mac := hmac.New(newHash, mat.KeyBytes)
	mac.Write(req.Message)
	expected := mac.Sum(nil)

	if !hmac.Equal(expected, req.Mac) {
		writeError(w, http.StatusBadRequest, "KMSInvalidMacException",
			"The MAC did not verify successfully")
		return
	}

	slog.Debug("KMS VerifyMac", "keyID", meta.KeyID, "algorithm", req.MacAlgorithm)
	writeJSON(w, http.StatusOK, map[string]any{
		"KeyId":        meta.Arn,
		"MacAlgorithm": req.MacAlgorithm,
		"MacValid":     true,
	})
}
