package kms

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// hashForSigningAlgorithm returns the hash function and crypto.Hash for the
// signing algorithm. Returns crypto.Hash(0) for Ed25519 (no separate hash step).
func hashForSigningAlgorithm(algo string) (crypto.Hash, error) {
	switch algo {
	case "RSASSA_PKCS1_V1_5_SHA_256", "RSASSA_PSS_SHA_256", "ECDSA_SHA_256":
		return crypto.SHA256, nil
	case "RSASSA_PKCS1_V1_5_SHA_384", "RSASSA_PSS_SHA_384", "ECDSA_SHA_384":
		return crypto.SHA384, nil
	case "RSASSA_PKCS1_V1_5_SHA_512", "RSASSA_PSS_SHA_512", "ECDSA_SHA_512":
		return crypto.SHA512, nil
	case "ED25519_SHA_512":
		return crypto.Hash(0), nil // Ed25519 handles hashing internally
	}
	return 0, fmt.Errorf("unsupported signing algorithm: %s", algo)
}

// digestMessage hashes msg with the hash for algo.
// For Ed25519 (hash == 0) the message is returned unchanged.
func digestMessage(algo string, h crypto.Hash, msg []byte) []byte {
	if h == 0 {
		return msg
	}
	var sum []byte
	switch h {
	case crypto.SHA256:
		d := sha256.Sum256(msg)
		sum = d[:]
	case crypto.SHA384:
		d := sha512.Sum384(msg)
		sum = d[:]
	case crypto.SHA512:
		d := sha512.Sum512(msg)
		sum = d[:]
	}
	_ = algo
	return sum
}

// resolveAndValidateSignKey resolves keyID and validates that it is enabled
// and has SIGN_VERIFY usage with the requested algorithm.
func (ro *Router) resolveAndValidateSignKey(
	w http.ResponseWriter,
	keyIDParam, signingAlgorithm string,
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
	if meta.KeyUsage != "SIGN_VERIFY" {
		writeError(w, http.StatusBadRequest, "InvalidKeyUsageException",
			fmt.Sprintf("Key %s has KeyUsage %s; SIGN_VERIFY is required", keyID, meta.KeyUsage))
		return KeyMetadata{}, KeyMaterial{}, false
	}

	supported := false
	for _, a := range meta.SigningAlgorithms {
		if a == signingAlgorithm {
			supported = true
			break
		}
	}
	if !supported {
		writeError(w, http.StatusBadRequest, "InvalidKeyUsageException",
			fmt.Sprintf("SigningAlgorithm %s is not supported by key %s", signingAlgorithm, keyID))
		return KeyMetadata{}, KeyMaterial{}, false
	}

	mat, err := ro.storage.GetKeyMaterial(keyID)
	if err != nil {
		if errors.Is(err, ErrKeyMaterialNotFound) {
			writeError(w, http.StatusBadRequest, "UnsupportedOperationException",
				fmt.Sprintf(
					"kumolo does not support key material generation for key spec %s",
					meta.KeySpec,
				))
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

// signData signs digest (or raw msg for Ed25519) using the private key and algorithm.
func signData(privKeyDER []byte, algo string, h crypto.Hash, data []byte) ([]byte, error) {
	priv, err := x509.ParsePKCS8PrivateKey(privKeyDER)
	if err != nil {
		// untestable: stored DER is always written by MarshalPKCS8PrivateKey
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	switch k := priv.(type) {
	case *rsa.PrivateKey:
		switch {
		case isPSSAlgorithm(algo):
			opts := &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: h}
			return rsa.SignPSS(rand.Reader, k, h, data, opts)
		default:
			return rsa.SignPKCS1v15(rand.Reader, k, h, data)
		}
	case *ecdsa.PrivateKey:
		return ecdsa.SignASN1(rand.Reader, k, data)
	case ed25519.PrivateKey:
		return ed25519.Sign(k, data), nil
	default:
		// untestable: generateKeyPair only produces RSA, ECDSA, and Ed25519 keys
		return nil, fmt.Errorf("unsupported key type %T", priv)
	}
}

// verifyData verifies sig against data using the public key and algorithm.
// Returns nil on success, non-nil on failure.
func verifyData(privKeyDER []byte, algo string, h crypto.Hash, data, sig []byte) error {
	priv, err := x509.ParsePKCS8PrivateKey(privKeyDER)
	if err != nil {
		// untestable: stored DER is always written by MarshalPKCS8PrivateKey
		return fmt.Errorf("parse private key: %w", err)
	}

	switch k := priv.(type) {
	case *rsa.PrivateKey:
		pub := &k.PublicKey
		if isPSSAlgorithm(algo) {
			return rsa.VerifyPSS(pub, h, data, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto})
		}
		return rsa.VerifyPKCS1v15(pub, h, data, sig)
	case *ecdsa.PrivateKey:
		if !ecdsa.VerifyASN1(&k.PublicKey, data, sig) {
			return fmt.Errorf("ECDSA signature verification failed")
		}
		return nil
	case ed25519.PrivateKey:
		if !ed25519.Verify(k.Public().(ed25519.PublicKey), data, sig) {
			return fmt.Errorf("Ed25519 signature verification failed")
		}
		return nil
	default:
		// untestable: generateKeyPair only produces RSA, ECDSA, and Ed25519 keys
		return fmt.Errorf("unsupported key type %T", priv)
	}
}

func isPSSAlgorithm(algo string) bool {
	switch algo {
	case "RSASSA_PSS_SHA_256", "RSASSA_PSS_SHA_384", "RSASSA_PSS_SHA_512":
		return true
	}
	return false
}

// ---- Sign ------------------------------------------------------------------

func (ro *Router) handleSign(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyID            string `json:"KeyId"`
		Message          []byte `json:"Message"`
		SigningAlgorithm string `json:"SigningAlgorithm"`
		MessageType      string `json:"MessageType"`
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
	if req.SigningAlgorithm == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "SigningAlgorithm is required")
		return
	}
	if req.MessageType == "" {
		req.MessageType = "RAW"
	}
	if req.MessageType != "RAW" && req.MessageType != "DIGEST" {
		writeError(
			w,
			http.StatusBadRequest,
			"ValidationException",
			fmt.Sprintf(
				"Invalid MessageType: %s; valid values are RAW and DIGEST",
				req.MessageType,
			),
		)
		return
	}
	if req.SigningAlgorithm == "ED25519_SHA_512" && req.MessageType != "RAW" {
		writeError(w, http.StatusBadRequest, "ValidationException",
			"ED25519_SHA_512 requires MessageType RAW")
		return
	}
	if req.SigningAlgorithm == "ED25519_PH_SHA_512" {
		writeError(w, http.StatusBadRequest, "UnsupportedOperationException",
			"ED25519_PH_SHA_512 is not supported in kumolo")
		return
	}

	h, err := hashForSigningAlgorithm(req.SigningAlgorithm)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", err.Error())
		return
	}

	meta, mat, ok := ro.resolveAndValidateSignKey(w, req.KeyID, req.SigningAlgorithm)
	if !ok {
		return
	}

	var dataToSign []byte
	if req.MessageType == "DIGEST" {
		dataToSign = req.Message
	} else {
		dataToSign = digestMessage(req.SigningAlgorithm, h, req.Message)
	}

	sig, err := signData(mat.PrivateKeyDER, req.SigningAlgorithm, h, dataToSign)
	if err != nil {
		slog.Error("KMS Sign: sign failure", "keyID", meta.KeyID, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	slog.Debug("KMS Sign", "keyID", meta.KeyID, "algorithm", req.SigningAlgorithm)
	writeJSON(w, http.StatusOK, map[string]any{
		"KeyId":            meta.Arn,
		"Signature":        sig,
		"SigningAlgorithm": req.SigningAlgorithm,
	})
}

// ---- Verify ----------------------------------------------------------------

func (ro *Router) handleVerify(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyID            string `json:"KeyId"`
		Message          []byte `json:"Message"`
		Signature        []byte `json:"Signature"`
		SigningAlgorithm string `json:"SigningAlgorithm"`
		MessageType      string `json:"MessageType"`
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
	if len(req.Signature) == 0 {
		writeError(w, http.StatusBadRequest, "ValidationException", "Signature is required")
		return
	}
	if req.SigningAlgorithm == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "SigningAlgorithm is required")
		return
	}
	if req.MessageType == "" {
		req.MessageType = "RAW"
	}
	if req.MessageType != "RAW" && req.MessageType != "DIGEST" {
		writeError(
			w,
			http.StatusBadRequest,
			"ValidationException",
			fmt.Sprintf(
				"Invalid MessageType: %s; valid values are RAW and DIGEST",
				req.MessageType,
			),
		)
		return
	}
	if req.SigningAlgorithm == "ED25519_SHA_512" && req.MessageType != "RAW" {
		writeError(w, http.StatusBadRequest, "ValidationException",
			"ED25519_SHA_512 requires MessageType RAW")
		return
	}
	if req.SigningAlgorithm == "ED25519_PH_SHA_512" {
		writeError(w, http.StatusBadRequest, "UnsupportedOperationException",
			"ED25519_PH_SHA_512 is not supported in kumolo")
		return
	}

	h, err := hashForSigningAlgorithm(req.SigningAlgorithm)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", err.Error())
		return
	}

	meta, mat, ok := ro.resolveAndValidateSignKey(w, req.KeyID, req.SigningAlgorithm)
	if !ok {
		return
	}

	var dataToVerify []byte
	if req.MessageType == "DIGEST" {
		dataToVerify = req.Message
	} else {
		dataToVerify = digestMessage(req.SigningAlgorithm, h, req.Message)
	}

	if err := verifyData(mat.PrivateKeyDER, req.SigningAlgorithm, h, dataToVerify, req.Signature); err != nil {
		slog.Debug("KMS Verify: signature verification failed", "keyID", meta.KeyID, "err", err)
		writeError(w, http.StatusBadRequest, "KMSInvalidSignatureException",
			"The signature verification failed")
		return
	}

	slog.Debug("KMS Verify", "keyID", meta.KeyID, "algorithm", req.SigningAlgorithm)
	writeJSON(w, http.StatusOK, map[string]any{
		"KeyId":            meta.Arn,
		"SignatureValid":   true,
		"SigningAlgorithm": req.SigningAlgorithm,
	})
}
