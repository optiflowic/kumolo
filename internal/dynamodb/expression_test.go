package dynamodb

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalFilterExpr(t *testing.T) {
	item := map[string]any{
		"pk":    map[string]any{"S": "user1"},
		"name":  map[string]any{"S": "Alice"},
		"age":   map[string]any{"N": "30"},
		"score": map[string]any{"N": "95"},
		"tags": map[string]any{
			"L": []any{map[string]any{"S": "admin"}, map[string]any{"S": "user"}},
		},
	}
	names := map[string]string{
		"#n":     "name",
		"#a":     "age",
		"#score": "score",
		"#tags":  "tags",
		"#nope":  "nonexistent",
	}
	values := map[string]any{
		":alice":  map[string]any{"S": "Alice"},
		":bob":    map[string]any{"S": "Bob"},
		":al":     map[string]any{"S": "Al"},
		":thirty": map[string]any{"N": "30"},
		":twenty": map[string]any{"N": "20"},
		":forty":  map[string]any{"N": "40"},
		":ninety": map[string]any{"N": "90"},
		":admin":  map[string]any{"S": "admin"},
		":ice":    map[string]any{"S": "ice"},
	}

	tests := []struct {
		name    string
		expr    string
		want    bool
		wantErr bool
	}{
		// Equality
		{"eq match", "#n = :alice", true, false},
		{"eq no match", "#n = :bob", false, false},
		// Not equal
		{"neq match", "#n <> :bob", true, false},
		{"neq no match", "#n <> :alice", false, false},
		// Numeric comparisons
		{"lt match", "#a < :forty", true, false},
		{"lt no match", "#a < :twenty", false, false},
		{"lte match eq", "#a <= :thirty", true, false},
		{"lte match lt", "#a <= :forty", true, false},
		{"lte no match", "#a <= :twenty", false, false},
		{"gt match", "#a > :twenty", true, false},
		{"gt no match", "#a > :forty", false, false},
		{"gte match eq", "#a >= :thirty", true, false},
		{"gte match gt", "#a >= :twenty", true, false},
		{"gte no match", "#a >= :forty", false, false},
		// BETWEEN
		{"between match", "#a BETWEEN :twenty AND :forty", true, false},
		{"between at lo", "#a BETWEEN :thirty AND :forty", true, false},
		{"between at hi", "#a BETWEEN :twenty AND :thirty", true, false},
		{"between no match", "#a BETWEEN :twenty AND :twenty", false, false},
		// attribute_exists / attribute_not_exists
		{"attr exists yes", "attribute_exists(#n)", true, false},
		{"attr exists no", "attribute_exists(#nope)", false, false},
		{"attr not exists yes", "attribute_not_exists(#nope)", true, false},
		{"attr not exists no", "attribute_not_exists(#n)", false, false},
		{"attr exists missing name ref", "attribute_exists(#undefined)", false, true},
		{"attr not exists missing name ref", "attribute_not_exists(#undefined)", false, true},
		// begins_with
		{"begins_with match", "begins_with(#n, :al)", true, false},
		{"begins_with no match", "begins_with(#n, :bob)", false, false},
		// contains (string)
		{"contains string match", "contains(#n, :ice)", true, false},
		{"contains string no match", "contains(#n, :bob)", false, false},
		// contains (list)
		{"contains list match", "contains(#tags, :admin)", true, false},
		{"contains list no match", "contains(#tags, :bob)", false, false},
		// AND / OR / NOT
		{"and both true", "#n = :alice AND #a = :thirty", true, false},
		{"and one false", "#n = :alice AND #a = :twenty", false, false},
		{"or one true", "#n = :bob OR #a = :thirty", true, false},
		{"or both false", "#n = :bob OR #a = :twenty", false, false},
		{"not true", "NOT #n = :bob", true, false},
		{"not false", "NOT #n = :alice", false, false},
		// Parentheses
		{"parens", "(#n = :alice OR #n = :bob) AND #a = :thirty", true, false},
		// Plain attr name (no #)
		{"plain attr", "name = :alice", true, false},
		// Missing value ref
		{"missing val ref", "#n = :missing", false, true},
		// Missing name ref
		{"missing name ref", "#missing = :alice", false, true},
		// Invalid expression
		{"invalid expr", "#n !! :alice", false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalFilterExpr(tc.expr, item, names, values)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestApplyFilterExpression(t *testing.T) {
	items := []map[string]any{
		{
			"pk":     map[string]any{"S": "1"},
			"status": map[string]any{"S": "active"},
			"age":    map[string]any{"N": "25"},
		},
		{
			"pk":     map[string]any{"S": "2"},
			"status": map[string]any{"S": "inactive"},
			"age":    map[string]any{"N": "40"},
		},
		{
			"pk":     map[string]any{"S": "3"},
			"status": map[string]any{"S": "active"},
			"age":    map[string]any{"N": "35"},
		},
	}

	t.Run("no filter returns all", func(t *testing.T) {
		got, err := applyFilterExpression(items, "", nil, nil)
		require.NoError(t, err)
		assert.Len(t, got, 3)
	})

	t.Run("filter by equality", func(t *testing.T) {
		got, err := applyFilterExpression(items, "#s = :active",
			map[string]string{"#s": "status"},
			map[string]any{":active": map[string]any{"S": "active"}},
		)
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("filter preserves order", func(t *testing.T) {
		got, err := applyFilterExpression(items, "#a > :thirty",
			map[string]string{"#a": "age"},
			map[string]any{":thirty": map[string]any{"N": "30"}},
		)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "2", got[0]["pk"].(map[string]any)["S"])
		assert.Equal(t, "3", got[1]["pk"].(map[string]any)["S"])
	})

	t.Run("invalid expression returns error", func(t *testing.T) {
		_, err := applyFilterExpression(items, "#a !! :val", nil, nil)
		require.Error(t, err)
	})
}

func TestHandleScanWithFilterExpression(t *testing.T) {
	setup := func(t *testing.T) *Router {
		t.Helper()
		ro := newTestRouter(t)
		require.Equal(t, 200, dynamo(t, ro, "CreateTable", createTableBody).Code)
		for _, item := range []string{
			`{"TableName":"test-table","Item":{"pk":{"S":"1"},"status":{"S":"active"},"age":{"N":"25"}}}`,
			`{"TableName":"test-table","Item":{"pk":{"S":"2"},"status":{"S":"inactive"},"age":{"N":"40"}}}`,
			`{"TableName":"test-table","Item":{"pk":{"S":"3"},"status":{"S":"active"},"age":{"N":"35"}}}`,
		} {
			require.Equal(t, 200, dynamo(t, ro, "PutItem", item).Code)
		}
		return ro
	}

	tests := []struct {
		name        string
		body        string
		wantCount   int
		wantScanned int
	}{
		{
			name:        "no filter returns all",
			body:        `{"TableName":"test-table"}`,
			wantCount:   3,
			wantScanned: 3,
		},
		{
			name: "filter by status",
			body: `{
				"TableName":"test-table",
				"FilterExpression":"#s = :active",
				"ExpressionAttributeNames":{"#s":"status"},
				"ExpressionAttributeValues":{":active":{"S":"active"}}
			}`,
			wantCount:   2,
			wantScanned: 3,
		},
		{
			name: "filter with BETWEEN",
			body: `{
				"TableName":"test-table",
				"FilterExpression":"#a BETWEEN :lo AND :hi",
				"ExpressionAttributeNames":{"#a":"age"},
				"ExpressionAttributeValues":{":lo":{"N":"30"},":hi":{"N":"50"}}
			}`,
			wantCount:   2,
			wantScanned: 3,
		},
		{
			name: "filter matches none",
			body: `{
				"TableName":"test-table",
				"FilterExpression":"#s = :deleted",
				"ExpressionAttributeNames":{"#s":"status"},
				"ExpressionAttributeValues":{":deleted":{"S":"deleted"}}
			}`,
			wantCount:   0,
			wantScanned: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ro := setup(t)
			w := dynamo(t, ro, "Scan", tc.body)
			assert.Equal(t, 200, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, float64(tc.wantCount), resp["Count"])
			assert.Equal(t, float64(tc.wantScanned), resp["ScannedCount"])
		})
	}

	t.Run("invalid FilterExpression returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "Scan", `{
			"TableName":"test-table",
			"FilterExpression":"#s !! :val",
			"ExpressionAttributeNames":{"#s":"status"},
			"ExpressionAttributeValues":{":val":{"S":"x"}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "ValidationException")
	})
}

func TestHandleQueryWithFilterExpression(t *testing.T) {
	setup := func(t *testing.T) *Router {
		t.Helper()
		ro := newTestRouter(t)
		require.Equal(t, 200, dynamo(t, ro, "CreateTable", `{
			"TableName":"orders",
			"KeySchema":[
				{"AttributeName":"userId","KeyType":"HASH"},
				{"AttributeName":"orderId","KeyType":"RANGE"}
			],
			"AttributeDefinitions":[
				{"AttributeName":"userId","AttributeType":"S"},
				{"AttributeName":"orderId","AttributeType":"S"}
			],
			"BillingMode":"PAY_PER_REQUEST"
		}`).Code)
		for _, item := range []string{
			`{"TableName":"orders","Item":{"userId":{"S":"u1"},"orderId":{"S":"o1"},"status":{"S":"shipped"},"amount":{"N":"100"}}}`,
			`{"TableName":"orders","Item":{"userId":{"S":"u1"},"orderId":{"S":"o2"},"status":{"S":"pending"},"amount":{"N":"200"}}}`,
			`{"TableName":"orders","Item":{"userId":{"S":"u1"},"orderId":{"S":"o3"},"status":{"S":"shipped"},"amount":{"N":"50"}}}`,
		} {
			require.Equal(t, 200, dynamo(t, ro, "PutItem", item).Code)
		}
		return ro
	}

	tests := []struct {
		name        string
		body        string
		wantCount   int
		wantScanned int
	}{
		{
			name: "query without filter",
			body: `{
				"TableName":"orders",
				"KeyConditionExpression":"userId = :uid",
				"ExpressionAttributeValues":{":uid":{"S":"u1"}}
			}`,
			wantCount:   3,
			wantScanned: 3,
		},
		{
			name: "query with filter",
			body: `{
				"TableName":"orders",
				"KeyConditionExpression":"userId = :uid",
				"FilterExpression":"#s = :shipped",
				"ExpressionAttributeNames":{"#s":"status"},
				"ExpressionAttributeValues":{":uid":{"S":"u1"},":shipped":{"S":"shipped"}}
			}`,
			wantCount:   2,
			wantScanned: 3,
		},
		{
			name: "query with attribute_exists filter",
			body: `{
				"TableName":"orders",
				"KeyConditionExpression":"userId = :uid",
				"FilterExpression":"attribute_exists(amount)",
				"ExpressionAttributeValues":{":uid":{"S":"u1"}}
			}`,
			wantCount:   3,
			wantScanned: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ro := setup(t)
			w := dynamo(t, ro, "Query", tc.body)
			assert.Equal(t, 200, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, float64(tc.wantCount), resp["Count"])
			assert.Equal(t, float64(tc.wantScanned), resp["ScannedCount"])
		})
	}
}
