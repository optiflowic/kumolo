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

	t.Run("missing ResourceArn", func(t *testing.T) {
		ro := newTestRouter(t)
		w := doOp(t, ro, "TagResource", `{"Tags":{"k":"v"}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		w := doOp(t, ro, "TagResource", `{"ResourceArn":"not-an-arn","Tags":{"k":"v"}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("empty Tags", func(t *testing.T) {
		ro := newTestRouter(t)
		poolID := createPool(t, ro, "tag-pool")
		arn := poolARNFromID(poolID)
		w := doOp(t, ro, "TagResource", fmt.Sprintf(`{"ResourceArn":%q,"Tags":{}}`, arn))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("pool not found", func(t *testing.T) {
		ro := newTestRouter(t)
		arn := poolARNFromID("us-east-1_notexist")
		w := doOp(t, ro, "TagResource", fmt.Sprintf(
			`{"ResourceArn":%q,"Tags":{"k":"v"}}`, arn))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("tag key empty", func(t *testing.T) {
		ro := newTestRouter(t)
		poolID := createPool(t, ro, "tag-pool")
		arn := poolARNFromID(poolID)
		w := doOp(t, ro, "TagResource", fmt.Sprintf(`{"ResourceArn":%q,"Tags":{"":"v"}}`, arn))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("tag key too long", func(t *testing.T) {
		ro := newTestRouter(t)
		poolID := createPool(t, ro, "tag-pool")
		arn := poolARNFromID(poolID)
		longKey := strings.Repeat("k", 129)
		w := doOp(
			t,
			ro,
			"TagResource",
			fmt.Sprintf(`{"ResourceArn":%q,"Tags":{%q:"v"}}`, arn, longKey),
		)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("tag value too long", func(t *testing.T) {
		ro := newTestRouter(t)
		poolID := createPool(t, ro, "tag-pool")
		arn := poolARNFromID(poolID)
		longVal := strings.Repeat("v", 257)
		w := doOp(
			t,
			ro,
			"TagResource",
			fmt.Sprintf(`{"ResourceArn":%q,"Tags":{"k":%q}}`, arn, longVal),
		)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		ro := newTestRouter(t)
		w := doOp(t, ro, "TagResource", "invalid-json")
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
		poolID := createPool(t, ro, "untag-pool")
		arn := poolARNFromID(poolID)

		w := doOp(t, ro, "UntagResource", fmt.Sprintf(
			`{"ResourceArn":%q,"TagKeys":["doesnotexist"]}`, arn))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("missing ResourceArn", func(t *testing.T) {
		ro := newTestRouter(t)
		w := doOp(t, ro, "UntagResource", `{"TagKeys":["k"]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		w := doOp(t, ro, "UntagResource", `{"ResourceArn":"bad","TagKeys":["k"]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("empty TagKeys", func(t *testing.T) {
		ro := newTestRouter(t)
		poolID := createPool(t, ro, "untag-pool")
		arn := poolARNFromID(poolID)
		w := doOp(t, ro, "UntagResource", fmt.Sprintf(
			`{"ResourceArn":%q,"TagKeys":[]}`, arn))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("pool not found", func(t *testing.T) {
		ro := newTestRouter(t)
		arn := poolARNFromID("us-east-1_notexist")
		w := doOp(t, ro, "UntagResource", fmt.Sprintf(
			`{"ResourceArn":%q,"TagKeys":["k"]}`, arn))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		ro := newTestRouter(t)
		w := doOp(t, ro, "UntagResource", "invalid-json")
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

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
		poolID := createPool(t, ro, "list-tag-pool")
		arn := poolARNFromID(poolID)

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
		poolID := createPool(t, ro, "notag-pool")
		arn := poolARNFromID(poolID)

		w := doOp(t, ro, "ListTagsForResource", fmt.Sprintf(`{"ResourceArn":%q}`, arn))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Tags map[string]string `json:"Tags"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Empty(t, resp.Tags)
	})

	t.Run("missing ResourceArn", func(t *testing.T) {
		ro := newTestRouter(t)
		w := doOp(t, ro, "ListTagsForResource", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		w := doOp(t, ro, "ListTagsForResource", `{"ResourceArn":"bad"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("pool not found", func(t *testing.T) {
		ro := newTestRouter(t)
		arn := poolARNFromID("us-east-1_notexist")
		w := doOp(t, ro, "ListTagsForResource", fmt.Sprintf(`{"ResourceArn":%q}`, arn))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		ro := newTestRouter(t)
		w := doOp(t, ro, "ListTagsForResource", "invalid-json")
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("storage error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{getErr: errors.New("disk full")}}
		arn := "arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_abc123"
		w := doOp(t, ro, "ListTagsForResource", fmt.Sprintf(`{"ResourceArn":%q}`, arn))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}
