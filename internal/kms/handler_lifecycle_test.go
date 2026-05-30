package kms

import (
	"encoding/json"
	"net/http"
	"os"
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
