package kms

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

func (ro *Router) handleGetPublicKey(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyId string `json:"KeyId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.KeyId == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return
	}

	keyID, ok := ro.resolveKeyRef(w, req.KeyId)
	if !ok {
		return
	}

	meta, err := ro.storage.GetKeyMetadata(keyID)
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			slog.Debug("KMS GetPublicKey: key not found", "keyID", keyID)
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Invalid keyId %s", keyID))
			return
		}
		slog.Error("KMS GetPublicKey: GetKeyMetadata failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	if meta.KeyState == keyStatePendingDeletion {
		writeError(w, http.StatusBadRequest, "KMSInvalidStateException",
			fmt.Sprintf("KMS key %s is pending deletion", keyID))
		return
	}

	if meta.KeySpec == keySpecSymmetricDefault || isHMACSpec(meta.KeySpec) {
		writeError(
			w,
			http.StatusBadRequest,
			"InvalidKeyUsageException",
			fmt.Sprintf(
				"Key %s has a symmetric key spec (%s); GetPublicKey requires an asymmetric key",
				keyID,
				meta.KeySpec,
			),
		)
		return
	}

	mat, err := ro.storage.GetKeyMaterial(keyID)
	if err != nil {
		if errors.Is(err, ErrKeyMaterialNotFound) {
			slog.Debug(
				"KMS GetPublicKey: no key material for spec",
				"keyID",
				keyID,
				"spec",
				meta.KeySpec,
			)
			writeError(
				w,
				http.StatusBadRequest,
				"UnsupportedOperationException",
				fmt.Sprintf(
					"kumolo does not support key material generation for key spec %s",
					meta.KeySpec,
				),
			)
			return
		}
		slog.Error("KMS GetPublicKey: GetKeyMaterial failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	pubDER, err := extractPublicKeyDER(mat.PrivateKeyDER)
	if err != nil {
		// untestable: only reached if stored key material is corrupt; the normal API cannot produce this
		slog.Error("KMS GetPublicKey: extract public key failure", "keyID", keyID, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	resp := map[string]any{
		"KeyId":                 meta.Arn,
		"KeySpec":               meta.KeySpec,
		"CustomerMasterKeySpec": meta.CustomerMasterKeySpec,
		"KeyUsage":              meta.KeyUsage,
		"PublicKey":             pubDER,
	}
	if len(meta.EncryptionAlgorithms) > 0 {
		resp["EncryptionAlgorithms"] = meta.EncryptionAlgorithms
	}
	if len(meta.SigningAlgorithms) > 0 {
		resp["SigningAlgorithms"] = meta.SigningAlgorithms
	}
	if len(meta.KeyAgreementAlgorithms) > 0 {
		resp["KeyAgreementAlgorithms"] = meta.KeyAgreementAlgorithms
	}

	slog.Debug("KMS GetPublicKey", "keyID", keyID, "spec", meta.KeySpec)
	writeJSON(w, http.StatusOK, resp)
}

// isHMACSpec reports whether spec is an HMAC key spec.
func isHMACSpec(spec string) bool {
	switch spec {
	case "HMAC_224", "HMAC_256", "HMAC_384", "HMAC_512":
		return true
	}
	return false
}

// extractPublicKeyDER parses a PKCS#8 DER-encoded private key and returns the
// SubjectPublicKeyInfo (SPKI) DER-encoded public key.
func extractPublicKeyDER(privKeyDER []byte) ([]byte, error) {
	priv, err := x509.ParsePKCS8PrivateKey(privKeyDER)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS#8 private key: %w", err)
	}
	var pub any
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		pub = &k.PublicKey
	case *ecdsa.PrivateKey:
		pub = &k.PublicKey
	case ed25519.PrivateKey:
		pub = k.Public()
	default:
		// untestable: generateKeyPair only produces RSA, ECDSA, and Ed25519 keys
		return nil, fmt.Errorf("unsupported private key type %T", priv)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		// untestable: MarshalPKIXPublicKey always succeeds for RSA, ECDSA, and Ed25519 public keys
		return nil, fmt.Errorf("marshal public key: %w", err)
	}
	return der, nil
}
