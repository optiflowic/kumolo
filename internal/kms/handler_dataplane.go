package kms

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
)

// Ciphertext envelope layout:
//
//	[version: 1 byte = 0x01]
//	[key ID: 36 bytes (UUID string)]
//	[algo:   1 byte  (0x01 = SYMMETRIC_DEFAULT)]
//	[nonce:  12 bytes (AES-GCM)]
//	[sealed: variable (plaintext + 16-byte GCM tag)]
const (
	envelopeVersion      = byte(0x01)
	algoSymmetricDefault = byte(0x01)
	envelopeKeyIDOffset  = 1
	envelopeKeyIDLen     = 36
	envelopeAlgoOffset   = 37
	envelopeNonceOffset  = 38
	envelopeNonceLen     = 12
	envelopeSealedOffset = 50
)

// sealEnvelope encrypts plaintext with AES-256-GCM and wraps the result in the
// kumolo ciphertext envelope. context is used as AEAD additional data.
func sealEnvelope(
	keyID string,
	material KeyMaterial,
	plaintext []byte,
	context map[string]string,
	randRead func([]byte) (int, error),
) ([]byte, error) {
	block, err := aes.NewCipher(material.KeyBytes)
	if err != nil {
		return nil, fmt.Errorf("new AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		// untestable: standard AES block with NewGCM never fails
		return nil, fmt.Errorf("new GCM: %w", err)
	}

	var nonce [envelopeNonceLen]byte
	if _, err := randRead(nonce[:]); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	aad := marshalContext(context)
	sealed := gcm.Seal(nil, nonce[:], plaintext, aad)

	out := make([]byte, envelopeSealedOffset+len(sealed))
	out[0] = envelopeVersion
	copy(out[envelopeKeyIDOffset:], []byte(keyID))
	out[envelopeAlgoOffset] = algoSymmetricDefault
	copy(out[envelopeNonceOffset:], nonce[:])
	copy(out[envelopeSealedOffset:], sealed)
	return out, nil
}

// openEnvelope parses a kumolo ciphertext envelope and decrypts it.
// Returns the embedded keyID, decrypted plaintext, or an error.
func openEnvelope(
	blob []byte,
	material KeyMaterial,
	context map[string]string,
) (embeddedKeyID string, plaintext []byte, err error) {
	if len(blob) < envelopeSealedOffset+1 {
		return "", nil, fmt.Errorf("ciphertext too short")
	}
	if blob[0] != envelopeVersion {
		return "", nil, fmt.Errorf("unknown envelope version %d", blob[0])
	}
	embeddedKeyID = string(blob[envelopeKeyIDOffset : envelopeKeyIDOffset+envelopeKeyIDLen])
	algo := blob[envelopeAlgoOffset]
	if algo != algoSymmetricDefault {
		return "", nil, fmt.Errorf("unsupported algorithm code %d", algo)
	}
	nonce := blob[envelopeNonceOffset:envelopeSealedOffset]
	sealed := blob[envelopeSealedOffset:]

	block, err := aes.NewCipher(material.KeyBytes)
	if err != nil {
		return "", nil, fmt.Errorf("new AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		// untestable: standard AES block with NewGCM never fails
		return "", nil, fmt.Errorf("new GCM: %w", err)
	}

	aad := marshalContext(context)
	plaintext, err = gcm.Open(nil, nonce, sealed, aad)
	if err != nil {
		return embeddedKeyID, nil, fmt.Errorf("aead open: %w", err)
	}
	return embeddedKeyID, plaintext, nil
}

// marshalContext produces a deterministic byte representation of an encryption
// context for use as AES-GCM additional authenticated data.
// Keys are sorted lexicographically; empty context maps to empty bytes.
func marshalContext(ctx map[string]string) []byte {
	if len(ctx) == 0 {
		return nil
	}
	keys := make([]string, 0, len(ctx))
	for k := range ctx {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(0x00)
		}
		sb.WriteString(k)
		sb.WriteByte(0x00)
		sb.WriteString(ctx[k])
	}
	return []byte(sb.String())
}

// resolveAndValidateKey resolves keyID, reads its metadata, and validates that it
// is enabled and has ENCRYPT_DECRYPT usage. Returns the metadata or writes an
// appropriate error response.
func (ro *Router) resolveAndValidateKey(
	w http.ResponseWriter,
	keyIDParam string,
) (KeyMetadata, string, bool) {
	if keyIDParam == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "KeyId is required")
		return KeyMetadata{}, "", false
	}
	keyID, ok := resolveKeyID(keyIDParam)
	if !ok {
		if isAliasRef(keyIDParam) {
			writeError(w, http.StatusBadRequest, "NotFoundException",
				"Alias key lookup is not supported; use a key ID or key ARN")
			return KeyMetadata{}, "", false
		}
		writeError(w, http.StatusBadRequest, "InvalidArnException",
			fmt.Sprintf("Invalid key ARN: %s", keyIDParam))
		return KeyMetadata{}, "", false
	}

	meta, err := ro.storage.GetKeyMetadata(keyID)
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			slog.Debug("KMS: key not found", "keyID", keyID)
			writeError(w, http.StatusBadRequest, "NotFoundException",
				fmt.Sprintf("Invalid keyId %s", keyID))
			return KeyMetadata{}, "", false
		}
		slog.Error("KMS: GetKeyMetadata failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return KeyMetadata{}, "", false
	}

	if !meta.Enabled || meta.KeyState != "Enabled" {
		writeError(w, http.StatusBadRequest, "DisabledException",
			fmt.Sprintf("KMS key %s is disabled", keyID))
		return KeyMetadata{}, "", false
	}
	if meta.KeyUsage != "ENCRYPT_DECRYPT" {
		writeError(
			w,
			http.StatusBadRequest,
			"InvalidKeyUsageException",
			fmt.Sprintf(
				"Key %s has KeyUsage %s; ENCRYPT_DECRYPT is required",
				keyID,
				meta.KeyUsage,
			),
		)
		return KeyMetadata{}, "", false
	}

	return meta, keyID, true
}

// loadSymmetricMaterial fetches the key material for a SYMMETRIC_DEFAULT key.
// Writes an error response if the key is not SYMMETRIC_DEFAULT or material is missing.
func (ro *Router) loadSymmetricMaterial(
	w http.ResponseWriter,
	meta KeyMetadata,
	keyID string,
) (KeyMaterial, bool) {
	if meta.KeySpec != "SYMMETRIC_DEFAULT" {
		writeError(
			w,
			http.StatusBadRequest,
			"InvalidKeyUsageException",
			fmt.Sprintf(
				"Key %s has KeySpec %s; only SYMMETRIC_DEFAULT keys support this operation in kumolo",
				keyID,
				meta.KeySpec,
			),
		)
		return KeyMaterial{}, false
	}
	mat, err := ro.storage.GetKeyMaterial(keyID)
	if err != nil {
		if errors.Is(err, ErrKeyMaterialNotFound) {
			writeError(w, http.StatusBadRequest, "KMSInvalidStateException",
				fmt.Sprintf("Key material not available for key %s", keyID))
			return KeyMaterial{}, false
		}
		slog.Error("KMS: GetKeyMaterial failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return KeyMaterial{}, false
	}
	return mat, true
}

// ---- Encrypt ---------------------------------------------------------------

func (ro *Router) handleEncrypt(w http.ResponseWriter, body []byte) {
	var req struct {
		KeyID               string            `json:"KeyId"`
		Plaintext           []byte            `json:"Plaintext"`
		EncryptionAlgorithm string            `json:"EncryptionAlgorithm"`
		EncryptionContext   map[string]string `json:"EncryptionContext"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if len(req.Plaintext) == 0 {
		writeError(w, http.StatusBadRequest, "ValidationException", "Plaintext is required")
		return
	}
	if len(req.Plaintext) > 4096 {
		writeError(w, http.StatusBadRequest, "ValidationException",
			fmt.Sprintf("Plaintext length %d exceeds maximum of 4096 bytes", len(req.Plaintext)))
		return
	}
	if req.EncryptionAlgorithm != "" && req.EncryptionAlgorithm != "SYMMETRIC_DEFAULT" {
		writeError(
			w,
			http.StatusBadRequest,
			"InvalidKeyUsageException",
			fmt.Sprintf(
				"EncryptionAlgorithm %s is not supported; only SYMMETRIC_DEFAULT is supported in kumolo",
				req.EncryptionAlgorithm,
			),
		)
		return
	}

	meta, keyID, ok := ro.resolveAndValidateKey(w, req.KeyID)
	if !ok {
		return
	}
	mat, ok := ro.loadSymmetricMaterial(w, meta, keyID)
	if !ok {
		return
	}

	ciphertext, err := sealEnvelope(keyID, mat, req.Plaintext, req.EncryptionContext, ro.randRead)
	if err != nil {
		slog.Error("KMS Encrypt: seal failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return
	}

	slog.Info("KMS Encrypt", "keyID", keyID)
	writeJSON(w, http.StatusOK, map[string]any{
		"CiphertextBlob":      ciphertext,
		"KeyId":               meta.Arn,
		"EncryptionAlgorithm": "SYMMETRIC_DEFAULT",
	})
}

// ---- Decrypt ---------------------------------------------------------------

func (ro *Router) handleDecrypt(w http.ResponseWriter, body []byte) {
	var req struct {
		CiphertextBlob      []byte            `json:"CiphertextBlob"`
		KeyID               string            `json:"KeyId"`
		EncryptionAlgorithm string            `json:"EncryptionAlgorithm"`
		EncryptionContext   map[string]string `json:"EncryptionContext"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if len(req.CiphertextBlob) == 0 {
		writeError(w, http.StatusBadRequest, "ValidationException", "CiphertextBlob is required")
		return
	}
	if req.EncryptionAlgorithm != "" && req.EncryptionAlgorithm != "SYMMETRIC_DEFAULT" {
		writeError(
			w,
			http.StatusBadRequest,
			"InvalidKeyUsageException",
			fmt.Sprintf(
				"EncryptionAlgorithm %s is not supported; only SYMMETRIC_DEFAULT is supported in kumolo",
				req.EncryptionAlgorithm,
			),
		)
		return
	}

	// Parse the envelope header to extract the embedded key ID before loading the key.
	if len(req.CiphertextBlob) < envelopeSealedOffset+1 {
		writeError(
			w,
			http.StatusBadRequest,
			"InvalidCiphertextException",
			"ciphertext is malformed",
		)
		return
	}
	if req.CiphertextBlob[0] != envelopeVersion {
		writeError(
			w,
			http.StatusBadRequest,
			"InvalidCiphertextException",
			"ciphertext is malformed",
		)
		return
	}
	embeddedKeyID := string(
		req.CiphertextBlob[envelopeKeyIDOffset : envelopeKeyIDOffset+envelopeKeyIDLen],
	)

	// If the caller provided a KeyId, verify it matches the embedded key ID.
	if req.KeyID != "" {
		resolvedID, ok := resolveKeyID(req.KeyID)
		if !ok {
			if isAliasRef(req.KeyID) {
				writeError(w, http.StatusBadRequest, "NotFoundException",
					"Alias key lookup is not supported; use a key ID or key ARN")
				return
			}
			writeError(w, http.StatusBadRequest, "InvalidArnException",
				fmt.Sprintf("Invalid key ARN: %s", req.KeyID))
			return
		}
		if resolvedID != embeddedKeyID {
			writeError(
				w,
				http.StatusBadRequest,
				"IncorrectKeyException",
				"The key ID in the request does not match the key used to encrypt the ciphertext",
			)
			return
		}
	}

	meta, keyID, ok := ro.resolveAndValidateKey(w, embeddedKeyID)
	if !ok {
		return
	}
	mat, ok := ro.loadSymmetricMaterial(w, meta, keyID)
	if !ok {
		return
	}

	_, plaintext, err := openEnvelope(req.CiphertextBlob, mat, req.EncryptionContext)
	if err != nil {
		slog.Debug("KMS Decrypt: open envelope failed", "err", err)
		writeError(w, http.StatusBadRequest, "InvalidCiphertextException",
			"ciphertext is invalid or the encryption context does not match")
		return
	}

	slog.Info("KMS Decrypt", "keyID", keyID)
	writeJSON(w, http.StatusOK, map[string]any{
		"Plaintext":           plaintext,
		"KeyId":               meta.Arn,
		"EncryptionAlgorithm": "SYMMETRIC_DEFAULT",
		"KeyMaterialId":       mat.KeyMaterialID,
	})
}

// ---- GenerateDataKey -------------------------------------------------------

func (ro *Router) handleGenerateDataKey(w http.ResponseWriter, body []byte) {
	keyBytes, ciphertextBlob, arn, materialID, ok := ro.generateDataKeyCommon(w, body)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"CiphertextBlob": ciphertextBlob,
		"Plaintext":      keyBytes,
		"KeyId":          arn,
		"KeyMaterialId":  materialID,
	})
}

// ---- GenerateDataKeyWithoutPlaintext ---------------------------------------

func (ro *Router) handleGenerateDataKeyWithoutPlaintext(w http.ResponseWriter, body []byte) {
	_, ciphertextBlob, arn, materialID, ok := ro.generateDataKeyCommon(w, body)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"CiphertextBlob": ciphertextBlob,
		"KeyId":          arn,
		"KeyMaterialId":  materialID,
	})
}

// generateDataKeyCommon handles the shared logic of GenerateDataKey and
// GenerateDataKeyWithoutPlaintext. Returns (rawKeyBytes, ciphertext, arn, materialID, ok).
func (ro *Router) generateDataKeyCommon(
	w http.ResponseWriter,
	body []byte,
) ([]byte, []byte, string, string, bool) {
	var req struct {
		KeyID             string            `json:"KeyId"`
		KeySpec           string            `json:"KeySpec"`
		NumberOfBytes     *int              `json:"NumberOfBytes"`
		EncryptionContext map[string]string `json:"EncryptionContext"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return nil, nil, "", "", false
	}

	// Exactly one of KeySpec or NumberOfBytes must be set.
	if req.KeySpec != "" && req.NumberOfBytes != nil {
		writeError(w, http.StatusBadRequest, "ValidationException",
			"specify either KeySpec or NumberOfBytes, not both")
		return nil, nil, "", "", false
	}
	if req.KeySpec == "" && req.NumberOfBytes == nil {
		writeError(w, http.StatusBadRequest, "ValidationException",
			"specify either KeySpec or NumberOfBytes")
		return nil, nil, "", "", false
	}

	var numBytes int
	switch req.KeySpec {
	case "AES_256":
		numBytes = 32
	case "AES_128":
		numBytes = 16
	case "":
		numBytes = *req.NumberOfBytes
		if numBytes < 1 || numBytes > 1024 {
			writeError(w, http.StatusBadRequest, "ValidationException",
				fmt.Sprintf("NumberOfBytes must be between 1 and 1024, got %d", numBytes))
			return nil, nil, "", "", false
		}
	default:
		writeError(w, http.StatusBadRequest, "ValidationException",
			fmt.Sprintf("Invalid KeySpec: %s; valid values are AES_256 and AES_128", req.KeySpec))
		return nil, nil, "", "", false
	}

	meta, keyID, ok := ro.resolveAndValidateKey(w, req.KeyID)
	if !ok {
		return nil, nil, "", "", false
	}
	mat, ok := ro.loadSymmetricMaterial(w, meta, keyID)
	if !ok {
		return nil, nil, "", "", false
	}

	dataKey := make([]byte, numBytes)
	if _, err := ro.randRead(dataKey); err != nil {
		slog.Error("KMS GenerateDataKey: rand read failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return nil, nil, "", "", false
	}

	ciphertextBlob, err := sealEnvelope(keyID, mat, dataKey, req.EncryptionContext, ro.randRead)
	if err != nil {
		slog.Error("KMS GenerateDataKey: seal failure", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"KMSInternalException",
			"internal server error",
		)
		return nil, nil, "", "", false
	}

	slog.Info("KMS GenerateDataKey", "keyID", keyID, "numBytes", numBytes)
	return dataKey, ciphertextBlob, meta.Arn, mat.KeyMaterialID, true
}
