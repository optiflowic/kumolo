package kms

import (
	"crypto/sha256"
	"encoding/base64"
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

	t.Run("400 description exceeds 8192 chars", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"Description": strings.Repeat("x", 8193)})
		w := kmsReq(t, ro, "CreateKey", string(body))
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

	t.Run("400 invalid marker format", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListKeys", `{"Marker":"not-a-uuid"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidMarkerException")
	})

	t.Run("stale marker triggers binary search", func(t *testing.T) {
		ro := newTestRouter(t)
		mustCreateKey(t, ro, `{}`)
		mustCreateKey(t, ro, `{}`)
		// All-zeros UUID is a valid UUID format but will never exist; it sorts before random UUIDs,
		// so binary search sets start=0 and all keys are returned.
		w := kmsReq(t, ro, "ListKeys", `{"Marker":"00000000-0000-0000-0000-000000000000"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Len(t, resp["Keys"].([]any), 2)
	})
}

func TestHandleListResourceTags(t *testing.T) {
	t.Run("returns empty tags for existing key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "ListResourceTags", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, []any{}, resp["Tags"])
		assert.Equal(t, false, resp["Truncated"])
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListResourceTags", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 when KeyId is missing", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListResourceTags", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for Limit less than 1", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListResourceTags", `{"KeyId":"some-id","Limit":0}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for Limit greater than 1000", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListResourceTags", `{"KeyId":"some-id","Limit":1001}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-empty Marker", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "ListResourceTags", `{"KeyId":"`+keyID+`","Marker":"some-marker"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidMarkerException")
	})

	t.Run("400 for invalid ARN format", func(t *testing.T) {
		ro := newTestRouter(t)
		// ARN prefix without ":key/" makes resolveKeyRef return !ok.
		w := kmsReq(t, ro, "ListResourceTags",
			`{"KeyId":"arn:aws:kms:us-east-1:123456789012:invalid"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})

	t.Run("400 for unknown key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListResourceTags",
			`{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		w := kmsReq(t, ro, "ListResourceTags",
			`{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
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

	t.Run("400 for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "ScheduleKeyDeletion",
			`{"KeyId":"`+keyID+`","PendingWindowInDays":7}`)
		require.Equal(t, http.StatusOK, w.Code)
		w2 := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w2.Code)
		assertErrType(t, w2, "KMSInvalidStateException")
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

	t.Run("400 for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "ScheduleKeyDeletion",
			`{"KeyId":"`+keyID+`","PendingWindowInDays":7}`)
		require.Equal(t, http.StatusOK, w.Code)
		w2 := kmsReq(t, ro, "PutKeyPolicy", `{"KeyId":"`+keyID+`","Policy":"{}"}`)
		assert.Equal(t, http.StatusBadRequest, w2.Code)
		assertErrType(t, w2, "KMSInvalidStateException")
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
func (a *alwaysFailStore) EnableKey(string) error  { return errors.New("storage error") }
func (a *alwaysFailStore) DisableKey(string) error { return errors.New("storage error") }
func (a *alwaysFailStore) ScheduleKeyDeletion(string, int) (KeyMetadata, error) {
	return KeyMetadata{}, errors.New("storage error")
}
func (a *alwaysFailStore) CancelKeyDeletion(string) (KeyMetadata, error) {
	return KeyMetadata{}, errors.New("storage error")
}

func (a *alwaysFailStore) EnableKeyRotation(
	string,
	int,
) error {
	return errors.New("storage error")
}

func (a *alwaysFailStore) DisableKeyRotation(
	string,
) error {
	return errors.New("storage error")
}
func (a *alwaysFailStore) GetKeyRotationStatus(string) (KeyMetadata, KeyRotationConfig, error) {
	return KeyMetadata{}, KeyRotationConfig{}, errors.New("storage error")
}
func (a *alwaysFailStore) GetTags(string) ([]TagEntry, error) {
	return nil, errors.New("storage error")
}

func (a *alwaysFailStore) TagResource(
	string,
	[]TagEntry,
) error {
	return errors.New("storage error")
}

func (a *alwaysFailStore) UntagResource(
	string,
	[]string,
) error {
	return errors.New("storage error")
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

func TestHandleGetKeyPolicy_policyStorageFailure(t *testing.T) {
	fs := &aliasFailStore{
		getKeyPolicy: func(string) (string, error) {
			return "", errors.New("policy storage error")
		},
	}
	ro, _ := makeAliasRouter(t, fs)
	keyID := mustCreateKey(t, ro, `{}`)
	w := kmsReq(t, ro, "GetKeyPolicy", `{"KeyId":"`+keyID+`"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, "KMSInternalException")
}

func TestHandlePutKeyPolicy_policyStorageFailure(t *testing.T) {
	fs := &aliasFailStore{
		putKeyPolicy: func(string, string) error {
			return errors.New("policy storage error")
		},
	}
	ro, _ := makeAliasRouter(t, fs)
	keyID := mustCreateKey(t, ro, `{}`)
	w := kmsReq(t, ro, "PutKeyPolicy", `{"KeyId":"`+keyID+`","Policy":"{}"}`)
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

	t.Run("400 EncryptionContext key exceeds 2048 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":             keyID,
			"Plaintext":         "aGVsbG8=",
			"EncryptionContext": map[string]string{strings.Repeat("k", 2049): "v"},
		})
		w := kmsReq(t, ro, "Encrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 EncryptionContext value exceeds 2048 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":             keyID,
			"Plaintext":         "aGVsbG8=",
			"EncryptionContext": map[string]string{"k": strings.Repeat("v", 2049)},
		})
		w := kmsReq(t, ro, "Encrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 EncryptionContext total size exceeds 8192 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":     keyID,
			"Plaintext": "aGVsbG8=",
			"EncryptionContext": map[string]string{
				strings.Repeat("a", 1000): strings.Repeat("v", 700),
				strings.Repeat("b", 1000): strings.Repeat("v", 700),
				strings.Repeat("c", 1000): strings.Repeat("v", 700),
				strings.Repeat("d", 1000): strings.Repeat("v", 700),
				strings.Repeat("e", 1000): strings.Repeat("v", 700),
			},
		})
		w := kmsReq(t, ro, "Encrypt", string(body))
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

	t.Run("400 EncryptionContext key exceeds 2048 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		encW := kmsReq(t, ro, "Encrypt", `{"KeyId":"`+keyID+`","Plaintext":"aGVsbG8="}`)
		require.Equal(t, http.StatusOK, encW.Code)
		var encResp map[string]any
		require.NoError(t, json.Unmarshal(encW.Body.Bytes(), &encResp))
		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":    encResp["CiphertextBlob"],
			"EncryptionContext": map[string]string{strings.Repeat("k", 2049): "v"},
		})
		w := kmsReq(t, ro, "Decrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 EncryptionContext value exceeds 2048 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		encW := kmsReq(t, ro, "Encrypt", `{"KeyId":"`+keyID+`","Plaintext":"aGVsbG8="}`)
		require.Equal(t, http.StatusOK, encW.Code)
		var encResp map[string]any
		require.NoError(t, json.Unmarshal(encW.Body.Bytes(), &encResp))
		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":    encResp["CiphertextBlob"],
			"EncryptionContext": map[string]string{"k": strings.Repeat("v", 2049)},
		})
		w := kmsReq(t, ro, "Decrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 EncryptionContext total size exceeds 8192 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		encW := kmsReq(t, ro, "Encrypt", `{"KeyId":"`+keyID+`","Plaintext":"aGVsbG8="}`)
		require.Equal(t, http.StatusOK, encW.Code)
		var encResp map[string]any
		require.NoError(t, json.Unmarshal(encW.Body.Bytes(), &encResp))
		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob": encResp["CiphertextBlob"],
			"EncryptionContext": map[string]string{
				strings.Repeat("a", 1000): strings.Repeat("v", 700),
				strings.Repeat("b", 1000): strings.Repeat("v", 700),
				strings.Repeat("c", 1000): strings.Repeat("v", 700),
				strings.Repeat("d", 1000): strings.Repeat("v", 700),
				strings.Repeat("e", 1000): strings.Repeat("v", 700),
			},
		})
		w := kmsReq(t, ro, "Decrypt", string(body))
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

	t.Run("400 EncryptionContext key exceeds 2048 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":             keyID,
			"KeySpec":           "AES_256",
			"EncryptionContext": map[string]string{strings.Repeat("k", 2049): "v"},
		})
		w := kmsReq(t, ro, "GenerateDataKey", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 EncryptionContext value exceeds 2048 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":             keyID,
			"KeySpec":           "AES_256",
			"EncryptionContext": map[string]string{"k": strings.Repeat("v", 2049)},
		})
		w := kmsReq(t, ro, "GenerateDataKey", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 EncryptionContext total size exceeds 8192 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":   keyID,
			"KeySpec": "AES_256",
			"EncryptionContext": map[string]string{
				strings.Repeat("a", 1000): strings.Repeat("v", 700),
				strings.Repeat("b", 1000): strings.Repeat("v", 700),
				strings.Repeat("c", 1000): strings.Repeat("v", 700),
				strings.Repeat("d", 1000): strings.Repeat("v", 700),
				strings.Repeat("e", 1000): strings.Repeat("v", 700),
			},
		})
		w := kmsReq(t, ro, "GenerateDataKey", string(body))
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
func (p *partialFailStore) EnableKey(keyID string) error  { return p.inner.EnableKey(keyID) }
func (p *partialFailStore) DisableKey(keyID string) error { return p.inner.DisableKey(keyID) }
func (p *partialFailStore) ScheduleKeyDeletion(keyID string, days int) (KeyMetadata, error) {
	return p.inner.ScheduleKeyDeletion(keyID, days)
}
func (p *partialFailStore) CancelKeyDeletion(keyID string) (KeyMetadata, error) {
	return p.inner.CancelKeyDeletion(keyID)
}
func (p *partialFailStore) EnableKeyRotation(keyID string, days int) error {
	return p.inner.EnableKeyRotation(keyID, days)
}
func (p *partialFailStore) DisableKeyRotation(keyID string) error {
	return p.inner.DisableKeyRotation(keyID)
}

func (p *partialFailStore) GetKeyRotationStatus(
	keyID string,
) (KeyMetadata, KeyRotationConfig, error) {
	return p.inner.GetKeyRotationStatus(keyID)
}
func (p *partialFailStore) GetTags(keyID string) ([]TagEntry, error) {
	return p.inner.GetTags(keyID)
}
func (p *partialFailStore) TagResource(keyID string, tags []TagEntry) error {
	return p.inner.TagResource(keyID, tags)
}
func (p *partialFailStore) UntagResource(keyID string, tagKeys []string) error {
	return p.inner.UntagResource(keyID, tagKeys)
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
	getKeyPolicy   func(string) (string, error)
	putKeyPolicy   func(string, string) error
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
	if a.getKeyPolicy != nil {
		return a.getKeyPolicy(keyID)
	}
	return a.inner.GetKeyPolicy(keyID)
}
func (a *aliasFailStore) PutKeyPolicy(keyID, policy string) error {
	if a.putKeyPolicy != nil {
		return a.putKeyPolicy(keyID, policy)
	}
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
func (a *aliasFailStore) EnableKey(keyID string) error  { return a.inner.EnableKey(keyID) }
func (a *aliasFailStore) DisableKey(keyID string) error { return a.inner.DisableKey(keyID) }
func (a *aliasFailStore) ScheduleKeyDeletion(keyID string, days int) (KeyMetadata, error) {
	return a.inner.ScheduleKeyDeletion(keyID, days)
}
func (a *aliasFailStore) CancelKeyDeletion(keyID string) (KeyMetadata, error) {
	return a.inner.CancelKeyDeletion(keyID)
}
func (a *aliasFailStore) EnableKeyRotation(keyID string, days int) error {
	return a.inner.EnableKeyRotation(keyID, days)
}
func (a *aliasFailStore) DisableKeyRotation(keyID string) error {
	return a.inner.DisableKeyRotation(keyID)
}

func (a *aliasFailStore) GetKeyRotationStatus(
	keyID string,
) (KeyMetadata, KeyRotationConfig, error) {
	return a.inner.GetKeyRotationStatus(keyID)
}
func (a *aliasFailStore) GetTags(keyID string) ([]TagEntry, error) {
	return a.inner.GetTags(keyID)
}
func (a *aliasFailStore) TagResource(keyID string, tags []TagEntry) error {
	return a.inner.TagResource(keyID, tags)
}
func (a *aliasFailStore) UntagResource(keyID string, tagKeys []string) error {
	return a.inner.UntagResource(keyID, tagKeys)
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

	t.Run("400 keyId exceeds 2048 chars", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"KeyId": strings.Repeat("x", 2049)})
		w := kmsReq(t, ro, "DescribeKey", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
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

	t.Run("400 aliasName exceeds 256 chars", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/" + strings.Repeat("x", 251),
			"TargetKeyId": keyID,
		})
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

	t.Run("400 target key pending deletion", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "PendingWindowInDays": 7})
		require.Equal(t, http.StatusOK, kmsReq(t, ro, "ScheduleKeyDeletion", string(body)).Code)
		body, _ = json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": keyID})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 key not found at CreateAlias call", func(t *testing.T) {
		fs := &aliasFailStore{
			createAlias: func(_, _ string) error { return ErrKeyNotFound },
		}
		ro, _ := makeAliasRouter(t, fs)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/my-key",
			"TargetKeyId": keyID,
		})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 key pending deletion returned from storage", func(t *testing.T) {
		fs := &aliasFailStore{
			createAlias: func(_, _ string) error { return ErrKeyPendingDeletion },
		}
		ro, _ := makeAliasRouter(t, fs)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/my-key",
			"TargetKeyId": keyID,
		})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 alias limit exceeded", func(t *testing.T) {
		fs := &aliasFailStore{
			createAlias: func(_, _ string) error { return ErrAliasLimitExceeded },
		}
		ro, _ := makeAliasRouter(t, fs)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/my-key",
			"TargetKeyId": keyID,
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
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"AliasName":   "alias/my-key",
			"TargetKeyId": keyID,
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
		assertErrType(t, w, "InvalidAliasNameException")
	})

	t.Run("400 alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/nonexistent"})
		w := kmsReq(t, ro, "DeleteAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("200 target key pending deletion", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", keyID)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "PendingWindowInDays": 7})
		require.Equal(t, http.StatusOK, kmsReq(t, ro, "ScheduleKeyDeletion", string(body)).Code)
		body, _ = json.Marshal(map[string]any{"AliasName": "alias/my-key"})
		w := kmsReq(t, ro, "DeleteAlias", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("500 storage error", func(t *testing.T) {
		fs := &aliasFailStore{
			deleteAlias: func(string) error { return errors.New("storage failure") },
		}
		ro, _ := makeAliasRouter(t, fs)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", keyID)
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

	t.Run("200 old key pending deletion", func(t *testing.T) {
		ro := newTestRouter(t)
		key1 := mustCreateKey(t, ro, `{}`)
		key2 := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", key1)
		body, _ := json.Marshal(map[string]any{"KeyId": key1, "PendingWindowInDays": 7})
		require.Equal(t, http.StatusOK, kmsReq(t, ro, "ScheduleKeyDeletion", string(body)).Code)
		body, _ = json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": key2})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 new key pending deletion", func(t *testing.T) {
		ro := newTestRouter(t)
		key1 := mustCreateKey(t, ro, `{}`)
		key2 := mustCreateKey(t, ro, `{}`)
		mustCreateAlias(t, ro, "alias/my-key", key1)
		body, _ := json.Marshal(map[string]any{"KeyId": key2, "PendingWindowInDays": 7})
		require.Equal(t, http.StatusOK, kmsReq(t, ro, "ScheduleKeyDeletion", string(body)).Code)
		body, _ = json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": key2})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
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

	t.Run("400 UpdateAlias returns pending deletion", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		realRo := NewRouter(s)
		key1 := mustCreateKey(t, realRo, `{}`)
		mustCreateAlias(t, realRo, "alias/my-key", key1)

		fs := &aliasFailStore{
			inner:       s,
			updateAlias: func(_, _ string) error { return ErrKeyPendingDeletion },
		}
		ro := NewRouter(fs)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/my-key", "TargetKeyId": key1})
		w := kmsReq(t, ro, "UpdateAlias", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
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

	t.Run("400 invalid marker format", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"Marker": "not-an-alias"})
		w := kmsReq(t, ro, "ListAliases", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidMarkerException")
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

		body, _ := json.Marshal(map[string]any{"Marker": "alias/zzz"})
		w := kmsReq(t, ro, "ListAliases", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Empty(t, resp["Aliases"].([]any))
	})
}

func mustEncryptForReEncrypt(t *testing.T, ro *Router, keyID string, plaintext []byte) []byte {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Plaintext": plaintext})
	w := kmsReq(t, ro, "Encrypt", string(body))
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	got, _ := json.Marshal(resp["CiphertextBlob"])
	var blob []byte
	require.NoError(t, json.Unmarshal(got, &blob))
	return blob
}

func TestHandleReEncrypt(t *testing.T) {
	t.Run("roundtrip: same key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		plaintext := []byte("hello reencrypt")
		ciphertext := mustEncryptForReEncrypt(t, ro, keyID, plaintext)

		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":   ciphertext,
			"DestinationKeyId": keyID,
		})
		w := kmsReq(t, ro, "ReEncrypt", string(body))
		require.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "SYMMETRIC_DEFAULT", resp["SourceEncryptionAlgorithm"])
		assert.Equal(t, "SYMMETRIC_DEFAULT", resp["DestinationEncryptionAlgorithm"])
		assert.NotEmpty(t, resp["KeyId"])
		assert.NotEmpty(t, resp["SourceKeyId"])
		assert.NotEmpty(t, resp["SourceKeyMaterialId"])
		assert.NotEmpty(t, resp["DestinationKeyMaterialId"])

		// Decrypt the re-encrypted ciphertext and verify plaintext.
		got, _ := json.Marshal(resp["CiphertextBlob"])
		var newBlob []byte
		require.NoError(t, json.Unmarshal(got, &newBlob))
		decBody, _ := json.Marshal(map[string]any{"KeyId": keyID, "CiphertextBlob": newBlob})
		dw := kmsReq(t, ro, "Decrypt", string(decBody))
		require.Equal(t, http.StatusOK, dw.Code)
		var dResp map[string]any
		require.NoError(t, json.Unmarshal(dw.Body.Bytes(), &dResp))
		gotBytes, _ := json.Marshal(dResp["Plaintext"])
		var decrypted []byte
		require.NoError(t, json.Unmarshal(gotBytes, &decrypted))
		assert.Equal(t, plaintext, decrypted)
	})

	t.Run("roundtrip: different key", func(t *testing.T) {
		ro := newTestRouter(t)
		srcKey := mustCreateKey(t, ro, `{}`)
		dstKey := mustCreateKey(t, ro, `{}`)
		plaintext := []byte("cross-key reencrypt")
		ciphertext := mustEncryptForReEncrypt(t, ro, srcKey, plaintext)

		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":   ciphertext,
			"DestinationKeyId": dstKey,
		})
		w := kmsReq(t, ro, "ReEncrypt", string(body))
		require.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		got, _ := json.Marshal(resp["CiphertextBlob"])
		var newBlob []byte
		require.NoError(t, json.Unmarshal(got, &newBlob))
		decBody, _ := json.Marshal(map[string]any{"KeyId": dstKey, "CiphertextBlob": newBlob})
		dw := kmsReq(t, ro, "Decrypt", string(decBody))
		require.Equal(t, http.StatusOK, dw.Code)
		var dResp map[string]any
		require.NoError(t, json.Unmarshal(dw.Body.Bytes(), &dResp))
		gotBytes, _ := json.Marshal(dResp["Plaintext"])
		var decrypted []byte
		require.NoError(t, json.Unmarshal(gotBytes, &decrypted))
		assert.Equal(t, plaintext, decrypted)
	})

	t.Run("roundtrip: with encryption contexts", func(t *testing.T) {
		ro := newTestRouter(t)
		srcKey := mustCreateKey(t, ro, `{}`)
		dstKey := mustCreateKey(t, ro, `{}`)
		srcCtx := map[string]string{"src": "a"}
		dstCtx := map[string]string{"dst": "b"}
		encBody, _ := json.Marshal(map[string]any{
			"KeyId":             srcKey,
			"Plaintext":         []byte("ctx test"),
			"EncryptionContext": srcCtx,
		})
		ew := kmsReq(t, ro, "Encrypt", string(encBody))
		require.Equal(t, http.StatusOK, ew.Code)
		var eResp map[string]any
		require.NoError(t, json.Unmarshal(ew.Body.Bytes(), &eResp))
		gotBlob, _ := json.Marshal(eResp["CiphertextBlob"])
		var ciphertext []byte
		require.NoError(t, json.Unmarshal(gotBlob, &ciphertext))

		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":               ciphertext,
			"DestinationKeyId":             dstKey,
			"SourceEncryptionContext":      srcCtx,
			"DestinationEncryptionContext": dstCtx,
		})
		w := kmsReq(t, ro, "ReEncrypt", string(body))
		require.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		got, _ := json.Marshal(resp["CiphertextBlob"])
		var newBlob []byte
		require.NoError(t, json.Unmarshal(got, &newBlob))
		decBody, _ := json.Marshal(map[string]any{
			"KeyId":             dstKey,
			"CiphertextBlob":    newBlob,
			"EncryptionContext": dstCtx,
		})
		dw := kmsReq(t, ro, "Decrypt", string(decBody))
		require.Equal(t, http.StatusOK, dw.Code)
	})

	t.Run("with valid SourceKeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		srcKey := mustCreateKey(t, ro, `{}`)
		dstKey := mustCreateKey(t, ro, `{}`)
		ciphertext := mustEncryptForReEncrypt(t, ro, srcKey, []byte("data"))

		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":   ciphertext,
			"SourceKeyId":      srcKey,
			"DestinationKeyId": dstKey,
		})
		w := kmsReq(t, ro, "ReEncrypt", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("error: missing CiphertextBlob", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"DestinationKeyId": keyID})
		w := kmsReq(t, ro, "ReEncrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: missing DestinationKeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		ciphertext := mustEncryptForReEncrypt(t, ro, keyID, []byte("data"))
		body, _ := json.Marshal(map[string]any{"CiphertextBlob": ciphertext})
		w := kmsReq(t, ro, "ReEncrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: SourceKeyId mismatch", func(t *testing.T) {
		ro := newTestRouter(t)
		srcKey := mustCreateKey(t, ro, `{}`)
		otherKey := mustCreateKey(t, ro, `{}`)
		dstKey := mustCreateKey(t, ro, `{}`)
		ciphertext := mustEncryptForReEncrypt(t, ro, srcKey, []byte("data"))

		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":   ciphertext,
			"SourceKeyId":      otherKey,
			"DestinationKeyId": dstKey,
		})
		w := kmsReq(t, ro, "ReEncrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "IncorrectKeyException")
	})

	t.Run("error: malformed ciphertext", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":   []byte("not-a-valid-ciphertext"),
			"DestinationKeyId": keyID,
		})
		w := kmsReq(t, ro, "ReEncrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidCiphertextException")
	})

	t.Run("error: wrong source encryption context", func(t *testing.T) {
		ro := newTestRouter(t)
		srcKey := mustCreateKey(t, ro, `{}`)
		dstKey := mustCreateKey(t, ro, `{}`)
		encBody, _ := json.Marshal(map[string]any{
			"KeyId":             srcKey,
			"Plaintext":         []byte("data"),
			"EncryptionContext": map[string]string{"k": "v"},
		})
		ew := kmsReq(t, ro, "Encrypt", string(encBody))
		require.Equal(t, http.StatusOK, ew.Code)
		var eResp map[string]any
		require.NoError(t, json.Unmarshal(ew.Body.Bytes(), &eResp))
		gotBlob, _ := json.Marshal(eResp["CiphertextBlob"])
		var ciphertext []byte
		require.NoError(t, json.Unmarshal(gotBlob, &ciphertext))

		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":          ciphertext,
			"DestinationKeyId":        dstKey,
			"SourceEncryptionContext": map[string]string{"wrong": "ctx"},
		})
		w := kmsReq(t, ro, "ReEncrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidCiphertextException")
	})

	t.Run("error: unsupported source algorithm", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		ciphertext := mustEncryptForReEncrypt(t, ro, keyID, []byte("data"))
		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":            ciphertext,
			"DestinationKeyId":          keyID,
			"SourceEncryptionAlgorithm": "RSAES_OAEP_SHA_256",
		})
		w := kmsReq(t, ro, "ReEncrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("error: unsupported destination algorithm", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		ciphertext := mustEncryptForReEncrypt(t, ro, keyID, []byte("data"))
		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":                 ciphertext,
			"DestinationKeyId":               keyID,
			"DestinationEncryptionAlgorithm": "RSAES_OAEP_SHA_256",
		})
		w := kmsReq(t, ro, "ReEncrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("error: destination key not found", func(t *testing.T) {
		ro := newTestRouter(t)
		srcKey := mustCreateKey(t, ro, `{}`)
		ciphertext := mustEncryptForReEncrypt(t, ro, srcKey, []byte("data"))
		body, _ := json.Marshal(map[string]any{
			"CiphertextBlob":   ciphertext,
			"DestinationKeyId": "00000000-0000-0000-0000-000000000000",
		})
		w := kmsReq(t, ro, "ReEncrypt", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})
}

func TestHandleGenerateRandom(t *testing.T) {
	t.Run("returns random bytes of requested length", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GenerateRandom", `{"NumberOfBytes":32}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		plaintext, ok := resp["Plaintext"].(string)
		require.True(t, ok)
		decoded, err := base64.StdEncoding.DecodeString(plaintext)
		require.NoError(t, err)
		assert.Len(t, decoded, 32)
	})

	t.Run("boundary: 1 byte", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GenerateRandom", `{"NumberOfBytes":1}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		decoded, err := base64.StdEncoding.DecodeString(resp["Plaintext"].(string))
		require.NoError(t, err)
		assert.Len(t, decoded, 1)
	})

	t.Run("boundary: 1024 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GenerateRandom", `{"NumberOfBytes":1024}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		decoded, err := base64.StdEncoding.DecodeString(resp["Plaintext"].(string))
		require.NoError(t, err)
		assert.Len(t, decoded, 1024)
	})

	t.Run("error: NumberOfBytes missing", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GenerateRandom", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: NumberOfBytes zero", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GenerateRandom", `{"NumberOfBytes":0}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: NumberOfBytes exceeds 1024", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GenerateRandom", `{"NumberOfBytes":1025}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: CustomKeyStoreId provided", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GenerateRandom", `{"NumberOfBytes":32,"CustomKeyStoreId":"cks-123"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "UnsupportedOperationException")
	})

	t.Run("error: rand read failure", func(t *testing.T) {
		s, err := newStorage(t.TempDir(), os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ro := newRouterWithRand(s, func(b []byte) (int, error) {
			return 0, errors.New("entropy failure")
		})
		w := kmsReq(t, ro, "GenerateRandom", `{"NumberOfBytes":32}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// mustCreateHMACKey creates an HMAC key with the given spec and returns its key ID.
func mustCreateHMACKey(t *testing.T, ro *Router, spec string) string {
	t.Helper()
	return mustCreateKey(t, ro, `{"KeySpec":"`+spec+`","KeyUsage":"GENERATE_VERIFY_MAC"}`)
}

func TestHandleGenerateMac(t *testing.T) {
	specs := []struct {
		spec string
		algo string
	}{
		{"HMAC_224", "HMAC_SHA_224"},
		{"HMAC_256", "HMAC_SHA_256"},
		{"HMAC_384", "HMAC_SHA_384"},
		{"HMAC_512", "HMAC_SHA_512"},
	}

	for _, tc := range specs {
		t.Run("roundtrip "+tc.spec, func(t *testing.T) {
			ro := newTestRouter(t)
			keyID := mustCreateHMACKey(t, ro, tc.spec)
			msg := []byte("hello mac")

			genBody, _ := json.Marshal(map[string]any{
				"KeyId":        keyID,
				"MacAlgorithm": tc.algo,
				"Message":      msg,
			})
			gw := kmsReq(t, ro, "GenerateMac", string(genBody))
			require.Equal(t, http.StatusOK, gw.Code)

			var gResp map[string]any
			require.NoError(t, json.Unmarshal(gw.Body.Bytes(), &gResp))
			assert.Equal(t, tc.algo, gResp["MacAlgorithm"])
			assert.NotEmpty(t, gResp["KeyId"])

			// Decode the Mac for VerifyMac
			macRaw, _ := json.Marshal(gResp["Mac"])
			var macBytes []byte
			require.NoError(t, json.Unmarshal(macRaw, &macBytes))

			verBody, _ := json.Marshal(map[string]any{
				"KeyId":        keyID,
				"MacAlgorithm": tc.algo,
				"Message":      msg,
				"Mac":          macBytes,
			})
			vw := kmsReq(t, ro, "VerifyMac", string(verBody))
			require.Equal(t, http.StatusOK, vw.Code)
			var vResp map[string]any
			require.NoError(t, json.Unmarshal(vw.Body.Bytes(), &vResp))
			assert.Equal(t, true, vResp["MacValid"])
		})
	}

	t.Run("error: missing Message", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateHMACKey(t, ro, "HMAC_256")
		body, _ := json.Marshal(map[string]any{
			"KeyId":        keyID,
			"MacAlgorithm": "HMAC_SHA_256",
		})
		w := kmsReq(t, ro, "GenerateMac", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: missing MacAlgorithm", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateHMACKey(t, ro, "HMAC_256")
		body, _ := json.Marshal(map[string]any{
			"KeyId":   keyID,
			"Message": []byte("hello"),
		})
		w := kmsReq(t, ro, "GenerateMac", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: invalid MacAlgorithm", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateHMACKey(t, ro, "HMAC_256")
		body, _ := json.Marshal(map[string]any{
			"KeyId":        keyID,
			"MacAlgorithm": "INVALID",
			"Message":      []byte("hello"),
		})
		w := kmsReq(t, ro, "GenerateMac", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: algorithm not compatible with key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateHMACKey(t, ro, "HMAC_256")
		body, _ := json.Marshal(map[string]any{
			"KeyId":        keyID,
			"MacAlgorithm": "HMAC_SHA_512",
			"Message":      []byte("hello"),
		})
		w := kmsReq(t, ro, "GenerateMac", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("error: ENCRYPT_DECRYPT key not allowed", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":        keyID,
			"MacAlgorithm": "HMAC_SHA_256",
			"Message":      []byte("hello"),
		})
		w := kmsReq(t, ro, "GenerateMac", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("error: Message exceeds 4096 bytes", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateHMACKey(t, ro, "HMAC_256")
		big := make([]byte, 4097)
		body, _ := json.Marshal(map[string]any{
			"KeyId":        keyID,
			"MacAlgorithm": "HMAC_SHA_256",
			"Message":      big,
		})
		w := kmsReq(t, ro, "GenerateMac", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})
}

func TestHandleVerifyMac(t *testing.T) {
	t.Run("error: tampered Mac", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateHMACKey(t, ro, "HMAC_256")
		msg := []byte("authentic message")

		genBody, _ := json.Marshal(map[string]any{
			"KeyId":        keyID,
			"MacAlgorithm": "HMAC_SHA_256",
			"Message":      msg,
		})
		gw := kmsReq(t, ro, "GenerateMac", string(genBody))
		require.Equal(t, http.StatusOK, gw.Code)
		var gResp map[string]any
		require.NoError(t, json.Unmarshal(gw.Body.Bytes(), &gResp))
		macRaw, _ := json.Marshal(gResp["Mac"])
		var macBytes []byte
		require.NoError(t, json.Unmarshal(macRaw, &macBytes))
		macBytes[0] ^= 0xFF // tamper

		verBody, _ := json.Marshal(map[string]any{
			"KeyId":        keyID,
			"MacAlgorithm": "HMAC_SHA_256",
			"Message":      msg,
			"Mac":          macBytes,
		})
		vw := kmsReq(t, ro, "VerifyMac", string(verBody))
		assert.Equal(t, http.StatusBadRequest, vw.Code)
		assertErrType(t, vw, "KMSInvalidMacException")
	})

	t.Run("error: wrong message", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateHMACKey(t, ro, "HMAC_256")
		genBody, _ := json.Marshal(map[string]any{
			"KeyId":        keyID,
			"MacAlgorithm": "HMAC_SHA_256",
			"Message":      []byte("original"),
		})
		gw := kmsReq(t, ro, "GenerateMac", string(genBody))
		require.Equal(t, http.StatusOK, gw.Code)
		var gResp map[string]any
		require.NoError(t, json.Unmarshal(gw.Body.Bytes(), &gResp))
		macRaw, _ := json.Marshal(gResp["Mac"])
		var macBytes []byte
		require.NoError(t, json.Unmarshal(macRaw, &macBytes))

		verBody, _ := json.Marshal(map[string]any{
			"KeyId":        keyID,
			"MacAlgorithm": "HMAC_SHA_256",
			"Message":      []byte("tampered"),
			"Mac":          macBytes,
		})
		vw := kmsReq(t, ro, "VerifyMac", string(verBody))
		assert.Equal(t, http.StatusBadRequest, vw.Code)
		assertErrType(t, vw, "KMSInvalidMacException")
	})

	t.Run("error: missing Mac", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateHMACKey(t, ro, "HMAC_256")
		body, _ := json.Marshal(map[string]any{
			"KeyId":        keyID,
			"MacAlgorithm": "HMAC_SHA_256",
			"Message":      []byte("hello"),
		})
		w := kmsReq(t, ro, "VerifyMac", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: key disabled", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateHMACKey(t, ro, "HMAC_256")
		msg := []byte("msg")
		genBody, _ := json.Marshal(map[string]any{
			"KeyId":        keyID,
			"MacAlgorithm": "HMAC_SHA_256",
			"Message":      msg,
		})
		gw := kmsReq(t, ro, "GenerateMac", string(genBody))
		require.Equal(t, http.StatusOK, gw.Code)
		var gResp map[string]any
		require.NoError(t, json.Unmarshal(gw.Body.Bytes(), &gResp))
		macRaw, _ := json.Marshal(gResp["Mac"])
		var macBytes []byte
		require.NoError(t, json.Unmarshal(macRaw, &macBytes))

		dw := kmsReq(t, ro, "DisableKey", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, dw.Code)

		verBody, _ := json.Marshal(map[string]any{
			"KeyId":        keyID,
			"MacAlgorithm": "HMAC_SHA_256",
			"Message":      msg,
			"Mac":          macBytes,
		})
		vw := kmsReq(t, ro, "VerifyMac", string(verBody))
		assert.Equal(t, http.StatusBadRequest, vw.Code)
		assertErrType(t, vw, "DisabledException")
	})
}

// mustCreateSignVerifyKey creates a SIGN_VERIFY key with the given spec.
func mustCreateSignVerifyKey(t *testing.T, ro *Router, spec string) string {
	t.Helper()
	return mustCreateKey(t, ro, `{"KeySpec":"`+spec+`","KeyUsage":"SIGN_VERIFY"}`)
}

func TestHandleSign(t *testing.T) {
	type algoCase struct {
		spec string
		algo string
	}
	cases := []algoCase{
		{"RSA_2048", "RSASSA_PSS_SHA_256"},
		{"RSA_2048", "RSASSA_PSS_SHA_384"},
		{"RSA_2048", "RSASSA_PSS_SHA_512"},
		{"RSA_2048", "RSASSA_PKCS1_V1_5_SHA_256"},
		{"RSA_2048", "RSASSA_PKCS1_V1_5_SHA_384"},
		{"RSA_2048", "RSASSA_PKCS1_V1_5_SHA_512"},
		{"ECC_NIST_P256", "ECDSA_SHA_256"},
		{"ECC_NIST_P384", "ECDSA_SHA_384"},
		{"ECC_NIST_P521", "ECDSA_SHA_512"},
		{"ECC_NIST_EDWARDS25519", "ED25519_SHA_512"},
	}
	for _, tc := range cases {
		t.Run("roundtrip RAW "+tc.spec+"/"+tc.algo, func(t *testing.T) {
			ro := newTestRouter(t)
			keyID := mustCreateSignVerifyKey(t, ro, tc.spec)
			msg := []byte("hello signature")

			signBody, _ := json.Marshal(map[string]any{
				"KeyId":            keyID,
				"Message":          msg,
				"SigningAlgorithm": tc.algo,
				"MessageType":      "RAW",
			})
			sw := kmsReq(t, ro, "Sign", string(signBody))
			require.Equal(t, http.StatusOK, sw.Code, "Sign failed: %s", sw.Body.String())

			var sResp map[string]any
			require.NoError(t, json.Unmarshal(sw.Body.Bytes(), &sResp))
			assert.Equal(t, tc.algo, sResp["SigningAlgorithm"])
			assert.NotEmpty(t, sResp["KeyId"])

			sigRaw, _ := json.Marshal(sResp["Signature"])
			var sigBytes []byte
			require.NoError(t, json.Unmarshal(sigRaw, &sigBytes))

			verBody, _ := json.Marshal(map[string]any{
				"KeyId":            keyID,
				"Message":          msg,
				"Signature":        sigBytes,
				"SigningAlgorithm": tc.algo,
				"MessageType":      "RAW",
			})
			vw := kmsReq(t, ro, "Verify", string(verBody))
			require.Equal(t, http.StatusOK, vw.Code, "Verify failed: %s", vw.Body.String())
			var vResp map[string]any
			require.NoError(t, json.Unmarshal(vw.Body.Bytes(), &vResp))
			assert.Equal(t, true, vResp["SignatureValid"])
		})
	}

	t.Run("roundtrip DIGEST RSA_2048/RSASSA_PSS_SHA_256", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateSignVerifyKey(t, ro, "RSA_2048")
		msg := []byte("hello digest")
		h := sha256.Sum256(msg)
		digest := h[:]

		signBody, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          digest,
			"SigningAlgorithm": "RSASSA_PSS_SHA_256",
			"MessageType":      "DIGEST",
		})
		sw := kmsReq(t, ro, "Sign", string(signBody))
		require.Equal(t, http.StatusOK, sw.Code)

		var sResp map[string]any
		require.NoError(t, json.Unmarshal(sw.Body.Bytes(), &sResp))
		sigRaw, _ := json.Marshal(sResp["Signature"])
		var sigBytes []byte
		require.NoError(t, json.Unmarshal(sigRaw, &sigBytes))

		verBody, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          digest,
			"Signature":        sigBytes,
			"SigningAlgorithm": "RSASSA_PSS_SHA_256",
			"MessageType":      "DIGEST",
		})
		vw := kmsReq(t, ro, "Verify", string(verBody))
		require.Equal(t, http.StatusOK, vw.Code)
	})

	t.Run("error: missing Message", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateSignVerifyKey(t, ro, "ECC_NIST_P256")
		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"SigningAlgorithm": "ECDSA_SHA_256",
		})
		w := kmsReq(t, ro, "Sign", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: missing SigningAlgorithm", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateSignVerifyKey(t, ro, "ECC_NIST_P256")
		body, _ := json.Marshal(map[string]any{
			"KeyId":   keyID,
			"Message": []byte("hello"),
		})
		w := kmsReq(t, ro, "Sign", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: algorithm not compatible with key spec", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateSignVerifyKey(t, ro, "ECC_NIST_P256")
		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          []byte("hello"),
			"SigningAlgorithm": "ECDSA_SHA_512",
		})
		w := kmsReq(t, ro, "Sign", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("error: ENCRYPT_DECRYPT key not allowed", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          []byte("hello"),
			"SigningAlgorithm": "ECDSA_SHA_256",
		})
		w := kmsReq(t, ro, "Sign", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidKeyUsageException")
	})

	t.Run("error: ED25519_SHA_512 with DIGEST rejected", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateSignVerifyKey(t, ro, "ECC_NIST_EDWARDS25519")
		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          []byte("hello"),
			"SigningAlgorithm": "ED25519_SHA_512",
			"MessageType":      "DIGEST",
		})
		w := kmsReq(t, ro, "Sign", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: ED25519_PH_SHA_512 not supported", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateSignVerifyKey(t, ro, "ECC_NIST_EDWARDS25519")
		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          []byte("hello"),
			"SigningAlgorithm": "ED25519_PH_SHA_512",
		})
		w := kmsReq(t, ro, "Sign", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "UnsupportedOperationException")
	})
}

func TestHandleVerify(t *testing.T) {
	t.Run("error: tampered signature", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateSignVerifyKey(t, ro, "ECC_NIST_P256")
		msg := []byte("authentic message")

		signBody, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          msg,
			"SigningAlgorithm": "ECDSA_SHA_256",
		})
		sw := kmsReq(t, ro, "Sign", string(signBody))
		require.Equal(t, http.StatusOK, sw.Code)
		var sResp map[string]any
		require.NoError(t, json.Unmarshal(sw.Body.Bytes(), &sResp))
		sigRaw, _ := json.Marshal(sResp["Signature"])
		var sigBytes []byte
		require.NoError(t, json.Unmarshal(sigRaw, &sigBytes))
		sigBytes[0] ^= 0xFF

		verBody, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          msg,
			"Signature":        sigBytes,
			"SigningAlgorithm": "ECDSA_SHA_256",
		})
		vw := kmsReq(t, ro, "Verify", string(verBody))
		assert.Equal(t, http.StatusBadRequest, vw.Code)
		assertErrType(t, vw, "KMSInvalidSignatureException")
	})

	t.Run("error: wrong message", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateSignVerifyKey(t, ro, "ECC_NIST_P256")
		signBody, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          []byte("original"),
			"SigningAlgorithm": "ECDSA_SHA_256",
		})
		sw := kmsReq(t, ro, "Sign", string(signBody))
		require.Equal(t, http.StatusOK, sw.Code)
		var sResp map[string]any
		require.NoError(t, json.Unmarshal(sw.Body.Bytes(), &sResp))
		sigRaw, _ := json.Marshal(sResp["Signature"])
		var sigBytes []byte
		require.NoError(t, json.Unmarshal(sigRaw, &sigBytes))

		verBody, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          []byte("tampered"),
			"Signature":        sigBytes,
			"SigningAlgorithm": "ECDSA_SHA_256",
		})
		vw := kmsReq(t, ro, "Verify", string(verBody))
		assert.Equal(t, http.StatusBadRequest, vw.Code)
		assertErrType(t, vw, "KMSInvalidSignatureException")
	})

	t.Run("error: missing Signature", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateSignVerifyKey(t, ro, "ECC_NIST_P256")
		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          []byte("hello"),
			"SigningAlgorithm": "ECDSA_SHA_256",
		})
		w := kmsReq(t, ro, "Verify", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("error: key disabled", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateSignVerifyKey(t, ro, "ECC_NIST_P256")
		msg := []byte("msg")
		signBody, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          msg,
			"SigningAlgorithm": "ECDSA_SHA_256",
		})
		sw := kmsReq(t, ro, "Sign", string(signBody))
		require.Equal(t, http.StatusOK, sw.Code)
		var sResp map[string]any
		require.NoError(t, json.Unmarshal(sw.Body.Bytes(), &sResp))
		sigRaw, _ := json.Marshal(sResp["Signature"])
		var sigBytes []byte
		require.NoError(t, json.Unmarshal(sigRaw, &sigBytes))

		dw := kmsReq(t, ro, "DisableKey", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, dw.Code)

		verBody, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"Message":          msg,
			"Signature":        sigBytes,
			"SigningAlgorithm": "ECDSA_SHA_256",
		})
		vw := kmsReq(t, ro, "Verify", string(verBody))
		assert.Equal(t, http.StatusBadRequest, vw.Code)
		assertErrType(t, vw, "DisabledException")
	})
}
