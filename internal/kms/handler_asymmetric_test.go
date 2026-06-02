package kms

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodePubKeyDER base64-decodes the PublicKey field from a GetPublicKey response.
func decodePubKeyDER(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var der []byte
	require.NoError(t, json.Unmarshal(raw, &der))
	return der
}

// ---- GetPublicKey happy path -------------------------------------------------

func TestHandleGetPublicKey(t *testing.T) {
	t.Run(
		"200 RSA_2048 ENCRYPT_DECRYPT — returns RSA public key and EncryptionAlgorithms",
		func(t *testing.T) {
			ro := newTestRouter(t)
			keyID := mustCreateKey(t, ro, `{"KeySpec":"RSA_2048","KeyUsage":"ENCRYPT_DECRYPT"}`)
			w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, keyARN(keyID), resp["KeyId"])
			assert.Equal(t, "RSA_2048", resp["KeySpec"])
			assert.Equal(t, "RSA_2048", resp["CustomerMasterKeySpec"])
			assert.Equal(t, "ENCRYPT_DECRYPT", resp["KeyUsage"])
			assert.NotNil(t, resp["EncryptionAlgorithms"])
			assert.Nil(t, resp["SigningAlgorithms"])
			assert.Nil(t, resp["KeyAgreementAlgorithms"])
			pubDER := decodePubKeyDER(t, resp["PublicKey"])
			require.NotEmpty(t, pubDER)
			pub, err := x509.ParsePKIXPublicKey(pubDER)
			require.NoError(t, err)
			_, ok := pub.(*rsa.PublicKey)
			assert.True(t, ok)
		},
	)

	t.Run(
		"200 RSA_2048 SIGN_VERIFY — returns RSA public key and SigningAlgorithms",
		func(t *testing.T) {
			ro := newTestRouter(t)
			keyID := mustCreateKey(t, ro, `{"KeySpec":"RSA_2048","KeyUsage":"SIGN_VERIFY"}`)
			w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, "RSA_2048", resp["KeySpec"])
			assert.Equal(t, "SIGN_VERIFY", resp["KeyUsage"])
			assert.Nil(t, resp["EncryptionAlgorithms"])
			assert.NotNil(t, resp["SigningAlgorithms"])
			pub, err := x509.ParsePKIXPublicKey(decodePubKeyDER(t, resp["PublicKey"]))
			require.NoError(t, err)
			_, ok := pub.(*rsa.PublicKey)
			assert.True(t, ok)
		},
	)

	t.Run(
		"200 ECC_NIST_P256 SIGN_VERIFY — returns ECDSA public key and SigningAlgorithms",
		func(t *testing.T) {
			ro := newTestRouter(t)
			keyID := mustCreateKey(t, ro, `{"KeySpec":"ECC_NIST_P256","KeyUsage":"SIGN_VERIFY"}`)
			w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, "ECC_NIST_P256", resp["KeySpec"])
			assert.Equal(t, "SIGN_VERIFY", resp["KeyUsage"])
			assert.NotNil(t, resp["SigningAlgorithms"])
			pub, err := x509.ParsePKIXPublicKey(decodePubKeyDER(t, resp["PublicKey"]))
			require.NoError(t, err)
			ecPub, ok := pub.(*ecdsa.PublicKey)
			require.True(t, ok)
			assert.Equal(t, "P-256", ecPub.Curve.Params().Name)
		},
	)

	t.Run(
		"200 ECC_NIST_P256 KEY_AGREEMENT — returns ECDSA public key and KeyAgreementAlgorithms",
		func(t *testing.T) {
			ro := newTestRouter(t)
			keyID := mustCreateKey(t, ro, `{"KeySpec":"ECC_NIST_P256","KeyUsage":"KEY_AGREEMENT"}`)
			w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, "KEY_AGREEMENT", resp["KeyUsage"])
			assert.Nil(t, resp["EncryptionAlgorithms"])
			assert.Nil(t, resp["SigningAlgorithms"])
			assert.NotNil(t, resp["KeyAgreementAlgorithms"])
		},
	)

	t.Run("200 ECC_NIST_P384 SIGN_VERIFY — returns P-384 public key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"ECC_NIST_P384","KeyUsage":"SIGN_VERIFY"}`)
		w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		pub, err := x509.ParsePKIXPublicKey(decodePubKeyDER(t, resp["PublicKey"]))
		require.NoError(t, err)
		ecPub, ok := pub.(*ecdsa.PublicKey)
		require.True(t, ok)
		assert.Equal(t, "P-384", ecPub.Curve.Params().Name)
	})

	t.Run("200 ECC_NIST_P521 SIGN_VERIFY — returns P-521 public key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"ECC_NIST_P521","KeyUsage":"SIGN_VERIFY"}`)
		w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		pub, err := x509.ParsePKIXPublicKey(decodePubKeyDER(t, resp["PublicKey"]))
		require.NoError(t, err)
		ecPub, ok := pub.(*ecdsa.PublicKey)
		require.True(t, ok)
		assert.Equal(t, "P-521", ecPub.Curve.Params().Name)
	})

	t.Run("200 ECC_NIST_EDWARDS25519 SIGN_VERIFY — returns Ed25519 public key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(
			t,
			ro,
			`{"KeySpec":"ECC_NIST_EDWARDS25519","KeyUsage":"SIGN_VERIFY"}`,
		)
		w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		pub, err := x509.ParsePKIXPublicKey(decodePubKeyDER(t, resp["PublicKey"]))
		require.NoError(t, err)
		_, ok := pub.(ed25519.PublicKey)
		assert.True(t, ok)
	})

	t.Run("200 disabled key — GetPublicKey is allowed in Disabled state", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ro := NewRouter(s)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"ECC_NIST_P256","KeyUsage":"SIGN_VERIFY"}`)
		mustDisableKey(t, s, keyID)
		w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, keyARN(keyID), resp["KeyId"])
	})

	t.Run("200 via alias name", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"ECC_NIST_P256","KeyUsage":"SIGN_VERIFY"}`)
		mustCreateAlias(t, ro, "alias/my-ecc-key", keyID)
		w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"alias/my-ecc-key"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, keyARN(keyID), resp["KeyId"])
	})

	t.Run("200 via key ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"ECC_NIST_P256","KeyUsage":"SIGN_VERIFY"}`)
		arn := keyARN(keyID)
		w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+arn+`"}`)
		require.Equal(t, http.StatusOK, w.Code)
	})
}

// ---- GetPublicKey error cases ------------------------------------------------

func TestHandleGetPublicKey_errors(t *testing.T) {
	t.Run("400 missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetPublicKey", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetPublicKey", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"00000000-0000-0000-0000-000000000000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 malformed ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetPublicKey",
			`{"KeyId":"arn:aws:kms:us-east-1:123456789012:garbage"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})

	t.Run("400 PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"ECC_NIST_P256","KeyUsage":"SIGN_VERIFY"}`)
		mustScheduleKeyDeletion(t, ro, keyID)
		w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 SYMMETRIC_DEFAULT key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("400 HMAC_256 key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"HMAC_256","KeyUsage":"GENERATE_VERIFY_MAC"}`)
		w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("400 ECC_SECG_P256K1 — no material, unsupported spec", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"ECC_SECG_P256K1","KeyUsage":"SIGN_VERIFY"}`)
		w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "UnsupportedOperationException")
	})

	t.Run("400 ML_DSA_44 — no material, unsupported spec", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"ML_DSA_44","KeyUsage":"SIGN_VERIFY"}`)
		w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "UnsupportedOperationException")
	})
}

// ---- GetPublicKey storage failure paths -------------------------------------

func TestHandleGetPublicKey_storageFailure(t *testing.T) {
	ro := newFailRouter()
	w := kmsReq(t, ro, "GetPublicKey",
		`{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

func TestHandleGetPublicKey_materialStorageFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	realRouter := NewRouter(s)
	keyID := mustCreateKey(t, realRouter, `{"KeySpec":"ECC_NIST_P256","KeyUsage":"SIGN_VERIFY"}`)

	pfs := &partialFailStore{inner: s}
	ro := newRouterWithRand(pfs, s.randRead)
	w := kmsReq(t, ro, "GetPublicKey", `{"KeyId":"`+keyID+`"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}
