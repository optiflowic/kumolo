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

	t.Run("400 for alias ref KeyId not found", func(t *testing.T) {
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

	t.Run("200 via alias name", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", keyID)
		w := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"alias/my-key"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "default", resp["PolicyName"])
		assert.NotEmpty(t, resp["Policy"])
	})

	t.Run("200 via alias ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", keyID)
		arn := aliasARN("alias/my-key")
		w := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"`+arn+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "default", resp["PolicyName"])
		assert.NotEmpty(t, resp["Policy"])
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

	t.Run("400 for alias ref KeyId not found", func(t *testing.T) {
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

	t.Run("200 via alias name", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/put-key", keyID)
		newPolicy := `{"Version":"2012-10-17","Statement":[]}`
		w := kmsReq(t, ro, "PutKeyPolicy",
			`{"KeyId":"alias/put-key","Policy":"`+escapeJSON(newPolicy)+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)

		w2 := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"alias/put-key"}`)
		assert.Equal(t, http.StatusOK, w2.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
		assert.Equal(t, newPolicy, resp["Policy"])
	})

	t.Run("200 via alias ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/put-key", keyID)
		arn := aliasARN("alias/put-key")
		newPolicy := `{"Version":"2012-10-17","Statement":[]}`
		w := kmsReq(t, ro, "PutKeyPolicy",
			`{"KeyId":"`+arn+`","Policy":"`+escapeJSON(newPolicy)+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)

		w2 := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"`+arn+`"}`)
		assert.Equal(t, http.StatusOK, w2.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
		assert.Equal(t, newPolicy, resp["Policy"])
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
func (a *alwaysFailStore) CreateAlias(string, string) error { return errors.New("storage error") }
func (a *alwaysFailStore) DeleteAlias(string) error         { return errors.New("storage error") }
func (a *alwaysFailStore) UpdateAlias(string, string) error { return errors.New("storage error") }
func (a *alwaysFailStore) ListAliases(string) ([]AliasEntry, error) {
	return nil, errors.New("storage error")
}
func (a *alwaysFailStore) ResolveAlias(string) (string, error) {
	return "", errors.New("storage error")
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
		t.Run(c.op+"/alias not found", func(t *testing.T) {
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
		t.Run(c.op+"/alias name success", func(t *testing.T) {
			ro := newTestRouter(t)
			keyID := mustCreateKey(t, ro, `{}`)
			mustCreateAlias(t, ro, "alias/my-key", keyID)
			w := kmsReq(t, ro, c.op, c.bodyF("alias/my-key"))
			assert.Equal(t, http.StatusOK, w.Code)
		})
		t.Run(c.op+"/alias ARN success", func(t *testing.T) {
			ro := newTestRouter(t)
			keyID := mustCreateKey(t, ro, `{}`)
			mustCreateAlias(t, ro, "alias/my-key", keyID)
			w := kmsReq(t, ro, c.op, c.bodyF(aliasARN("alias/my-key")))
			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

func TestDataPlane_Decrypt_aliasResolution(t *testing.T) {
	t.Run("200 via alias name", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", keyID)
		cipherBlob := mustEncrypt(t, ro, keyID, []byte("hello"))
		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob": cipherBlob,
			"KeyId":          "alias/my-key",
		})
		w := kmsReq(t, ro, "Decrypt", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("200 via alias ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", keyID)
		cipherBlob := mustEncrypt(t, ro, keyID, []byte("hello"))
		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob": cipherBlob,
			"KeyId":          aliasARN("alias/my-key"),
		})
		w := kmsReq(t, ro, "Decrypt", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})
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

func TestHandleEncrypt_shortRandRead(t *testing.T) {
	ro := newTestRouter(t)
	keyID := mustCreateKey(t, ro, `{}`)
	// Return fewer bytes than requested with no error — readFullRand must reject this.
	ro.randRead = func(b []byte) (int, error) { return 0, nil }
	body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Plaintext": []byte("x")})
	w := kmsReq(t, ro, "Encrypt", string(body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

func TestGenerateDataKey_shortRandRead(t *testing.T) {
	ro := newTestRouter(t)
	keyID := mustCreateKey(t, ro, `{}`)
	ro.randRead = func(b []byte) (int, error) { return 0, nil }
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
func (p *partialFailStore) CreateAlias(aliasName, targetKeyID string) error {
	return p.inner.CreateAlias(aliasName, targetKeyID)
}
func (p *partialFailStore) DeleteAlias(aliasName string) error {
	return p.inner.DeleteAlias(aliasName)
}
func (p *partialFailStore) UpdateAlias(aliasName, targetKeyID string) error {
	return p.inner.UpdateAlias(aliasName, targetKeyID)
}
func (p *partialFailStore) ListAliases(filterKeyID string) ([]AliasEntry, error) {
	return p.inner.ListAliases(filterKeyID)
}
func (p *partialFailStore) ResolveAlias(aliasName string) (string, error) {
	return p.inner.ResolveAlias(aliasName)
}

func TestLoadSymmetricMaterial_storageError(t *testing.T) {
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	realRouter := NewRouter(s)
	keyID := mustCreateKey(t, realRouter, `{}`)

	ro := newRouterWithRand(&partialFailStore{inner: s}, s.randRead)
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

// ---- looksLikeUUID ----------------------------------------------------------

func TestLooksLikeUUID(t *testing.T) {
	assert.True(t, looksLikeUUID("12345678-1234-1234-1234-123456789abc"))
	assert.False(t, looksLikeUUID("short"))                                // len != 36
	assert.False(t, looksLikeUUID("12345678x1234-1234-1234-123456789abc")) // wrong hyphen pos
	assert.False(t, looksLikeUUID("1234567g-1234-1234-1234-123456789abc")) // non-hex char
	assert.False(t, looksLikeUUID("12345678-1234-1234-1234-123456789ABC")) // uppercase rejected
}

// ---- Decrypt: garbage embedded key ID (looksLikeUUID gate) ------------------

func TestHandleDecrypt_garbageEmbeddedKeyID(t *testing.T) {
	ro := newTestRouter(t)
	// Build a blob with valid length and version byte but all-zero key ID bytes
	// (NUL bytes are not valid UUID hex chars, so looksLikeUUID returns false).
	blob := make([]byte, envelopeSealedOffset+1)
	blob[0] = envelopeVersion
	body, _ := json.Marshal(map[string]any{"CiphertextBlob": blob})
	w := kmsReq(t, ro, "Decrypt", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, "InvalidCiphertextException")
}

// ---- Decrypt: loadSymmetricMaterial failure path ----------------------------

func TestHandleDecrypt_loadMaterialFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	ro := NewRouter(s)

	keyID := mustCreateKey(t, ro, `{}`)
	cipherBlob := mustEncrypt(t, ro, keyID, []byte("data"))

	// Remove material.json so loadSymmetricMaterial returns ErrKeyMaterialNotFound.
	require.NoError(t, os.Remove(filepath.Join(dir, "kms", "keys", keyID, "material.json")))

	body, _ := json.Marshal(map[string]any{"CiphertextBlob": cipherBlob})
	w := kmsReq(t, ro, "Decrypt", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, "KMSInvalidStateException")
}

// ---- alias operations -------------------------------------------------------

// aliasFailStore wraps a store with overridable alias and key-metadata methods.
type aliasFailStore struct {
	inner          store
	createAlias    func(string, string) error
	deleteAlias    func(string) error
	updateAlias    func(string, string) error
	listAliases    func(string) ([]AliasEntry, error)
	resolveAlias   func(string) (string, error)
	getKeyMetadata func(string) (KeyMetadata, error)
}

func (a *aliasFailStore) CreateKey(in CreateKeyInput) (KeyMetadata, error) {
	return a.inner.CreateKey(in)
}
func (a *aliasFailStore) GetKeyMetadata(keyID string) (KeyMetadata, error) {
	if a.getKeyMetadata != nil {
		return a.getKeyMetadata(keyID)
	}
	return a.inner.GetKeyMetadata(keyID)
}
func (a *aliasFailStore) ListKeyIDs() ([]string, error) { return a.inner.ListKeyIDs() }
func (a *aliasFailStore) GetKeyPolicy(keyID string) (string, error) {
	return a.inner.GetKeyPolicy(keyID)
}
func (a *aliasFailStore) PutKeyPolicy(keyID, policy string) error {
	return a.inner.PutKeyPolicy(keyID, policy)
}
func (a *aliasFailStore) GetKeyMaterial(keyID string) (KeyMaterial, error) {
	return a.inner.GetKeyMaterial(keyID)
}
func (a *aliasFailStore) CreateAlias(aliasName, targetKeyID string) error {
	if a.createAlias != nil {
		return a.createAlias(aliasName, targetKeyID)
	}
	return a.inner.CreateAlias(aliasName, targetKeyID)
}
func (a *aliasFailStore) DeleteAlias(aliasName string) error {
	if a.deleteAlias != nil {
		return a.deleteAlias(aliasName)
	}
	return a.inner.DeleteAlias(aliasName)
}
func (a *aliasFailStore) UpdateAlias(aliasName, targetKeyID string) error {
	if a.updateAlias != nil {
		return a.updateAlias(aliasName, targetKeyID)
	}
	return a.inner.UpdateAlias(aliasName, targetKeyID)
}
func (a *aliasFailStore) ListAliases(filterKeyID string) ([]AliasEntry, error) {
	if a.listAliases != nil {
		return a.listAliases(filterKeyID)
	}
	return a.inner.ListAliases(filterKeyID)
}
func (a *aliasFailStore) ResolveAlias(aliasName string) (string, error) {
	if a.resolveAlias != nil {
		return a.resolveAlias(aliasName)
	}
	return a.inner.ResolveAlias(aliasName)
}

// makeAliasRouter creates a Router backed by aliasFailStore with a real storage as inner.
func makeAliasRouter(t *testing.T, fs *aliasFailStore) (*Router, *Storage) {
	t.Helper()
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	if fs.inner == nil {
		fs.inner = s
	}
	return NewRouter(fs), s
}

func mustCreateAlias(t *testing.T, ro *Router, aliasName, targetKeyID string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"AliasName": aliasName, "TargetKeyId": targetKeyID})
	w := kmsReq(t, ro, "CreateAlias", string(body))
	require.Equal(t, http.StatusOK, w.Code, "CreateAlias should succeed")
}

// ---- resolveKeyRef ----------------------------------------------------------

func TestResolveKeyRef_aliasResolution(t *testing.T) {
	t.Run("200 DescribeKey via alias name", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", keyID)
		w := kmsReq(t, ro, "DescribeKey", `{"KeyId":"alias/my-key"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, keyID, resp["KeyMetadata"].(map[string]any)["KeyId"])
	})

	t.Run("200 DescribeKey via alias ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", keyID)
		aliasArn := aliasARN("alias/my-key")
		w := kmsReq(t, ro, "DescribeKey", `{"KeyId":"`+aliasArn+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, keyID, resp["KeyMetadata"].(map[string]any)["KeyId"])
	})

	t.Run("400 invalid alias ARN empty name", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DescribeKey", `{"KeyId":"arn:aws:kms:us-east-1:123456789:alias/"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})

	t.Run("500 ResolveAlias storage error", func(t *testing.T) {
		fs := &aliasFailStore{
			resolveAlias: func(string) (string, error) { return "", errors.New("storage failure") },
		}
		ro, _ := makeAliasRouter(t, fs)
		w := kmsReq(t, ro, "DescribeKey", `{"KeyId":"alias/my-key"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- CreateAlias ------------------------------------------------------------

func TestHandleCreateAlias(t *testing.T) {
	t.Run("200 valid", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", keyID)
	})

	t.Run("200 accepts key ARN as TargetKeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/by-arn",
			"TargetKeyId": keyARN(keyID),
		})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CreateAlias", `{bad`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 empty AliasName", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"AliasName": "", "TargetKeyId": keyID})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidAliasNameException")
	})

	t.Run("400 invalid alias name no prefix", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"AliasName": "noprefix", "TargetKeyId": keyID})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidAliasNameException")
	})

	t.Run("400 alias/aws/ prefix reserved", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/aws/s3", "TargetKeyId": keyID})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidAliasNameException")
	})

	t.Run("400 empty TargetKeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": ""})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 alias ref as TargetKeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/my-key",
			"TargetKeyId": "alias/other",
		})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})

	t.Run("400 malformed ARN TargetKeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/my-key",
			"TargetKeyId": "arn:aws:kms:us-east-1:123:garbage",
		})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})

	t.Run("400 target key not found", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/my-key",
			"TargetKeyId": "00000000-0000-0000-0000-000000000000",
		})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 alias already exists", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", keyID)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": keyID})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "AlreadyExistsException")
	})

	t.Run("400 alias limit exceeded", func(t *testing.T) {
		fs := &aliasFailStore{
			createAlias: func(_, _ string) error { return ErrAliasLimitExceeded },
		}
		ro, _ := makeAliasRouter(t, fs)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/my-key",
			"TargetKeyId": "00000000-0000-0000-0000-000000000001",
		})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "LimitExceededException")
	})

	t.Run("500 storage error", func(t *testing.T) {
		fs := &aliasFailStore{
			createAlias: func(_, _ string) error { return errors.New("storage failure") },
		}
		ro, _ := makeAliasRouter(t, fs)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/my-key",
			"TargetKeyId": "00000000-0000-0000-0000-000000000001",
		})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- DeleteAlias ------------------------------------------------------------

func TestHandleDeleteAlias(t *testing.T) {
	t.Run("200 valid", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", keyID)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key"})
		w := kmsReq(t, ro, "DeleteAlias", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DeleteAlias", `{bad`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 empty AliasName", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"AliasName": ""})
		w := kmsReq(t, ro, "DeleteAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/nonexistent"})
		w := kmsReq(t, ro, "DeleteAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 storage error", func(t *testing.T) {
		fs := &aliasFailStore{
			deleteAlias: func(string) error { return errors.New("storage failure") },
		}
		ro, _ := makeAliasRouter(t, fs)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key"})
		w := kmsReq(t, ro, "DeleteAlias", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- UpdateAlias ------------------------------------------------------------

func TestHandleUpdateAlias(t *testing.T) {
	t.Run("200 valid same spec and usage", func(t *testing.T) {
		ro := newTestRouter(t)
		key1 := mustCreateKey(t, ro, `{}`)
		key2 := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", key1)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": key2})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "UpdateAlias", `{bad`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 invalid AliasName", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(
			map[string]any{
				"AliasName":   "noprefix",
				"TargetKeyId": "00000000-0000-0000-0000-000000000001",
			},
		)
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidAliasNameException")
	})

	t.Run("400 alias/aws/ prefix rejected", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/aws/s3",
			"TargetKeyId": "00000000-0000-0000-0000-000000000001",
		})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidAliasNameException")
	})

	t.Run("400 empty TargetKeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": ""})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 malformed ARN TargetKeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/my-key",
			"TargetKeyId": "arn:aws:kms:us-east-1:123:garbage",
		})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})

	t.Run("400 alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/nonexistent",
			"TargetKeyId": keyID,
		})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 ResolveAlias storage error", func(t *testing.T) {
		fs := &aliasFailStore{
			resolveAlias: func(string) (string, error) { return "", errors.New("storage failure") },
		}
		ro, _ := makeAliasRouter(t, fs)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/my-key",
			"TargetKeyId": "00000000-0000-0000-0000-000000000001",
		})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})

	t.Run("400 old key not found", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		realRo := NewRouter(s)
		key1 := mustCreateKey(t, realRo, `{}`)
		mustCreateAlias(t, realRo, "alias/my-key", key1)

		fs := &aliasFailStore{
			inner:          s,
			getKeyMetadata: func(string) (KeyMetadata, error) { return KeyMetadata{}, ErrKeyNotFound },
		}
		ro := NewRouter(fs)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": key1})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 old key metadata storage error", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		realRo := NewRouter(s)
		key1 := mustCreateKey(t, realRo, `{}`)
		mustCreateAlias(t, realRo, "alias/my-key", key1)

		fs := &aliasFailStore{
			inner:          s,
			getKeyMetadata: func(string) (KeyMetadata, error) { return KeyMetadata{}, errors.New("storage failure") },
		}
		ro := NewRouter(fs)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": key1})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})

	t.Run("400 new key not found", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		realRo := NewRouter(s)
		key1 := mustCreateKey(t, realRo, `{}`)
		mustCreateAlias(t, realRo, "alias/my-key", key1)

		calls := 0
		fs := &aliasFailStore{
			inner: s,
			getKeyMetadata: func(keyID string) (KeyMetadata, error) {
				calls++
				if calls == 2 {
					return KeyMetadata{}, ErrKeyNotFound
				}
				return s.GetKeyMetadata(keyID)
			},
		}
		ro := NewRouter(fs)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": key1})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 new key metadata storage error", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		realRo := NewRouter(s)
		key1 := mustCreateKey(t, realRo, `{}`)
		mustCreateAlias(t, realRo, "alias/my-key", key1)

		calls := 0
		fs := &aliasFailStore{
			inner: s,
			getKeyMetadata: func(keyID string) (KeyMetadata, error) {
				calls++
				if calls == 2 {
					return KeyMetadata{}, errors.New("storage failure")
				}
				return s.GetKeyMetadata(keyID)
			},
		}
		ro := NewRouter(fs)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": key1})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})

	t.Run("400 incompatible KeySpec and KeyUsage", func(t *testing.T) {
		ro := newTestRouter(t)
		sym := mustCreateKey(t, ro, `{}`)
		rsa := mustCreateKey(t, ro, `{"KeySpec":"RSA_2048","KeyUsage":"SIGN_VERIFY"}`)
		mustCreateAlias(t, ro, "alias/my-key", sym)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": rsa})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 UpdateAlias returns alias not found", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		realRo := NewRouter(s)
		key1 := mustCreateKey(t, realRo, `{}`)
		mustCreateAlias(t, realRo, "alias/my-key", key1)

		fs := &aliasFailStore{
			inner:       s,
			updateAlias: func(_, _ string) error { return ErrAliasNotFound },
		}
		ro := NewRouter(fs)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": key1})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 UpdateAlias returns key not found", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		realRo := NewRouter(s)
		key1 := mustCreateKey(t, realRo, `{}`)
		mustCreateAlias(t, realRo, "alias/my-key", key1)

		fs := &aliasFailStore{
			inner:       s,
			updateAlias: func(_, _ string) error { return ErrKeyNotFound },
		}
		ro := NewRouter(fs)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": key1})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 UpdateAlias storage error", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		realRo := NewRouter(s)
		key1 := mustCreateKey(t, realRo, `{}`)
		mustCreateAlias(t, realRo, "alias/my-key", key1)

		fs := &aliasFailStore{
			inner:       s,
			updateAlias: func(_, _ string) error { return errors.New("storage failure") },
		}
		ro := NewRouter(fs)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": key1})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- ListAliases ------------------------------------------------------------

func TestHandleListAliases(t *testing.T) {
	t.Run("200 empty list", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListAliases", `{}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Empty(t, resp["Aliases"].([]any))
		assert.Equal(t, false, resp["Truncated"])
	})

	t.Run("200 with aliases", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/a", keyID)
		mustCreateAlias(t, ro, "alias/b", keyID)
		w := kmsReq(t, ro, "ListAliases", `{}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Len(t, resp["Aliases"].([]any), 2)
	})

	t.Run("200 with KeyId filter", func(t *testing.T) {
		ro := newTestRouter(t)
		key1 := mustCreateKey(t, ro, `{}`)
		key2 := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/for-key1", key1)
		mustCreateAlias(t, ro, "alias/for-key2", key2)
		body, _ := json.Marshal(map[string]any{"KeyId": key1})
		w := kmsReq(t, ro, "ListAliases", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		aliases := resp["Aliases"].([]any)
		require.Len(t, aliases, 1)
		assert.Equal(t, "alias/for-key1", aliases[0].(map[string]any)["AliasName"])
	})

	t.Run("400 invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListAliases", `{bad`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 Limit too small", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"Limit": 0})
		w := kmsReq(t, ro, "ListAliases", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 Limit too large", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"Limit": 101})
		w := kmsReq(t, ro, "ListAliases", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 invalid ARN for KeyId filter", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"KeyId": "arn:aws:kms:us-east-1:123:garbage"})
		w := kmsReq(t, ro, "ListAliases", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})

	t.Run("400 key not found for KeyId filter", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"KeyId": "00000000-0000-0000-0000-000000000000"})
		w := kmsReq(t, ro, "ListAliases", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 GetKeyMetadata storage error", func(t *testing.T) {
		fs := &aliasFailStore{
			getKeyMetadata: func(string) (KeyMetadata, error) { return KeyMetadata{}, errors.New("storage failure") },
		}
		ro, _ := makeAliasRouter(t, fs)
		body, _ := json.Marshal(map[string]any{"KeyId": "00000000-0000-0000-0000-000000000000"})
		w := kmsReq(t, ro, "ListAliases", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})

	t.Run("500 ListAliases storage error", func(t *testing.T) {
		fs := &aliasFailStore{
			listAliases: func(string) ([]AliasEntry, error) { return nil, errors.New("storage failure") },
		}
		ro, _ := makeAliasRouter(t, fs)
		w := kmsReq(t, ro, "ListAliases", `{}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})

	t.Run("200 pagination with Limit and NextMarker", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		for _, name := range []string{"alias/a", "alias/b", "alias/c"} {
			mustCreateAlias(t, ro, name, keyID)
		}

		// First page: limit=2.
		body, _ := json.Marshal(map[string]any{"Limit": 2})
		w := kmsReq(t, ro, "ListAliases", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, true, resp["Truncated"])
		nextMarker := resp["NextMarker"].(string)
		assert.Equal(t, "alias/b", nextMarker)

		// Second page: use NextMarker.
		body, _ = json.Marshal(map[string]any{"Marker": nextMarker})
		w = kmsReq(t, ro, "ListAliases", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, false, resp["Truncated"])
		aliases := resp["Aliases"].([]any)
		require.Len(t, aliases, 1)
		assert.Equal(t, "alias/c", aliases[0].(map[string]any)["AliasName"])
	})

	t.Run("200 Marker binary search fallback", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/a", keyID)
		mustCreateAlias(t, ro, "alias/c", keyID)

		// "alias/b" doesn't exist; binary search positions to "alias/c".
		body, _ := json.Marshal(map[string]any{"Marker": "alias/b"})
		w := kmsReq(t, ro, "ListAliases", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		aliases := resp["Aliases"].([]any)
		require.Len(t, aliases, 1)
		assert.Equal(t, "alias/c", aliases[0].(map[string]any)["AliasName"])
	})

	t.Run("200 Marker beyond all aliases returns empty", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/a", keyID)

		body, _ := json.Marshal(map[string]any{"Marker": "zzz"})
		w := kmsReq(t, ro, "ListAliases", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Empty(t, resp["Aliases"].([]any))
	})
}
