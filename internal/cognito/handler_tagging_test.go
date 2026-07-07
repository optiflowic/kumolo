package cognito

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func poolARNFromID(poolID string) string {
	return fmt.Sprintf("arn:aws:cognito-idp:us-east-1:000000000000:userpool/%s", poolID)
}

func TestPoolIDFromARN(t *testing.T) {
	longARN := "arn:aws:cognito-idp:us-east-1:000000000000:userpool/" + strings.Repeat("a", 2000)
	tests := []struct {
		arn    string
		wantID string
		wantOK bool
	}{
		{
			arn:    "arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_abc123",
			wantID: "us-east-1_abc123",
			wantOK: true,
		},
		{
			arn:    "arn:aws:cognito-idp:us-east-1:123456789012:userpool/us-east-1_XYZxyz789",
			wantID: "us-east-1_XYZxyz789",
			wantOK: true,
		},
		{arn: "", wantOK: false},
		{arn: "arn:aws:cognito-idp:us-east-1:000000000000:userpool/", wantOK: false},
		{arn: "arn:aws:cognito-idp:us-east-1:000000000000:somethingelse/id", wantOK: false},
		{arn: "not-an-arn", wantOK: false},
		{arn: "userpool/abc12345", wantOK: false}, // 17 chars — below minimum (20)
		{arn: longARN, wantOK: false},             // 2051 chars — above maximum (2048)
	}
	for _, tc := range tests {
		t.Run(tc.arn, func(t *testing.T) {
			id, ok := poolIDFromARN(tc.arn)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantID, id)
			}
		})
	}
}

func TestTagResource(t *testing.T) {
	t.Run("success — tags are merged", func(t *testing.T) {
		ro := newTestRouter(t)
		poolID := createPool(t, ro, "tag-pool")
		arn := poolARNFromID(poolID)

		// First tag
		w := doOp(t, ro, "TagResource", fmt.Sprintf(
			`{"ResourceArn":%q,"Tags":{"env":"test","owner":"alice"}}`, arn))
		require.Equal(t, http.StatusOK, w.Code)

		// Second tag merges (new key added, existing key updated)
		w = doOp(t, ro, "TagResource", fmt.Sprintf(
			`{"ResourceArn":%q,"Tags":{"owner":"bob","team":"platform"}}`, arn))
		require.Equal(t, http.StatusOK, w.Code)

		// Verify via ListTagsForResource
		w = doOp(t, ro, "ListTagsForResource", fmt.Sprintf(`{"ResourceArn":%q}`, arn))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Tags map[string]string `json:"Tags"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, map[string]string{
			"env":   "test",
			"owner": "bob",
			"team":  "platform",
		}, resp.Tags)
	})

	t.Run("tag limit exceeded", func(t *testing.T) {
		ro := newTestRouter(t)
		poolID := createPool(t, ro, "tag-pool")
		arn := poolARNFromID(poolID)

		// Fill to the limit
		tags := make(map[string]string, maxUserPoolTags)
		for i := range maxUserPoolTags {
			tags[fmt.Sprintf("key%d", i)] = "v"
		}
		body, err := json.Marshal(map[string]any{"ResourceArn": arn, "Tags": tags})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, doOp(t, ro, "TagResource", string(body)).Code)

		// One additional new key must fail
		w := doOp(t, ro, "TagResource", fmt.Sprintf(`{"ResourceArn":%q,"Tags":{"extra":"v"}}`, arn))
		assert.Equal(t, http.StatusBadRequest, w.Code)

		// Overwriting an existing key must still succeed
		w = doOp(
			t,
			ro,
			"TagResource",
			fmt.Sprintf(`{"ResourceArn":%q,"Tags":{"key0":"updated"}}`, arn),
		)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	// Common validation errors that require no pool setup.
	for _, tc := range []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"invalid JSON body", "invalid-json", http.StatusBadRequest},
		{"missing ResourceArn", `{"Tags":{"k":"v"}}`, http.StatusBadRequest},
		{"invalid ARN", `{"ResourceArn":"not-an-arn","Tags":{"k":"v"}}`, http.StatusBadRequest},
		{"empty Tags", fmt.Sprintf(`{"ResourceArn":%q,"Tags":{}}`, poolARNFromID("us-east-1_x")), http.StatusBadRequest},
		{"pool not found", fmt.Sprintf(`{"ResourceArn":%q,"Tags":{"k":"v"}}`, poolARNFromID("us-east-1_notexist")), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := doOp(t, newTestRouter(t), "TagResource", tc.body)
			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}

	t.Run("tag key empty", func(t *testing.T) {
		ro := newTestRouter(t)
		arn := poolARNFromID(createPool(t, ro, "tag-pool"))
		w := doOp(t, ro, "TagResource", fmt.Sprintf(`{"ResourceArn":%q,"Tags":{"":"v"}}`, arn))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("tag key too long", func(t *testing.T) {
		ro := newTestRouter(t)
		arn := poolARNFromID(createPool(t, ro, "tag-pool"))
		longKey := strings.Repeat("k", maxTagKeyLength+1)
		w := doOp(t, ro, "TagResource",
			fmt.Sprintf(`{"ResourceArn":%q,"Tags":{%q:"v"}}`, arn, longKey))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("tag value too long", func(t *testing.T) {
		ro := newTestRouter(t)
		arn := poolARNFromID(createPool(t, ro, "tag-pool"))
		longVal := strings.Repeat("v", maxTagValueLength+1)
		w := doOp(t, ro, "TagResource",
			fmt.Sprintf(`{"ResourceArn":%q,"Tags":{"k":%q}}`, arn, longVal))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("storage error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{updateErr: errors.New("disk full")}}
		arn := "arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_abc123"
		w := doOp(t, ro, "TagResource", fmt.Sprintf(`{"ResourceArn":%q,"Tags":{"k":"v"}}`, arn))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestUntagResource(t *testing.T) {
	t.Run("success — listed keys removed, others preserved", func(t *testing.T) {
		ro := newTestRouter(t)
		poolID := createPool(t, ro, "untag-pool")
		arn := poolARNFromID(poolID)

		// Set initial tags
		w := doOp(t, ro, "TagResource", fmt.Sprintf(
			`{"ResourceArn":%q,"Tags":{"a":"1","b":"2","c":"3"}}`, arn))
		require.Equal(t, http.StatusOK, w.Code)

		// Remove two keys
		w = doOp(t, ro, "UntagResource", fmt.Sprintf(
			`{"ResourceArn":%q,"TagKeys":["a","c"]}`, arn))
		require.Equal(t, http.StatusOK, w.Code)

		// Verify only "b" remains
		w = doOp(t, ro, "ListTagsForResource", fmt.Sprintf(`{"ResourceArn":%q}`, arn))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Tags map[string]string `json:"Tags"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, map[string]string{"b": "2"}, resp.Tags)
	})

	t.Run("non-existent keys are silently ignored", func(t *testing.T) {
		ro := newTestRouter(t)
		arn := poolARNFromID(createPool(t, ro, "untag-pool"))
		w := doOp(t, ro, "UntagResource", fmt.Sprintf(
			`{"ResourceArn":%q,"TagKeys":["doesnotexist"]}`, arn))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	// Common validation errors that require no pool setup.
	for _, tc := range []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"invalid JSON body", "invalid-json", http.StatusBadRequest},
		{"missing ResourceArn", `{"TagKeys":["k"]}`, http.StatusBadRequest},
		{"invalid ARN", `{"ResourceArn":"bad","TagKeys":["k"]}`, http.StatusBadRequest},
		{"empty TagKeys", fmt.Sprintf(`{"ResourceArn":%q,"TagKeys":[]}`, poolARNFromID("us-east-1_x")), http.StatusBadRequest},
		{"pool not found", fmt.Sprintf(`{"ResourceArn":%q,"TagKeys":["k"]}`, poolARNFromID("us-east-1_notexist")), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := doOp(t, newTestRouter(t), "UntagResource", tc.body)
			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}

	t.Run("storage error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{updateErr: errors.New("disk full")}}
		arn := "arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_abc123"
		w := doOp(t, ro, "UntagResource", fmt.Sprintf(`{"ResourceArn":%q,"TagKeys":["k"]}`, arn))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestListTagsForResource(t *testing.T) {
	t.Run("success — returns tags", func(t *testing.T) {
		ro := newTestRouter(t)
		arn := poolARNFromID(createPool(t, ro, "list-tag-pool"))

		w := doOp(t, ro, "TagResource", fmt.Sprintf(
			`{"ResourceArn":%q,"Tags":{"env":"prod"}}`, arn))
		require.Equal(t, http.StatusOK, w.Code)

		w = doOp(t, ro, "ListTagsForResource", fmt.Sprintf(`{"ResourceArn":%q}`, arn))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Tags map[string]string `json:"Tags"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, map[string]string{"env": "prod"}, resp.Tags)
	})

	t.Run("success — empty map when no tags", func(t *testing.T) {
		ro := newTestRouter(t)
		arn := poolARNFromID(createPool(t, ro, "notag-pool"))

		w := doOp(t, ro, "ListTagsForResource", fmt.Sprintf(`{"ResourceArn":%q}`, arn))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Tags map[string]string `json:"Tags"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Empty(t, resp.Tags)
	})

	// Common validation errors that require no pool setup.
	for _, tc := range []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"invalid JSON body", "invalid-json", http.StatusBadRequest},
		{"missing ResourceArn", `{}`, http.StatusBadRequest},
		{"invalid ARN", `{"ResourceArn":"bad"}`, http.StatusBadRequest},
		{"pool not found", fmt.Sprintf(`{"ResourceArn":%q}`, poolARNFromID("us-east-1_notexist")), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := doOp(t, newTestRouter(t), "ListTagsForResource", tc.body)
			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}

	t.Run("storage error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{getErr: errors.New("disk full")}}
		arn := "arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_abc123"
		w := doOp(t, ro, "ListTagsForResource", fmt.Sprintf(`{"ResourceArn":%q}`, arn))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}
