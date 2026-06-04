package kms

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustScheduleKeyDeletion puts a key into PendingDeletion state via the API.
func mustScheduleKeyDeletion(t *testing.T, ro *Router, keyID string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"KeyId": keyID})
	w := kmsReq(t, ro, "ScheduleKeyDeletion", string(body))
	require.Equal(t, http.StatusOK, w.Code, "ScheduleKeyDeletion should succeed")
}

// mustEnableKeyRotation enables key rotation via the API.
func mustEnableKeyRotation(t *testing.T, ro *Router, keyID string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"KeyId": keyID})
	w := kmsReq(t, ro, "EnableKeyRotation", string(body))
	require.Equal(t, http.StatusOK, w.Code, "EnableKeyRotation should succeed")
}

// ---- EnableKey ---------------------------------------------------------------

func TestHandleEnableKey(t *testing.T) {
	t.Run("200 re-enables disabled key", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ro := NewRouter(s)

		keyID := mustCreateKey(t, ro, `{}`)
		mustDisableKey(t, s, keyID)

		w := kmsReq(t, ro, "EnableKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Body.String())

		// Verify key is now Enabled.
		w2 := kmsReq(t, ro, "DescribeKey", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w2.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
		meta := resp["KeyMetadata"].(map[string]any)
		assert.Equal(t, "Enabled", meta["KeyState"])
		assert.Equal(t, true, meta["Enabled"])
	})

	t.Run("200 idempotent on already-enabled key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "EnableKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "EnableKey", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "EnableKey", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "EnableKey", `{"KeyId":"00000000-0000-0000-0000-000000000000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)
		w := kmsReq(t, ro, "EnableKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 for alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "EnableKey", `{"KeyId":"alias/nonexistent"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		w := kmsReq(t, ro, "EnableKey", `{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- DisableKey --------------------------------------------------------------

func TestHandleDisableKey(t *testing.T) {
	t.Run("200 disables enabled key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)

		w := kmsReq(t, ro, "DisableKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Body.String())

		// Verify key is now Disabled.
		w2 := kmsReq(t, ro, "DescribeKey", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w2.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
		meta := resp["KeyMetadata"].(map[string]any)
		assert.Equal(t, "Disabled", meta["KeyState"])
		assert.Equal(t, false, meta["Enabled"])
	})

	t.Run("200 idempotent on already-disabled key", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ro := NewRouter(s)

		keyID := mustCreateKey(t, ro, `{}`)
		mustDisableKey(t, s, keyID)

		w := kmsReq(t, ro, "DisableKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DisableKey", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DisableKey", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DisableKey", `{"KeyId":"00000000-0000-0000-0000-000000000000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)
		w := kmsReq(t, ro, "DisableKey", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 for alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DisableKey", `{"KeyId":"alias/nonexistent"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		w := kmsReq(t, ro, "DisableKey", `{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- ScheduleKeyDeletion -----------------------------------------------------

func TestHandleScheduleKeyDeletion(t *testing.T) {
	t.Run("200 with default pending window (30 days)", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)

		w := kmsReq(t, ro, "ScheduleKeyDeletion", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["KeyId"], keyID)
		assert.Equal(t, "PendingDeletion", resp["KeyState"])
		assert.NotNil(t, resp["DeletionDate"])
		assert.Equal(t, float64(30), resp["PendingWindowInDays"])
	})

	t.Run("200 with explicit pending window", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)

		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "PendingWindowInDays": 7})
		w := kmsReq(t, ro, "ScheduleKeyDeletion", string(body))
		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(7), resp["PendingWindowInDays"])
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ScheduleKeyDeletion", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ScheduleKeyDeletion", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for PendingWindowInDays less than 7", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "PendingWindowInDays": 6})
		w := kmsReq(t, ro, "ScheduleKeyDeletion", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for PendingWindowInDays greater than 30", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "PendingWindowInDays": 31})
		w := kmsReq(t, ro, "ScheduleKeyDeletion", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(
			t,
			ro,
			"ScheduleKeyDeletion",
			`{"KeyId":"00000000-0000-0000-0000-000000000000"}`,
		)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 for already-PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)
		w := kmsReq(t, ro, "ScheduleKeyDeletion", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 for alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ScheduleKeyDeletion", `{"KeyId":"alias/nonexistent"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		w := kmsReq(t, ro, "ScheduleKeyDeletion",
			`{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- CancelKeyDeletion -------------------------------------------------------

func TestHandleCancelKeyDeletion(t *testing.T) {
	t.Run("200 cancels pending deletion", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)

		w := kmsReq(t, ro, "CancelKeyDeletion", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["KeyId"], keyID)

		// Key should now be Disabled (not PendingDeletion).
		w2 := kmsReq(t, ro, "DescribeKey", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w2.Code)
		var descResp map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &descResp))
		meta := descResp["KeyMetadata"].(map[string]any)
		assert.Equal(t, "Disabled", meta["KeyState"])
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CancelKeyDeletion", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CancelKeyDeletion", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CancelKeyDeletion", `{"KeyId":"00000000-0000-0000-0000-000000000000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 for key not in PendingDeletion state", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		// Key is Enabled, not PendingDeletion.
		w := kmsReq(t, ro, "CancelKeyDeletion", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 for alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CancelKeyDeletion", `{"KeyId":"alias/nonexistent"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		w := kmsReq(t, ro, "CancelKeyDeletion",
			`{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- EnableKeyRotation -------------------------------------------------------

func TestHandleEnableKeyRotation(t *testing.T) {
	t.Run("200 enables rotation with default period", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)

		w := kmsReq(t, ro, "EnableKeyRotation", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Body.String())
	})

	t.Run("200 enables rotation with explicit period", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)

		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "RotationPeriodInDays": 90})
		w := kmsReq(t, ro, "EnableKeyRotation", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "EnableKeyRotation", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "EnableKeyRotation", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for RotationPeriodInDays less than 90", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "RotationPeriodInDays": 89})
		w := kmsReq(t, ro, "EnableKeyRotation", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for RotationPeriodInDays greater than 2560", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "RotationPeriodInDays": 2561})
		w := kmsReq(t, ro, "EnableKeyRotation", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "EnableKeyRotation", `{"KeyId":"00000000-0000-0000-0000-000000000000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 DisabledException for disabled key", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ro := NewRouter(s)

		keyID := mustCreateKey(t, ro, `{}`)
		mustDisableKey(t, s, keyID)

		w := kmsReq(t, ro, "EnableKeyRotation", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "DisabledException")
	})

	t.Run("400 UnsupportedOperationException for non-SYMMETRIC key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"RSA_2048","KeyUsage":"SIGN_VERIFY"}`)
		w := kmsReq(t, ro, "EnableKeyRotation", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "UnsupportedOperationException")
	})

	t.Run("400 KMSInvalidStateException for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)
		w := kmsReq(t, ro, "EnableKeyRotation", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 for alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "EnableKeyRotation", `{"KeyId":"alias/nonexistent"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		w := kmsReq(t, ro, "EnableKeyRotation",
			`{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- DisableKeyRotation ------------------------------------------------------

func TestHandleDisableKeyRotation(t *testing.T) {
	t.Run("200 disables rotation", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustEnableKeyRotation(t, ro, keyID)

		w := kmsReq(t, ro, "DisableKeyRotation", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Body.String())
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DisableKeyRotation", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DisableKeyRotation", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DisableKeyRotation", `{"KeyId":"00000000-0000-0000-0000-000000000000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 DisabledException for disabled key", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ro := NewRouter(s)

		keyID := mustCreateKey(t, ro, `{}`)
		mustDisableKey(t, s, keyID)

		w := kmsReq(t, ro, "DisableKeyRotation", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "DisabledException")
	})

	t.Run("400 UnsupportedOperationException for non-SYMMETRIC key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"RSA_2048","KeyUsage":"SIGN_VERIFY"}`)
		w := kmsReq(t, ro, "DisableKeyRotation", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "UnsupportedOperationException")
	})

	t.Run("400 KMSInvalidStateException for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)
		w := kmsReq(t, ro, "DisableKeyRotation", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 for alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "DisableKeyRotation", `{"KeyId":"alias/nonexistent"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		w := kmsReq(t, ro, "DisableKeyRotation",
			`{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- GetKeyRotationStatus ----------------------------------------------------

func TestHandleGetKeyRotationStatus(t *testing.T) {
	t.Run("200 rotation not enabled", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)

		w := kmsReq(t, ro, "GetKeyRotationStatus", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["KeyId"], keyID)
		assert.Equal(t, false, resp["KeyRotationEnabled"])
		assert.Nil(t, resp["RotationPeriodInDays"])
		assert.Nil(t, resp["NextRotationDate"])
	})

	t.Run("200 rotation enabled with period and next date", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustEnableKeyRotation(t, ro, keyID)

		w := kmsReq(t, ro, "GetKeyRotationStatus", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, true, resp["KeyRotationEnabled"])
		assert.NotNil(t, resp["RotationPeriodInDays"])
		assert.NotNil(t, resp["NextRotationDate"])
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetKeyRotationStatus", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetKeyRotationStatus", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetKeyRotationStatus",
			`{"KeyId":"00000000-0000-0000-0000-000000000000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 UnsupportedOperationException for non-SYMMETRIC key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"RSA_2048","KeyUsage":"SIGN_VERIFY"}`)
		w := kmsReq(t, ro, "GetKeyRotationStatus", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "UnsupportedOperationException")
	})

	t.Run("400 KMSInvalidStateException for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)
		w := kmsReq(t, ro, "GetKeyRotationStatus", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 for alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "GetKeyRotationStatus", `{"KeyId":"alias/nonexistent"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		w := kmsReq(t, ro, "GetKeyRotationStatus",
			`{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- enable/disable key roundtrip with rotation ------------------------------

func TestKeyLifecycle_rotationRoundtrip(t *testing.T) {
	ro := newTestRouter(t)
	keyID := mustCreateKey(t, ro, `{}`)

	// Enable rotation.
	mustEnableKeyRotation(t, ro, keyID)

	// Status shows enabled.
	w := kmsReq(t, ro, "GetKeyRotationStatus", `{"KeyId":"`+keyID+`"}`)
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["KeyRotationEnabled"])

	// Disable rotation.
	w2 := kmsReq(t, ro, "DisableKeyRotation", `{"KeyId":"`+keyID+`"}`)
	require.Equal(t, http.StatusOK, w2.Code)

	// Status shows disabled.
	w3 := kmsReq(t, ro, "GetKeyRotationStatus", `{"KeyId":"`+keyID+`"}`)
	require.Equal(t, http.StatusOK, w3.Code)
	var resp3 map[string]any
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &resp3))
	assert.Equal(t, false, resp3["KeyRotationEnabled"])
}

func TestDataPlane_pendingDeletionKey(t *testing.T) {
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
			ro := newTestRouter(t)
			keyID := mustCreateKey(t, ro, `{}`)
			mustScheduleKeyDeletion(t, ro, keyID)
			w := kmsReq(t, ro, op.name, op.body(keyID))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, "KMSInvalidStateException")
		})
	}
}

func TestDecrypt_pendingDeletionKey(t *testing.T) {
	ro := newTestRouter(t)
	keyID := mustCreateKey(t, ro, `{}`)
	cipherBlob := mustEncrypt(t, ro, keyID, []byte("data"))
	mustScheduleKeyDeletion(t, ro, keyID)

	body, _ := json.Marshal(map[string]any{"CiphertextBlob": cipherBlob})
	w := kmsReq(t, ro, "Decrypt", string(body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, "KMSInvalidStateException")
}

// mustTagResource adds tags via the API.
func mustTagResource(t *testing.T, ro *Router, keyID string, tags map[string]string) {
	t.Helper()
	entries := make([]map[string]string, 0, len(tags))
	for k, v := range tags {
		entries = append(entries, map[string]string{"TagKey": k, "TagValue": v})
	}
	body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Tags": entries})
	w := kmsReq(t, ro, "TagResource", string(body))
	require.Equal(t, http.StatusOK, w.Code, "TagResource should succeed")
}

// ---- TagResource -------------------------------------------------------------

func TestHandleTagResource(t *testing.T) {
	t.Run("200 adds tags and reads them back via ListResourceTags", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustTagResource(t, ro, keyID, map[string]string{"Env": "test", "Team": "platform"})

		w := kmsReq(t, ro, "ListResourceTags", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		tags := resp["Tags"].([]any)
		assert.Len(t, tags, 2)
		// Tags are sorted by key.
		assert.Equal(t, "Env", tags[0].(map[string]any)["TagKey"])
		assert.Equal(t, "test", tags[0].(map[string]any)["TagValue"])
	})

	t.Run("200 overwrites existing tag value", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustTagResource(t, ro, keyID, map[string]string{"Env": "staging"})
		mustTagResource(t, ro, keyID, map[string]string{"Env": "prod"})

		w := kmsReq(t, ro, "ListResourceTags", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		tags := resp["Tags"].([]any)
		require.Len(t, tags, 1)
		assert.Equal(t, "prod", tags[0].(map[string]any)["TagValue"])
	})

	t.Run("200 accepts empty tag value", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustTagResource(t, ro, keyID, map[string]string{"Empty": ""})

		w := kmsReq(t, ro, "ListResourceTags", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		tags := resp["Tags"].([]any)
		require.Len(t, tags, 1)
		assert.Equal(t, "", tags[0].(map[string]any)["TagValue"])
	})

	t.Run("200 works on disabled key", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ro := NewRouter(s)

		keyID := mustCreateKey(t, ro, `{}`)
		mustDisableKey(t, s, keyID)

		body, _ := json.Marshal(map[string]any{
			"KeyId": keyID,
			"Tags":  []map[string]string{{"TagKey": "k", "TagValue": "v"}},
		})
		w := kmsReq(t, ro, "TagResource", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"Tags": []map[string]string{{"TagKey": "k", "TagValue": "v"}},
		})
		w := kmsReq(t, ro, "TagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for missing Tags", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "TagResource", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for empty Tags array", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Tags": []any{}})
		w := kmsReq(t, ro, "TagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "TagResource", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 TagException for empty tag key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId": keyID,
			"Tags":  []map[string]string{{"TagKey": "", "TagValue": "v"}},
		})
		w := kmsReq(t, ro, "TagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "TagException")
	})

	t.Run("400 TagException for tag key exceeding 128 chars", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId": keyID,
			"Tags":  []map[string]string{{"TagKey": strings.Repeat("k", 129), "TagValue": "v"}},
		})
		w := kmsReq(t, ro, "TagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "TagException")
	})

	t.Run("400 TagException for aws: reserved prefix", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId": keyID,
			"Tags":  []map[string]string{{"TagKey": "aws:Env", "TagValue": "prod"}},
		})
		w := kmsReq(t, ro, "TagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "TagException")
	})

	t.Run("400 TagException for tag value exceeding 256 chars", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId": keyID,
			"Tags":  []map[string]string{{"TagKey": "k", "TagValue": strings.Repeat("v", 257)}},
		})
		w := kmsReq(t, ro, "TagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "TagException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"KeyId": "00000000-0000-0000-0000-000000000000",
			"Tags":  []map[string]string{{"TagKey": "k", "TagValue": "v"}},
		})
		w := kmsReq(t, ro, "TagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 KMSInvalidStateException for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)
		body, _ := json.Marshal(map[string]any{
			"KeyId": keyID,
			"Tags":  []map[string]string{{"TagKey": "k", "TagValue": "v"}},
		})
		w := kmsReq(t, ro, "TagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 LimitExceededException when exceeding 50 tags", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		// Add 50 tags first.
		tags := make([]map[string]string, 50)
		for i := range 50 {
			tags[i] = map[string]string{
				"TagKey":   "key" + string(rune('A'+i%26)) + string(rune('0'+i/26)),
				"TagValue": "v",
			}
		}
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Tags": tags})
		w := kmsReq(t, ro, "TagResource", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		// Adding one more should fail.
		extra, _ := json.Marshal(map[string]any{
			"KeyId": keyID,
			"Tags":  []map[string]string{{"TagKey": "extra", "TagValue": "v"}},
		})
		w2 := kmsReq(t, ro, "TagResource", string(extra))
		assert.Equal(t, http.StatusBadRequest, w2.Code)
		assertErrType(t, w2, "LimitExceededException")
	})

	t.Run("400 for alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"KeyId": "alias/nonexistent",
			"Tags":  []map[string]string{{"TagKey": "k", "TagValue": "v"}},
		})
		w := kmsReq(t, ro, "TagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		body, _ := json.Marshal(map[string]any{
			"KeyId": "00000000-0000-0000-0000-000000000001",
			"Tags":  []map[string]string{{"TagKey": "k", "TagValue": "v"}},
		})
		w := kmsReq(t, ro, "TagResource", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- UntagResource -----------------------------------------------------------

func TestHandleUntagResource(t *testing.T) {
	t.Run("200 removes specified tags", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustTagResource(t, ro, keyID, map[string]string{"Env": "test", "Team": "platform"})

		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "TagKeys": []string{"Env"}})
		w := kmsReq(t, ro, "UntagResource", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Body.String())

		// Verify only "Team" remains.
		w2 := kmsReq(t, ro, "ListResourceTags", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w2.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
		tags := resp["Tags"].([]any)
		require.Len(t, tags, 1)
		assert.Equal(t, "Team", tags[0].(map[string]any)["TagKey"])
	})

	t.Run("200 silently ignores non-existent tag key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)

		body, _ := json.Marshal(map[string]any{
			"KeyId":   keyID,
			"TagKeys": []string{"nonexistent"},
		})
		w := kmsReq(t, ro, "UntagResource", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("200 works on disabled key", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ro := NewRouter(s)

		keyID := mustCreateKey(t, ro, `{}`)
		mustTagResource(t, ro, keyID, map[string]string{"k": "v"})
		mustDisableKey(t, s, keyID)

		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "TagKeys": []string{"k"}})
		w := kmsReq(t, ro, "UntagResource", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"TagKeys": []string{"k"}})
		w := kmsReq(t, ro, "UntagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for missing TagKeys", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "UntagResource", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for empty TagKeys array", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "TagKeys": []string{}})
		w := kmsReq(t, ro, "UntagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "UntagResource", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 TagException for empty tag key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "TagKeys": []string{""}})
		w := kmsReq(t, ro, "UntagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "TagException")
	})

	t.Run("400 TagException for tag key exceeding 128 chars", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":   keyID,
			"TagKeys": []string{strings.Repeat("k", 129)},
		})
		w := kmsReq(t, ro, "UntagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "TagException")
	})

	t.Run("400 TagException for aws: reserved prefix", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":   keyID,
			"TagKeys": []string{"aws:Env"},
		})
		w := kmsReq(t, ro, "UntagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "TagException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"KeyId":   "00000000-0000-0000-0000-000000000000",
			"TagKeys": []string{"k"},
		})
		w := kmsReq(t, ro, "UntagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 KMSInvalidStateException for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "TagKeys": []string{"k"}})
		w := kmsReq(t, ro, "UntagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 for alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"KeyId":   "alias/nonexistent",
			"TagKeys": []string{"k"},
		})
		w := kmsReq(t, ro, "UntagResource", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		body, _ := json.Marshal(map[string]any{
			"KeyId":   "00000000-0000-0000-0000-000000000001",
			"TagKeys": []string{"k"},
		})
		w := kmsReq(t, ro, "UntagResource", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- ListResourceTags (with real tag data) -----------------------------------

func TestHandleListResourceTags_withTags(t *testing.T) {
	t.Run("pagination: Marker and NextMarker work", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustTagResource(t, ro, keyID, map[string]string{"A": "1", "B": "2", "C": "3"})

		// First page: Limit=2
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Limit": 2})
		w := kmsReq(t, ro, "ListResourceTags", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var page1 map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page1))
		assert.Equal(t, true, page1["Truncated"])
		assert.Len(t, page1["Tags"].([]any), 2)
		nextMarker := page1["NextMarker"].(string)
		assert.Equal(t, "B", nextMarker)

		// Second page using NextMarker.
		body2, _ := json.Marshal(map[string]any{"KeyId": keyID, "Marker": nextMarker})
		w2 := kmsReq(t, ro, "ListResourceTags", string(body2))
		require.Equal(t, http.StatusOK, w2.Code)
		var page2 map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &page2))
		assert.Equal(t, false, page2["Truncated"])
		assert.Len(t, page2["Tags"].([]any), 1)
		assert.Equal(t, "C", page2["Tags"].([]any)[0].(map[string]any)["TagKey"])
	})

	t.Run("400 ValidationException for Limit > 50", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Limit": 51})
		w := kmsReq(t, ro, "ListResourceTags", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 ValidationException for Limit = 0", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Limit": 0})
		w := kmsReq(t, ro, "ListResourceTags", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("200 ListResourceTags permitted for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustTagResource(t, ro, keyID, map[string]string{"Env": "test"})
		mustScheduleKeyDeletion(t, ro, keyID)

		w := kmsReq(t, ro, "ListResourceTags", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Len(t, resp["Tags"].([]any), 1)
	})

	t.Run("400 InvalidMarkerException for unknown marker", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustTagResource(t, ro, keyID, map[string]string{"A": "1"})

		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Marker": "nonexistent-marker"})
		w := kmsReq(t, ro, "ListResourceTags", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidMarkerException")
	})

	t.Run("500 on GetTags storage failure", func(t *testing.T) {
		ro := newFailRouter()
		w := kmsReq(t, ro, "ListResourceTags",
			`{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- tag roundtrip -----------------------------------------------------------

func TestTagRoundtrip(t *testing.T) {
	ro := newTestRouter(t)
	keyID := mustCreateKey(t, ro, `{}`)

	mustTagResource(t, ro, keyID, map[string]string{"Env": "dev", "App": "myapp"})

	body, _ := json.Marshal(map[string]any{"KeyId": keyID, "TagKeys": []string{"Env"}})
	w := kmsReq(t, ro, "UntagResource", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	w2 := kmsReq(t, ro, "ListResourceTags", `{"KeyId":"`+keyID+`"}`)
	require.Equal(t, http.StatusOK, w2.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	tags := resp["Tags"].([]any)
	require.Len(t, tags, 1)
	assert.Equal(t, "App", tags[0].(map[string]any)["TagKey"])
	assert.Equal(t, "myapp", tags[0].(map[string]any)["TagValue"])
}

// ---- RotateKeyOnDemand -------------------------------------------------------

// mustRotateKeyOnDemand rotates a key via the API and fails the test if it does not succeed.
func mustRotateKeyOnDemand(t *testing.T, ro *Router, keyID string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"KeyId": keyID})
	w := kmsReq(t, ro, "RotateKeyOnDemand", string(body))
	require.Equal(t, http.StatusOK, w.Code, "RotateKeyOnDemand should succeed")
}

func TestHandleRotateKeyOnDemand(t *testing.T) {
	t.Run("200 rotates key and returns ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)

		w := kmsReq(t, ro, "RotateKeyOnDemand", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["KeyId"], keyID)
	})

	t.Run("200 via alias", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"AliasName": "alias/mykey", "TargetKeyId": keyID})
		w := kmsReq(t, ro, "CreateAlias", string(body))
		require.Equal(t, http.StatusOK, w.Code)

		w2 := kmsReq(t, ro, "RotateKeyOnDemand", `{"KeyId":"alias/mykey"}`)
		assert.Equal(t, http.StatusOK, w2.Code)
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "RotateKeyOnDemand", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "RotateKeyOnDemand", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 NotFoundException for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "RotateKeyOnDemand", `{"KeyId":"00000000-0000-0000-0000-000000000000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 DisabledException for disabled key", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		ro := NewRouter(s)

		keyID := mustCreateKey(t, ro, `{}`)
		mustDisableKey(t, s, keyID)

		w := kmsReq(t, ro, "RotateKeyOnDemand", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "DisabledException")
	})

	t.Run("400 KMSInvalidStateException for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)

		w := kmsReq(t, ro, "RotateKeyOnDemand", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 UnsupportedOperationException for non-SYMMETRIC key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"RSA_2048","KeyUsage":"SIGN_VERIFY"}`)

		w := kmsReq(t, ro, "RotateKeyOnDemand", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "UnsupportedOperationException")
	})

	t.Run("400 LimitExceededException after 25 on-demand rotations", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)

		for i := range 25 {
			w := kmsReq(t, ro, "RotateKeyOnDemand", `{"KeyId":"`+keyID+`"}`)
			require.Equal(t, http.StatusOK, w.Code, "rotation %d should succeed", i+1)
		}

		w := kmsReq(t, ro, "RotateKeyOnDemand", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "LimitExceededException")
	})

	t.Run("400 for alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "RotateKeyOnDemand", `{"KeyId":"alias/nonexistent"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		w := kmsReq(t, ro, "RotateKeyOnDemand", `{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- ListKeyRotations --------------------------------------------------------

func TestHandleListKeyRotations(t *testing.T) {
	t.Run("200 empty history for new key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)

		w := kmsReq(t, ro, "ListKeyRotations", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, false, resp["Truncated"])
		assert.Nil(t, resp["NextMarker"])
		rotations := resp["Rotations"].([]any)
		assert.Empty(t, rotations)
	})

	t.Run("200 ON_DEMAND record appears after rotation", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustRotateKeyOnDemand(t, ro, keyID)

		w := kmsReq(t, ro, "ListKeyRotations", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		rotations := resp["Rotations"].([]any)
		require.Len(t, rotations, 1)
		rec := rotations[0].(map[string]any)
		assert.Equal(t, "ON_DEMAND", rec["RotationType"])
		assert.Contains(t, rec["KeyId"], keyID)
		assert.NotZero(t, rec["RotationDate"])
	})

	t.Run("200 multiple rotations in insertion order", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustRotateKeyOnDemand(t, ro, keyID)
		mustRotateKeyOnDemand(t, ro, keyID)
		mustRotateKeyOnDemand(t, ro, keyID)

		w := kmsReq(t, ro, "ListKeyRotations", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		rotations := resp["Rotations"].([]any)
		assert.Len(t, rotations, 3)
		for _, r := range rotations {
			assert.Equal(t, "ON_DEMAND", r.(map[string]any)["RotationType"])
		}
	})

	t.Run("200 pagination with Limit and Marker", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustRotateKeyOnDemand(t, ro, keyID)
		mustRotateKeyOnDemand(t, ro, keyID)
		mustRotateKeyOnDemand(t, ro, keyID)

		// Page 1: limit 2
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Limit": 2})
		w := kmsReq(t, ro, "ListKeyRotations", string(body))
		require.Equal(t, http.StatusOK, w.Code)

		var page1 map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page1))
		assert.Equal(t, true, page1["Truncated"])
		assert.Len(t, page1["Rotations"].([]any), 2)
		nextMarker := page1["NextMarker"].(string)
		assert.NotEmpty(t, nextMarker)

		// Page 2 using NextMarker
		body2, _ := json.Marshal(map[string]any{"KeyId": keyID, "Marker": nextMarker})
		w2 := kmsReq(t, ro, "ListKeyRotations", string(body2))
		require.Equal(t, http.StatusOK, w2.Code)

		var page2 map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &page2))
		assert.Equal(t, false, page2["Truncated"])
		assert.Len(t, page2["Rotations"].([]any), 1)
		assert.Nil(t, page2["NextMarker"])
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListKeyRotations", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListKeyRotations", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 ValidationException for Limit out of range", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)

		for _, limit := range []int{0, 1001} {
			body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Limit": limit})
			w := kmsReq(t, ro, "ListKeyRotations", string(body))
			assert.Equal(t, http.StatusBadRequest, w.Code, "limit=%d", limit)
			assertErrType(t, w, "ValidationException")
		}
	})

	t.Run("400 NotFoundException for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListKeyRotations", `{"KeyId":"00000000-0000-0000-0000-000000000000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 KMSInvalidStateException for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)

		w := kmsReq(t, ro, "ListKeyRotations", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 UnsupportedOperationException for non-SYMMETRIC key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{"KeySpec":"RSA_2048","KeyUsage":"SIGN_VERIFY"}`)

		w := kmsReq(t, ro, "ListKeyRotations", `{"KeyId":"`+keyID+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "UnsupportedOperationException")
	})

	t.Run("400 InvalidMarkerException for unknown marker", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustRotateKeyOnDemand(t, ro, keyID)

		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Marker": "not-a-real-marker"})
		w := kmsReq(t, ro, "ListKeyRotations", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidMarkerException")
	})

	t.Run("400 for alias not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListKeyRotations", `{"KeyId":"alias/nonexistent"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		w := kmsReq(t, ro, "ListKeyRotations", `{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- Decrypt and ReEncrypt with rotated key material -------------------------

func TestDecrypt_afterRotation(t *testing.T) {
	ro := newTestRouter(t)
	keyID := mustCreateKey(t, ro, `{}`)

	// Encrypt with original key material.
	cipherBlob := mustEncrypt(t, ro, keyID, []byte("rotate-test"))

	// Rotate the key.
	mustRotateKeyOnDemand(t, ro, keyID)

	// Decrypt must still succeed using the retained old material.
	body, _ := json.Marshal(map[string]any{"CiphertextBlob": cipherBlob})
	w := kmsReq(t, ro, "Decrypt", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotNil(t, resp["Plaintext"])
}

func TestReEncrypt_afterRotation(t *testing.T) {
	ro := newTestRouter(t)
	srcKeyID := mustCreateKey(t, ro, `{}`)
	dstKeyID := mustCreateKey(t, ro, `{}`)

	cipherBlob := mustEncrypt(t, ro, srcKeyID, []byte("reencrypt-rotate-test"))

	// Rotate the source key.
	mustRotateKeyOnDemand(t, ro, srcKeyID)

	// ReEncrypt from old-material ciphertext to dstKey must succeed.
	body, _ := json.Marshal(map[string]any{
		"CiphertextBlob":   cipherBlob,
		"DestinationKeyId": dstKeyID,
	})
	w := kmsReq(t, ro, "ReEncrypt", string(body))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestKeyLifecycle_scheduleAndCancelRoundtrip(t *testing.T) {
	ro := newTestRouter(t)
	keyID := mustCreateKey(t, ro, `{}`)

	// Schedule deletion.
	mustScheduleKeyDeletion(t, ro, keyID)

	// Verify PendingDeletion.
	w := kmsReq(t, ro, "DescribeKey", `{"KeyId":"`+keyID+`"}`)
	require.Equal(t, http.StatusOK, w.Code)
	var desc map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &desc))
	assert.Equal(t, "PendingDeletion", desc["KeyMetadata"].(map[string]any)["KeyState"])

	// Cancel deletion.
	w2 := kmsReq(t, ro, "CancelKeyDeletion", `{"KeyId":"`+keyID+`"}`)
	require.Equal(t, http.StatusOK, w2.Code)

	// Key should now be Disabled.
	w3 := kmsReq(t, ro, "DescribeKey", `{"KeyId":"`+keyID+`"}`)
	require.Equal(t, http.StatusOK, w3.Code)
	var desc3 map[string]any
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &desc3))
	assert.Equal(t, "Disabled", desc3["KeyMetadata"].(map[string]any)["KeyState"])
}
