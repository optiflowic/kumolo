package kms

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRouter(t *testing.T) *Router {
	t.Helper()
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return NewRouter(s)
}

func kmsReq(t *testing.T, router http.Handler, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Amz-Target", "TrentService."+target)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func assertErrType(t *testing.T, w *httptest.ResponseRecorder, errType string) {
	t.Helper()
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, errType, resp["__type"])
}

func mustCreateKey(t *testing.T, ro *Router, body string) string {
	t.Helper()
	w := kmsReq(t, ro, "CreateKey", body)
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	meta := resp["KeyMetadata"].(map[string]any)
	return meta["KeyId"].(string)
}

func TestHandleCreateKey(t *testing.T) {
	t.Run("creates SYMMETRIC_DEFAULT key with defaults", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CreateKey", `{}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		meta := resp["KeyMetadata"].(map[string]any)
		assert.Equal(t, "SYMMETRIC_DEFAULT", meta["KeySpec"])
		assert.Equal(t, "SYMMETRIC_DEFAULT", meta["CustomerMasterKeySpec"])
		assert.Equal(t, "ENCRYPT_DECRYPT", meta["KeyUsage"])
		assert.Equal(t, "Enabled", meta["KeyState"])
		assert.Equal(t, true, meta["Enabled"])
		assert.Equal(t, "CUSTOMER", meta["KeyManager"])
		assert.Equal(t, "AWS_KMS", meta["Origin"])
		assert.Equal(t, false, meta["MultiRegion"])
		assert.NotEmpty(t, meta["KeyId"])
		assert.NotEmpty(t, meta["Arn"])
		assert.Equal(t, fixedAccount, meta["AWSAccountId"])
		algos := meta["EncryptionAlgorithms"].([]any)
		assert.Equal(t, []any{"SYMMETRIC_DEFAULT"}, algos)
	})

	t.Run("creates key with explicit spec and usage", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(
			t,
			ro,
			"CreateKey",
			`{"KeySpec":"RSA_2048","KeyUsage":"SIGN_VERIFY","Description":"test key"}`,
		)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		meta := resp["KeyMetadata"].(map[string]any)
		assert.Equal(t, "RSA_2048", meta["KeySpec"])
		assert.Equal(t, "SIGN_VERIFY", meta["KeyUsage"])
		assert.Equal(t, "test key", meta["Description"])
		assert.NotNil(t, meta["SigningAlgorithms"])
		assert.Nil(t, meta["EncryptionAlgorithms"])
	})

	t.Run("creates HMAC key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CreateKey", `{"KeySpec":"HMAC_256","KeyUsage":"GENERATE_VERIFY_MAC"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		meta := resp["KeyMetadata"].(map[string]any)
		assert.Equal(t, "HMAC_256", meta["KeySpec"])
		algos := meta["MacAlgorithms"].([]any)
		assert.Equal(t, []any{"HMAC_SHA_256"}, algos)
	})

	t.Run("accepts CustomerMasterKeySpec alias", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CreateKey", `{"CustomerMasterKeySpec":"RSA_3072"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		meta := resp["KeyMetadata"].(map[string]any)
		assert.Equal(t, "RSA_3072", meta["KeySpec"])
	})

	t.Run("400 for invalid KeySpec", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CreateKey", `{"KeySpec":"INVALID_SPEC"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for incompatible KeyUsage", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CreateKey", `{"KeySpec":"SYMMETRIC_DEFAULT","KeyUsage":"SIGN_VERIFY"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for unsupported origin", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CreateKey", `{"Origin":"EXTERNAL"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "UnsupportedOperationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CreateKey", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})
}

func TestHandleDescribeKey(t *testing.T) {
	t.Run("describes key by ID", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "DescribeKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		meta := resp["KeyMetadata"].(map[string]any)
		assert.Equal(t, keyID, meta["KeyId"])
		assert.Equal(t, "SYMMETRIC_DEFAULT", meta["KeySpec"])
	})

	t.Run("describes key by ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		arn := keyARN(keyID)
		w := kmsReq(t, ro, "DescribeKey", `{"KeyId":"`+arn+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		meta := resp["KeyMetadata"].(map[string]any)
		assert.Equal(t, keyID, meta["KeyId"])
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DescribeKey", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DescribeKey", `{"KeyId":"00000000-0000-0000-0000-000000000000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 for alias reference", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DescribeKey", `{"KeyId":"alias/my-key"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 for malformed ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DescribeKey", `{"KeyId":"arn:aws:kms:us-east-1:123456789012:garbage"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DescribeKey", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})
}

func TestHandleListKeys(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListKeys", `{}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, false, resp["Truncated"])
		assert.Empty(t, resp["Keys"].([]any))
	})

	t.Run("lists all keys", func(t *testing.T) {
		ro := newTestRouter(t)
		id1 := mustCreateKey(t, ro, `{}`)
		id2 := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "ListKeys", `{}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		keys := resp["Keys"].([]any)
		assert.Len(t, keys, 2)
		ids := []string{
			keys[0].(map[string]any)["KeyId"].(string),
			keys[1].(map[string]any)["KeyId"].(string),
		}
		assert.ElementsMatch(t, []string{id1, id2}, ids)
		assert.Equal(t, false, resp["Truncated"])
	})

	t.Run("pagination with Limit and Marker", func(t *testing.T) {
		ro := newTestRouter(t)
		for range 5 {
			mustCreateKey(t, ro, `{}`)
		}
		// First page: Limit=3
		w := kmsReq(t, ro, "ListKeys", `{"Limit":3}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var page1 map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page1))
		assert.Equal(t, true, page1["Truncated"])
		assert.Len(t, page1["Keys"].([]any), 3)
		marker := page1["NextMarker"].(string)

		// Second page
		w = kmsReq(t, ro, "ListKeys", `{"Limit":3,"Marker":"`+marker+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var page2 map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page2))
		assert.Equal(t, false, page2["Truncated"])
		assert.Len(t, page2["Keys"].([]any), 2)
	})

	t.Run("400 for Limit less than 1", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListKeys", `{"Limit":-1}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for Limit > 1000", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListKeys", `{"Limit":1001}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListKeys", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("stale marker triggers binary search", func(t *testing.T) {
		ro := newTestRouter(t)
		mustCreateKey(t, ro, `{}`)
		mustCreateKey(t, ro, `{}`)
		// Marker "0" sorts before all UUIDs; binary search sets start=0 and all keys are returned.
		w := kmsReq(t, ro, "ListKeys", `{"Marker":"0"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Len(t, resp["Keys"].([]any), 2)
	})
}

func TestHandleGetKeyPolicy(t *testing.T) {
	t.Run("returns default policy", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "default", resp["PolicyName"])
		assert.NotEmpty(t, resp["Policy"])
	})

	t.Run("returns policy for explicit PolicyName default", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"`+keyID+`","PolicyName":"default"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "default", resp["PolicyName"])
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetKeyPolicy", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"00000000-0000-0000-0000-000000000000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 for unsupported PolicyName", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"`+keyID+`","PolicyName":"custom"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetKeyPolicy", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for alias ref KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"alias/my-key"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 for malformed ARN KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"arn:aws:kms:us-east-1:123456789012:garbage"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})
}

func TestHandlePutKeyPolicy(t *testing.T) {
	t.Run("updates policy and reads it back", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		newPolicy := `{"Version":"2012-10-17","Statement":[]}`
		w := kmsReq(t, ro, "PutKeyPolicy",
			`{"KeyId":"`+keyID+`","Policy":"`+escapeJSON(newPolicy)+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Body.String())

		// Read it back.
		w2 := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w2.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
		assert.Equal(t, newPolicy, resp["Policy"])
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "PutKeyPolicy", `{"Policy":"{}"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for missing Policy", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "PutKeyPolicy", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "PutKeyPolicy",
			`{"KeyId":"00000000-0000-0000-0000-000000000000","Policy":"{}"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 for policy exceeding 32768 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		bigPolicy := strings.Repeat("x", 32769)
		w := kmsReq(t, ro, "PutKeyPolicy",
			`{"KeyId":"`+keyID+`","Policy":"`+bigPolicy+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "LimitExceededException")
	})

	t.Run("400 for unsupported PolicyName", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "PutKeyPolicy",
			`{"KeyId":"`+keyID+`","Policy":"{}","PolicyName":"custom"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "PutKeyPolicy", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for alias ref KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "PutKeyPolicy", `{"KeyId":"alias/my-key","Policy":"{}"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 for malformed ARN KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(
			t,
			ro,
			"PutKeyPolicy",
			`{"KeyId":"arn:aws:kms:us-east-1:123456789012:garbage","Policy":"{}"}`,
		)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})
}

func TestUnknownOperation(t *testing.T) {
	ro := newTestRouter(t)
	w := kmsReq(t, ro, "UnknownOp", `{}`)
	assert.Equal(t, http.StatusNotImplemented, w.Code)
	assertErrType(t, w, "UnsupportedOperationException")
}

// alwaysFailStore is a store implementation that returns errors for every operation.
type alwaysFailStore struct{}

func (a *alwaysFailStore) CreateKey(CreateKeyInput) (KeyMetadata, error) {
	return KeyMetadata{}, errors.New("storage error")
}
func (a *alwaysFailStore) GetKeyMetadata(string) (KeyMetadata, error) {
	return KeyMetadata{}, errors.New("storage error")
}
func (a *alwaysFailStore) ListKeyIDs() ([]string, error) { return nil, errors.New("storage error") }
func (a *alwaysFailStore) GetKeyPolicy(string) (string, error) {
	return "", errors.New("storage error")
}
func (a *alwaysFailStore) PutKeyPolicy(string, string) error { return errors.New("storage error") }
func (a *alwaysFailStore) GetKeyMaterial(string) (KeyMaterial, error) {
	return KeyMaterial{}, errors.New("storage error")
}

func newFailRouter() *Router {
	return &Router{storage: &alwaysFailStore{}, randRead: func(b []byte) (int, error) {
		return 0, errors.New("rand error")
	}}
}

func TestHandleCreateKey_storageFailure(t *testing.T) {
	ro := newFailRouter()
	w := kmsReq(t, ro, "CreateKey", `{}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

func TestHandleDescribeKey_storageFailure(t *testing.T) {
	ro := newFailRouter()
	w := kmsReq(t, ro, "DescribeKey", `{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

func TestHandleListKeys_storageFailure(t *testing.T) {
	ro := newFailRouter()
	w := kmsReq(t, ro, "ListKeys", `{}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

func TestHandleGetKeyPolicy_storageFailure(t *testing.T) {
	ro := newFailRouter()
	w := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

func TestHandlePutKeyPolicy_storageFailure(t *testing.T) {
	ro := newFailRouter()
	w := kmsReq(
		t,
		ro,
		"PutKeyPolicy",
		`{"KeyId":"00000000-0000-0000-0000-000000000001","Policy":"{}"}`,
	)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

// brokenReader always errors on Read to trigger the body-read failure path in ServeHTTP.
type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("broken") }

func TestRouter_brokenBody(t *testing.T) {
	ro := newTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/", brokenReader{})
	req.Header.Set("X-Amz-Target", "TrentService.CreateKey")
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	w := httptest.NewRecorder()
	ro.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, "ValidationException")
}

func TestCreateKey_algorithmBranches(t *testing.T) {
	tests := []struct {
		spec  string
		usage string
	}{
		{"ECC_NIST_P256", "SIGN_VERIFY"},
		{"ECC_NIST_P384", "SIGN_VERIFY"},
		{"ECC_NIST_P521", "SIGN_VERIFY"},
		{"ECC_SECG_P256K1", "SIGN_VERIFY"},
		{"ECC_NIST_EDWARDS25519", "SIGN_VERIFY"},
		{"ML_DSA_44", "SIGN_VERIFY"},
		{"ML_DSA_65", "SIGN_VERIFY"},
		{"ML_DSA_87", "SIGN_VERIFY"},
		{"SM2", "SIGN_VERIFY"},
		{"SM2", "ENCRYPT_DECRYPT"},
		{"SM2", "KEY_AGREEMENT"},
		{"ECC_NIST_P256", "KEY_AGREEMENT"},
		{"HMAC_224", "GENERATE_VERIFY_MAC"},
		{"HMAC_384", "GENERATE_VERIFY_MAC"},
		{"HMAC_512", "GENERATE_VERIFY_MAC"},
	}
	for _, tc := range tests {
		t.Run(tc.spec+"/"+tc.usage, func(t *testing.T) {
			ro := newTestRouter(t)
			w := kmsReq(t, ro, "CreateKey",
				`{"KeySpec":"`+tc.spec+`","KeyUsage":"`+tc.usage+`"}`)
			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

// failWriter overrides Write to always fail, triggering the defensive slog.Warn paths.
type failWriter struct {
	http.ResponseWriter
}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestWriteError_writeFails(t *testing.T) {
	writeError(failWriter{httptest.NewRecorder()}, http.StatusBadRequest, "TestError", "msg")
}

func TestWriteJSON_writeFails(t *testing.T) {
	writeJSON(failWriter{httptest.NewRecorder()}, http.StatusOK, map[string]string{"k": "v"})
}

// escapeJSON escapes a JSON string for embedding inside another JSON string.
func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1]) // strip surrounding quotes
}

// ---- helpers for data-plane tests ------------------------------------------

// mustEncrypt encrypts plaintext under keyID and returns the base64-encoded CiphertextBlob string.
func mustEncrypt(t *testing.T, ro *Router, keyID string, plaintext []byte) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"KeyId":     keyID,
		"Plaintext": plaintext,
	})
	w := kmsReq(t, ro, "Encrypt", string(body))
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp["CiphertextBlob"].(string)
}

// ---- Encrypt ----------------------------------------------------------------

func TestHandleEncrypt(t *testing.T) {
	t.Run("encrypts plaintext and returns ciphertext blob", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "Encrypt", `{"KeyId":"`+keyID+`","Plaintext":"aGVsbG8="}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp["CiphertextBlob"])
		assert.Equal(t, keyARN(keyID), resp["KeyId"])
		assert.Equal(t, "SYMMETRIC_DEFAULT", resp["EncryptionAlgorithm"])
	})

	t.Run("accepts SYMMETRIC_DEFAULT algorithm explicitly", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(
			t,
			ro,
			"Encrypt",
			`{"KeyId":"`+keyID+`","Plaintext":"aGVsbG8=","EncryptionAlgorithm":"SYMMETRIC_DEFAULT"}`,
		)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for unsupported EncryptionAlgorithm", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(
			t,
			ro,
			"Encrypt",
			`{"KeyId":"`+keyID+`","Plaintext":"aGVsbG8=","EncryptionAlgorithm":"RSAES_OAEP_SHA_256"}`,
		)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("400 for missing Plaintext", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "Encrypt", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for Plaintext exceeding 4096 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		big := make([]byte, 4097)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Plaintext": big})
		w := kmsReq(t, ro, "Encrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "Encrypt", `{"Plaintext":"aGVsbG8="}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "Encrypt",
			`{"KeyId":"00000000-0000-0000-0000-000000000000","Plaintext":"aGVsbG8="}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 for non-ENCRYPT_DECRYPT key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"ECC_NIST_P256","KeyUsage":"SIGN_VERIFY"}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Plaintext": []byte("hi")})
		w := kmsReq(t, ro, "Encrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("400 for non-SYMMETRIC_DEFAULT ENCRYPT_DECRYPT key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"RSA_2048","KeyUsage":"ENCRYPT_DECRYPT"}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Plaintext": []byte("hi")})
		w := kmsReq(t, ro, "Encrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "Encrypt", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})
}

func TestHandleEncrypt_storageFailure(t *testing.T) {
	ro := newFailRouter()
	w := kmsReq(t, ro, "Encrypt",
		`{"KeyId":"00000000-0000-0000-0000-000000000001","Plaintext":"aGVsbG8="}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

// ---- Decrypt ----------------------------------------------------------------

func TestHandleDecrypt(t *testing.T) {
	t.Run("roundtrip encrypt then decrypt", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		plaintext := []byte("hello kumolo")
		cipherBlob := mustEncrypt(t, ro, keyID, plaintext)

		body, _ := json.Marshal(map[string]any{"CiphertextBlob": cipherBlob})
		w := kmsReq(t, ro, "Decrypt", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, keyARN(keyID), resp["KeyId"])
		assert.Equal(t, "SYMMETRIC_DEFAULT", resp["EncryptionAlgorithm"])
		assert.NotEmpty(t, resp["KeyMaterialId"])

		// Plaintext is base64 in JSON; json.Unmarshal decodes it to a string for []byte fields.
		got, _ := json.Marshal(resp["Plaintext"])
		var gotBytes []byte
		require.NoError(t, json.Unmarshal(got, &gotBytes))
		assert.Equal(t, plaintext, gotBytes)
	})

	t.Run("roundtrip with EncryptionContext", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		ctx := map[string]string{"service": "s3", "bucket": "my-bucket"}
		encBody, _ := json.Marshal(map[string]any{
			"KeyId":             keyID,
			"Plaintext":         []byte("secret"),
			"EncryptionContext": ctx,
		})
		ew := kmsReq(t, ro, "Encrypt", string(encBody))
		require.Equal(t, http.StatusOK, ew.Code)
		var encResp map[string]any
		require.NoError(t, json.Unmarshal(ew.Body.Bytes(), &encResp))
		cipherBlob := encResp["CiphertextBlob"]

		decBody, _ := json.Marshal(map[string]any{
			"CiphertextBlob":    cipherBlob,
			"EncryptionContext": ctx,
		})
		dw := kmsReq(t, ro, "Decrypt", string(decBody))
		assert.Equal(t, http.StatusOK, dw.Code)
	})

	t.Run("400 wrong EncryptionContext", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		encBody, _ := json.Marshal(map[string]any{
			"KeyId":             keyID,
			"Plaintext":         []byte("secret"),
			"EncryptionContext": map[string]string{"k": "v"},
		})
		ew := kmsReq(t, ro, "Encrypt", string(encBody))
		require.Equal(t, http.StatusOK, ew.Code)
		var encResp map[string]any
		require.NoError(t, json.Unmarshal(ew.Body.Bytes(), &encResp))

		decBody, _ := json.Marshal(map[string]any{
			"CiphertextBlob":    encResp["CiphertextBlob"],
			"EncryptionContext": map[string]string{"k": "wrong"},
		})
		dw := kmsReq(t, ro, "Decrypt", string(decBody))
		assert.Equal(t, http.StatusBadRequest, dw.Code)
		assertErrType(t, dw, "InvalidCiphertextException")
	})

	t.Run("400 for missing CiphertextBlob", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "Decrypt", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for malformed ciphertext (too short)", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"CiphertextBlob": []byte("short")})
		w := kmsReq(t, ro, "Decrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidCiphertextException")
	})

	t.Run("400 for malformed ciphertext (wrong version)", func(t *testing.T) {
		ro := newTestRouter(t)
		blob := make([]byte, envelopeSealedOffset+1)
		blob[0] = 0xFF // bad version
		body, _ := json.Marshal(map[string]any{"CiphertextBlob": blob})
		w := kmsReq(t, ro, "Decrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidCiphertextException")
	})

	t.Run("400 when KeyId does not match embedded key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		keyID2 := mustCreateKey(t, ro, `{}`)
		cipherBlob := mustEncrypt(t, ro, keyID, []byte("data"))

		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob": cipherBlob,
			"KeyId":          keyID2,
		})
		w := kmsReq(t, ro, "Decrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "IncorrectKeyException")
	})

	t.Run("200 when provided KeyId matches embedded key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		cipherBlob := mustEncrypt(t, ro, keyID, []byte("data"))

		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob": cipherBlob,
			"KeyId":          keyID,
		})
		w := kmsReq(t, ro, "Decrypt", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for unsupported EncryptionAlgorithm", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		cipherBlob := mustEncrypt(t, ro, keyID, []byte("data"))
		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":      cipherBlob,
			"EncryptionAlgorithm": "RSAES_OAEP_SHA_256",
		})
		w := kmsReq(t, ro, "Decrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "Decrypt", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})
}

func TestHandleDecrypt_storageFailure(t *testing.T) {
	ro := newFailRouter()
	// Build a syntactically valid envelope with a non-existent key ID so the router
	// reaches the storage layer and triggers the storage error.
	blob := make([]byte, envelopeSealedOffset+1)
	blob[0] = envelopeVersion
	copy(blob[envelopeKeyIDOffset:], []byte("00000000-0000-0000-0000-000000000001"))
	blob[envelopeAlgoOffset] = algoSymmetricDefault
	body, _ := json.Marshal(map[string]any{"CiphertextBlob": blob})
	w := kmsReq(t, ro, "Decrypt", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

// ---- GenerateDataKey --------------------------------------------------------

func TestHandleGenerateDataKey(t *testing.T) {
	t.Run("AES_256 returns 32-byte plaintext and ciphertext", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "GenerateDataKey",
			`{"KeyId":"`+keyID+`","KeySpec":"AES_256"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp["CiphertextBlob"])
		assert.NotEmpty(t, resp["Plaintext"])
		assert.Equal(t, keyARN(keyID), resp["KeyId"])
		assert.NotEmpty(t, resp["KeyMaterialId"])
	})

	t.Run("AES_128 returns 16-byte plaintext", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "KeySpec": "AES_128"})
		w := kmsReq(t, ro, "GenerateDataKey", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		ptJSON, _ := json.Marshal(resp["Plaintext"])
		var ptBytes []byte
		require.NoError(t, json.Unmarshal(ptJSON, &ptBytes))
		assert.Len(t, ptBytes, 16)
	})

	t.Run("NumberOfBytes=64 returns 64-byte key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "NumberOfBytes": 64})
		w := kmsReq(t, ro, "GenerateDataKey", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		ptJSON, _ := json.Marshal(resp["Plaintext"])
		var ptBytes []byte
		require.NoError(t, json.Unmarshal(ptJSON, &ptBytes))
		assert.Len(t, ptBytes, 64)
	})

	t.Run("generated data key can be decrypted", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "KeySpec": "AES_256"})
		w := kmsReq(t, ro, "GenerateDataKey", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var genResp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &genResp))

		decBody, _ := json.Marshal(map[string]any{"CiphertextBlob": genResp["CiphertextBlob"]})
		dw := kmsReq(t, ro, "Decrypt", string(decBody))
		require.Equal(t, http.StatusOK, dw.Code)
		var decResp map[string]any
		require.NoError(t, json.Unmarshal(dw.Body.Bytes(), &decResp))

		// The decrypted plaintext must match the original plaintext.
		ptJSON, _ := json.Marshal(genResp["Plaintext"])
		decJSON, _ := json.Marshal(decResp["Plaintext"])
		assert.Equal(t, string(ptJSON), string(decJSON))
	})

	t.Run("400 for both KeySpec and NumberOfBytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":         keyID,
			"KeySpec":       "AES_256",
			"NumberOfBytes": 32,
		})
		w := kmsReq(t, ro, "GenerateDataKey", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for neither KeySpec nor NumberOfBytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "GenerateDataKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid KeySpec", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "GenerateDataKey",
			`{"KeyId":"`+keyID+`","KeySpec":"AES_512"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for NumberOfBytes=0", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "NumberOfBytes": 0})
		w := kmsReq(t, ro, "GenerateDataKey", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for NumberOfBytes=1025", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "NumberOfBytes": 1025})
		w := kmsReq(t, ro, "GenerateDataKey", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"KeyId":   "00000000-0000-0000-0000-000000000000",
			"KeySpec": "AES_256",
		})
		w := kmsReq(t, ro, "GenerateDataKey", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 for non-ENCRYPT_DECRYPT key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"ECC_NIST_P256","KeyUsage":"SIGN_VERIFY"}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "KeySpec": "AES_256"})
		w := kmsReq(t, ro, "GenerateDataKey", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("400 for non-SYMMETRIC_DEFAULT ENCRYPT_DECRYPT key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"RSA_2048","KeyUsage":"ENCRYPT_DECRYPT"}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "KeySpec": "AES_256"})
		w := kmsReq(t, ro, "GenerateDataKey", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GenerateDataKey", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})
}

func TestHandleGenerateDataKey_storageFailure(t *testing.T) {
	ro := newFailRouter()
	body, _ := json.Marshal(map[string]any{
		"KeyId":   "00000000-0000-0000-0000-000000000001",
		"KeySpec": "AES_256",
	})
	w := kmsReq(t, ro, "GenerateDataKey", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

// ---- GenerateDataKeyWithoutPlaintext ----------------------------------------

func TestHandleGenerateDataKeyWithoutPlaintext(t *testing.T) {
	t.Run("AES_256 returns ciphertext only (no Plaintext field)", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "GenerateDataKeyWithoutPlaintext",
			`{"KeyId":"`+keyID+`","KeySpec":"AES_256"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp["CiphertextBlob"])
		assert.Nil(t, resp["Plaintext"])
		assert.Equal(t, keyARN(keyID), resp["KeyId"])
		assert.NotEmpty(t, resp["KeyMaterialId"])
	})

	t.Run("ciphertext can be decrypted to recover the data key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "KeySpec": "AES_256"})
		w := kmsReq(t, ro, "GenerateDataKeyWithoutPlaintext", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var genResp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &genResp))

		decBody, _ := json.Marshal(map[string]any{"CiphertextBlob": genResp["CiphertextBlob"]})
		dw := kmsReq(t, ro, "Decrypt", string(decBody))
		require.Equal(t, http.StatusOK, dw.Code)
		var decResp map[string]any
		require.NoError(t, json.Unmarshal(dw.Body.Bytes(), &decResp))
		ptJSON, _ := json.Marshal(decResp["Plaintext"])
		var ptBytes []byte
		require.NoError(t, json.Unmarshal(ptJSON, &ptBytes))
		assert.Len(t, ptBytes, 32)
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GenerateDataKeyWithoutPlaintext", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})
}

func TestHandleGenerateDataKeyWithoutPlaintext_storageFailure(t *testing.T) {
	ro := newFailRouter()
	body, _ := json.Marshal(map[string]any{
		"KeyId":   "00000000-0000-0000-0000-000000000001",
		"KeySpec": "AES_256",
	})
	w := kmsReq(t, ro, "GenerateDataKeyWithoutPlaintext", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

// ---- disabled key -----------------------------------------------------------

func mustDisableKey(t *testing.T, s *Storage, keyID string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readKeyMeta(keyID)
	require.NoError(t, err)
	meta.Enabled = false
	meta.KeyState = "Disabled"
	require.NoError(t, s.writeJSON("keys/"+keyID+"/meta.json", meta))
}

func TestDataPlane_disabledKey(t *testing.T) {
	ops := []struct {
		name string
		body func(keyID string) string
	}{
		{"Encrypt", func(id string) string {
			b, _ := json.Marshal(map[string]any{"KeyId": id, "Plaintext": []byte("x")})
			return string(b)
		}},
		{"GenerateDataKey", func(id string) string {
			b, _ := json.Marshal(map[string]any{"KeyId": id, "KeySpec": "AES_256"})
			return string(b)
		}},
		{"GenerateDataKeyWithoutPlaintext", func(id string) string {
			b, _ := json.Marshal(map[string]any{"KeyId": id, "KeySpec": "AES_256"})
			return string(b)
		}},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := newStorage(dir, os.OpenRoot)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			ro := NewRouter(s)

			keyID := mustCreateKey(t, ro, `{}`)
			mustDisableKey(t, s, keyID)
			w := kmsReq(t, ro, op.name, op.body(keyID))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, "DisabledException")
		})
	}
}

func TestDecrypt_disabledKey(t *testing.T) {
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	ro := NewRouter(s)

	keyID := mustCreateKey(t, ro, `{}`)
	cipherBlob := mustEncrypt(t, ro, keyID, []byte("data"))
	mustDisableKey(t, s, keyID)

	body, _ := json.Marshal(map[string]any{"CiphertextBlob": cipherBlob})
	w := kmsReq(t, ro, "Decrypt", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, "DisabledException")
}

// ---- no-material key (key without material.json) ----------------------------

func TestDataPlane_noMaterial(t *testing.T) {
	// RSA_2048 ENCRYPT_DECRYPT keys have no material.json and must return
	// InvalidKeyUsageException because they are not SYMMETRIC_DEFAULT.
	ops := []struct {
		name string
		body func(keyID string) string
	}{
		{"Encrypt", func(id string) string {
			b, _ := json.Marshal(map[string]any{"KeyId": id, "Plaintext": []byte("x")})
			return string(b)
		}},
		{"GenerateDataKey", func(id string) string {
			b, _ := json.Marshal(map[string]any{"KeyId": id, "KeySpec": "AES_256"})
			return string(b)
		}},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			ro := newTestRouter(t)
			keyID := mustCreateKey(t, ro, `{"KeySpec":"RSA_2048","KeyUsage":"ENCRYPT_DECRYPT"}`)
			w := kmsReq(t, ro, op.name, op.body(keyID))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, "InvalidKeyUsageException")
		})
	}
}

// ---- alias / ARN validation for data-plane key IDs -------------------------

func TestDataPlane_aliasAndARNErrors(t *testing.T) {
	cases := []struct {
		op    string
		bodyF func(keyID string) string
	}{
		{"Encrypt", func(id string) string {
			b, _ := json.Marshal(map[string]any{"KeyId": id, "Plaintext": []byte("x")})
			return string(b)
		}},
		{"GenerateDataKey", func(id string) string {
			b, _ := json.Marshal(map[string]any{"KeyId": id, "KeySpec": "AES_256"})
			return string(b)
		}},
		{"GenerateDataKeyWithoutPlaintext", func(id string) string {
			b, _ := json.Marshal(map[string]any{"KeyId": id, "KeySpec": "AES_256"})
			return string(b)
		}},
	}
	for _, c := range cases {
		t.Run(c.op+"/alias", func(t *testing.T) {
			ro := newTestRouter(t)
			w := kmsReq(t, ro, c.op, c.bodyF("alias/my-key"))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, "NotFoundException")
		})
		t.Run(c.op+"/malformedARN", func(t *testing.T) {
			ro := newTestRouter(t)
			w := kmsReq(t, ro, c.op, c.bodyF("arn:aws:kms:us-east-1:123456789012:garbage"))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, "InvalidArnException")
		})
	}
}

// ---- nonce randRead failure in sealEnvelope ---------------------------------

func TestHandleEncrypt_nonceRandReadFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	ro := NewRouter(s)
	keyID := mustCreateKey(t, ro, `{}`)

	// Replace randRead with one that fails so sealEnvelope cannot generate a nonce.
	ro.randRead = func([]byte) (int, error) { return 0, errors.New("rand failed") }
	body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Plaintext": []byte("x")})
	w := kmsReq(t, ro, "Encrypt", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

func TestGenerateDataKey_dataKeyRandReadFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	ro := NewRouter(s)
	mustCreateKey(t, ro, `{}`)

	// Fail on the first randRead call (data key bytes generation).
	keyID := mustCreateKey(t, ro, `{}`)
	ro.randRead = func([]byte) (int, error) { return 0, errors.New("rand failed") }
	body, _ := json.Marshal(map[string]any{"KeyId": keyID, "KeySpec": "AES_256"})
	w := kmsReq(t, ro, "GenerateDataKey", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

func TestGenerateDataKey_nonceRandReadFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	ro := NewRouter(s)
	keyID := mustCreateKey(t, ro, `{}`)

	// The first randRead call in generateDataKeyCommon generates the data key bytes;
	// the second (inside sealEnvelope) generates the nonce.
	// Fail on the second call so the data key is generated but sealing fails.
	calls := 0
	realRand := ro.randRead
	ro.randRead = func(b []byte) (int, error) {
		calls++
		if calls >= 2 {
			return 0, errors.New("rand failed")
		}
		return realRand(b)
	}
	body, _ := json.Marshal(map[string]any{"KeyId": keyID, "KeySpec": "AES_256"})
	w := kmsReq(t, ro, "GenerateDataKey", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

// ---- ciphertext with unknown algorithm byte ---------------------------------

func TestHandleDecrypt_unknownAlgo(t *testing.T) {
	ro := newTestRouter(t)
	keyID := mustCreateKey(t, ro, `{}`)
	// Build a valid-looking envelope but with an unsupported algorithm byte.
	blob := make([]byte, envelopeSealedOffset+1)
	blob[0] = envelopeVersion
	copy(blob[envelopeKeyIDOffset:], []byte(keyID))
	blob[envelopeAlgoOffset] = 0x99 // unknown algo code
	body, _ := json.Marshal(map[string]any{"CiphertextBlob": blob})
	w := kmsReq(t, ro, "Decrypt", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, "InvalidCiphertextException")
}

// ---- Decrypt provided-KeyId alias and ARN errors ---------------------------

func TestHandleDecrypt_providedKeyIDErrors(t *testing.T) {
	ro := newTestRouter(t)
	keyID := mustCreateKey(t, ro, `{}`)
	cipherBlob := mustEncrypt(t, ro, keyID, []byte("data"))

	t.Run("alias KeyId returns NotFoundException", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob": cipherBlob,
			"KeyId":          "alias/my-key",
		})
		w := kmsReq(t, ro, "Decrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("malformed ARN KeyId returns InvalidArnException", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob": cipherBlob,
			"KeyId":          "arn:aws:kms:us-east-1:123456789012:garbage",
		})
		w := kmsReq(t, ro, "Decrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})
}

// ---- loadSymmetricMaterial: ErrKeyMaterialNotFound path --------------------

// partialFailStore succeeds on metadata reads but fails on GetKeyMaterial.
type partialFailStore struct {
	inner store
}

func (p *partialFailStore) CreateKey(in CreateKeyInput) (KeyMetadata, error) {
	return p.inner.CreateKey(in)
}
func (p *partialFailStore) GetKeyMetadata(keyID string) (KeyMetadata, error) {
	return p.inner.GetKeyMetadata(keyID)
}
func (p *partialFailStore) ListKeyIDs() ([]string, error) { return p.inner.ListKeyIDs() }
func (p *partialFailStore) GetKeyPolicy(keyID string) (string, error) {
	return p.inner.GetKeyPolicy(keyID)
}
func (p *partialFailStore) PutKeyPolicy(keyID, policy string) error {
	return p.inner.PutKeyPolicy(keyID, policy)
}
func (p *partialFailStore) GetKeyMaterial(string) (KeyMaterial, error) {
	return KeyMaterial{}, errors.New("storage error")
}

func TestLoadSymmetricMaterial_storageError(t *testing.T) {
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	realRouter := NewRouter(s)
	keyID := mustCreateKey(t, realRouter, `{}`)

	ro := &Router{storage: &partialFailStore{inner: s}, randRead: s.randRead}
	body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Plaintext": []byte("x")})
	w := kmsReq(t, ro, "Encrypt", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

func TestLoadSymmetricMaterial_materialNotFound(t *testing.T) {
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	ro := NewRouter(s)

	keyID := mustCreateKey(t, ro, `{}`)
	// Remove material.json to simulate a key created before this feature.
	require.NoError(t, os.Remove(filepath.Join(dir, "kms", "keys", keyID, "material.json")))

	body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Plaintext": []byte("x")})
	w := kmsReq(t, ro, "Encrypt", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, "KMSInvalidStateException")
}
