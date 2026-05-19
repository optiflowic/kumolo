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
		"labels": map[string]any{"SS": []any{"a", "b", "c"}},
		"meta": map[string]any{"M": map[string]any{
			"k1": map[string]any{"S": "v1"},
			"k2": map[string]any{"S": "v2"},
		}},
		"boolFlag": map[string]any{"BOOL": true},
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
		":zero":   map[string]any{"N": "0"},
		":two":    map[string]any{"N": "2"},
		":three":  map[string]any{"N": "3"},
		":four":   map[string]any{"N": "4"},
		":five":   map[string]any{"N": "5"},
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
		{"not eval error", "NOT #missing = :alice", false, true},
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
		// AND/OR left-side error propagation
		{"and left error", "#missing = :alice AND name = :alice", false, true},
		{"or left error", "#missing = :alice OR name = :alice", false, true},
		// BETWEEN resolve errors
		{"between attr error", "#missing BETWEEN :twenty AND :forty", false, true},
		{"between lo error", "#a BETWEEN :missing AND :forty", false, true},
		{"between hi error", "#a BETWEEN :twenty AND :missing", false, true},
		// begins_with resolve errors and edge cases
		{"begins_with path error", "begins_with(#missing, :al)", false, true},
		{"begins_with prefix error", "begins_with(#n, :missing)", false, true},
		{"begins_with non-map path", "begins_with(nonExistent, :al)", false, false},
		// contains resolve errors and edge cases
		{"contains path error", "contains(#missing, :admin)", false, true},
		{"contains val error", "contains(#tags, :missing)", false, true},
		{"contains non-map path", "contains(nonExistent, :admin)", false, false},
		{"contains string non-string search", "contains(#n, :thirty)", false, false},
		{"contains non-string non-list attr", "contains(#a, :admin)", false, false},
		// parseOperand nameRef and plain ident paths
		{"operand as name ref", "name = #n", true, false},
		{"operand as plain attr", "name = age", false, false},
		// cmpCondNode: dynamoValueCmp type mismatch → false, no error
		{"lt type mismatch no error", "name < :thirty", false, false},
		// size() function
		{"size S string length", "size(name) = :five", true, false},
		{"size S via name ref", "size(#n) = :five", true, false},
		{"size L list length", "size(#tags) = :two", true, false},
		{"size SS set cardinality", "size(labels) = :three", true, false},
		{"size M map count", "size(meta) = :two", true, false},
		{"size missing attr is null", "size(nonExistent) = :zero", false, false},
		{"size BOOL type is error", "size(boolFlag) = :zero", false, true},
		{"size comparison GT", "size(name) > :four", true, false},
		{"size resolve error", "size(#missing) = :five", false, true},
		// size() as right-hand operand (covers parseOperand SIZE branch)
		{"size as rhs operand", "age = size(name)", false, false},
		{"size in between lo", "age BETWEEN size(name) AND :forty", true, false},
		// size() as BETWEEN LHS (covers parseComparison SIZE + BETWEEN path)
		{"size as between lhs", "size(name) BETWEEN :four AND :five", true, false},
		// IN operator
		{"in match first", "name IN (:alice, :bob)", true, false},
		{"in match second", "name IN (:bob, :alice)", true, false},
		{"in no match", "name IN (:bob, :al)", false, false},
		{"in single value match", "name IN (:alice)", true, false},
		{"in single value no match", "name IN (:bob)", false, false},
		{"in with name ref", "#n IN (:alice, :bob)", true, false},
		{"in attr error", "#missing IN (:alice)", false, true},
		{"in value error", "name IN (:missing)", false, true},
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

	t.Run("eval error during filtering returns error", func(t *testing.T) {
		_, err := applyFilterExpression(items, "#undefined = :active",
			map[string]string{},
			map[string]any{":active": map[string]any{"S": "active"}},
		)
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
		{
			name: "filter with IN operator matches two",
			body: `{
				"TableName":"test-table",
				"FilterExpression":"#s IN (:active, :inactive)",
				"ExpressionAttributeNames":{"#s":"status"},
				"ExpressionAttributeValues":{":active":{"S":"active"},":inactive":{"S":"inactive"}}
			}`,
			wantCount:   3,
			wantScanned: 3,
		},
		{
			name: "filter with IN operator matches one",
			body: `{
				"TableName":"test-table",
				"FilterExpression":"#s IN (:inactive)",
				"ExpressionAttributeNames":{"#s":"status"},
				"ExpressionAttributeValues":{":inactive":{"S":"inactive"}}
			}`,
			wantCount:   1,
			wantScanned: 3,
		},
		{
			name: "filter with IN operator matches none",
			body: `{
				"TableName":"test-table",
				"FilterExpression":"#s IN (:deleted)",
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
			assert.IsType(t, []any{}, resp["Items"])
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
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
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
		{
			name: "query filter matches none",
			body: `{
				"TableName":"orders",
				"KeyConditionExpression":"userId = :uid",
				"FilterExpression":"#s = :cancelled",
				"ExpressionAttributeNames":{"#s":"status"},
				"ExpressionAttributeValues":{":uid":{"S":"u1"},":cancelled":{"S":"cancelled"}}
			}`,
			wantCount:   0,
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
			assert.IsType(t, []any{}, resp["Items"])
		})
	}

	t.Run("invalid FilterExpression returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "Query", `{
			"TableName":"orders",
			"KeyConditionExpression":"userId = :uid",
			"FilterExpression":"#s !! :val",
			"ExpressionAttributeNames":{"#s":"status"},
			"ExpressionAttributeValues":{":uid":{"S":"u1"},":val":{"S":"x"}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestParseFilterExprErrors(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		// tokenizeExpr errors
		{"empty name ref", "#"},
		{"empty val ref", ":"},
		// trailing token after valid expression
		{"trailing token", "name = :val )"},
		// parseComparison: no operator after attr path
		{"comparison no operator", "name name"},
		// parseComparison: BETWEEN missing AND
		{"between without and", "age BETWEEN :lo :hi"},
		// parsePrimary: paren errors
		{"paren no close", "(name = :val"},
		{"paren inner error", "(attribute_exists"},
		// parseNot: error in primary
		{"not inner error", "NOT attribute_exists"},
		// parseOr/parseAnd: error in right operand
		{"or right error", "name = :val OR attribute_exists"},
		{"and right error", "name = :val AND attribute_exists"},
		// parseAttrPath: non-path token
		{"attr path got val ref", "attribute_exists(:val)"},
		// parseOperand: non-operand token
		{"operand got paren", "begins_with(name, ("},
		// parseAttrExistsFunc: missing lparen
		{"attr exists no lparen", "attribute_exists #n"},
		// parseAttrExistsFunc: missing rparen
		{"attr exists no rparen", "attribute_exists(name"},
		// parseBeginsWithFunc: missing lparen
		{"begins_with no lparen", "begins_with name, :val)"},
		// parseBeginsWithFunc: attr path is a val ref
		{"begins_with attr path error", "begins_with(:val, :al)"},
		// parseBeginsWithFunc: missing comma
		{"begins_with no comma", "begins_with(name :val)"},
		// parseBeginsWithFunc: bad operand after comma
		{"begins_with operand error", "begins_with(name, ("},
		// parseBeginsWithFunc: missing rparen
		{"begins_with no rparen", "begins_with(name, :val"},
		// parseContainsFunc: missing lparen
		{"contains no lparen", "contains name, :val)"},
		// parseContainsFunc: attr path is a val ref
		{"contains attr path error", "contains(:val, :al)"},
		// parseContainsFunc: missing comma
		{"contains no comma", "contains(name :val)"},
		// parseContainsFunc: bad operand after comma
		{"contains operand error", "contains(name, ("},
		// parseContainsFunc: missing rparen
		{"contains no rparen", "contains(name, :val"},
		// parseComparison: attr path error (val ref where attr path expected)
		{"comparison attr path error", ":val = :alice"},
		// parseComparison: BETWEEN lo/hi operand errors
		{"between lo operand error", "age BETWEEN ( AND :hi"},
		{"between hi operand error", "age BETWEEN :lo AND ("},
		// parseComparison: right operand error
		{"comparison right operand error", "age = ("},
		// parseSizeFunc errors
		{"size no lparen", "size name) = :five"},
		{"size attr path error", "size(:val) = :five"},
		{"size no rparen", "size(name = :five"},
		// IN operator parse errors
		{"in no lparen", "name IN :val)"},
		{"in no rparen", "name IN (:val"},
		{"in bad first operand", "name IN (,"},
		{"in bad subsequent operand", "name IN (:val, ("},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFilterExpr(tc.expr)
			require.Error(t, err)
		})
	}
}

func TestExprNodesDirect(t *testing.T) {
	item := map[string]any{
		"name": map[string]any{"S": "Alice"},
		"age":  map[string]any{"N": "30"},
	}
	names := map[string]string{"#n": "name"}
	values := map[string]any{":alice": map[string]any{"S": "Alice"}}

	t.Run("valRefOperand attrName returns empty string and false", func(t *testing.T) {
		op := valRefOperand{":alice"}
		got, ok := op.attrName(names)
		assert.Equal(t, "", got)
		assert.False(t, ok)
	})

	t.Run("attrExistsCondNode with non-path operand returns error", func(t *testing.T) {
		node := attrExistsCondNode{operand: valRefOperand{":alice"}, negate: false}
		_, err := node.eval(item, names, values)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "attribute_exists")
	})

	t.Run("tokenKindName fallback for unknown kind", func(t *testing.T) {
		got := tokenKindName(tokenKind(999))
		assert.Equal(t, "token(999)", got)
	})

	t.Run("cmpCondNode unknown operator returns error", func(t *testing.T) {
		node := cmpCondNode{
			left:  plainOperand{"name"},
			op:    "???",
			right: valRefOperand{":alice"},
		}
		_, err := node.eval(item, names, values)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown operator")
	})

	t.Run("sizeOperand attrName returns empty string and false", func(t *testing.T) {
		op := sizeOperand{path: plainOperand{"name"}}
		got, ok := op.attrName(nil)
		assert.Equal(t, "", got)
		assert.False(t, ok)
	})

	t.Run("sizeOperand resolve propagates path error", func(t *testing.T) {
		op := sizeOperand{path: nameRefOperand{"#missing"}}
		_, err := op.resolve(map[string]any{}, map[string]string{}, map[string]any{})
		require.Error(t, err)
	})

	t.Run("dynamoAttrSize nil returns 0", func(t *testing.T) {
		n, err := dynamoAttrSize(nil)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	})

	t.Run("dynamoAttrSize non-map returns 0", func(t *testing.T) {
		n, err := dynamoAttrSize("not a map")
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	})

	t.Run("dynamoAttrSize N type returns digit count", func(t *testing.T) {
		n, err := dynamoAttrSize(map[string]any{"N": "42"})
		require.NoError(t, err)
		assert.Equal(t, 2, n)
	})

	t.Run("dynamoAttrSize BS set returns count", func(t *testing.T) {
		n, err := dynamoAttrSize(map[string]any{"BS": []any{"a", "b"}})
		require.NoError(t, err)
		assert.Equal(t, 2, n)
	})

	t.Run("dynamoAttrSize B type returns byte count", func(t *testing.T) {
		// base64("abc") = "YWJj" → 3 bytes
		n, err := dynamoAttrSize(map[string]any{"B": "YWJj"})
		require.NoError(t, err)
		assert.Equal(t, 3, n)
	})

	t.Run("dynamoAttrSize B type invalid base64 returns 0", func(t *testing.T) {
		n, err := dynamoAttrSize(map[string]any{"B": "!!!invalid!!!"})
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	})

	t.Run("dynamoAttrSize unknown type returns error", func(t *testing.T) {
		_, err := dynamoAttrSize(map[string]any{"BOOL": true})
		assert.Error(t, err)
	})
}

func TestParseProjPath(t *testing.T) {
	noNames := map[string]string{}
	named := map[string]string{"#n": "name", "#a": "address"}

	cases := []struct {
		name      string
		token     string
		attrNames map[string]string
		want      []projSegment
		wantErr   bool
	}{
		{
			name:      "simple attribute",
			token:     "name",
			attrNames: noNames,
			want:      []projSegment{{attr: "name", index: 0}},
		},
		{
			name:      "name alias",
			token:     "#n",
			attrNames: named,
			want:      []projSegment{{attr: "name", index: 0}},
		},
		{
			name:      "nested map path",
			token:     "address.city",
			attrNames: noNames,
			want:      []projSegment{{attr: "address", index: 0}, {attr: "city", index: 0}},
		},
		{
			name:      "nested alias path",
			token:     "#a.city",
			attrNames: named,
			want:      []projSegment{{attr: "address", index: 0}, {attr: "city", index: 0}},
		},
		{
			name:      "list index",
			token:     "tags[0]",
			attrNames: noNames,
			want:      []projSegment{{attr: "tags", index: 0}, {attr: "", index: 0}},
		},
		{ //nolint:gosec // G101 false positive: "label" is not a credential
			name:      "list index nested",
			token:     "tags[0].label",
			attrNames: noNames,
			want: []projSegment{
				{attr: "tags", index: 0},
				{attr: "", index: 0},
				{attr: "label", index: 0},
			},
		},
		{
			name:      "multiple list indexes",
			token:     "matrix[1][2]",
			attrNames: noNames,
			want: []projSegment{
				{attr: "matrix", index: 0},
				{attr: "", index: 1},
				{attr: "", index: 2},
			},
		},
		{
			name:      "starts with bracket",
			token:     "[0]",
			attrNames: noNames,
			wantErr:   true,
		},
		{
			name:      "missing closing bracket",
			token:     "tags[0",
			attrNames: noNames,
			wantErr:   true,
		},
		{
			name:      "unknown name alias",
			token:     "#missing",
			attrNames: noNames,
			wantErr:   true,
		},
		{
			name:      "empty dot segment",
			token:     "a..b",
			attrNames: noNames,
			wantErr:   true,
		},
		{
			name:      "unexpected char after closing bracket",
			token:     "tags[0]x",
			attrNames: noNames,
			wantErr:   true,
		},
		{
			name:      "negative list index",
			token:     "tags[-1]",
			attrNames: noNames,
			wantErr:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseProjPath(tc.token, tc.attrNames)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestApplyProjection(t *testing.T) {
	item := map[string]any{
		"pk":   map[string]any{"S": "key1"},
		"name": map[string]any{"S": "Alice"},
		"age":  map[string]any{"N": "30"},
		"address": map[string]any{"M": map[string]any{
			"city": map[string]any{"S": "NYC"},
			"zip":  map[string]any{"S": "10001"},
		}},
		"tags": map[string]any{
			"L": []any{
				map[string]any{"S": "admin"},
				map[string]any{"S": "user"},
				map[string]any{"S": "viewer"},
			},
		},
	}
	noNames := map[string]string{}

	t.Run("empty expression returns item unchanged", func(t *testing.T) {
		got, err := applyProjection(item, "", noNames)
		require.NoError(t, err)
		assert.Equal(t, item, got)
	})

	t.Run("single attribute", func(t *testing.T) {
		got, err := applyProjection(item, "name", noNames)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"name": map[string]any{"S": "Alice"},
		}, got)
	})

	t.Run("multiple top-level attributes", func(t *testing.T) {
		got, err := applyProjection(item, "pk, age", noNames)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"pk":  map[string]any{"S": "key1"},
			"age": map[string]any{"N": "30"},
		}, got)
	})

	t.Run("nested map attribute", func(t *testing.T) {
		got, err := applyProjection(item, "address.city", noNames)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"address": map[string]any{"M": map[string]any{
				"city": map[string]any{"S": "NYC"},
			}},
		}, got)
	})

	t.Run("multiple nested map attributes merged", func(t *testing.T) {
		got, err := applyProjection(item, "address.city, address.zip", noNames)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"address": map[string]any{"M": map[string]any{
				"city": map[string]any{"S": "NYC"},
				"zip":  map[string]any{"S": "10001"},
			}},
		}, got)
	})

	t.Run("list index", func(t *testing.T) {
		got, err := applyProjection(item, "tags[0]", noNames)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"tags": map[string]any{"L": []any{
				map[string]any{"S": "admin"},
			}},
		}, got)
	})

	t.Run("multiple list indexes in order", func(t *testing.T) {
		got, err := applyProjection(item, "tags[2], tags[0]", noNames)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"tags": map[string]any{"L": []any{
				map[string]any{"S": "admin"},
				map[string]any{"S": "viewer"},
			}},
		}, got)
	})

	t.Run("name alias", func(t *testing.T) {
		names := map[string]string{"#n": "name"}
		got, err := applyProjection(item, "#n", names)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"name": map[string]any{"S": "Alice"},
		}, got)
	})

	t.Run("missing attribute silently omitted", func(t *testing.T) {
		got, err := applyProjection(item, "nonexistent", noNames)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{}, got)
	})

	t.Run("invalid expression returns error", func(t *testing.T) {
		_, err := applyProjection(item, "#missing", noNames)
		assert.Error(t, err)
	})

	t.Run("broad path takes precedence over sub-path (broad first)", func(t *testing.T) {
		// AWS: "address, address.city" returns full address, not just city
		got, err := applyProjection(item, "address, address.city", noNames)
		require.NoError(t, err)
		addr := got["address"].(map[string]any)
		inner := addr["M"].(map[string]any)
		assert.NotNil(t, inner["city"])
		assert.NotNil(t, inner["zip"]) // zip must be present because address is whole
	})

	t.Run("broad path takes precedence over sub-path (sub first)", func(t *testing.T) {
		// Order in the expression should not matter
		got, err := applyProjection(item, "address.city, address", noNames)
		require.NoError(t, err)
		addr := got["address"].(map[string]any)
		inner := addr["M"].(map[string]any)
		assert.NotNil(t, inner["city"])
		assert.NotNil(t, inner["zip"])
	})

	t.Run("list broad path takes precedence over list index (broad first)", func(t *testing.T) {
		// "tags, tags[0]" → full tags list returned
		got, err := applyProjection(item, "tags, tags[0]", noNames)
		require.NoError(t, err)
		tags := got["tags"].(map[string]any)
		elems := tags["L"].([]any)
		assert.Len(t, elems, 3) // all elements, not just index 0
	})

	t.Run("list broad path takes precedence over list index (index first)", func(t *testing.T) {
		// Order should not matter
		got, err := applyProjection(item, "tags[0], tags", noNames)
		require.NoError(t, err)
		tags := got["tags"].(map[string]any)
		elems := tags["L"].([]any)
		assert.Len(t, elems, 3)
	})

	t.Run("expression with empty comma tokens skips blanks", func(t *testing.T) {
		got, err := applyProjection(item, "pk,,name", noNames)
		require.NoError(t, err)
		assert.NotNil(t, got["pk"])
		assert.NotNil(t, got["name"])
	})

	t.Run("nested key missing in map is silently omitted", func(t *testing.T) {
		got, err := applyProjection(item, "address.nonexistent", noNames)
		require.NoError(t, err)
		addr := got["address"].(map[string]any)
		inner := addr["M"].(map[string]any)
		assert.Empty(t, inner)
	})

	t.Run("nested path into non-map type returns outer value unchanged", func(t *testing.T) {
		// "name" is {"S":"Alice"}, projecting "name.sub" should return name unchanged
		got, err := applyProjection(item, "name.sub", noNames)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"S": "Alice"}, got["name"])
	})

	t.Run("list index into non-list type returns attribute unchanged", func(t *testing.T) {
		// "name" is {"S":"Alice"}, projecting "name[0]" should return name unchanged
		got, err := applyProjection(item, "name[0]", noNames)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"S": "Alice"}, got["name"])
	})

	t.Run("out-of-range list index returns empty list", func(t *testing.T) {
		got, err := applyProjection(item, "tags[99]", noNames)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"tags": map[string]any{"L": []any{}},
		}, got)
	})
}

func TestProjectValue(t *testing.T) {
	t.Run("returns value unchanged when node is leaf", func(t *testing.T) {
		n := &projNode{}
		val := map[string]any{"S": "hello"}
		assert.Equal(t, val, projectValue(val, n))
	})

	t.Run("returns value unchanged when val is not a map", func(t *testing.T) {
		// defensive: non-map[string]any value with children node
		n := &projNode{children: map[string]*projNode{"x": {}}}
		assert.Equal(t, "not-a-map", projectValue("not-a-map", n))
	})

	t.Run("returns value unchanged when M key is not a map", func(t *testing.T) {
		// defensive: M value is not map[string]any
		n := &projNode{children: map[string]*projNode{"x": {}}}
		val := map[string]any{"M": "not-a-map"}
		assert.Equal(t, val, projectValue(val, n))
	})

	t.Run("returns value unchanged when L key is not a slice", func(t *testing.T) {
		// defensive: L value is not []any
		n := &projNode{listIdxs: map[int]*projNode{0: {}}}
		val := map[string]any{"L": "not-a-slice"}
		assert.Equal(t, val, projectValue(val, n))
	})
}

func TestApplyProjectionToItems(t *testing.T) {
	items := []map[string]any{
		{"pk": map[string]any{"S": "a"}, "val": map[string]any{"N": "1"}},
		{"pk": map[string]any{"S": "b"}, "val": map[string]any{"N": "2"}},
	}
	noNames := map[string]string{}

	t.Run("empty expression returns items unchanged", func(t *testing.T) {
		got, err := applyProjectionToItems(items, "", noNames)
		require.NoError(t, err)
		assert.Equal(t, items, got)
	})

	t.Run("projects each item", func(t *testing.T) {
		got, err := applyProjectionToItems(items, "pk", noNames)
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{
			{"pk": map[string]any{"S": "a"}},
			{"pk": map[string]any{"S": "b"}},
		}, got)
	})
}
