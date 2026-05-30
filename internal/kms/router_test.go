package kms

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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

func newFailRouter() *Router { return &Router{storage: &alwaysFailStore{}} }

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
