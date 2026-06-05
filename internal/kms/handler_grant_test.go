package kms

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustCreateGrant creates a grant via the API and returns grantID and grantToken.
func mustCreateGrant(
	t *testing.T,
	ro *Router,
	keyID, granteePrincipal string,
	ops []string,
) (string, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"KeyId":            keyID,
		"GranteePrincipal": granteePrincipal,
		"Operations":       ops,
	})
	w := kmsReq(t, ro, "CreateGrant", string(body))
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp["GrantId"].(string), resp["GrantToken"].(string)
}

// ---- CreateGrant ------------------------------------------------------------

func TestHandleCreateGrant(t *testing.T) {
	t.Run("200 creates grant with required fields", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		grantID, grantToken := mustCreateGrant(
			t,
			ro,
			keyID,
			"arn:aws:iam::000000000000:role/tester",
			[]string{"Decrypt"},
		)
		assert.NotEmpty(t, grantID)
		assert.NotEmpty(t, grantToken)
	})

	t.Run("200 creates grant with all optional fields", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":             keyID,
			"GranteePrincipal":  "arn:aws:iam::000000000000:role/tester",
			"Operations":        []string{"Decrypt", "Encrypt"},
			"RetiringPrincipal": "arn:aws:iam::000000000000:role/admin",
			"Name":              "my-grant",
			"Constraints": map[string]any{
				"EncryptionContextEquals": map[string]string{"env": "prod"},
			},
		})
		w := kmsReq(t, ro, "CreateGrant", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotEmpty(t, resp["GrantId"])
		assert.NotEmpty(t, resp["GrantToken"])
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CreateGrant",
			`{"GranteePrincipal":"arn:aws:iam::000000000000:role/r","Operations":["Decrypt"]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for missing GranteePrincipal", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Operations": []string{"Decrypt"}})
		w := kmsReq(t, ro, "CreateGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for missing Operations", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"GranteePrincipal": "arn:aws:iam::000000000000:role/r",
		})
		w := kmsReq(t, ro, "CreateGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid operation", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"GranteePrincipal": "arn:aws:iam::000000000000:role/r",
			"Operations":       []string{"InvalidOp"},
		})
		w := kmsReq(t, ro, "CreateGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for Name exceeding 256 chars", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"GranteePrincipal": "arn:aws:iam::000000000000:role/r",
			"Operations":       []string{"Decrypt"},
			"Name":             strings.Repeat("a", 257),
		})
		w := kmsReq(t, ro, "CreateGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for Name with invalid characters", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"GranteePrincipal": "arn:aws:iam::000000000000:role/r",
			"Operations":       []string{"Decrypt"},
			"Name":             "invalid name!",
		})
		w := kmsReq(t, ro, "CreateGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"KeyId":            "00000000-0000-0000-0000-000000000000",
			"GranteePrincipal": "arn:aws:iam::000000000000:role/r",
			"Operations":       []string{"Decrypt"},
		})
		w := kmsReq(t, ro, "CreateGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 DisabledException for disabled key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		w := kmsReq(t, ro, "DisableKey", `{"KeyId":"`+keyID+`"}`)
		require.Equal(t, http.StatusOK, w.Code)

		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"GranteePrincipal": "arn:aws:iam::000000000000:role/r",
			"Operations":       []string{"Decrypt"},
		})
		w2 := kmsReq(t, ro, "CreateGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w2.Code)
		assertErrType(t, w2, "DisabledException")
	})

	t.Run("400 KMSInvalidStateException for pending deletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)

		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"GranteePrincipal": "arn:aws:iam::000000000000:role/r",
			"Operations":       []string{"Decrypt"},
		})
		w := kmsReq(t, ro, "CreateGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "CreateGrant", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for malformed ARN KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"KeyId":            "arn:aws:kms:us-east-1:123456789012:garbage",
			"GranteePrincipal": "arn:aws:iam::000000000000:role/r",
			"Operations":       []string{"Decrypt"},
		})
		w := kmsReq(t, ro, "CreateGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		body, _ := json.Marshal(map[string]any{
			"KeyId":            "00000000-0000-0000-0000-000000000001",
			"GranteePrincipal": "arn:aws:iam::000000000000:role/r",
			"Operations":       []string{"Decrypt"},
		})
		w := kmsReq(t, ro, "CreateGrant", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})

	t.Run("400 LimitExceededException when grant limit reached", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		ro.storage = &grantLimitStore{ro.storage}
		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"GranteePrincipal": "arn:aws:iam::000000000000:role/r",
			"Operations":       []string{"Decrypt"},
		})
		w := kmsReq(t, ro, "CreateGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "LimitExceededException")
	})
}

// grantLimitStore wraps a store but returns ErrGrantLimitExceeded from CreateGrant.
type grantLimitStore struct{ store }

func (g *grantLimitStore) CreateGrant(_ string, _ CreateGrantInput) (Grant, error) {
	return Grant{}, ErrGrantLimitExceeded
}

// ---- ListGrants -------------------------------------------------------------

func TestHandleListGrants(t *testing.T) {
	t.Run("200 empty list when no grants", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID})
		w := kmsReq(t, ro, "ListGrants", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, []any{}, resp["Grants"])
		assert.Equal(t, false, resp["Truncated"])
	})

	t.Run("200 lists created grants", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		grantID1, _ := mustCreateGrant(
			t,
			ro,
			keyID,
			"arn:aws:iam::000000000000:role/a",
			[]string{"Decrypt"},
		)
		grantID2, _ := mustCreateGrant(
			t,
			ro,
			keyID,
			"arn:aws:iam::000000000000:role/b",
			[]string{"Encrypt"},
		)

		body, _ := json.Marshal(map[string]any{"KeyId": keyID})
		w := kmsReq(t, ro, "ListGrants", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		grants := resp["Grants"].([]any)
		assert.Len(t, grants, 2)
		ids := []string{
			grants[0].(map[string]any)["GrantId"].(string),
			grants[1].(map[string]any)["GrantId"].(string),
		}
		assert.ElementsMatch(t, []string{grantID1, grantID2}, ids)
		for _, g := range grants {
			assert.Empty(
				t,
				g.(map[string]any)["GrantToken"],
				"GrantToken must not appear in ListGrants response",
			)
		}
	})

	t.Run("200 filter by GrantId", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		grantID1, _ := mustCreateGrant(
			t,
			ro,
			keyID,
			"arn:aws:iam::000000000000:role/a",
			[]string{"Decrypt"},
		)
		mustCreateGrant(t, ro, keyID, "arn:aws:iam::000000000000:role/b", []string{"Encrypt"})

		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "GrantId": grantID1})
		w := kmsReq(t, ro, "ListGrants", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		grants := resp["Grants"].([]any)
		require.Len(t, grants, 1)
		assert.Equal(t, grantID1, grants[0].(map[string]any)["GrantId"])
	})

	t.Run("200 filter by GranteePrincipal", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateGrant(t, ro, keyID, "arn:aws:iam::000000000000:role/a", []string{"Decrypt"})
		grantID2, _ := mustCreateGrant(
			t,
			ro,
			keyID,
			"arn:aws:iam::000000000000:role/b",
			[]string{"Encrypt"},
		)

		body, _ := json.Marshal(map[string]any{
			"KeyId":            keyID,
			"GranteePrincipal": "arn:aws:iam::000000000000:role/b",
		})
		w := kmsReq(t, ro, "ListGrants", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		grants := resp["Grants"].([]any)
		require.Len(t, grants, 1)
		assert.Equal(t, grantID2, grants[0].(map[string]any)["GrantId"])
	})

	t.Run("200 pagination with Limit and Marker", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		for range 5 {
			mustCreateGrant(t, ro, keyID, "arn:aws:iam::000000000000:role/r", []string{"Decrypt"})
		}

		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "Limit": 3})
		w := kmsReq(t, ro, "ListGrants", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
		var page1 map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page1))
		assert.Equal(t, true, page1["Truncated"])
		assert.Len(t, page1["Grants"].([]any), 3)
		marker := page1["NextMarker"].(string)

		body2, _ := json.Marshal(map[string]any{"KeyId": keyID, "Limit": 3, "Marker": marker})
		w2 := kmsReq(t, ro, "ListGrants", string(body2))
		assert.Equal(t, http.StatusOK, w2.Code)
		var page2 map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &page2))
		assert.Equal(t, false, page2["Truncated"])
		assert.Len(t, page2["Grants"].([]any), 2)
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListGrants", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for Limit < 1", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListGrants", `{"KeyId":"some-id","Limit":0}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for Limit > 100", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListGrants", `{"KeyId":"some-id","Limit":101}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListGrants",
			`{"KeyId":"00000000-0000-0000-0000-000000000000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 KMSInvalidStateException for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustScheduleKeyDeletion(t, ro, keyID)

		body, _ := json.Marshal(map[string]any{"KeyId": keyID})
		w := kmsReq(t, ro, "ListGrants", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 for malformed ARN KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListGrants",
			`{"KeyId":"arn:aws:kms:us-east-1:123456789012:garbage"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})

	t.Run("200 stale marker triggers binary search", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		mustCreateGrant(t, ro, keyID, "arn:aws:iam::000000000000:role/r", []string{"Decrypt"})
		mustCreateGrant(t, ro, keyID, "arn:aws:iam::000000000000:role/r", []string{"Encrypt"})

		// All-zeros UUID never exists as a grant; it sorts before random UUIDs so binary
		// search sets start=0 and all grants are returned.
		body, _ := json.Marshal(map[string]any{
			"KeyId":  keyID,
			"Marker": "00000000-0000-0000-0000-000000000000",
		})
		w := kmsReq(t, ro, "ListGrants", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Len(t, resp["Grants"].([]any), 2)
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListGrants", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		w := kmsReq(t, ro, "ListGrants",
			`{"KeyId":"00000000-0000-0000-0000-000000000001"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- RevokeGrant ------------------------------------------------------------

func TestHandleRevokeGrant(t *testing.T) {
	t.Run("200 revokes existing grant", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		grantID, _ := mustCreateGrant(
			t,
			ro,
			keyID,
			"arn:aws:iam::000000000000:role/r",
			[]string{"Decrypt"},
		)

		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "GrantId": grantID})
		w := kmsReq(t, ro, "RevokeGrant", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Body.String())

		// Confirm the grant is gone.
		listBody, _ := json.Marshal(map[string]any{"KeyId": keyID})
		lw := kmsReq(t, ro, "ListGrants", string(listBody))
		require.Equal(t, http.StatusOK, lw.Code)
		var listResp map[string]any
		require.NoError(t, json.Unmarshal(lw.Body.Bytes(), &listResp))
		assert.Empty(t, listResp["Grants"].([]any))
	})

	t.Run("400 for missing KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "RevokeGrant", `{"GrantId":"some-id"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for missing GrantId", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{"KeyId": keyID})
		w := kmsReq(t, ro, "RevokeGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 NotFoundException for non-existent grant", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":   keyID,
			"GrantId": "00000000-0000-0000-0000-000000000000",
		})
		w := kmsReq(t, ro, "RevokeGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 NotFoundException for non-existent key", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"KeyId":   "00000000-0000-0000-0000-000000000000",
			"GrantId": "00000000-0000-0000-0000-000000000001",
		})
		w := kmsReq(t, ro, "RevokeGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 KMSInvalidStateException for PendingDeletion key", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		grantID, _ := mustCreateGrant(
			t,
			ro,
			keyID,
			"arn:aws:iam::000000000000:role/r",
			[]string{"Decrypt"},
		)
		mustScheduleKeyDeletion(t, ro, keyID)

		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "GrantId": grantID})
		w := kmsReq(t, ro, "RevokeGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "KMSInvalidStateException")
	})

	t.Run("400 for malformed ARN KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"KeyId":   "arn:aws:kms:us-east-1:123456789012:garbage",
			"GrantId": "00000000-0000-0000-0000-000000000001",
		})
		w := kmsReq(t, ro, "RevokeGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "RevokeGrant", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		body, _ := json.Marshal(map[string]any{
			"KeyId":   "00000000-0000-0000-0000-000000000001",
			"GrantId": "00000000-0000-0000-0000-000000000002",
		})
		w := kmsReq(t, ro, "RevokeGrant", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- RetireGrant ------------------------------------------------------------

func TestHandleRetireGrant(t *testing.T) {
	t.Run("200 retires by GrantToken", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		_, token := mustCreateGrant(
			t,
			ro,
			keyID,
			"arn:aws:iam::000000000000:role/r",
			[]string{"Decrypt"},
		)

		body, _ := json.Marshal(map[string]any{"GrantToken": token})
		w := kmsReq(t, ro, "RetireGrant", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Body.String())
	})

	t.Run("200 retires by KeyId and GrantId", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		grantID, _ := mustCreateGrant(
			t,
			ro,
			keyID,
			"arn:aws:iam::000000000000:role/r",
			[]string{"Decrypt"},
		)

		body, _ := json.Marshal(map[string]any{"KeyId": keyID, "GrantId": grantID})
		w := kmsReq(t, ro, "RetireGrant", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 when no form provided", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "RetireGrant", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 when GrantId provided without KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{"GrantId": "some-id"})
		w := kmsReq(t, ro, "RetireGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 NotFoundException for unknown GrantToken", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(
			map[string]any{"GrantToken": "00000000-0000-0000-0000-000000000000"},
		)
		w := kmsReq(t, ro, "RetireGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 NotFoundException for unknown GrantId", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID := mustCreateKey(t, ro, `{}`)
		body, _ := json.Marshal(map[string]any{
			"KeyId":   keyID,
			"GrantId": "00000000-0000-0000-0000-000000000000",
		})
		w := kmsReq(t, ro, "RetireGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "NotFoundException")
	})

	t.Run("400 when GrantToken and GrantId set but no KeyId", func(t *testing.T) {
		ro := newTestRouter(t)
		// GrantToken != "", GrantId != "", KeyId == "": passes first check, hits second check.
		body, _ := json.Marshal(map[string]any{
			"GrantToken": "some-token",
			"GrantId":    "00000000-0000-0000-0000-000000000000",
		})
		w := kmsReq(t, ro, "RetireGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for malformed ARN in ID-based RetireGrant", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"KeyId":   "arn:aws:kms:us-east-1:123456789012:garbage",
			"GrantId": "00000000-0000-0000-0000-000000000001",
		})
		w := kmsReq(t, ro, "RetireGrant", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "InvalidArnException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "RetireGrant", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("500 on storage failure (token)", func(t *testing.T) {
		ro := newFailRouter()
		body, _ := json.Marshal(map[string]any{"GrantToken": "some-token"})
		w := kmsReq(t, ro, "RetireGrant", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})

	t.Run("500 on storage failure (ID-based)", func(t *testing.T) {
		ro := newFailRouter()
		body, _ := json.Marshal(map[string]any{
			"KeyId":   "00000000-0000-0000-0000-000000000001",
			"GrantId": "00000000-0000-0000-0000-000000000002",
		})
		w := kmsReq(t, ro, "RetireGrant", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}

// ---- ListRetirableGrants ----------------------------------------------------

func TestHandleListRetirableGrants(t *testing.T) {
	t.Run("200 empty when no matching grants", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"RetiringPrincipal": "arn:aws:iam::000000000000:role/admin",
		})
		w := kmsReq(t, ro, "ListRetirableGrants", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, []any{}, resp["Grants"])
		assert.Equal(t, false, resp["Truncated"])
	})

	t.Run("200 returns matching grants across keys", func(t *testing.T) {
		ro := newTestRouter(t)
		keyID1 := mustCreateKey(t, ro, `{}`)
		keyID2 := mustCreateKey(t, ro, `{}`)

		retiringPrincipal := "arn:aws:iam::000000000000:role/admin"
		body1, _ := json.Marshal(map[string]any{
			"KeyId":             keyID1,
			"GranteePrincipal":  "arn:aws:iam::000000000000:role/r",
			"Operations":        []string{"Decrypt"},
			"RetiringPrincipal": retiringPrincipal,
		})
		w1 := kmsReq(t, ro, "CreateGrant", string(body1))
		require.Equal(t, http.StatusOK, w1.Code)
		var r1 map[string]any
		require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &r1))
		grantID1 := r1["GrantId"].(string)

		body2, _ := json.Marshal(map[string]any{
			"KeyId":             keyID2,
			"GranteePrincipal":  "arn:aws:iam::000000000000:role/r",
			"Operations":        []string{"Encrypt"},
			"RetiringPrincipal": retiringPrincipal,
		})
		w2 := kmsReq(t, ro, "CreateGrant", string(body2))
		require.Equal(t, http.StatusOK, w2.Code)
		var r2 map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &r2))
		grantID2 := r2["GrantId"].(string)

		// Grant without RetiringPrincipal should NOT appear.
		mustCreateGrant(t, ro, keyID1, "arn:aws:iam::000000000000:role/r2", []string{"Decrypt"})

		body, _ := json.Marshal(map[string]any{"RetiringPrincipal": retiringPrincipal})
		w := kmsReq(t, ro, "ListRetirableGrants", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		grants := resp["Grants"].([]any)
		assert.Len(t, grants, 2)
		ids := []string{
			grants[0].(map[string]any)["GrantId"].(string),
			grants[1].(map[string]any)["GrantId"].(string),
		}
		assert.ElementsMatch(t, []string{grantID1, grantID2}, ids)
		for _, g := range grants {
			assert.Empty(
				t,
				g.(map[string]any)["GrantToken"],
				"GrantToken must not appear in ListRetirableGrants response",
			)
		}
	})

	t.Run("200 pagination with Limit and Marker", func(t *testing.T) {
		ro := newTestRouter(t)
		retiringPrincipal := "arn:aws:iam::000000000000:role/admin"
		keyID := mustCreateKey(t, ro, `{}`)
		for range 5 {
			body, _ := json.Marshal(map[string]any{
				"KeyId":             keyID,
				"GranteePrincipal":  "arn:aws:iam::000000000000:role/r",
				"Operations":        []string{"Decrypt"},
				"RetiringPrincipal": retiringPrincipal,
			})
			w := kmsReq(t, ro, "CreateGrant", string(body))
			require.Equal(t, http.StatusOK, w.Code)
		}

		body, _ := json.Marshal(map[string]any{"RetiringPrincipal": retiringPrincipal, "Limit": 3})
		w := kmsReq(t, ro, "ListRetirableGrants", string(body))
		assert.Equal(t, http.StatusOK, w.Code)
		var page1 map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page1))
		assert.Equal(t, true, page1["Truncated"])
		assert.Len(t, page1["Grants"].([]any), 3)
		marker := page1["NextMarker"].(string)

		body2, _ := json.Marshal(map[string]any{
			"RetiringPrincipal": retiringPrincipal,
			"Limit":             3,
			"Marker":            marker,
		})
		w2 := kmsReq(t, ro, "ListRetirableGrants", string(body2))
		assert.Equal(t, http.StatusOK, w2.Code)
		var page2 map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &page2))
		assert.Equal(t, false, page2["Truncated"])
		assert.Len(t, page2["Grants"].([]any), 2)
	})

	t.Run("400 for missing RetiringPrincipal", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListRetirableGrants", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for Limit < 1", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"RetiringPrincipal": "arn:aws:iam::000000000000:role/admin",
			"Limit":             0,
		})
		w := kmsReq(t, ro, "ListRetirableGrants", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for Limit > 100", func(t *testing.T) {
		ro := newTestRouter(t)
		body, _ := json.Marshal(map[string]any{
			"RetiringPrincipal": "arn:aws:iam::000000000000:role/admin",
			"Limit":             101,
		})
		w := kmsReq(t, ro, "ListRetirableGrants", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := kmsReq(t, ro, "ListRetirableGrants", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, "ValidationException")
	})

	t.Run("500 on storage failure", func(t *testing.T) {
		ro := newFailRouter()
		body, _ := json.Marshal(map[string]any{
			"RetiringPrincipal": "arn:aws:iam::000000000000:role/admin",
		})
		w := kmsReq(t, ro, "ListRetirableGrants", string(body))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrType(t, w, "KMSInternalException")
	})
}
