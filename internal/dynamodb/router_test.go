package dynamodb

import (
	"encoding/json"
	"errors"
	"fmt"
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
	s := newTestStorage(t)
	return NewRouter(s)
}

func dynamo(t *testing.T, router http.Handler, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810."+target)
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

const createTableBody = `{
    "TableName": "test-table",
    "KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}],
    "AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}],
    "BillingMode": "PAY_PER_REQUEST"
}`

const createTblBody = `{
	"TableName": "tbl",
	"KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}],
	"AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}],
	"BillingMode": "PAY_PER_REQUEST"
}`

func TestHandleCreateTable(t *testing.T) {
	t.Run("creates table and returns description", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", createTableBody)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["TableDescription"].(map[string]any)
		assert.Equal(t, "test-table", desc["TableName"])
		assert.Equal(t, "ACTIVE", desc["TableStatus"])
		assert.NotEmpty(t, desc["TableArn"])
	})

	t.Run("400 for duplicate table", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "CreateTable", createTableBody)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceInUseException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", `{bad json}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestHandleCreateTable_IndexValidation(t *testing.T) {
	assertValidationError := func(t *testing.T, w *httptest.ResponseRecorder) {
		t.Helper()
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	}

	t.Run("GSI without HASH key rejected", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "t",
			"KeySchema": [{"AttributeName":"pk","KeyType":"HASH"}],
			"AttributeDefinitions": [
				{"AttributeName":"pk","AttributeType":"S"},
				{"AttributeName":"sk","AttributeType":"S"}
			],
			"BillingMode": "PAY_PER_REQUEST",
			"GlobalSecondaryIndexes": [{
				"IndexName": "gsi",
				"KeySchema": [{"AttributeName":"sk","KeyType":"RANGE"}],
				"Projection": {"ProjectionType":"ALL"}
			}]
		}`)
		assertValidationError(t, w)
	})

	t.Run("GSI key attribute missing from AttributeDefinitions rejected", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "t",
			"KeySchema": [{"AttributeName":"pk","KeyType":"HASH"}],
			"AttributeDefinitions": [
				{"AttributeName":"pk","AttributeType":"S"}
			],
			"BillingMode": "PAY_PER_REQUEST",
			"GlobalSecondaryIndexes": [{
				"IndexName": "gsi",
				"KeySchema": [{"AttributeName":"gsi_pk","KeyType":"HASH"}],
				"Projection": {"ProjectionType":"ALL"}
			}]
		}`)
		assertValidationError(t, w)
	})

	t.Run("LSI with different HASH key rejected", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "t",
			"KeySchema": [
				{"AttributeName":"pk","KeyType":"HASH"},
				{"AttributeName":"sk","KeyType":"RANGE"}
			],
			"AttributeDefinitions": [
				{"AttributeName":"pk","AttributeType":"S"},
				{"AttributeName":"sk","AttributeType":"S"},
				{"AttributeName":"other","AttributeType":"S"}
			],
			"BillingMode": "PAY_PER_REQUEST",
			"LocalSecondaryIndexes": [{
				"IndexName": "lsi",
				"KeySchema": [
					{"AttributeName":"other","KeyType":"HASH"},
					{"AttributeName":"sk","KeyType":"RANGE"}
				],
				"Projection": {"ProjectionType":"ALL"}
			}]
		}`)
		assertValidationError(t, w)
	})

	t.Run("LSI key attribute missing from AttributeDefinitions rejected", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "t",
			"KeySchema": [
				{"AttributeName":"pk","KeyType":"HASH"},
				{"AttributeName":"sk","KeyType":"RANGE"}
			],
			"AttributeDefinitions": [
				{"AttributeName":"pk","AttributeType":"S"},
				{"AttributeName":"sk","AttributeType":"S"}
			],
			"BillingMode": "PAY_PER_REQUEST",
			"LocalSecondaryIndexes": [{
				"IndexName": "lsi",
				"KeySchema": [
					{"AttributeName":"pk","KeyType":"HASH"},
					{"AttributeName":"undefined_attr","KeyType":"RANGE"}
				],
				"Projection": {"ProjectionType":"ALL"}
			}]
		}`)
		assertValidationError(t, w)
	})

	t.Run("more than 5 LSIs rejected", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "t",
			"KeySchema": [
				{"AttributeName":"pk","KeyType":"HASH"},
				{"AttributeName":"sk","KeyType":"RANGE"}
			],
			"AttributeDefinitions": [
				{"AttributeName":"pk","AttributeType":"S"},
				{"AttributeName":"sk","AttributeType":"S"},
				{"AttributeName":"s1","AttributeType":"S"},
				{"AttributeName":"s2","AttributeType":"S"},
				{"AttributeName":"s3","AttributeType":"S"},
				{"AttributeName":"s4","AttributeType":"S"},
				{"AttributeName":"s5","AttributeType":"S"},
				{"AttributeName":"s6","AttributeType":"S"}
			],
			"BillingMode": "PAY_PER_REQUEST",
			"LocalSecondaryIndexes": [
				{"IndexName":"l1","KeySchema":[{"AttributeName":"pk","KeyType":"HASH"},{"AttributeName":"s1","KeyType":"RANGE"}],"Projection":{"ProjectionType":"ALL"}},
				{"IndexName":"l2","KeySchema":[{"AttributeName":"pk","KeyType":"HASH"},{"AttributeName":"s2","KeyType":"RANGE"}],"Projection":{"ProjectionType":"ALL"}},
				{"IndexName":"l3","KeySchema":[{"AttributeName":"pk","KeyType":"HASH"},{"AttributeName":"s3","KeyType":"RANGE"}],"Projection":{"ProjectionType":"ALL"}},
				{"IndexName":"l4","KeySchema":[{"AttributeName":"pk","KeyType":"HASH"},{"AttributeName":"s4","KeyType":"RANGE"}],"Projection":{"ProjectionType":"ALL"}},
				{"IndexName":"l5","KeySchema":[{"AttributeName":"pk","KeyType":"HASH"},{"AttributeName":"s5","KeyType":"RANGE"}],"Projection":{"ProjectionType":"ALL"}},
				{"IndexName":"l6","KeySchema":[{"AttributeName":"pk","KeyType":"HASH"},{"AttributeName":"s6","KeyType":"RANGE"}],"Projection":{"ProjectionType":"ALL"}}
			]
		}`)
		assertValidationError(t, w)
	})

	t.Run("table key attribute missing from AttributeDefinitions rejected", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "t",
			"KeySchema": [
				{"AttributeName":"pk","KeyType":"HASH"},
				{"AttributeName":"sk","KeyType":"RANGE"}
			],
			"AttributeDefinitions": [
				{"AttributeName":"pk","AttributeType":"S"}
			],
			"BillingMode": "PAY_PER_REQUEST"
		}`)
		assertValidationError(t, w)
	})

	t.Run("LSI without RANGE key rejected", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "t",
			"KeySchema": [
				{"AttributeName":"pk","KeyType":"HASH"},
				{"AttributeName":"sk","KeyType":"RANGE"}
			],
			"AttributeDefinitions": [
				{"AttributeName":"pk","AttributeType":"S"},
				{"AttributeName":"sk","AttributeType":"S"}
			],
			"BillingMode": "PAY_PER_REQUEST",
			"LocalSecondaryIndexes": [{
				"IndexName": "lsi",
				"KeySchema": [{"AttributeName":"pk","KeyType":"HASH"}],
				"Projection": {"ProjectionType":"ALL"}
			}]
		}`)
		assertValidationError(t, w)
	})

	t.Run("duplicate index name across GSI and LSI rejected", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "t",
			"KeySchema": [
				{"AttributeName":"pk","KeyType":"HASH"},
				{"AttributeName":"sk","KeyType":"RANGE"}
			],
			"AttributeDefinitions": [
				{"AttributeName":"pk","AttributeType":"S"},
				{"AttributeName":"sk","AttributeType":"S"},
				{"AttributeName":"gsi_pk","AttributeType":"S"}
			],
			"BillingMode": "PAY_PER_REQUEST",
			"GlobalSecondaryIndexes": [{
				"IndexName": "my-index",
				"KeySchema": [{"AttributeName":"gsi_pk","KeyType":"HASH"}],
				"Projection": {"ProjectionType":"ALL"}
			}],
			"LocalSecondaryIndexes": [{
				"IndexName": "my-index",
				"KeySchema": [
					{"AttributeName":"pk","KeyType":"HASH"},
					{"AttributeName":"sk","KeyType":"RANGE"}
				],
				"Projection": {"ProjectionType":"ALL"}
			}]
		}`)
		assertValidationError(t, w)
	})

	t.Run("duplicate GSI names rejected", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "t",
			"KeySchema": [{"AttributeName":"pk","KeyType":"HASH"}],
			"AttributeDefinitions": [
				{"AttributeName":"pk","AttributeType":"S"},
				{"AttributeName":"gsi_pk","AttributeType":"S"}
			],
			"BillingMode": "PAY_PER_REQUEST",
			"GlobalSecondaryIndexes": [
				{
					"IndexName": "same-name",
					"KeySchema": [{"AttributeName":"gsi_pk","KeyType":"HASH"}],
					"Projection": {"ProjectionType":"ALL"}
				},
				{
					"IndexName": "same-name",
					"KeySchema": [{"AttributeName":"gsi_pk","KeyType":"HASH"}],
					"Projection": {"ProjectionType":"ALL"}
				}
			]
		}`)
		assertValidationError(t, w)
	})
}

func TestHandleDeleteTable(t *testing.T) {
	t.Run("deletes table and returns DELETING status", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "DeleteTable", `{"TableName": "test-table"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["TableDescription"].(map[string]any)
		assert.Equal(t, "DELETING", desc["TableStatus"])
	})

	t.Run("400 for missing table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DeleteTable", `{"TableName": "no-such-table"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DeleteTable", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DeleteTable", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestHandleDescribeTable(t *testing.T) {
	t.Run("returns table description", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "DescribeTable", `{"TableName": "test-table"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		tbl := resp["Table"].(map[string]any)
		assert.Equal(t, "test-table", tbl["TableName"])
	})

	t.Run(
		"WarmThroughput present with ACTIVE status for PAY_PER_REQUEST table",
		func(t *testing.T) {
			ro := newTestRouter(t)
			require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
			w := dynamo(t, ro, "DescribeTable", `{"TableName": "test-table"}`)
			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			tbl := resp["Table"].(map[string]any)
			wt, ok := tbl["WarmThroughput"].(map[string]any)
			require.True(t, ok, "WarmThroughput must be present")
			assert.Equal(t, "ACTIVE", wt["Status"])
			assert.Equal(t, float64(0), wt["ReadUnitsPerSecond"])
			assert.Equal(t, float64(0), wt["WriteUnitsPerSecond"])
		},
	)

	t.Run("WarmThroughput reflects provisioned capacity for PROVISIONED table", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", `{
			"TableName": "prov-table",
			"KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}],
			"AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}],
			"BillingMode": "PROVISIONED",
			"ProvisionedThroughput": {"ReadCapacityUnits": 5, "WriteCapacityUnits": 10}
		}`).Code)
		w := dynamo(t, ro, "DescribeTable", `{"TableName": "prov-table"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		tbl := resp["Table"].(map[string]any)
		wt, ok := tbl["WarmThroughput"].(map[string]any)
		require.True(t, ok, "WarmThroughput must be present")
		assert.Equal(t, "ACTIVE", wt["Status"])
		assert.Equal(t, float64(5), wt["ReadUnitsPerSecond"])
		assert.Equal(t, float64(10), wt["WriteUnitsPerSecond"])
	})

	t.Run("400 for missing table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeTable", `{"TableName": "no-such-table"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeTable", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeTable", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestHandleListTables(t *testing.T) {
	t.Run("returns empty list when no tables", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "ListTables", `{}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		names := resp["TableNames"].([]any)
		assert.Empty(t, names)
	})

	t.Run("returns table names", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "ListTables", `{}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		names := resp["TableNames"].([]any)
		assert.Equal(t, []any{"test-table"}, names)
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "ListTables", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("returns tables sorted alphabetically", func(t *testing.T) {
		ro := newTestRouter(t)
		for _, name := range []string{"zzz", "aaa", "mmm"} {
			body := fmt.Sprintf(
				`{"TableName": %q, "KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}], "AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}], "BillingMode": "PAY_PER_REQUEST"}`,
				name,
			)
			require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", body).Code)
		}
		w := dynamo(t, ro, "ListTables", `{}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		names := resp["TableNames"].([]any)
		assert.Equal(t, []any{"aaa", "mmm", "zzz"}, names)
		assert.Nil(t, resp["LastEvaluatedTableName"])
	})

	t.Run("paginates with Limit", func(t *testing.T) {
		ro := newTestRouter(t)
		for _, name := range []string{"aaa", "bbb", "ccc"} {
			body := fmt.Sprintf(
				`{"TableName": %q, "KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}], "AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}], "BillingMode": "PAY_PER_REQUEST"}`,
				name,
			)
			require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", body).Code)
		}
		w := dynamo(t, ro, "ListTables", `{"Limit": 2}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		names := resp["TableNames"].([]any)
		assert.Equal(t, []any{"aaa", "bbb"}, names)
		assert.Equal(t, "bbb", resp["LastEvaluatedTableName"])
	})

	t.Run("paginates with ExclusiveStartTableName", func(t *testing.T) {
		ro := newTestRouter(t)
		for _, name := range []string{"aaa", "bbb", "ccc"} {
			body := fmt.Sprintf(
				`{"TableName": %q, "KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}], "AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}], "BillingMode": "PAY_PER_REQUEST"}`,
				name,
			)
			require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", body).Code)
		}
		w := dynamo(t, ro, "ListTables", `{"ExclusiveStartTableName": "bbb"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		names := resp["TableNames"].([]any)
		assert.Equal(t, []any{"ccc"}, names)
		assert.Nil(t, resp["LastEvaluatedTableName"])
	})

	t.Run("resumes alphabetically when cursor table is absent", func(t *testing.T) {
		ro := newTestRouter(t)
		for _, name := range []string{"aaa", "ccc"} {
			body := fmt.Sprintf(
				`{"TableName": %q, "KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}], "AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}], "BillingMode": "PAY_PER_REQUEST"}`,
				name,
			)
			require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", body).Code)
		}
		// "bbb" was never created; cursor should resume from "ccc"
		w := dynamo(t, ro, "ListTables", `{"ExclusiveStartTableName": "bbb"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		names := resp["TableNames"].([]any)
		assert.Equal(t, []any{"ccc"}, names)
		assert.Nil(t, resp["LastEvaluatedTableName"])
	})

	t.Run("400 for out-of-range Limit", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "ListTables", `{"Limit": 0}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for Limit over 100", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "ListTables", `{"Limit": 101}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandlePutItem(t *testing.T) {
	putBody := `{"TableName": "test-table", "Item": {"pk": {"S": "key1"}, "data": {"S": "hello"}}}`

	t.Run("puts item successfully", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "PutItem", putBody)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for missing table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "PutItem", putBody)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for missing key attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "PutItem", `{"TableName": "test-table", "Item": {"other": {"S": "x"}}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "PutItem", `{"Item": {"pk": {"S": "k"}}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "PutItem", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("ReturnValues ALL_OLD returns previous item", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"val":{"S":"old"}}}`).Code)
		w := dynamo(
			t,
			ro,
			"PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"val":{"S":"new"}},"ReturnValues":"ALL_OLD"}`,
		)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "old"}, attrs["val"])
	})

	t.Run("ReturnValues ALL_OLD returns empty when no previous item", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(
			t,
			ro,
			"PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"val":{"S":"new"}},"ReturnValues":"ALL_OLD"}`,
		)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Nil(t, resp["Attributes"])
	})

	t.Run("400 for invalid ReturnValues", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"}},"ReturnValues":"ALL_NEW"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("ConditionExpression attribute_not_exists passes when item absent", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "k1"}},
			"ConditionExpression": "attribute_not_exists(pk)"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("ConditionExpression attribute_not_exists fails when item exists", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"}}}`).Code)
		w := dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "k1"}},
			"ConditionExpression": "attribute_not_exists(pk)"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException")
	})

	t.Run("ConditionExpression attribute_exists passes when item exists", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"v":{"N":"1"}}}`).Code)
		w := dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "k1"},"v":{"N":"2"}},
			"ConditionExpression": "attribute_exists(pk)"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("ConditionExpression with ExpressionAttributeNames", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "k1"}},
			"ConditionExpression": "attribute_not_exists(#p)",
			"ExpressionAttributeNames": {"#p": "pk"}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for invalid ConditionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "k1"}},
			"ConditionExpression": "attribute_not_exists(#missing)",
			"ExpressionAttributeNames": {}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandleGetItem(t *testing.T) {
	t.Run("returns item when found", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"val":{"S":"hello"}}}`).Code)
		w := dynamo(t, ro, "GetItem", `{"TableName":"test-table","Key":{"pk":{"S":"k1"}}}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotNil(t, resp["Item"])
	})

	t.Run("returns empty response when item not found", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "GetItem", `{"TableName":"test-table","Key":{"pk":{"S":"missing"}}}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Nil(t, resp["Item"])
	})

	t.Run("400 for missing table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "GetItem", `{"TableName":"no-such-table","Key":{"pk":{"S":"k"}}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for missing key attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "GetItem", `{"TableName":"test-table","Key":{}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "GetItem", `{"Key":{"pk":{"S":"k"}}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "GetItem", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestHandleDeleteItem(t *testing.T) {
	t.Run("deletes existing item", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"}}}`).Code)
		w := dynamo(t, ro, "DeleteItem", `{"TableName":"test-table","Key":{"pk":{"S":"k1"}}}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("200 when item does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "DeleteItem", `{"TableName":"test-table","Key":{"pk":{"S":"missing"}}}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for missing table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DeleteItem", `{"TableName":"no-such-table","Key":{"pk":{"S":"k"}}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for missing key attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "DeleteItem", `{"TableName":"test-table","Key":{}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DeleteItem", `{"Key":{"pk":{"S":"k"}}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DeleteItem", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("ReturnValues ALL_OLD returns deleted item", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"val":{"S":"hello"}}}`).Code)
		w := dynamo(t, ro, "DeleteItem",
			`{"TableName":"test-table","Key":{"pk":{"S":"k1"}},"ReturnValues":"ALL_OLD"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "hello"}, attrs["val"])
	})

	t.Run("ReturnValues ALL_OLD returns empty when item not found", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "DeleteItem",
			`{"TableName":"test-table","Key":{"pk":{"S":"missing"}},"ReturnValues":"ALL_OLD"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Nil(t, resp["Attributes"])
	})

	t.Run("400 for invalid ReturnValues", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "DeleteItem",
			`{"TableName":"test-table","Key":{"pk":{"S":"k1"}},"ReturnValues":"UPDATED_NEW"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("ConditionExpression attribute_exists passes when item present", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"}}}`).Code)
		w := dynamo(t, ro, "DeleteItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k1"}},
			"ConditionExpression": "attribute_exists(pk)"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("ConditionExpression attribute_exists fails when item absent", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "DeleteItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k1"}},
			"ConditionExpression": "attribute_exists(pk)"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException")
	})

	t.Run("400 for invalid ConditionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "DeleteItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k1"}},
			"ConditionExpression": "attribute_exists(#missing)",
			"ExpressionAttributeNames": {}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandleScan(t *testing.T) {
	t.Run("returns all items", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"a"}}}`).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"b"}}}`).Code)
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(2), resp["Count"])
		assert.Len(t, resp["Items"].([]any), 2)
	})

	t.Run("returns empty Items for empty table", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(0), resp["Count"])
	})

	t.Run("400 for missing table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "Scan", `{"TableName":"no-such-table"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "Scan", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "Scan", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for Limit less than 1", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","Limit":0}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 when only Segment is provided without TotalSegments", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","Segment":0}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 when only TotalSegments is provided without Segment", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","TotalSegments":2}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 when Segment is negative", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","Segment":-1,"TotalSegments":2}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 when TotalSegments is less than 1", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","Segment":0,"TotalSegments":0}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 when Segment is greater than or equal to TotalSegments", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","Segment":3,"TotalSegments":3}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 when Segment exceeds maximum (999999)", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(
			t,
			ro,
			"Scan",
			`{"TableName":"test-table","Segment":1000000,"TotalSegments":1000000}`,
		)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 when TotalSegments exceeds maximum (1000000)", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","Segment":0,"TotalSegments":1000001}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for ExclusiveStartKey with missing key attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan",
			`{"TableName":"test-table","ExclusiveStartKey":{"wrong-attr":{"S":"a"}}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("Limit returns LastEvaluatedKey when more items exist", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		for _, pk := range []string{"a", "b", "c"} {
			require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
				fmt.Sprintf(`{"TableName":"test-table","Item":{"pk":{"S":%q}}}`, pk)).Code)
		}
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","Limit":2}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(2), resp["Count"])
		assert.NotNil(t, resp["LastEvaluatedKey"])
	})

	t.Run("paginated scan covers all items", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		for _, pk := range []string{"a", "b", "c"} {
			require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
				fmt.Sprintf(`{"TableName":"test-table","Item":{"pk":{"S":%q}}}`, pk)).Code)
		}
		var allItems []any
		var esk string
		for {
			body := `{"TableName":"test-table","Limit":1}`
			if esk != "" {
				body = fmt.Sprintf(
					`{"TableName":"test-table","Limit":1,"ExclusiveStartKey":%s}`,
					esk,
				)
			}
			w := dynamo(t, ro, "Scan", body)
			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			allItems = append(allItems, resp["Items"].([]any)...)
			lek := resp["LastEvaluatedKey"]
			if lek == nil {
				break
			}
			b, _ := json.Marshal(lek)
			esk = string(b)
		}
		assert.Len(t, allItems, 3)
	})

	t.Run("parallel scan covers all items across segments", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		for i := range 6 {
			require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
				fmt.Sprintf(`{"TableName":"test-table","Item":{"pk":{"S":"item%d"}}}`, i)).Code)
		}
		var allItems []any
		for seg := range 3 {
			w := dynamo(t, ro, "Scan",
				fmt.Sprintf(`{"TableName":"test-table","Segment":%d,"TotalSegments":3}`, seg))
			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			allItems = append(allItems, resp["Items"].([]any)...)
		}
		assert.Len(t, allItems, 6)
	})

	t.Run("Select COUNT returns Count and ScannedCount without Items", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		for _, pk := range []string{"a", "b", "c"} {
			require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
				fmt.Sprintf(`{"TableName":"test-table","Item":{"pk":{"S":%q}}}`, pk)).Code)
		}
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","Select":"COUNT"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(3), resp["Count"])
		assert.Equal(t, float64(3), resp["ScannedCount"])
		assert.Nil(t, resp["Items"])
	})

	t.Run(
		"Select COUNT with FilterExpression returns different Count and ScannedCount",
		func(t *testing.T) {
			ro := newTestRouter(t)
			require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
			for _, pk := range []string{"a", "b", "c"} {
				require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
					fmt.Sprintf(`{"TableName":"test-table","Item":{"pk":{"S":%q}}}`, pk)).Code)
			}
			w := dynamo(t, ro, "Scan", `{
				"TableName": "test-table",
				"Select": "COUNT",
				"FilterExpression": "pk = :v",
				"ExpressionAttributeValues": {":v": {"S": "a"}}
			}`)
			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, float64(1), resp["Count"])
			assert.Equal(t, float64(3), resp["ScannedCount"])
			assert.Nil(t, resp["Items"])
		},
	)

	t.Run("400 for Select SPECIFIC_ATTRIBUTES without ProjectionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","Select":"SPECIFIC_ATTRIBUTES"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for Select ALL_PROJECTED_ATTRIBUTES on table without index", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","Select":"ALL_PROJECTED_ATTRIBUTES"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for invalid Select value", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","Select":"INVALID"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for Select ALL_ATTRIBUTES with ProjectionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{
			"TableName": "test-table",
			"Select": "ALL_ATTRIBUTES",
			"ProjectionExpression": "pk"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for Select COUNT with ProjectionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Scan", `{
			"TableName": "test-table",
			"Select": "COUNT",
			"ProjectionExpression": "pk"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("Select COUNT with Limit returns LastEvaluatedKey", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		for _, pk := range []string{"a", "b", "c"} {
			require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
				fmt.Sprintf(`{"TableName":"test-table","Item":{"pk":{"S":%q}}}`, pk)).Code)
		}
		w := dynamo(t, ro, "Scan", `{"TableName":"test-table","Select":"COUNT","Limit":2}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(2), resp["Count"])
		assert.Equal(t, float64(2), resp["ScannedCount"])
		assert.NotNil(t, resp["LastEvaluatedKey"])
		assert.Nil(t, resp["Items"])
	})
}

func TestUnknownOperation(t *testing.T) {
	t.Run("501 for unknown target", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UnknownOperation", `{}`)
		assert.Equal(t, http.StatusNotImplemented, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#NotImplemented")
	})
}

const createTableWithSKBody = `{
    "TableName": "test-table",
    "KeySchema": [
        {"AttributeName": "pk", "KeyType": "HASH"},
        {"AttributeName": "sk", "KeyType": "RANGE"}
    ],
    "AttributeDefinitions": [
        {"AttributeName": "pk", "AttributeType": "S"},
        {"AttributeName": "sk", "AttributeType": "S"}
    ],
    "BillingMode": "PAY_PER_REQUEST"
}`

func TestHandleUpdateItem(t *testing.T) {
	t.Run("returns empty response by default (ReturnValues NONE)", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"old":{"S":"x"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "SET #n = :v",
            "ExpressionAttributeNames": {"#n": "name"},
            "ExpressionAttributeValues": {":v": {"S": "Alice"}}
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Nil(t, resp["Attributes"])
	})

	t.Run("returns updated item when ReturnValues is ALL_NEW", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"old":{"S":"x"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "SET #n = :v",
            "ExpressionAttributeNames": {"#n": "name"},
            "ExpressionAttributeValues": {":v": {"S": "Alice"}},
            "ReturnValues": "ALL_NEW"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "Alice"}, attrs["name"])
	})

	t.Run(
		"updates item with REMOVE clause and ALL_NEW returns removed attr absent",
		func(t *testing.T) {
			ro := newTestRouter(t)
			require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
			require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
				`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"old":{"S":"x"}}}`).Code)
			w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "REMOVE old",
            "ReturnValues": "ALL_NEW"
        }`)
			assert.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			attrs := resp["Attributes"].(map[string]any)
			assert.Nil(t, attrs["old"])
		},
	)

	t.Run("creates item if not exists", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "new"}},
            "UpdateExpression": "SET val = :v",
            "ExpressionAttributeValues": {":v": {"N": "1"}}
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("updates item with AttributeUpdates PUT", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "AttributeUpdates": {"name": {"Action": "PUT", "Value": {"S": "Bob"}}}
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("updates item with AttributeUpdates DELETE", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"val":{"S":"x"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "AttributeUpdates": {"val": {"Action": "DELETE"}}
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run(
		"200 with no updates when UpdateExpression and AttributeUpdates are absent",
		func(t *testing.T) {
			ro := newTestRouter(t)
			require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
			require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
				`{"TableName":"test-table","Item":{"pk":{"S":"k1"}}}`).Code)
			w := dynamo(t, ro, "UpdateItem", `{"TableName":"test-table","Key":{"pk":{"S":"k1"}}}`)
			assert.Equal(t, http.StatusOK, w.Code)
		},
	)

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateItem", `{"Key":{"pk":{"S":"k"}}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateItem", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for missing table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName":"no-such-table",
            "Key":{"pk":{"S":"k"}},
            "UpdateExpression":"SET x = :v",
            "ExpressionAttributeValues":{":v":{"S":"1"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for invalid UpdateExpression missing placeholder", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName":"test-table",
            "Key":{"pk":{"S":"k"}},
            "UpdateExpression":"SET x = :missing"
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for unsupported AttributeUpdates Action", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName":"test-table",
            "Key":{"pk":{"S":"k"}},
            "AttributeUpdates":{"x":{"Action":"ADD","Value":{"N":"1"}}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing key attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName":"test-table",
            "Key":{},
            "UpdateExpression":"SET x = :v",
            "ExpressionAttributeValues":{":v":{"S":"1"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("returns old item when ReturnValues is ALL_OLD", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"val":{"S":"old"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "SET val = :v",
            "ExpressionAttributeValues": {":v": {"S": "new"}},
            "ReturnValues": "ALL_OLD"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "old"}, attrs["val"])
		assert.Nil(t, attrs["name"])
	})

	t.Run(
		"returns only changed attrs after update when ReturnValues is UPDATED_NEW",
		func(t *testing.T) {
			ro := newTestRouter(t)
			require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
			require.Equal(t, http.StatusOK, dynamo(
				t,
				ro,
				"PutItem",
				`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"other":{"S":"keep"},"val":{"S":"old"}}}`,
			).Code)
			w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "SET val = :v",
            "ExpressionAttributeValues": {":v": {"S": "new"}},
            "ReturnValues": "UPDATED_NEW"
        }`)
			assert.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			attrs := resp["Attributes"].(map[string]any)
			assert.Equal(t, map[string]any{"S": "new"}, attrs["val"])
			assert.Nil(t, attrs["other"])
			assert.Nil(t, attrs["pk"])
		},
	)

	t.Run(
		"returns only changed attrs before update when ReturnValues is UPDATED_OLD",
		func(t *testing.T) {
			ro := newTestRouter(t)
			require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
			require.Equal(t, http.StatusOK, dynamo(
				t,
				ro,
				"PutItem",
				`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"other":{"S":"keep"},"val":{"S":"old"}}}`,
			).Code)
			w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "SET val = :v",
            "ExpressionAttributeValues": {":v": {"S": "new"}},
            "ReturnValues": "UPDATED_OLD"
        }`)
			assert.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			attrs := resp["Attributes"].(map[string]any)
			assert.Equal(t, map[string]any{"S": "old"}, attrs["val"])
			assert.Nil(t, attrs["other"])
			assert.Nil(t, attrs["pk"])
		},
	)

	t.Run("UPDATED_OLD excludes removed attr absent in original", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"val":{"S":"x"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "REMOVE nonexistent",
            "ReturnValues": "UPDATED_OLD"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Nil(t, resp["Attributes"])
	})

	t.Run("ALL_OLD omits Attributes when item did not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "new"}},
            "UpdateExpression": "SET val = :v",
            "ExpressionAttributeValues": {":v": {"S": "x"}},
            "ReturnValues": "ALL_OLD"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Nil(t, resp["Attributes"])
	})

	t.Run("UPDATED_OLD returns old value of REMOVEd existing attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"val":{"S":"old"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "REMOVE val",
            "ReturnValues": "UPDATED_OLD"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "old"}, attrs["val"])
	})

	t.Run("UPDATED_NEW returns SET attrs when item did not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "new"}},
            "UpdateExpression": "SET val = :v",
            "ExpressionAttributeValues": {":v": {"S": "x"}},
            "ReturnValues": "UPDATED_NEW"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "x"}, attrs["val"])
		assert.Nil(t, attrs["pk"])
	})

	t.Run("UPDATED_OLD omits Attributes when item did not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "new"}},
            "UpdateExpression": "SET val = :v",
            "ExpressionAttributeValues": {":v": {"S": "x"}},
            "ReturnValues": "UPDATED_OLD"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Nil(t, resp["Attributes"])
	})

	t.Run("400 for invalid ReturnValues", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateItem",
			`{"TableName":"test-table","Key":{"pk":{"S":"k1"}},"ReturnValues":"INVALID"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Contains(t, body["message"], "INVALID")
		assert.Contains(t, body["message"], "returnValues")
	})

	t.Run("ADD increments existing Number attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"cnt":{"N":"10"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "ADD cnt :delta",
            "ExpressionAttributeValues": {":delta": {"N": "5"}},
            "ReturnValues": "ALL_NEW"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"N": "15"}, attrs["cnt"])
	})

	t.Run("ADD creates Number attribute when absent", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "ADD views :one",
            "ExpressionAttributeValues": {":one": {"N": "1"}},
            "ReturnValues": "ALL_NEW"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"N": "1"}, attrs["views"])
	})

	t.Run("ADD unions String Set attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"tags":{"SS":["a","b"]}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "ADD tags :new",
            "ExpressionAttributeValues": {":new": {"SS": ["c"]}},
            "ReturnValues": "ALL_NEW"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		tags := attrs["tags"].(map[string]any)["SS"].([]any)
		assert.ElementsMatch(t, []any{"a", "b", "c"}, tags)
	})

	t.Run("ADD ignores duplicate elements in String Set", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"tags":{"SS":["a","b"]}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "ADD tags :dup",
            "ExpressionAttributeValues": {":dup": {"SS": ["b","c"]}},
            "ReturnValues": "ALL_NEW"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		tags := attrs["tags"].(map[string]any)["SS"].([]any)
		assert.ElementsMatch(t, []any{"a", "b", "c"}, tags)
	})

	t.Run("DELETE removes elements from String Set", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"tags":{"SS":["a","b","c"]}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "DELETE tags :rem",
            "ExpressionAttributeValues": {":rem": {"SS": ["b"]}},
            "ReturnValues": "ALL_NEW"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		tags := attrs["tags"].(map[string]any)["SS"].([]any)
		assert.ElementsMatch(t, []any{"a", "c"}, tags)
	})

	t.Run("DELETE removes attribute when all set elements are deleted", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"tags":{"SS":["a"]}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "DELETE tags :rem",
            "ExpressionAttributeValues": {":rem": {"SS": ["a"]}},
            "ReturnValues": "ALL_NEW"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Nil(t, attrs["tags"])
	})

	t.Run("DELETE on absent attribute is a no-op", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "DELETE tags :rem",
            "ExpressionAttributeValues": {":rem": {"SS": ["a"]}},
            "ReturnValues": "ALL_NEW"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Nil(t, attrs["tags"])
	})

	t.Run("400 for ADD with missing ExpressionAttributeValues placeholder", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "ADD cnt :missing"
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("ADD decrements Number attribute with negative delta", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"cnt":{"N":"10"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "ADD cnt :delta",
            "ExpressionAttributeValues": {":delta": {"N": "-3"}},
            "ReturnValues": "ALL_NEW"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"N": "7"}, attrs["cnt"])
	})

	t.Run("ADD with #attrName resolves ExpressionAttributeNames", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"count":{"N":"5"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "ADD #cnt :delta",
            "ExpressionAttributeNames": {"#cnt": "count"},
            "ExpressionAttributeValues": {":delta": {"N": "2"}},
            "ReturnValues": "ALL_NEW"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"N": "7"}, attrs["count"])
	})

	t.Run("DELETE with #attrName resolves ExpressionAttributeNames", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(
			t,
			ro,
			"PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"labels":{"SS":["x","y","z"]}}}`,
		).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "DELETE #lbl :rem",
            "ExpressionAttributeNames": {"#lbl": "labels"},
            "ExpressionAttributeValues": {":rem": {"SS": ["y"]}},
            "ReturnValues": "ALL_NEW"
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		labels := attrs["labels"].(map[string]any)["SS"].([]any)
		assert.ElementsMatch(t, []any{"x", "z"}, labels)
	})

	t.Run("ConditionExpression passes when condition met", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"v":{"N":"1"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k1"}},
			"UpdateExpression": "SET v = :new",
			"ConditionExpression": "#v = :cur",
			"ExpressionAttributeNames": {"#v": "v"},
			"ExpressionAttributeValues": {":cur": {"N": "1"}, ":new": {"N": "2"}}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("ConditionExpression fails when condition not met", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"v":{"N":"5"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k1"}},
			"UpdateExpression": "SET v = :new",
			"ConditionExpression": "#v = :cur",
			"ExpressionAttributeNames": {"#v": "v"},
			"ExpressionAttributeValues": {":cur": {"N": "1"}, ":new": {"N": "2"}}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException")
	})

	t.Run("ConditionExpression attribute_not_exists passes for new item", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "new-key"}},
			"UpdateExpression": "SET v = :v",
			"ConditionExpression": "attribute_not_exists(pk)",
			"ExpressionAttributeValues": {":v": {"N": "1"}}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for invalid ConditionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k1"}},
			"UpdateExpression": "SET v = :v",
			"ConditionExpression": "attribute_exists(#missing)",
			"ExpressionAttributeNames": {},
			"ExpressionAttributeValues": {":v": {"N": "1"}}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandleUpdateItem_InternalErrors(t *testing.T) {
	t.Run("500 when UpdateItem fails with unexpected error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			updateItemFn: func(string, map[string]any, map[string]any) (map[string]any, map[string]any, error) {
				return nil, nil, errInternal
			},
		}}
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName":"t",
            "Key":{"pk":{"S":"k"}},
            "UpdateExpression":"SET x = :v",
            "ExpressionAttributeValues":{":v":{"S":"1"}}
        }`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleQuery(t *testing.T) {
	t.Run("returns matching items by hash key", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"a"},"val":{"S":"1"}}}`).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"b"},"val":{"S":"2"}}}`).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "a"}}
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(1), resp["Count"])
		items := resp["Items"].([]any)
		assert.Len(t, items, 1)
	})

	t.Run("returns matching items using ExpressionAttributeNames", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"x"}}}`).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "#pk = :pk",
            "ExpressionAttributeNames": {"#pk": "pk"},
            "ExpressionAttributeValues": {":pk": {"S": "x"}}
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(1), resp["Count"])
	})

	t.Run("filters by sort key equality", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"p1"},"sk":{"S":"s1"}}}`).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"p1"},"sk":{"S":"s2"}}}`).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND sk = :sk",
            "ExpressionAttributeValues": {":pk": {"S": "p1"}, ":sk": {"S": "s1"}}
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(1), resp["Count"])
		assert.Equal(t, float64(1), resp["ScannedCount"])
	})

	t.Run("filters by sort key BETWEEN", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		for _, sk := range []string{"a", "b", "c", "d"} {
			require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem", `{
                "TableName":"test-table",
                "Item":{"pk":{"S":"p1"},"sk":{"S":"`+sk+`"}}
            }`).Code)
		}
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND sk BETWEEN :lo AND :hi",
            "ExpressionAttributeValues": {":pk":{"S":"p1"},":lo":{"S":"b"},":hi":{"S":"c"}}
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(2), resp["Count"])
	})

	t.Run("filters by sort key begins_with", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		for _, sk := range []string{"foo1", "foo2", "bar1"} {
			require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem", `{
                "TableName":"test-table",
                "Item":{"pk":{"S":"p1"},"sk":{"S":"`+sk+`"}}
            }`).Code)
		}
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND begins_with(sk, :pfx)",
            "ExpressionAttributeValues": {":pk":{"S":"p1"},":pfx":{"S":"foo"}}
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(2), resp["Count"])
	})

	t.Run("filters by sort key comparison operators", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		for _, sk := range []string{"1", "2", "3", "4", "5"} {
			require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem", `{
                "TableName":"test-table",
                "Item":{"pk":{"S":"p1"},"sk":{"N":"`+sk+`"}}
            }`).Code)
		}
		cases := []struct {
			op    string
			skVal string
			want  float64
		}{
			{"<", "3", 2},
			{"<=", "3", 3},
			{">", "3", 2},
			{">=", "3", 3},
		}
		for _, tc := range cases {
			t.Run("sk "+tc.op+" val", func(t *testing.T) {
				w := dynamo(t, ro, "Query", `{
                    "TableName": "test-table",
                    "KeyConditionExpression": "pk = :pk AND sk `+tc.op+` :sk",
                    "ExpressionAttributeValues": {":pk":{"S":"p1"},":sk":{"N":"`+tc.skVal+`"}}
                }`)
				assert.Equal(t, http.StatusOK, w.Code)
				var resp map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				assert.Equal(t, tc.want, resp["Count"])
			})
		}
	})

	t.Run("400 for invalid sort key condition", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND contains(sk, :v)",
            "ExpressionAttributeValues": {":pk":{"S":"p1"},":v":{"S":"x"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for begins_with with no comma (malformed)", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND begins_with(sk)",
            "ExpressionAttributeValues": {":pk":{"S":"p1"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for begins_with with missing ExpressionAttributeNames", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND begins_with(#sk, :pfx)",
            "ExpressionAttributeValues": {":pk":{"S":"p1"},":pfx":{"S":"x"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for begins_with with missing ExpressionAttributeValues", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND begins_with(sk, :missing)",
            "ExpressionAttributeValues": {":pk":{"S":"p1"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for BETWEEN with missing lower bound", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND sk BETWEEN :lo AND :hi",
            "ExpressionAttributeValues": {":pk":{"S":"p1"},":hi":{"S":"z"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for BETWEEN with missing upper bound", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND sk BETWEEN :lo AND :hi",
            "ExpressionAttributeValues": {":pk":{"S":"p1"},":lo":{"S":"a"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for BETWEEN with missing ExpressionAttributeNames", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND #sk BETWEEN :lo AND :hi",
            "ExpressionAttributeValues": {":pk":{"S":"p1"},":lo":{"S":"a"},":hi":{"S":"z"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for comparison with missing ExpressionAttributeNames", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND #sk = :sk",
            "ExpressionAttributeValues": {":pk":{"S":"p1"},":sk":{"S":"s1"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for comparison with unsupported operator", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND sk <> :sk",
            "ExpressionAttributeValues": {":pk":{"S":"p1"},":sk":{"S":"s1"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for comparison with missing ExpressionAttributeValues", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND sk = :missing",
            "ExpressionAttributeValues": {":pk":{"S":"p1"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("filters by sort key using ExpressionAttributeNames alias", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableWithSKBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem", `{
            "TableName": "test-table",
            "Item": {"pk": {"S": "p1"}, "sk": {"S": "s1"}}
        }`).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk AND #s = :sk",
            "ExpressionAttributeNames": {"#s": "sk"},
            "ExpressionAttributeValues": {":pk": {"S": "p1"}, ":sk": {"S": "s1"}}
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(1), resp["Count"])
	})

	t.Run("returns empty Items when no match", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "notfound"}}
        }`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(0), resp["Count"])
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "Query", `{
            "KeyConditionExpression": "pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "a"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing KeyConditionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "Query", `{"TableName":"test-table"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for unsupported KeyConditionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "begins_with(pk, :v)",
            "ExpressionAttributeValues": {":v": {"S": "a"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "Query", `{
            "TableName": "no-such-table",
            "KeyConditionExpression": "pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "a"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "Query", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for missing ExpressionAttributeNames entry", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "#pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "a"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing ExpressionAttributeValues entry", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk"
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("Select COUNT returns Count and ScannedCount without Items", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"a"}}}`).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"b"}}}`).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "Select": "COUNT",
            "KeyConditionExpression": "pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "a"}}
        }`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(1), resp["Count"])
		assert.Equal(t, float64(1), resp["ScannedCount"])
		assert.Nil(t, resp["Items"])
	})

	t.Run("400 for Select SPECIFIC_ATTRIBUTES without ProjectionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "Select": "SPECIFIC_ATTRIBUTES",
            "KeyConditionExpression": "pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "a"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for Select ALL_PROJECTED_ATTRIBUTES without IndexName", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "Select": "ALL_PROJECTED_ATTRIBUTES",
            "KeyConditionExpression": "pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "a"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for invalid Select value", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "Select": "INVALID",
            "KeyConditionExpression": "pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "a"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for Select ALL_ATTRIBUTES with ProjectionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "Select": "ALL_ATTRIBUTES",
            "ProjectionExpression": "pk",
            "KeyConditionExpression": "pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "a"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for Select COUNT with ProjectionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "Select": "COUNT",
            "ProjectionExpression": "pk",
            "KeyConditionExpression": "pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "a"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("Select ALL_PROJECTED_ATTRIBUTES with valid IndexName returns 200", func(t *testing.T) {
		ro := setupGSITable(t)
		w := dynamo(t, ro, "Query", `{
            "TableName": "gsi-table",
            "IndexName": "gsi-index",
            "Select": "ALL_PROJECTED_ATTRIBUTES",
            "KeyConditionExpression": "gsi_pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "g1"}}
        }`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(2), resp["Count"])
	})

	t.Run("400 for Select ALL_PROJECTED_ATTRIBUTES with ProjectionExpression", func(t *testing.T) {
		ro := setupGSITable(t)
		w := dynamo(t, ro, "Query", `{
            "TableName": "gsi-table",
            "IndexName": "gsi-index",
            "Select": "ALL_PROJECTED_ATTRIBUTES",
            "ProjectionExpression": "pk",
            "KeyConditionExpression": "gsi_pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "g1"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("Select COUNT with Limit returns LastEvaluatedKey", func(t *testing.T) {
		ro := setupSkTable(t)
		w := dynamo(t, ro, "Query", `{
            "TableName": "sk-table",
            "Select": "COUNT",
            "KeyConditionExpression": "pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "p"}},
            "Limit": 2
        }`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(2), resp["Count"])
		assert.Equal(t, float64(2), resp["ScannedCount"])
		assert.NotNil(t, resp["LastEvaluatedKey"])
		assert.Nil(t, resp["Items"])
	})
}

func TestHandleQuery_InternalErrors(t *testing.T) {
	t.Run("500 when Query fails with unexpected error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			queryFn: func(string, string, any) ([]map[string]any, error) {
				return nil, errInternal
			},
		}}
		w := dynamo(t, ro, "Query", `{
            "TableName":"t",
            "KeyConditionExpression":"pk = :pk",
            "ExpressionAttributeValues":{":pk":{"S":"a"}}
        }`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestParseUpdateExpression(t *testing.T) {
	tests := []struct {
		name       string
		expr       string
		attrNames  map[string]string
		attrValues map[string]any
		wantErr    bool
		wantKeys   []string
	}{
		{
			name:       "SET single attribute",
			expr:       "SET x = :v",
			attrValues: map[string]any{":v": "val"},
			wantKeys:   []string{"x"},
		},
		{
			name:       "SET with expression attribute name",
			expr:       "SET #n = :v",
			attrNames:  map[string]string{"#n": "name"},
			attrValues: map[string]any{":v": "val"},
			wantKeys:   []string{"name"},
		},
		{
			name:       "REMOVE attribute",
			expr:       "REMOVE old",
			attrValues: map[string]any{},
			wantKeys:   []string{"old"},
		},
		{
			name:    "unsupported clause",
			expr:    "ADD count :one",
			wantErr: true,
		},
		{
			name:    "empty expression",
			expr:    "  ",
			wantErr: true,
		},
		{
			name:       "missing ExpressionAttributeNames",
			expr:       "SET #n = :v",
			attrValues: map[string]any{":v": "val"},
			wantErr:    true,
		},
		{
			name:      "missing ExpressionAttributeValues",
			expr:      "SET x = :v",
			attrNames: map[string]string{},
			wantErr:   true,
		},
		{
			name:       "invalid SET clause (no equals)",
			expr:       "SET x",
			attrValues: map[string]any{},
			wantErr:    true,
		},
		{
			name:       "REMOVE with expression attribute name",
			expr:       "REMOVE #old",
			attrNames:  map[string]string{"#old": "oldField"},
			attrValues: map[string]any{},
			wantKeys:   []string{"oldField"},
		},
		{
			name:       "REMOVE with missing expression attribute name",
			expr:       "REMOVE #old",
			attrValues: map[string]any{},
			wantErr:    true,
		},
		{
			name:       "SET and REMOVE in same expression",
			expr:       "SET x = :v REMOVE old",
			attrValues: map[string]any{":v": "val"},
			wantKeys:   []string{"x", "old"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			updates, err := parseUpdateExpression(tc.expr, tc.attrNames, tc.attrValues)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			for _, k := range tc.wantKeys {
				_, ok := updates[k]
				assert.True(t, ok, "expected key %q in updates", k)
			}
		})
	}
}

func TestReadBodyError(t *testing.T) {
	t.Run("400 when body read fails", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodPost, "/", &errorReader{})
		req.Header.Set("X-Amz-Target", "DynamoDB_20120810.ListTables")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestToTableDescription(t *testing.T) {
	meta := TableMetadata{
		Name:   "my-table",
		Status: "ACTIVE",
		KeySchema: []KeySchemaElement{
			{AttributeName: "pk", KeyType: "HASH"},
		},
	}
	desc := toTableDescription(meta)
	assert.Equal(t, "my-table", desc.TableName)
	assert.Equal(t, "ACTIVE", desc.TableStatus)
	assert.Contains(t, desc.TableArn, "my-table")
}

// assertErrorType checks that the response body contains a DynamoDB error with the given __type.
func assertErrorType(t *testing.T, w *httptest.ResponseRecorder, errType string) {
	t.Helper()
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, errType, resp["__type"])
}

// errorReader always returns an error on Read, used to test body read failure.
type errorReader struct{}

func (e *errorReader) Read(_ []byte) (int, error) {
	return 0, os.ErrClosed
}

// mockStore is a configurable in-memory store for router tests.
type mockStore struct {
	createTableFn                         func(meta TableMetadata) error
	deleteTableFn                         func(name string) error
	describeTableFn                       func(name string) (TableMetadata, error)
	listTablesFn                          func() ([]string, error)
	putItemFn                             func(tableName string, item map[string]any) (map[string]any, error)
	getItemFn                             func(tableName string, key map[string]any) (map[string]any, error)
	deleteItemFn                          func(tableName string, key map[string]any) (map[string]any, error)
	scanFn                                func(tableName string, opts ScanOptions) ([]map[string]any, map[string]any, error)
	updateItemFn                          func(tableName string, key map[string]any, updates map[string]any) (map[string]any, map[string]any, error)
	queryFn                               func(tableName, hashKeyName string, hashKeyValue any) ([]map[string]any, error)
	batchGetItemsFn                       func(tableName string, keys []map[string]any) ([]map[string]any, error)
	batchWriteItemsFn                     func(tableName string, puts []map[string]any, deletes []map[string]any) error
	updateTimeToLiveFn                    func(tableName string, spec TTLSpec) (TTLSpec, error)
	describeTimeToLiveFn                  func(tableName string) (string, *TTLSpec, error)
	tagResourceFn                         func(resourceARN string, tags map[string]string) error
	untagResourceFn                       func(resourceARN string, tagKeys []string) error
	listTagsOfResourceFn                  func(resourceARN string) (map[string]string, error)
	updateTableFn                         func(tableName string, in UpdateTableInput) (TableMetadata, error)
	transactGetItemsFn                    func(gets []TransactGetInput) ([]map[string]any, error)
	transactWriteItemsFn                  func(actions []TransactWriteAction) error
	describeContinuousBackupsFn           func(tableName string) (TableMetadata, error)
	updateContinuousBackupsFn             func(tableName string, enabled bool) (TableMetadata, error)
	describeKinesisStreamingDestinationFn func(tableName string) ([]KinesisDestination, error)
	enableKinesisStreamingDestinationFn   func(tableName, streamARN, precision string) (KinesisDestination, bool, error)
	disableKinesisStreamingDestinationFn  func(tableName, streamARN string) (KinesisDestination, error)
}

func (m *mockStore) CreateTable(meta TableMetadata) error {
	return m.createTableFn(meta)
}

func (m *mockStore) DeleteTable(name string) error {
	return m.deleteTableFn(name)
}

func (m *mockStore) DescribeTable(name string) (TableMetadata, error) {
	return m.describeTableFn(name)
}

func (m *mockStore) ListTables() ([]string, error) {
	return m.listTablesFn()
}

func (m *mockStore) PutItem(
	tableName string,
	item map[string]any,
	_ *ConditionCheck,
) (map[string]any, error) {
	return m.putItemFn(tableName, item)
}

func (m *mockStore) GetItem(tableName string, key map[string]any) (map[string]any, error) {
	return m.getItemFn(tableName, key)
}

func (m *mockStore) DeleteItem(
	tableName string,
	key map[string]any,
	_ *ConditionCheck,
) (map[string]any, error) {
	return m.deleteItemFn(tableName, key)
}

func (m *mockStore) Scan(
	tableName string,
	opts ScanOptions,
) ([]map[string]any, map[string]any, error) {
	return m.scanFn(tableName, opts)
}

func (m *mockStore) UpdateItem(
	tableName string,
	key map[string]any,
	updates map[string]any,
	_ *ConditionCheck,
) (map[string]any, map[string]any, error) {
	return m.updateItemFn(tableName, key, updates)
}

func (m *mockStore) Query(
	tableName, hashKeyName string,
	hashKeyValue any,
	_ *SortKeyCondition,
	_ QueryOptions,
) ([]map[string]any, map[string]any, error) {
	items, err := m.queryFn(tableName, hashKeyName, hashKeyValue)
	return items, nil, err
}

func (m *mockStore) BatchGetItems(
	tableName string,
	keys []map[string]any,
) ([]map[string]any, error) {
	return m.batchGetItemsFn(tableName, keys)
}

func (m *mockStore) BatchWriteItems(
	tableName string,
	puts []map[string]any,
	deletes []map[string]any,
) error {
	return m.batchWriteItemsFn(tableName, puts, deletes)
}

func (m *mockStore) UpdateTimeToLive(tableName string, spec TTLSpec) (TTLSpec, error) {
	return m.updateTimeToLiveFn(tableName, spec)
}

func (m *mockStore) DescribeTimeToLive(tableName string) (string, *TTLSpec, error) {
	return m.describeTimeToLiveFn(tableName)
}

func (m *mockStore) TagResource(resourceARN string, tags map[string]string) error {
	return m.tagResourceFn(resourceARN, tags)
}

func (m *mockStore) UntagResource(resourceARN string, tagKeys []string) error {
	return m.untagResourceFn(resourceARN, tagKeys)
}

func (m *mockStore) ListTagsOfResource(resourceARN string) (map[string]string, error) {
	return m.listTagsOfResourceFn(resourceARN)
}

func (m *mockStore) UpdateTable(tableName string, in UpdateTableInput) (TableMetadata, error) {
	return m.updateTableFn(tableName, in)
}

func (m *mockStore) TransactGetItems(gets []TransactGetInput) ([]map[string]any, error) {
	return m.transactGetItemsFn(gets)
}

func (m *mockStore) TransactWriteItems(actions []TransactWriteAction) error {
	return m.transactWriteItemsFn(actions)
}

func (m *mockStore) DescribeContinuousBackups(tableName string) (TableMetadata, error) {
	return m.describeContinuousBackupsFn(tableName)
}

func (m *mockStore) UpdateContinuousBackups(tableName string, enabled bool) (TableMetadata, error) {
	return m.updateContinuousBackupsFn(tableName, enabled)
}

func (m *mockStore) DescribeKinesisStreamingDestination(
	tableName string,
) ([]KinesisDestination, error) {
	return m.describeKinesisStreamingDestinationFn(tableName)
}

func (m *mockStore) EnableKinesisStreamingDestination(
	tableName, streamARN, precision string,
) (KinesisDestination, bool, error) {
	return m.enableKinesisStreamingDestinationFn(tableName, streamARN, precision)
}

func (m *mockStore) DisableKinesisStreamingDestination(
	tableName, streamARN string,
) (KinesisDestination, error) {
	return m.disableKinesisStreamingDestinationFn(tableName, streamARN)
}

var errInternal = errors.New("internal error")

func TestHandleCreateTable_InternalErrors(t *testing.T) {
	t.Run("500 when CreateTable fails with unexpected error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			createTableFn: func(TableMetadata) error { return errInternal },
		}}
		w := dynamo(t, ro, "CreateTable", createTableBody)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("500 when DescribeTable fails after create", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			createTableFn: func(TableMetadata) error { return nil },
			describeTableFn: func(string) (TableMetadata, error) {
				return TableMetadata{}, errInternal
			},
		}}
		w := dynamo(t, ro, "CreateTable", createTableBody)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleDeleteTable_InternalErrors(t *testing.T) {
	t.Run("500 when DescribeTable fails with unexpected error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			describeTableFn: func(string) (TableMetadata, error) {
				return TableMetadata{}, errInternal
			},
		}}
		w := dynamo(t, ro, "DeleteTable", `{"TableName":"t"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("500 when DeleteTable fails", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			describeTableFn: func(string) (TableMetadata, error) {
				return TableMetadata{Name: "t", Status: "ACTIVE"}, nil
			},
			deleteTableFn: func(string) error { return errInternal },
		}}
		w := dynamo(t, ro, "DeleteTable", `{"TableName":"t"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleDescribeTable_InternalErrors(t *testing.T) {
	t.Run("500 when DescribeTable fails with unexpected error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			describeTableFn: func(string) (TableMetadata, error) {
				return TableMetadata{}, errInternal
			},
		}}
		w := dynamo(t, ro, "DescribeTable", `{"TableName":"t"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleListTables_InternalErrors(t *testing.T) {
	t.Run("500 when ListTables fails", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			listTablesFn: func() ([]string, error) { return nil, errInternal },
		}}
		w := dynamo(t, ro, "ListTables", `{}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandlePutItem_InternalErrors(t *testing.T) {
	t.Run("500 when PutItem fails with unexpected error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			putItemFn: func(string, map[string]any) (map[string]any, error) { return nil, errInternal },
		}}
		w := dynamo(t, ro, "PutItem", `{"TableName":"t","Item":{"pk":{"S":"k"}}}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleGetItem_InternalErrors(t *testing.T) {
	t.Run("500 when GetItem fails with unexpected error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			getItemFn: func(string, map[string]any) (map[string]any, error) {
				return nil, errInternal
			},
		}}
		w := dynamo(t, ro, "GetItem", `{"TableName":"t","Key":{"pk":{"S":"k"}}}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleDeleteItem_InternalErrors(t *testing.T) {
	t.Run("500 when DeleteItem fails with unexpected error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			deleteItemFn: func(string, map[string]any) (map[string]any, error) { return nil, errInternal },
		}}
		w := dynamo(t, ro, "DeleteItem", `{"TableName":"t","Key":{"pk":{"S":"k"}}}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleScan_InternalErrors(t *testing.T) {
	t.Run("500 when Scan fails with unexpected error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			scanFn: func(string, ScanOptions) ([]map[string]any, map[string]any, error) {
				return nil, nil, errInternal
			},
		}}
		w := dynamo(t, ro, "Scan", `{"TableName":"t"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// failResponseWriter implements http.ResponseWriter with a Write that always fails.
type failResponseWriter struct {
	header http.Header
	code   int
}

func (f *failResponseWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}

func (f *failResponseWriter) WriteHeader(code int) { f.code = code }

func (f *failResponseWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestWriteErrorEncoderFail(t *testing.T) {
	t.Run("writeError logs warn when encode fails", func(t *testing.T) {
		w := &failResponseWriter{}
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"test",
		)
		assert.Equal(t, http.StatusBadRequest, w.code)
	})

	t.Run("writeJSON logs warn when encode fails", func(t *testing.T) {
		w := &failResponseWriter{}
		writeJSON(w, http.StatusOK, map[string]any{"k": "v"})
		assert.Equal(t, http.StatusOK, w.code)
	})
}

func TestHandleBatchGetItem(t *testing.T) {
	t.Run("returns found items", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTblBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem", `{
			"TableName": "tbl", "Item": {"pk": {"S": "k1"}}
		}`).Code)
		w := dynamo(t, ro, "BatchGetItem", `{
			"RequestItems": {"tbl": {"Keys": [{"pk": {"S": "k1"}}, {"pk": {"S": "missing"}}]}}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		responses := resp["Responses"].(map[string]any)
		items := responses["tbl"].([]any)
		assert.Len(t, items, 1)
		assert.NotNil(t, resp["UnprocessedKeys"])
	})

	t.Run("returns empty list when no items match", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTblBody).Code)
		w := dynamo(t, ro, "BatchGetItem", `{
			"RequestItems": {"tbl": {"Keys": [{"pk": {"S": "missing"}}]}}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		responses := resp["Responses"].(map[string]any)
		items := responses["tbl"].([]any)
		assert.Empty(t, items)
	})

	t.Run("400 for empty RequestItems", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "BatchGetItem", `{"RequestItems": {}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "BatchGetItem", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for table not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "BatchGetItem", `{
			"RequestItems": {"no-such-table": {"Keys": [{"pk": {"S": "k"}}]}}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for missing key attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTblBody).Code)
		w := dynamo(t, ro, "BatchGetItem", `{
			"RequestItems": {"tbl": {"Keys": [{}]}}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("500 when BatchGetItems fails with unexpected error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			batchGetItemsFn: func(string, []map[string]any) ([]map[string]any, error) {
				return nil, errInternal
			},
		}}
		w := dynamo(t, ro, "BatchGetItem", `{
			"RequestItems": {"tbl": {"Keys": [{"pk": {"S": "k"}}]}}
		}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("400 when total keys exceed 100", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTblBody).Code)
		keys := make([]string, 101)
		for i := range keys {
			keys[i] = fmt.Sprintf(`{"pk": {"S": "k%d"}}`, i)
		}
		body := fmt.Sprintf(`{"RequestItems": {"tbl": {"Keys": [%s]}}}`, strings.Join(keys, ","))
		w := dynamo(t, ro, "BatchGetItem", body)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandleBatchWriteItem(t *testing.T) {
	t.Run("puts items", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTblBody).Code)
		w := dynamo(t, ro, "BatchWriteItem", `{
			"RequestItems": {"tbl": [
				{"PutRequest": {"Item": {"pk": {"S": "k1"}}}},
				{"PutRequest": {"Item": {"pk": {"S": "k2"}}}}
			]}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotNil(t, resp["UnprocessedItems"])
		scan := dynamo(t, ro, "Scan", `{"TableName": "tbl"}`)
		var scanResp map[string]any
		require.NoError(t, json.Unmarshal(scan.Body.Bytes(), &scanResp))
		assert.Equal(t, float64(2), scanResp["Count"])
	})

	t.Run("deletes items", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTblBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem", `{
			"TableName": "tbl", "Item": {"pk": {"S": "k1"}}
		}`).Code)
		w := dynamo(t, ro, "BatchWriteItem", `{
			"RequestItems": {"tbl": [
				{"DeleteRequest": {"Key": {"pk": {"S": "k1"}}}}
			]}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		scan := dynamo(t, ro, "Scan", `{"TableName": "tbl"}`)
		var scanResp map[string]any
		require.NoError(t, json.Unmarshal(scan.Body.Bytes(), &scanResp))
		assert.Equal(t, float64(0), scanResp["Count"])
	})

	t.Run("handles mixed puts and deletes", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTblBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem", `{
			"TableName": "tbl", "Item": {"pk": {"S": "old"}}
		}`).Code)
		w := dynamo(t, ro, "BatchWriteItem", `{
			"RequestItems": {"tbl": [
				{"PutRequest": {"Item": {"pk": {"S": "new"}}}},
				{"DeleteRequest": {"Key": {"pk": {"S": "old"}}}}
			]}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		scan := dynamo(t, ro, "Scan", `{"TableName": "tbl"}`)
		var scanResp map[string]any
		require.NoError(t, json.Unmarshal(scan.Body.Bytes(), &scanResp))
		assert.Equal(t, float64(1), scanResp["Count"])
	})

	t.Run("400 for empty RequestItems", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "BatchWriteItem", `{"RequestItems": {}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "BatchWriteItem", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for table not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "BatchWriteItem", `{
			"RequestItems": {"no-such-table": [{"PutRequest": {"Item": {"pk": {"S": "k"}}}}]}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for missing key attribute in put", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTblBody).Code)
		w := dynamo(t, ro, "BatchWriteItem", `{
			"RequestItems": {"tbl": [{"PutRequest": {"Item": {}}}]}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("500 when BatchWriteItems fails with unexpected error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			batchWriteItemsFn: func(string, []map[string]any, []map[string]any) error {
				return errInternal
			},
		}}
		w := dynamo(t, ro, "BatchWriteItem", `{
			"RequestItems": {"tbl": [{"PutRequest": {"Item": {"pk": {"S": "k"}}}}]}
		}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("400 when total write operations exceed 25", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTblBody).Code)
		ops := make([]string, 26)
		for i := range ops {
			ops[i] = fmt.Sprintf(`{"PutRequest": {"Item": {"pk": {"S": "k%d"}}}}`, i)
		}
		body := fmt.Sprintf(`{"RequestItems": {"tbl": [%s]}}`, strings.Join(ops, ","))
		w := dynamo(t, ro, "BatchWriteItem", body)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandleUpdateTimeToLive(t *testing.T) {
	t.Run("enables TTL", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateTimeToLive", `{
			"TableName": "test-table",
			"TimeToLiveSpecification": {"AttributeName": "expires", "Enabled": true}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		spec := resp["TimeToLiveSpecification"].(map[string]any)
		assert.Equal(t, "expires", spec["AttributeName"])
		assert.Equal(t, true, spec["Enabled"])
	})

	t.Run("disables TTL", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		dynamo(t, ro, "UpdateTimeToLive", `{
			"TableName": "test-table",
			"TimeToLiveSpecification": {"AttributeName": "expires", "Enabled": true}
		}`)
		w := dynamo(t, ro, "UpdateTimeToLive", `{
			"TableName": "test-table",
			"TimeToLiveSpecification": {"AttributeName": "expires", "Enabled": false}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		spec := resp["TimeToLiveSpecification"].(map[string]any)
		assert.Equal(t, false, spec["Enabled"])
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateTimeToLive", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for table not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateTimeToLive", `{
			"TableName": "no-such",
			"TimeToLiveSpecification": {"AttributeName": "exp", "Enabled": true}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateTimeToLive", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("500 when storage fails", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			updateTimeToLiveFn: func(string, TTLSpec) (TTLSpec, error) { return TTLSpec{}, errInternal },
		}}
		w := dynamo(t, ro, "UpdateTimeToLive", `{
			"TableName": "t",
			"TimeToLiveSpecification": {"AttributeName": "exp", "Enabled": true}
		}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleDescribeTimeToLive(t *testing.T) {
	t.Run("returns DISABLED by default", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "DescribeTimeToLive", `{"TableName": "test-table"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["TimeToLiveDescription"].(map[string]any)
		assert.Equal(t, "DISABLED", desc["TimeToLiveStatus"])
	})

	t.Run("returns ENABLED after UpdateTimeToLive", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "UpdateTimeToLive", `{
			"TableName": "test-table",
			"TimeToLiveSpecification": {"AttributeName": "expires", "Enabled": true}
		}`).Code)
		w := dynamo(t, ro, "DescribeTimeToLive", `{"TableName": "test-table"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["TimeToLiveDescription"].(map[string]any)
		assert.Equal(t, "ENABLED", desc["TimeToLiveStatus"])
		assert.Equal(t, "expires", desc["AttributeName"])
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeTimeToLive", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for table not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeTimeToLive", `{"TableName": "no-such"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeTimeToLive", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("500 when storage fails", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			describeTimeToLiveFn: func(string) (string, *TTLSpec, error) {
				return "", nil, errInternal
			},
		}}
		w := dynamo(t, ro, "DescribeTimeToLive", `{"TableName": "t"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleUpdateTable(t *testing.T) {
	t.Run("updates billing mode and sets BillingModeSummary", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateTable", `{
			"TableName": "test-table",
			"BillingMode": "PROVISIONED",
			"ProvisionedThroughput": {"ReadCapacityUnits": 10, "WriteCapacityUnits": 10}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["TableDescription"].(map[string]any)
		assert.Equal(t, "test-table", desc["TableName"])
		assert.Equal(t, "ACTIVE", desc["TableStatus"])
		bms := desc["BillingModeSummary"].(map[string]any)
		assert.Equal(t, "PROVISIONED", bms["BillingMode"])
		assert.NotZero(t, bms["LastUpdateToPayPerRequestDateTime"])
	})

	t.Run("creates GSI and merges AttributeDefinitions", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "UpdateTable", `{
			"TableName": "test-table",
			"AttributeDefinitions": [{"AttributeName": "sk", "AttributeType": "S"}],
			"GlobalSecondaryIndexUpdates": [{"Create": {
				"IndexName": "gsi1",
				"KeySchema": [{"AttributeName": "sk", "KeyType": "HASH"}],
				"Projection": {"ProjectionType": "ALL"}
			}}]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["TableDescription"].(map[string]any)
		gsis := desc["GlobalSecondaryIndexes"].([]any)
		require.Len(t, gsis, 1)
		assert.Equal(t, "gsi1", gsis[0].(map[string]any)["IndexName"])
		attrDefs := desc["AttributeDefinitions"].([]any)
		var attrNames []string
		for _, a := range attrDefs {
			attrNames = append(attrNames, a.(map[string]any)["AttributeName"].(string))
		}
		assert.Contains(t, attrNames, "pk")
		assert.Contains(t, attrNames, "sk")
	})

	t.Run("deletes GSI", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "UpdateTable", `{
			"TableName": "test-table",
			"GlobalSecondaryIndexUpdates": [{"Create": {
				"IndexName": "gsi1",
				"KeySchema": [{"AttributeName": "sk", "KeyType": "HASH"}],
				"Projection": {"ProjectionType": "ALL"}
			}}]
		}`).Code)
		w := dynamo(t, ro, "UpdateTable", `{
			"TableName": "test-table",
			"GlobalSecondaryIndexUpdates": [{"Delete": {"IndexName": "gsi1"}}]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["TableDescription"].(map[string]any)
		assert.Empty(t, desc["GlobalSecondaryIndexes"])
	})

	t.Run("updates GSI throughput", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "UpdateTable", `{
			"TableName": "test-table",
			"GlobalSecondaryIndexUpdates": [{"Create": {
				"IndexName": "gsi1",
				"KeySchema": [{"AttributeName": "sk", "KeyType": "HASH"}],
				"Projection": {"ProjectionType": "ALL"},
				"ProvisionedThroughput": {"ReadCapacityUnits": 5, "WriteCapacityUnits": 5}
			}}]
		}`).Code)
		w := dynamo(t, ro, "UpdateTable", `{
			"TableName": "test-table",
			"GlobalSecondaryIndexUpdates": [{"Update": {
				"IndexName": "gsi1",
				"ProvisionedThroughput": {"ReadCapacityUnits": 20, "WriteCapacityUnits": 20}
			}}]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["TableDescription"].(map[string]any)
		gsis := desc["GlobalSecondaryIndexes"].([]any)
		pt := gsis[0].(map[string]any)["ProvisionedThroughput"].(map[string]any)
		assert.Equal(t, float64(20), pt["ReadCapacityUnits"])
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateTable", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for table not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateTable", `{"TableName": "no-such"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateTable", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("500 when storage fails", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			updateTableFn: func(string, UpdateTableInput) (TableMetadata, error) {
				return TableMetadata{}, errInternal
			},
		}}
		w := dynamo(t, ro, "UpdateTable", `{"TableName": "t"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func tableARNFor(name string) string {
	return "arn:aws:dynamodb:us-east-1:000000000000:table/" + name
}

func TestHandleTagResource(t *testing.T) {
	t.Run("tags resource", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		arn := tableARNFor("test-table")
		w := dynamo(
			t,
			ro,
			"TagResource",
			`{"ResourceArn":"`+arn+`","Tags":[{"Key":"env","Value":"dev"}]}`,
		)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for missing ResourceArn", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "TagResource", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for invalid ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "TagResource", `{"ResourceArn":"invalid","Tags":[]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "TagResource", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("500 when storage fails", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			tagResourceFn: func(string, map[string]string) error { return errInternal },
		}}
		arn := tableARNFor("t")
		w := dynamo(t, ro, "TagResource", `{"ResourceArn":"`+arn+`","Tags":[]}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleUntagResource(t *testing.T) {
	t.Run("untags resource", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		arn := tableARNFor("test-table")
		dynamo(
			t,
			ro,
			"TagResource",
			`{"ResourceArn":"`+arn+`","Tags":[{"Key":"env","Value":"dev"}]}`,
		)
		w := dynamo(t, ro, "UntagResource", `{"ResourceArn":"`+arn+`","TagKeys":["env"]}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 for missing ResourceArn", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UntagResource", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for invalid ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UntagResource", `{"ResourceArn":"invalid","TagKeys":[]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UntagResource", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("500 when storage fails", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			untagResourceFn: func(string, []string) error { return errInternal },
		}}
		arn := tableARNFor("t")
		w := dynamo(t, ro, "UntagResource", `{"ResourceArn":"`+arn+`","TagKeys":[]}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleListTagsOfResource(t *testing.T) {
	t.Run("lists tags", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		arn := tableARNFor("test-table")
		dynamo(
			t,
			ro,
			"TagResource",
			`{"ResourceArn":"`+arn+`","Tags":[{"Key":"env","Value":"dev"},{"Key":"app","Value":"kumolo"}]}`,
		)
		w := dynamo(t, ro, "ListTagsOfResource", `{"ResourceArn":"`+arn+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		tags := resp["Tags"].([]any)
		assert.Len(t, tags, 2)
	})

	t.Run("returns empty Tags for untagged resource", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		arn := tableARNFor("test-table")
		w := dynamo(t, ro, "ListTagsOfResource", `{"ResourceArn":"`+arn+`"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		tags := resp["Tags"].([]any)
		assert.Empty(t, tags)
	})

	t.Run("400 for missing ResourceArn", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "ListTagsOfResource", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for invalid ARN", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "ListTagsOfResource", `{"ResourceArn":"invalid"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "ListTagsOfResource", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("500 when storage fails", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			listTagsOfResourceFn: func(string) (map[string]string, error) {
				return nil, errInternal
			},
		}}
		arn := tableARNFor("t")
		w := dynamo(t, ro, "ListTagsOfResource", `{"ResourceArn":"`+arn+`"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleDescribeLimits(t *testing.T) {
	t.Run("returns hardcoded capacity limits", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeLimits", `{}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(80000), resp["AccountMaxReadCapacityUnits"])
		assert.Equal(t, float64(80000), resp["AccountMaxWriteCapacityUnits"])
		assert.Equal(t, float64(40000), resp["TableMaxReadCapacityUnits"])
		assert.Equal(t, float64(40000), resp["TableMaxWriteCapacityUnits"])
	})
}

func TestHandleDescribeEndpoints(t *testing.T) {
	t.Run("returns static localhost endpoint", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeEndpoints", `{}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		endpoints := resp["Endpoints"].([]any)
		require.Len(t, endpoints, 1)
		ep := endpoints[0].(map[string]any)
		assert.Equal(t, "localhost:5566", ep["Address"])
		assert.Equal(t, float64(1440), ep["CachePeriodInMinutes"])
	})
}

// --- parseUpdateExpression error paths ---

func TestParseUpdateExpression_ErrorPaths(t *testing.T) {
	noNames := map[string]string{}
	noVals := map[string]any{}

	t.Run("ADD clause with invalid token count", func(t *testing.T) {
		_, err := parseUpdateExpression("ADD attr", noNames, noVals)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid ADD clause")
	})

	t.Run("ADD clause with unknown #attrName", func(t *testing.T) {
		_, err := parseUpdateExpression(
			"ADD #missing :v",
			noNames,
			map[string]any{":v": map[string]any{"N": "1"}},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ExpressionAttributeNames missing")
	})

	t.Run("DELETE clause with invalid token count", func(t *testing.T) {
		_, err := parseUpdateExpression("DELETE attr", noNames, noVals)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid DELETE clause")
	})

	t.Run("DELETE clause with unknown #attrName", func(t *testing.T) {
		_, err := parseUpdateExpression(
			"DELETE #missing :v",
			noNames,
			map[string]any{":v": map[string]any{"SS": []any{"a"}}},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ExpressionAttributeNames missing")
	})

	t.Run("DELETE clause with missing ExpressionAttributeValues placeholder", func(t *testing.T) {
		_, err := parseUpdateExpression("DELETE attr :missing", noNames, noVals)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ExpressionAttributeValues missing")
	})

	t.Run("SET if_not_exists missing closing paren", func(t *testing.T) {
		_, err := parseUpdateExpression(
			"SET a = if_not_exists(a, :v",
			noNames,
			map[string]any{":v": map[string]any{"N": "1"}},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid if_not_exists")
	})

	t.Run("SET if_not_exists single arg no comma", func(t *testing.T) {
		_, err := parseUpdateExpression("SET a = if_not_exists(only_one)", noNames, noVals)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid if_not_exists")
	})

	t.Run("SET if_not_exists first arg is value ref (AWS rejects this)", func(t *testing.T) {
		_, err := parseUpdateExpression(
			"SET a = if_not_exists(:val, :fallback)",
			noNames,
			map[string]any{
				":val":      map[string]any{"N": "1"},
				":fallback": map[string]any{"N": "0"},
			},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "first argument must be a path")
	})

	t.Run(
		"SET if_not_exists check arg #name missing from ExpressionAttributeNames",
		func(t *testing.T) {
			_, err := parseUpdateExpression(
				"SET a = if_not_exists(#missing, :fallback)",
				noNames,
				map[string]any{":fallback": map[string]any{"N": "1"}},
			)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "ExpressionAttributeNames missing")
		},
	)

	t.Run(
		"SET if_not_exists nested path arg #name missing from ExpressionAttributeNames",
		func(t *testing.T) {
			_, err := parseUpdateExpression(
				"SET a = if_not_exists(#a.#missing, :fallback)",
				map[string]string{"#a": "a"},
				map[string]any{":fallback": map[string]any{"N": "1"}},
			)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "ExpressionAttributeNames missing")
		},
	)

	t.Run("SET if_not_exists second arg is function call (AWS rejects this)", func(t *testing.T) {
		_, err := parseUpdateExpression(
			"SET a = if_not_exists(b, if_not_exists(c, :v))",
			noNames,
			map[string]any{":v": map[string]any{"N": "0"}},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "second argument cannot be a function call")
	})

	t.Run("SET list_append missing closing paren", func(t *testing.T) {
		_, err := parseUpdateExpression(
			"SET a = list_append(a, :v",
			noNames,
			map[string]any{":v": map[string]any{"L": []any{}}},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid list_append")
	})

	t.Run("SET list_append single arg no comma", func(t *testing.T) {
		_, err := parseUpdateExpression("SET a = list_append(only_one)", noNames, noVals)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid list_append")
	})

	t.Run("SET list_append left arg missing from ExpressionAttributeValues", func(t *testing.T) {
		_, err := parseUpdateExpression(
			"SET a = list_append(:missing, :right)",
			noNames,
			map[string]any{":right": map[string]any{"L": []any{}}},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ExpressionAttributeValues missing")
	})

	t.Run(
		"SET list_append left arg #name missing from ExpressionAttributeNames",
		func(t *testing.T) {
			_, err := parseUpdateExpression(
				"SET a = list_append(#missing, :right)",
				noNames,
				map[string]any{":right": map[string]any{"L": []any{}}},
			)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "ExpressionAttributeNames missing")
		},
	)

	t.Run("SET nested path with leading bracket is invalid", func(t *testing.T) {
		// "[0].field = :v" — parseUpdatePath rejects paths that start with '['
		_, err := parseUpdateExpression(
			"SET [0].field = :v",
			noNames,
			map[string]any{":v": map[string]any{"S": "x"}},
		)
		assert.Error(t, err)
	})

	t.Run("SET nested path with missing closing bracket is invalid", func(t *testing.T) {
		_, err := parseUpdateExpression(
			"SET a[0 = :v",
			noNames,
			map[string]any{":v": map[string]any{"S": "x"}},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing ']'")
	})

	t.Run("SET nested path with non-numeric index is invalid", func(t *testing.T) {
		_, err := parseUpdateExpression(
			"SET a[abc] = :v",
			noNames,
			map[string]any{":v": map[string]any{"S": "x"}},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid list index")
	})

	t.Run("SET nested path with negative index is invalid", func(t *testing.T) {
		_, err := parseUpdateExpression(
			"SET a[-1] = :v",
			noNames,
			map[string]any{":v": map[string]any{"S": "x"}},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid list index")
	})

	t.Run("SET nested path with trailing garbage after index is invalid", func(t *testing.T) {
		// "a[0]extra" — after consuming [0], 'e' is not '[', triggering unexpected-char error.
		_, err := parseUpdateExpression(
			"SET a[0]extra = :v",
			noNames,
			map[string]any{":v": map[string]any{"S": "x"}},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected")
	})

	t.Run("SET path with empty segment between dots is invalid", func(t *testing.T) {
		// "a..b" splits into ["a", "", "b"]; the empty segment triggers the invalid-path error.
		_, err := parseUpdateExpression(
			"SET a..b = :v",
			noNames,
			map[string]any{":v": map[string]any{"S": "x"}},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid path")
	})

	t.Run("SET path with 33 dereferences exceeds limit", func(t *testing.T) {
		// 34 segments = 33 dereference operators, one more than the allowed 32.
		path := "a.b.c.d.e.f.g.h.i.j.k.l.m.n.o.p.q.r.s.t.u.v.w.x.y.z.a1.b1.c1.d1.e1.f1.g1.h1"
		_, err := parseUpdateExpression(
			"SET "+path+" = :v",
			noNames,
			map[string]any{":v": map[string]any{"S": "x"}},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "nesting levels")
	})
}

// --- applyAddOp unit tests ---

func TestApplyAddOp(t *testing.T) {
	t.Run("error when delta is not a map", func(t *testing.T) {
		_, err := applyAddOp(nil, "raw_string")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delta must be a DynamoDB typed value")
	})

	t.Run("error when delta N value is not a valid number", func(t *testing.T) {
		_, err := applyAddOp(nil, map[string]any{"N": "abc"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid number")
	})

	t.Run("error when existing attribute is not a typed value (Number path)", func(t *testing.T) {
		_, err := applyAddOp("raw_string", map[string]any{"N": "1"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "existing attribute is not a typed value")
	})

	t.Run("error when existing attribute is not a Number", func(t *testing.T) {
		_, err := applyAddOp(map[string]any{"S": "x"}, map[string]any{"N": "1"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "existing attribute is not a Number")
	})

	t.Run("error when existing N value is not a valid number", func(t *testing.T) {
		_, err := applyAddOp(map[string]any{"N": "invalid"}, map[string]any{"N": "1"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid existing number")
	})

	t.Run("error when existing attribute is not a typed value (Set path)", func(t *testing.T) {
		_, err := applyAddOp("raw_string", map[string]any{"SS": []any{"a"}})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "existing attribute is not a typed value")
	})

	t.Run("error when existing attribute type mismatches set type", func(t *testing.T) {
		_, err := applyAddOp(map[string]any{"S": "x"}, map[string]any{"SS": []any{"a"}})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "type mismatch")
	})

	t.Run("error when delta type is not supported (not N or set)", func(t *testing.T) {
		_, err := applyAddOp(nil, map[string]any{"BOOL": true})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported type")
	})
}

// --- applyDeleteOp unit tests ---

func TestApplyDeleteOp(t *testing.T) {
	t.Run("error when delta is not a map", func(t *testing.T) {
		_, err := applyDeleteOp(map[string]any{"SS": []any{"a"}}, "raw_string")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delta must be a DynamoDB typed value")
	})

	t.Run("error when existing attribute is not a typed value", func(t *testing.T) {
		_, err := applyDeleteOp("raw_string", map[string]any{"SS": []any{"a"}})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "existing attribute is not a typed value")
	})

	t.Run("error when existing attribute set type mismatches delta set type", func(t *testing.T) {
		_, err := applyDeleteOp(map[string]any{"NS": []any{"1"}}, map[string]any{"SS": []any{"a"}})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "existing attribute is not a SS")
	})

	t.Run("error when delta type is not a set type", func(t *testing.T) {
		_, err := applyDeleteOp(map[string]any{"SS": []any{"a"}}, map[string]any{"N": "1"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported type")
	})
}

// --- ProjectionExpression tests ---

const projTableBody = `{
	"TableName": "proj-table",
	"KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}],
	"AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}],
	"BillingMode": "PAY_PER_REQUEST"
}`

// projItem is the JSON for a rich test item with nested map and list.
const projItemJSON = `{
	"TableName": "proj-table",
	"Item": {
		"pk":   {"S": "k1"},
		"name": {"S": "Alice"},
		"age":  {"N": "30"},
		"address": {"M": {
			"city": {"S": "NYC"},
			"zip":  {"S": "10001"}
		}},
		"tags": {"L": [
			{"S": "admin"},
			{"S": "user"},
			{"S": "viewer"}
		]}
	}
}`

func setupProjTable(t *testing.T) *Router {
	t.Helper()
	ro := newTestRouter(t)
	require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", projTableBody).Code)
	require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem", projItemJSON).Code)
	return ro
}

func TestHandleGetItem_ProjectionExpression(t *testing.T) {
	t.Run("projects single attribute", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "GetItem", `{
			"TableName": "proj-table",
			"Key": {"pk": {"S": "k1"}},
			"ProjectionExpression": "name"
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		item := resp["Item"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "Alice"}, item["name"])
		assert.Nil(t, item["pk"])
		assert.Nil(t, item["age"])
	})

	t.Run("projects multiple attributes", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "GetItem", `{
			"TableName": "proj-table",
			"Key": {"pk": {"S": "k1"}},
			"ProjectionExpression": "pk, age"
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		item := resp["Item"].(map[string]any)
		assert.NotNil(t, item["pk"])
		assert.NotNil(t, item["age"])
		assert.Nil(t, item["name"])
	})

	t.Run("projects nested map attribute", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "GetItem", `{
			"TableName": "proj-table",
			"Key": {"pk": {"S": "k1"}},
			"ProjectionExpression": "address.city"
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		item := resp["Item"].(map[string]any)
		addr := item["address"].(map[string]any)
		inner := addr["M"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "NYC"}, inner["city"])
		assert.Nil(t, inner["zip"])
	})

	t.Run("projects list index", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "GetItem", `{
			"TableName": "proj-table",
			"Key": {"pk": {"S": "k1"}},
			"ProjectionExpression": "tags[0]"
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		item := resp["Item"].(map[string]any)
		tags := item["tags"].(map[string]any)
		elems := tags["L"].([]any)
		assert.Len(t, elems, 1)
		assert.Equal(t, map[string]any{"S": "admin"}, elems[0])
	})

	t.Run("uses ExpressionAttributeNames alias", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "GetItem", `{
			"TableName": "proj-table",
			"Key": {"pk": {"S": "k1"}},
			"ProjectionExpression": "#n",
			"ExpressionAttributeNames": {"#n": "name"}
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		item := resp["Item"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "Alice"}, item["name"])
	})

	t.Run("400 for invalid ProjectionExpression", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "GetItem", `{
			"TableName": "proj-table",
			"Key": {"pk": {"S": "k1"}},
			"ProjectionExpression": "#missing"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("no projection when item not found", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "GetItem", `{
			"TableName": "proj-table",
			"Key": {"pk": {"S": "no-such-key"}},
			"ProjectionExpression": "name"
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Nil(t, resp["Item"])
	})
}

func TestHandleScan_ProjectionExpression(t *testing.T) {
	t.Run("projects scanned items", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "Scan", `{
			"TableName": "proj-table",
			"ProjectionExpression": "pk, name"
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 1)
		item := items[0].(map[string]any)
		assert.NotNil(t, item["pk"])
		assert.NotNil(t, item["name"])
		assert.Nil(t, item["age"])
		assert.Nil(t, item["address"])
	})

	t.Run("projection applied after FilterExpression", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "Scan", `{
			"TableName": "proj-table",
			"FilterExpression": "age = :a",
			"ExpressionAttributeValues": {":a": {"N": "30"}},
			"ProjectionExpression": "name"
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 1)
		item := items[0].(map[string]any)
		assert.NotNil(t, item["name"])
		assert.Nil(t, item["age"])
	})

	t.Run("400 for invalid ProjectionExpression", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "Scan", `{
			"TableName": "proj-table",
			"ProjectionExpression": "#missing"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandleQuery_ProjectionExpression(t *testing.T) {
	t.Run("projects queried items", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "proj-table",
			"KeyConditionExpression": "pk = :k",
			"ExpressionAttributeValues": {":k": {"S": "k1"}},
			"ProjectionExpression": "name, age"
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 1)
		item := items[0].(map[string]any)
		assert.NotNil(t, item["name"])
		assert.NotNil(t, item["age"])
		assert.Nil(t, item["pk"])
		assert.Nil(t, item["address"])
	})

	t.Run("400 for invalid ProjectionExpression", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "proj-table",
			"KeyConditionExpression": "pk = :k",
			"ExpressionAttributeValues": {":k": {"S": "k1"}},
			"ProjectionExpression": "#missing"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandleBatchGetItem_ProjectionExpression(t *testing.T) {
	t.Run("projects items per table", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "BatchGetItem", `{
			"RequestItems": {
				"proj-table": {
					"Keys": [{"pk": {"S": "k1"}}],
					"ProjectionExpression": "name"
				}
			}
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		responses := resp["Responses"].(map[string]any)
		items := responses["proj-table"].([]any)
		require.Len(t, items, 1)
		item := items[0].(map[string]any)
		assert.Equal(t, map[string]any{"S": "Alice"}, item["name"])
		assert.Nil(t, item["pk"])
		assert.Nil(t, item["age"])
	})

	t.Run("uses ExpressionAttributeNames alias", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "BatchGetItem", `{
			"RequestItems": {
				"proj-table": {
					"Keys": [{"pk": {"S": "k1"}}],
					"ProjectionExpression": "#n",
					"ExpressionAttributeNames": {"#n": "name"}
				}
			}
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		responses := resp["Responses"].(map[string]any)
		items := responses["proj-table"].([]any)
		require.Len(t, items, 1)
		item := items[0].(map[string]any)
		assert.Equal(t, map[string]any{"S": "Alice"}, item["name"])
	})

	t.Run("400 for invalid ProjectionExpression", func(t *testing.T) {
		ro := setupProjTable(t)
		w := dynamo(t, ro, "BatchGetItem", `{
			"RequestItems": {
				"proj-table": {
					"Keys": [{"pk": {"S": "k1"}}],
					"ProjectionExpression": "#missing"
				}
			}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

// --- storage.UpdateItem error paths via router ---

func TestHandleUpdateItem_ApplyOpErrors(t *testing.T) {
	t.Run("400 when ADD applied to incompatible existing attribute type", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"cnt":{"S":"not-a-number"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "ADD cnt :delta",
            "ExpressionAttributeValues": {":delta": {"N": "1"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 when DELETE applied to incompatible existing attribute type", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"tags":{"S":"not-a-set"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
            "TableName": "test-table",
            "Key": {"pk": {"S": "k1"}},
            "UpdateExpression": "DELETE tags :rem",
            "ExpressionAttributeValues": {":rem": {"SS": ["a"]}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

const createSkTableBody = `{
	"TableName": "sk-table",
	"KeySchema": [
		{"AttributeName": "pk", "KeyType": "HASH"},
		{"AttributeName": "sk", "KeyType": "RANGE"}
	],
	"AttributeDefinitions": [
		{"AttributeName": "pk", "AttributeType": "S"},
		{"AttributeName": "sk", "AttributeType": "S"}
	],
	"BillingMode": "PAY_PER_REQUEST"
}`

func setupSkTable(t *testing.T) *Router {
	t.Helper()
	ro := newTestRouter(t)
	require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createSkTableBody).Code)
	for _, sk := range []string{"a", "b", "c", "d", "e"} {
		body := `{"TableName":"sk-table","Item":{"pk":{"S":"p"},"sk":{"S":"` + sk + `"}}}`
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem", body).Code)
	}
	return ro
}

func TestHandleQuery_ScanIndexForward(t *testing.T) {
	t.Run("ascending order by default", func(t *testing.T) {
		ro := setupSkTable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "sk-table",
			"KeyConditionExpression": "pk = :pk",
			"ExpressionAttributeValues": {":pk": {"S": "p"}}
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 5)
		assert.Equal(t, "a", items[0].(map[string]any)["sk"].(map[string]any)["S"])
		assert.Equal(t, "e", items[4].(map[string]any)["sk"].(map[string]any)["S"])
	})

	t.Run("ScanIndexForward=true gives ascending order", func(t *testing.T) {
		ro := setupSkTable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "sk-table",
			"KeyConditionExpression": "pk = :pk",
			"ExpressionAttributeValues": {":pk": {"S": "p"}},
			"ScanIndexForward": true
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 5)
		assert.Equal(t, "a", items[0].(map[string]any)["sk"].(map[string]any)["S"])
	})

	t.Run("ScanIndexForward=false gives descending order", func(t *testing.T) {
		ro := setupSkTable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "sk-table",
			"KeyConditionExpression": "pk = :pk",
			"ExpressionAttributeValues": {":pk": {"S": "p"}},
			"ScanIndexForward": false
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 5)
		assert.Equal(t, "e", items[0].(map[string]any)["sk"].(map[string]any)["S"])
		assert.Equal(t, "a", items[4].(map[string]any)["sk"].(map[string]any)["S"])
	})
}

func TestHandleQuery_Limit(t *testing.T) {
	t.Run("Limit caps returned items and sets LastEvaluatedKey", func(t *testing.T) {
		ro := setupSkTable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "sk-table",
			"KeyConditionExpression": "pk = :pk",
			"ExpressionAttributeValues": {":pk": {"S": "p"}},
			"Limit": 2
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 2)
		assert.Equal(t, float64(2), resp["Count"])
		assert.Equal(t, float64(2), resp["ScannedCount"])
		require.NotNil(t, resp["LastEvaluatedKey"])
		lek := resp["LastEvaluatedKey"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "b"}, lek["sk"])
	})

	t.Run("Limit >= total returns no LastEvaluatedKey", func(t *testing.T) {
		ro := setupSkTable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "sk-table",
			"KeyConditionExpression": "pk = :pk",
			"ExpressionAttributeValues": {":pk": {"S": "p"}},
			"Limit": 100
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Len(t, resp["Items"].([]any), 5)
		assert.Nil(t, resp["LastEvaluatedKey"])
	})

	t.Run("no Limit field returns all items without LastEvaluatedKey", func(t *testing.T) {
		ro := setupSkTable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "sk-table",
			"KeyConditionExpression": "pk = :pk",
			"ExpressionAttributeValues": {":pk": {"S": "p"}}
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Nil(t, resp["LastEvaluatedKey"])
	})

	t.Run("400 for Limit=0", func(t *testing.T) {
		ro := setupSkTable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "sk-table",
			"KeyConditionExpression": "pk = :pk",
			"ExpressionAttributeValues": {":pk": {"S": "p"}},
			"Limit": 0
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for negative Limit", func(t *testing.T) {
		ro := setupSkTable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "sk-table",
			"KeyConditionExpression": "pk = :pk",
			"ExpressionAttributeValues": {":pk": {"S": "p"}},
			"Limit": -1
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandleQuery_ExclusiveStartKey(t *testing.T) {
	t.Run("second page returns correct items", func(t *testing.T) {
		ro := setupSkTable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "sk-table",
			"KeyConditionExpression": "pk = :pk",
			"ExpressionAttributeValues": {":pk": {"S": "p"}},
			"Limit": 2,
			"ExclusiveStartKey": {"pk": {"S": "p"}, "sk": {"S": "b"}}
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 2)
		assert.Equal(t, "c", items[0].(map[string]any)["sk"].(map[string]any)["S"])
		assert.Equal(t, "d", items[1].(map[string]any)["sk"].(map[string]any)["S"])
		require.NotNil(t, resp["LastEvaluatedKey"])
	})

	t.Run("last page has no LastEvaluatedKey", func(t *testing.T) {
		ro := setupSkTable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "sk-table",
			"KeyConditionExpression": "pk = :pk",
			"ExpressionAttributeValues": {":pk": {"S": "p"}},
			"ExclusiveStartKey": {"pk": {"S": "p"}, "sk": {"S": "c"}}
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		assert.Len(t, items, 2) // "d" and "e"
		assert.Nil(t, resp["LastEvaluatedKey"])
	})

	t.Run("paginate all items with Limit=2", func(t *testing.T) {
		ro := setupSkTable(t)
		var allSKs []string
		var exclusiveStartKey map[string]any
		for {
			req := map[string]any{
				"TableName":                 "sk-table",
				"KeyConditionExpression":    "pk = :pk",
				"ExpressionAttributeValues": map[string]any{":pk": map[string]any{"S": "p"}},
				"Limit":                     2,
			}
			if exclusiveStartKey != nil {
				req["ExclusiveStartKey"] = exclusiveStartKey
			}
			body, err := json.Marshal(req)
			require.NoError(t, err)
			w := dynamo(t, ro, "Query", string(body))
			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			for _, it := range resp["Items"].([]any) {
				allSKs = append(allSKs, it.(map[string]any)["sk"].(map[string]any)["S"].(string))
			}
			if resp["LastEvaluatedKey"] == nil {
				break
			}
			exclusiveStartKey = resp["LastEvaluatedKey"].(map[string]any)
		}
		assert.Equal(t, []string{"a", "b", "c", "d", "e"}, allSKs)
	})
}

const createGSITableBody = `{
	"TableName": "gsi-table",
	"KeySchema": [
		{"AttributeName": "pk", "KeyType": "HASH"},
		{"AttributeName": "sk", "KeyType": "RANGE"}
	],
	"AttributeDefinitions": [
		{"AttributeName": "pk", "AttributeType": "S"},
		{"AttributeName": "sk", "AttributeType": "S"},
		{"AttributeName": "gsi_pk", "AttributeType": "S"},
		{"AttributeName": "gsi_sk", "AttributeType": "S"}
	],
	"BillingMode": "PAY_PER_REQUEST",
	"GlobalSecondaryIndexes": [
		{
			"IndexName": "gsi-index",
			"KeySchema": [
				{"AttributeName": "gsi_pk", "KeyType": "HASH"},
				{"AttributeName": "gsi_sk", "KeyType": "RANGE"}
			],
			"Projection": {"ProjectionType": "ALL"}
		}
	],
	"LocalSecondaryIndexes": [
		{
			"IndexName": "lsi-index",
			"KeySchema": [
				{"AttributeName": "pk", "KeyType": "HASH"},
				{"AttributeName": "gsi_sk", "KeyType": "RANGE"}
			],
			"Projection": {"ProjectionType": "ALL"}
		}
	]
}`

func setupGSITable(t *testing.T) *Router {
	t.Helper()
	ro := newTestRouter(t)
	require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createGSITableBody).Code)
	items := []struct{ pk, sk, gsiPK, gsiSK string }{
		{"p1", "s1", "g1", "a"},
		{"p1", "s2", "g1", "b"},
		{"p2", "s3", "g2", "c"},
	}
	for _, it := range items {
		body, err := json.Marshal(map[string]any{
			"TableName": "gsi-table",
			"Item": map[string]any{
				"pk":     map[string]any{"S": it.pk},
				"sk":     map[string]any{"S": it.sk},
				"gsi_pk": map[string]any{"S": it.gsiPK},
				"gsi_sk": map[string]any{"S": it.gsiSK},
			},
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem", string(body)).Code)
	}
	return ro
}

func TestHandleCreateTable_GSIAndLSI(t *testing.T) {
	t.Run("CreateTable stores GSI and LSI definitions", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", createGSITableBody)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["TableDescription"].(map[string]any)
		gsiList := desc["GlobalSecondaryIndexes"].([]any)
		require.Len(t, gsiList, 1)
		assert.Equal(t, "gsi-index", gsiList[0].(map[string]any)["IndexName"])
		lsiList := desc["LocalSecondaryIndexes"].([]any)
		require.Len(t, lsiList, 1)
		assert.Equal(t, "lsi-index", lsiList[0].(map[string]any)["IndexName"])
	})
}

func TestHandleDescribeTable_GSIAndLSI(t *testing.T) {
	t.Run("DescribeTable returns GSI and LSI definitions", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createGSITableBody).Code)
		w := dynamo(t, ro, "DescribeTable", `{"TableName":"gsi-table"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["Table"].(map[string]any)
		gsiList := desc["GlobalSecondaryIndexes"].([]any)
		require.Len(t, gsiList, 1)
		gsi := gsiList[0].(map[string]any)
		assert.Equal(t, "gsi-index", gsi["IndexName"])
		assert.Equal(t, "ACTIVE", gsi["IndexStatus"])
		lsiList := desc["LocalSecondaryIndexes"].([]any)
		require.Len(t, lsiList, 1)
		lsi := lsiList[0].(map[string]any)
		assert.Equal(t, "lsi-index", lsi["IndexName"])
		assert.Equal(t, "ACTIVE", lsi["IndexStatus"])
	})
}

func TestHandleQuery_GSI(t *testing.T) {
	t.Run("query by GSI returns matching items", func(t *testing.T) {
		ro := setupGSITable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "gsi-table",
			"IndexName": "gsi-index",
			"KeyConditionExpression": "gsi_pk = :gk",
			"ExpressionAttributeValues": {":gk": {"S": "g1"}}
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 2)
	})

	t.Run("GSI results sorted by GSI sort key ascending", func(t *testing.T) {
		ro := setupGSITable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "gsi-table",
			"IndexName": "gsi-index",
			"KeyConditionExpression": "gsi_pk = :gk",
			"ExpressionAttributeValues": {":gk": {"S": "g1"}},
			"ScanIndexForward": true
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 2)
		assert.Equal(t, "a", items[0].(map[string]any)["gsi_sk"].(map[string]any)["S"])
		assert.Equal(t, "b", items[1].(map[string]any)["gsi_sk"].(map[string]any)["S"])
	})

	t.Run("GSI results sorted descending with ScanIndexForward=false", func(t *testing.T) {
		ro := setupGSITable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "gsi-table",
			"IndexName": "gsi-index",
			"KeyConditionExpression": "gsi_pk = :gk",
			"ExpressionAttributeValues": {":gk": {"S": "g1"}},
			"ScanIndexForward": false
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 2)
		assert.Equal(t, "b", items[0].(map[string]any)["gsi_sk"].(map[string]any)["S"])
		assert.Equal(t, "a", items[1].(map[string]any)["gsi_sk"].(map[string]any)["S"])
	})

	t.Run("GSI LastEvaluatedKey includes GSI and primary key attributes", func(t *testing.T) {
		ro := setupGSITable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "gsi-table",
			"IndexName": "gsi-index",
			"KeyConditionExpression": "gsi_pk = :gk",
			"ExpressionAttributeValues": {":gk": {"S": "g1"}},
			"Limit": 1
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 1)
		lek := resp["LastEvaluatedKey"].(map[string]any)
		assert.Contains(t, lek, "pk")
		assert.Contains(t, lek, "gsi_pk")
		assert.Contains(t, lek, "gsi_sk")
	})

	t.Run("GSI pagination returns all items across pages", func(t *testing.T) {
		ro := setupGSITable(t)
		var allGSISKs []string
		var exclusiveStartKey map[string]any
		for {
			req := map[string]any{
				"TableName":                 "gsi-table",
				"IndexName":                 "gsi-index",
				"KeyConditionExpression":    "gsi_pk = :gk",
				"ExpressionAttributeValues": map[string]any{":gk": map[string]any{"S": "g1"}},
				"Limit":                     1,
			}
			if exclusiveStartKey != nil {
				req["ExclusiveStartKey"] = exclusiveStartKey
			}
			body, err := json.Marshal(req)
			require.NoError(t, err)
			w := dynamo(t, ro, "Query", string(body))
			require.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			for _, it := range resp["Items"].([]any) {
				allGSISKs = append(
					allGSISKs,
					it.(map[string]any)["gsi_sk"].(map[string]any)["S"].(string),
				)
			}
			if resp["LastEvaluatedKey"] == nil {
				break
			}
			exclusiveStartKey = resp["LastEvaluatedKey"].(map[string]any)
		}
		assert.Equal(t, []string{"a", "b"}, allGSISKs)
	})

	t.Run("invalid IndexName returns ValidationException", func(t *testing.T) {
		ro := setupGSITable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "gsi-table",
			"IndexName": "no-such-index",
			"KeyConditionExpression": "gsi_pk = :gk",
			"ExpressionAttributeValues": {":gk": {"S": "g1"}}
		}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("wrong hash key name for GSI returns ValidationException", func(t *testing.T) {
		ro := setupGSITable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "gsi-table",
			"IndexName": "gsi-index",
			"KeyConditionExpression": "pk = :v",
			"ExpressionAttributeValues": {":v": {"S": "p1"}}
		}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandleQuery_LSI(t *testing.T) {
	t.Run("query by LSI sorts by LSI sort key not table sort key", func(t *testing.T) {
		ro := setupGSITable(t)
		w := dynamo(t, ro, "Query", `{
			"TableName": "gsi-table",
			"IndexName": "lsi-index",
			"KeyConditionExpression": "pk = :pk",
			"ExpressionAttributeValues": {":pk": {"S": "p1"}},
			"ScanIndexForward": true
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 2)
		assert.Equal(t, "a", items[0].(map[string]any)["gsi_sk"].(map[string]any)["S"])
		assert.Equal(t, "b", items[1].(map[string]any)["gsi_sk"].(map[string]any)["S"])
	})
}

// --- TransactGetItems ---

func TestHandleTransactGetItems(t *testing.T) {
	createTable := func(t *testing.T, ro *Router) {
		t.Helper()
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "txn-table",
			"KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}],
			"AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}],
			"BillingMode": "PAY_PER_REQUEST"
		}`)
		require.Equal(t, http.StatusOK, w.Code)
	}
	putItem := func(t *testing.T, ro *Router, pk, val string) {
		t.Helper()
		w := dynamo(t, ro, "PutItem", fmt.Sprintf(`{
			"TableName": "txn-table",
			"Item": {"pk": {"S": %q}, "val": {"S": %q}}
		}`, pk, val))
		require.Equal(t, http.StatusOK, w.Code)
	}

	t.Run("returns found and missing items", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		putItem(t, ro, "k1", "v1")

		w := dynamo(t, ro, "TransactGetItems", `{
			"TransactItems": [
				{"Get": {"TableName": "txn-table", "Key": {"pk": {"S": "k1"}}}},
				{"Get": {"TableName": "txn-table", "Key": {"pk": {"S": "missing"}}}}
			]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		responses := resp["Responses"].([]any)
		require.Len(t, responses, 2)
		// first item found
		r0 := responses[0].(map[string]any)
		assert.NotNil(t, r0["Item"])
		// second item missing → empty map
		r1 := responses[1].(map[string]any)
		assert.Nil(t, r1["Item"])
	})

	t.Run("applies ProjectionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		putItem(t, ro, "k2", "v2")

		w := dynamo(t, ro, "TransactGetItems", `{
			"TransactItems": [
				{"Get": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "k2"}},
					"ProjectionExpression": "pk"
				}}
			]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		responses := resp["Responses"].([]any)
		item := responses[0].(map[string]any)["Item"].(map[string]any)
		assert.Contains(t, item, "pk")
		assert.NotContains(t, item, "val")
	})

	t.Run("400 for empty TransactItems", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "TransactGetItems", `{"TransactItems": []}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for missing table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "TransactGetItems", `{
			"TransactItems": [{"Get": {"TableName": "no-such-table", "Key": {"pk": {"S": "k"}}}}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for malformed JSON body", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "TransactGetItems", `{bad json`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 when entry has no Get field", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "TransactGetItems", `{"TransactItems": [{"Put": {}}]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("500 for internal storage error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			transactGetItemsFn: func(gets []TransactGetInput) ([]map[string]any, error) {
				return nil, errors.New("disk failure")
			},
		}}
		w := dynamo(t, ro, "TransactGetItems", `{
			"TransactItems": [{"Get": {"TableName": "t", "Key": {"pk": {"S": "k"}}}}]
		}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("400 for invalid ProjectionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		putItem(t, ro, "k3", "v3")
		// #ref without ExpressionAttributeNames triggers a resolution error
		w := dynamo(t, ro, "TransactGetItems", `{
			"TransactItems": [{"Get": {
				"TableName": "txn-table",
				"Key": {"pk": {"S": "k3"}},
				"ProjectionExpression": "#ref"
			}}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 when TransactGetItems exceeds 100 items", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		items := make([]string, 101)
		for i := range items {
			items[i] = fmt.Sprintf(`{"Get":{"TableName":"txn-table","Key":{"pk":{"S":"%d"}}}}`, i)
		}
		w := dynamo(t, ro, "TransactGetItems", `{"TransactItems":[`+strings.Join(items, ",")+`]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 ValidationException for duplicate item", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		w := dynamo(t, ro, "TransactGetItems", `{
			"TransactItems": [
				{"Get": {"TableName": "txn-table", "Key": {"pk": {"S": "same"}}}},
				{"Get": {"TableName": "txn-table", "Key": {"pk": {"S": "same"}}}}
			]
		}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["__type"], "ValidationException")
	})

	t.Run("400 ValidationException for malformed key", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		w := dynamo(t, ro, "TransactGetItems", `{
			"TransactItems": [
				{"Get": {"TableName": "txn-table", "Key": {"not-pk": {"S": "x"}}}}
			]
		}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["__type"], "ValidationException")
	})
}

// --- TransactWriteItems ---

func TestHandleTransactWriteItems(t *testing.T) {
	createTable := func(t *testing.T, ro *Router) {
		t.Helper()
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "txn-table",
			"KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}],
			"AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}],
			"BillingMode": "PAY_PER_REQUEST"
		}`)
		require.Equal(t, http.StatusOK, w.Code)
	}
	getItem := func(t *testing.T, ro *Router, pk string) map[string]any {
		t.Helper()
		w := dynamo(t, ro, "GetItem", fmt.Sprintf(`{
			"TableName": "txn-table",
			"Key": {"pk": {"S": %q}}
		}`, pk))
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		if item, ok := resp["Item"].(map[string]any); ok {
			return item
		}
		return nil
	}

	t.Run("Put and Delete applied atomically", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		// seed an item to delete
		dynamo(
			t,
			ro,
			"PutItem",
			`{"TableName":"txn-table","Item":{"pk":{"S":"del-me"},"x":{"S":"1"}}}`,
		)

		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [
				{"Put":    {"TableName":"txn-table","Item":{"pk":{"S":"new-item"},"val":{"S":"hello"}}}},
				{"Delete": {"TableName":"txn-table","Key":{"pk":{"S":"del-me"}}}}
			]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		assert.NotNil(t, getItem(t, ro, "new-item"))
		assert.Nil(t, getItem(t, ro, "del-me"))
	})

	t.Run("Update modifies existing item", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(
			t,
			ro,
			"PutItem",
			`{"TableName":"txn-table","Item":{"pk":{"S":"u1"},"cnt":{"N":"5"}}}`,
		)

		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "u1"}},
					"UpdateExpression": "SET cnt = :v",
					"ExpressionAttributeValues": {":v": {"N": "99"}}
				}
			}]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		item := getItem(t, ro, "u1")
		assert.Equal(t, "99", item["cnt"].(map[string]any)["N"])
	})

	t.Run("ConditionCheck passes when condition holds", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(
			t,
			ro,
			"PutItem",
			`{"TableName":"txn-table","Item":{"pk":{"S":"cc1"},"status":{"S":"ok"}}}`,
		)

		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [
				{"ConditionCheck": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "cc1"}},
					"ConditionExpression": "attribute_exists(pk)"
				}},
				{"Put": {"TableName":"txn-table","Item":{"pk":{"S":"new2"},"val":{"S":"x"}}}}
			]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		assert.NotNil(t, getItem(t, ro, "new2"))
	})

	t.Run("TransactionCanceledException when Put condition fails", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		// "guard" item exists, so attribute_not_exists(pk) should fail
		dynamo(t, ro, "PutItem", `{"TableName":"txn-table","Item":{"pk":{"S":"guard"}}}`)

		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [
				{"Put": {
					"TableName": "txn-table",
					"Item": {"pk": {"S": "guard"}},
					"ConditionExpression": "attribute_not_exists(pk)"
				}},
				{"Put": {"TableName":"txn-table","Item":{"pk":{"S":"side-effect"}}}}
			]
		}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(
			t,
			"com.amazonaws.dynamodb.v20120810#TransactionCanceledException",
			resp["__type"],
		)
		reasons := resp["CancellationReasons"].([]any)
		require.Len(t, reasons, 2)
		assert.Equal(t, "ConditionalCheckFailed", reasons[0].(map[string]any)["Code"])
		assert.Equal(t, "None", reasons[1].(map[string]any)["Code"])
		// message must include reason codes in brackets
		assert.Contains(t, resp["message"], "[ConditionalCheckFailed, None]")
		// side-effect item must NOT have been written
		assert.Nil(t, getItem(t, ro, "side-effect"))
	})

	t.Run("all actions cancelled when multiple conditions fail", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)

		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [
				{"Put": {
					"TableName": "txn-table",
					"Item": {"pk": {"S": "x"}},
					"ConditionExpression": "attribute_exists(pk)"
				}},
				{"Delete": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "y"}},
					"ConditionExpression": "attribute_exists(pk)"
				}}
			]
		}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(
			t,
			"com.amazonaws.dynamodb.v20120810#TransactionCanceledException",
			resp["__type"],
		)
		reasons := resp["CancellationReasons"].([]any)
		require.Len(t, reasons, 2)
		assert.Equal(t, "ConditionalCheckFailed", reasons[0].(map[string]any)["Code"])
		assert.Equal(t, "ConditionalCheckFailed", reasons[1].(map[string]any)["Code"])
	})

	t.Run("400 for invalid UpdateExpression in Update action", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "u"}},
					"UpdateExpression": "INVALID EXPR"
				}
			}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for ConditionCheck with empty ConditionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"ConditionCheck": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "cc"}},
					"ConditionExpression": ""
				}
			}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 when TransactWriteItems exceeds 100 items", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		items := make([]string, 101)
		for i := range items {
			items[i] = fmt.Sprintf(`{"Put":{"TableName":"txn-table","Item":{"pk":{"S":"%d"}}}}`, i)
		}
		w := dynamo(t, ro, "TransactWriteItems", `{"TransactItems":[`+strings.Join(items, ",")+`]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for duplicate primary key across actions", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [
				{"Put":    {"TableName":"txn-table","Item":{"pk":{"S":"dup"}}}},
				{"Delete": {"TableName":"txn-table","Key":{"pk":{"S":"dup"}}}}
			]
		}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["__type"], "ValidationException")
	})

	t.Run(
		"ReturnValuesOnConditionCheckFailure ALL_OLD returns existing item in reasons",
		func(t *testing.T) {
			ro := newTestRouter(t)
			createTable(t, ro)
			dynamo(
				t,
				ro,
				"PutItem",
				`{"TableName":"txn-table","Item":{"pk":{"S":"existing"},"val":{"S":"old"}}}`,
			)

			w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Put": {
					"TableName": "txn-table",
					"Item": {"pk": {"S": "existing"}},
					"ConditionExpression": "attribute_not_exists(pk)",
					"ReturnValuesOnConditionCheckFailure": "ALL_OLD"
				}
			}]
		}`)
			require.Equal(t, http.StatusBadRequest, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(
				t,
				"com.amazonaws.dynamodb.v20120810#TransactionCanceledException",
				resp["__type"],
			)
			reasons := resp["CancellationReasons"].([]any)
			require.Len(t, reasons, 1)
			reason := reasons[0].(map[string]any)
			assert.Equal(t, "ConditionalCheckFailed", reason["Code"])
			// old item must be present
			item := reason["Item"].(map[string]any)
			assert.Equal(t, "existing", item["pk"].(map[string]any)["S"])
			assert.Equal(t, "old", item["val"].(map[string]any)["S"])
		},
	)

	t.Run(
		"ReturnValuesOnConditionCheckFailure omitted → Item absent in reasons",
		func(t *testing.T) {
			ro := newTestRouter(t)
			createTable(t, ro)
			dynamo(t, ro, "PutItem", `{"TableName":"txn-table","Item":{"pk":{"S":"g2"}}}`)

			w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Put": {
					"TableName": "txn-table",
					"Item": {"pk": {"S": "g2"}},
					"ConditionExpression": "attribute_not_exists(pk)"
				}
			}]
		}`)
			require.Equal(t, http.StatusBadRequest, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			reasons := resp["CancellationReasons"].([]any)
			reason := reasons[0].(map[string]any)
			assert.Nil(t, reason["Item"])
		},
	)

	t.Run("400 for empty TransactItems", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "TransactWriteItems", `{"TransactItems": []}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for missing table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{"Put": {"TableName":"no-table","Item":{"pk":{"S":"x"}}}}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 for malformed JSON body", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "TransactWriteItems", `{bad json`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Update with ConditionExpression passes when condition holds", func(t *testing.T) {
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(
			t,
			ro,
			"PutItem",
			`{"TableName":"txn-table","Item":{"pk":{"S":"upd-cond"},"n":{"N":"5"}}}`,
		)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "upd-cond"}},
					"UpdateExpression": "SET n = :v",
					"ConditionExpression": "attribute_exists(pk)",
					"ExpressionAttributeValues": {":v": {"N": "99"}}
				}
			}]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		item := getItem(t, ro, "upd-cond")
		assert.Equal(t, "99", item["n"].(map[string]any)["N"])
	})

	t.Run("400 for entry with no recognized action type", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "TransactWriteItems", `{"TransactItems": [{}]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("500 for unexpected storage error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			transactWriteItemsFn: func(actions []TransactWriteAction) error {
				return errors.New("disk failure")
			},
		}}
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{"Put": {"TableName": "t", "Item": {"pk": {"S": "x"}}}}]
		}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("Update with if_not_exists uses fallback when absent", func(t *testing.T) {
		// Exercises ifNotExistsOp case in applyTransactActionLocked.
		ro := newTestRouter(t)
		createTable(t, ro)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "ife1"}},
					"UpdateExpression": "SET cnt = if_not_exists(cnt, :zero)",
					"ExpressionAttributeValues": {":zero": {"N": "0"}}
				}
			}]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		item := getItem(t, ro, "ife1")
		assert.Equal(t, "0", item["cnt"].(map[string]any)["N"])
	})

	t.Run("Update with list_append appends to existing list", func(t *testing.T) {
		// Exercises listAppendOp case in applyTransactActionLocked.
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(t, ro, "PutItem",
			`{"TableName":"txn-table","Item":{"pk":{"S":"la1"},"tags":{"L":[{"S":"a"}]}}}`,
		)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "la1"}},
					"UpdateExpression": "SET tags = list_append(tags, :new)",
					"ExpressionAttributeValues": {":new": {"L": [{"S": "b"}]}}
				}
			}]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		item := getItem(t, ro, "la1")
		list := item["tags"].(map[string]any)["L"].([]any)
		require.Len(t, list, 2)
		assert.Equal(t, map[string]any{"S": "a"}, list[0])
		assert.Equal(t, map[string]any{"S": "b"}, list[1])
	})

	t.Run("400 when Update list_append left arg is not a list", func(t *testing.T) {
		// Exercises the error path in listAppendOp case of applyTransactActionLocked.
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(t, ro, "PutItem",
			`{"TableName":"txn-table","Item":{"pk":{"S":"la2"},"cnt":{"N":"5"}}}`,
		)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "la2"}},
					"UpdateExpression": "SET cnt = list_append(cnt, :new)",
					"ExpressionAttributeValues": {":new": {"L": [{"S": "x"}]}}
				}
			}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Update nested SET with if_not_exists uses fallback", func(t *testing.T) {
		// Exercises nestedSetOp + ifNotExistsOp sub-case in applyTransactActionLocked.
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(t, ro, "PutItem", `{
			"TableName": "txn-table",
			"Item": {"pk": {"S": "nife1"}, "meta": {"M": {}}}
		}`)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "nife1"}},
					"UpdateExpression": "SET #meta.#count = if_not_exists(#count, :zero)",
					"ExpressionAttributeNames": {"#meta": "meta", "#count": "count"},
					"ExpressionAttributeValues": {":zero": {"N": "0"}}
				}
			}]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		item := getItem(t, ro, "nife1")
		metaM := item["meta"].(map[string]any)["M"].(map[string]any)
		assert.Equal(t, "0", metaM["count"].(map[string]any)["N"])
	})

	t.Run("Update nested SET with list_append appends to top-level list ref", func(t *testing.T) {
		// Exercises nestedSetOp + listAppendOp sub-case in applyTransactActionLocked.
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(t, ro, "PutItem", `{
			"TableName": "txn-table",
			"Item": {
				"pk": {"S": "nla1"},
				"tags": {"L": [{"S": "a"}]},
				"meta": {"M": {}}
			}
		}`)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "nla1"}},
					"UpdateExpression": "SET #meta.#tags = list_append(#tags, :new)",
					"ExpressionAttributeNames": {"#meta": "meta", "#tags": "tags"},
					"ExpressionAttributeValues": {":new": {"L": [{"S": "b"}]}}
				}
			}]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		item := getItem(t, ro, "nla1")
		metaM := item["meta"].(map[string]any)["M"].(map[string]any)
		list := metaM["tags"].(map[string]any)["L"].([]any)
		require.Len(t, list, 2)
		assert.Equal(t, map[string]any{"S": "a"}, list[0])
		assert.Equal(t, map[string]any{"S": "b"}, list[1])
	})

	t.Run("400 when nested SET list_append arg is not a list in transaction", func(t *testing.T) {
		// Exercises listAppendOp error inside nestedSetOp in applyTransactActionLocked.
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(t, ro, "PutItem", `{
			"TableName": "txn-table",
			"Item": {"pk": {"S": "nla2"}, "cnt": {"N": "5"}, "meta": {"M": {}}}
		}`)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "nla2"}},
					"UpdateExpression": "SET #meta.#x = list_append(#cnt, :new)",
					"ExpressionAttributeNames": {"#meta": "meta", "#x": "x", "#cnt": "cnt"},
					"ExpressionAttributeValues": {":new": {"L": [{"S": "b"}]}}
				}
			}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 when nested SET parent attribute is missing in transaction", func(t *testing.T) {
		// Exercises applyNestedSet error path in nestedSetOp inside applyTransactActionLocked.
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(t, ro, "PutItem", `{
			"TableName": "txn-table",
			"Item": {"pk": {"S": "nse1"}}
		}`)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "nse1"}},
					"UpdateExpression": "SET missing.field = :val",
					"ExpressionAttributeValues": {":val": {"S": "x"}}
				}
			}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Update with nested SET dot notation applies correctly", func(t *testing.T) {
		// Regression: applyTransactActionLocked must handle nestedSetOp (SET meta.count = :v).
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(t, ro, "PutItem", `{
			"TableName": "txn-table",
			"Item": {"pk": {"S": "ns1"}, "meta": {"M": {"count": {"N": "0"}}}}
		}`)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "ns1"}},
					"UpdateExpression": "SET #meta.#count = :val",
					"ExpressionAttributeNames": {"#meta": "meta", "#count": "count"},
					"ExpressionAttributeValues": {":val": {"N": "99"}}
				}
			}]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		item := getItem(t, ro, "ns1")
		metaM := item["meta"].(map[string]any)["M"].(map[string]any)
		assert.Equal(t, "99", metaM["count"].(map[string]any)["N"])
	})

	t.Run("Update with nested REMOVE dot notation applies correctly", func(t *testing.T) {
		// Regression: applyTransactActionLocked must handle nestedRemoveOp (REMOVE meta.label).
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(t, ro, "PutItem", `{
			"TableName": "txn-table",
			"Item": {"pk": {"S": "ns2"}, "meta": {"M": {"count": {"N": "1"}, "label": {"S": "x"}}}}
		}`)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "ns2"}},
					"UpdateExpression": "REMOVE #meta.#label",
					"ExpressionAttributeNames": {"#meta": "meta", "#label": "label"}
				}
			}]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		item := getItem(t, ro, "ns2")
		metaM := item["meta"].(map[string]any)["M"].(map[string]any)
		_, hasLabel := metaM["label"]
		assert.False(t, hasLabel)
		_, hasCount := metaM["count"]
		assert.True(t, hasCount)
	})

	t.Run("Update with nested SET list index applies correctly", func(t *testing.T) {
		// Regression: applyTransactActionLocked must handle nestedSetOp for list index (SET tags[1] = :v).
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(t, ro, "PutItem", `{
			"TableName": "txn-table",
			"Item": {"pk": {"S": "ns3"}, "tags": {"L": [{"S": "a"}, {"S": "b"}]}}
		}`)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "ns3"}},
					"UpdateExpression": "SET tags[1] = :val",
					"ExpressionAttributeValues": {":val": {"S": "replaced"}}
				}
			}]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		item := getItem(t, ro, "ns3")
		list := item["tags"].(map[string]any)["L"].([]any)
		assert.Equal(t, map[string]any{"S": "a"}, list[0])
		assert.Equal(t, map[string]any{"S": "replaced"}, list[1])
	})

	t.Run("Update with nested REMOVE list index applies correctly", func(t *testing.T) {
		// Regression: applyTransactActionLocked must handle nestedRemoveOp for list index (REMOVE tags[0]).
		ro := newTestRouter(t)
		createTable(t, ro)
		dynamo(t, ro, "PutItem", `{
			"TableName": "txn-table",
			"Item": {"pk": {"S": "ns4"}, "tags": {"L": [{"S": "x"}, {"S": "y"}, {"S": "z"}]}}
		}`)
		w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems": [{
				"Update": {
					"TableName": "txn-table",
					"Key": {"pk": {"S": "ns4"}},
					"UpdateExpression": "REMOVE tags[1]"
				}
			}]
		}`)
		require.Equal(t, http.StatusOK, w.Code)
		item := getItem(t, ro, "ns4")
		list := item["tags"].(map[string]any)["L"].([]any)
		require.Len(t, list, 2)
		assert.Equal(t, map[string]any{"S": "x"}, list[0])
		assert.Equal(t, map[string]any{"S": "z"}, list[1])
	})
}

func TestHandleDescribeContinuousBackups(t *testing.T) {
	t.Run("returns DISABLED status by default", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		w := dynamo(t, ro, "DescribeContinuousBackups", `{"TableName":"test-table"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["ContinuousBackupsDescription"].(map[string]any)
		assert.Equal(t, "ENABLED", desc["ContinuousBackupsStatus"])
		pitr := desc["PointInTimeRecoveryDescription"].(map[string]any)
		assert.Equal(t, "DISABLED", pitr["PointInTimeRecoveryStatus"])
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeContinuousBackups", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeContinuousBackups", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for unknown table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeContinuousBackups", `{"TableName":"no-such-table"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#TableNotFoundException")
	})

	t.Run("500 for unexpected storage error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			describeContinuousBackupsFn: func(string) (TableMetadata, error) {
				return TableMetadata{}, errors.New("disk failure")
			},
		}}
		w := dynamo(t, ro, "DescribeContinuousBackups", `{"TableName":"t"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleUpdateContinuousBackups(t *testing.T) {
	t.Run("enables PITR", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		w := dynamo(t, ro, "UpdateContinuousBackups", `{
			"TableName": "test-table",
			"PointInTimeRecoverySpecification": {"PointInTimeRecoveryEnabled": true}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["ContinuousBackupsDescription"].(map[string]any)
		pitr := desc["PointInTimeRecoveryDescription"].(map[string]any)
		assert.Equal(t, "ENABLED", pitr["PointInTimeRecoveryStatus"])
		assert.NotNil(t, pitr["EarliestRestorableDateTime"])
		assert.NotNil(t, pitr["LatestRestorableDateTime"])
	})

	t.Run("disables PITR", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		dynamo(t, ro, "UpdateContinuousBackups", `{
			"TableName": "test-table",
			"PointInTimeRecoverySpecification": {"PointInTimeRecoveryEnabled": true}
		}`)
		w := dynamo(t, ro, "UpdateContinuousBackups", `{
			"TableName": "test-table",
			"PointInTimeRecoverySpecification": {"PointInTimeRecoveryEnabled": false}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		desc := resp["ContinuousBackupsDescription"].(map[string]any)
		pitr := desc["PointInTimeRecoveryDescription"].(map[string]any)
		assert.Equal(t, "DISABLED", pitr["PointInTimeRecoveryStatus"])
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateContinuousBackups", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateContinuousBackups", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing PointInTimeRecoverySpecification", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		w := dynamo(t, ro, "UpdateContinuousBackups", `{"TableName":"test-table"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for unknown table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateContinuousBackups", `{
			"TableName": "no-such-table",
			"PointInTimeRecoverySpecification": {"PointInTimeRecoveryEnabled": true}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#TableNotFoundException")
	})

	t.Run("500 for unexpected storage error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			updateContinuousBackupsFn: func(string, bool) (TableMetadata, error) {
				return TableMetadata{}, errors.New("disk failure")
			},
		}}
		w := dynamo(t, ro, "UpdateContinuousBackups", `{
			"TableName": "t",
			"PointInTimeRecoverySpecification": {"PointInTimeRecoveryEnabled": true}
		}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleDescribeKinesisStreamingDestination(t *testing.T) {
	t.Run("returns empty destinations by default", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		w := dynamo(t, ro, "DescribeKinesisStreamingDestination", `{"TableName":"test-table"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		dests := resp["KinesisDataStreamDestinations"].([]any)
		assert.Empty(t, dests)
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeKinesisStreamingDestination", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeKinesisStreamingDestination", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("returns configured destinations", func(t *testing.T) {
		const arn = "arn:aws:kinesis:us-east-1:000000000000:stream/my-stream"
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table", "StreamArn": %q
		}`, arn))
		w := dynamo(t, ro, "DescribeKinesisStreamingDestination", `{"TableName":"test-table"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		dests := resp["KinesisDataStreamDestinations"].([]any)
		require.Len(t, dests, 1)
		d := dests[0].(map[string]any)
		assert.Equal(t, arn, d["StreamArn"])
		assert.Equal(t, "ACTIVE", d["DestinationStatus"])
	})

	t.Run("400 for unknown table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeKinesisStreamingDestination", `{"TableName":"no-such-table"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("500 for unexpected storage error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			describeKinesisStreamingDestinationFn: func(string) ([]KinesisDestination, error) {
				return nil, errors.New("disk failure")
			},
		}}
		w := dynamo(t, ro, "DescribeKinesisStreamingDestination", `{"TableName":"t"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleEnableKinesisStreamingDestination(t *testing.T) {
	const streamARN = "arn:aws:kinesis:us-east-1:000000000000:stream/my-stream"

	t.Run("new destination returns ENABLING", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		w := dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table",
			"StreamArn": %q,
			"EnableKinesisStreamingConfiguration": {"ApproximateCreationDateTimePrecision": "MICROSECOND"}
		}`, streamARN))
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "ENABLING", resp["DestinationStatus"])
		cfg := resp["EnableKinesisStreamingConfiguration"].(map[string]any)
		assert.Equal(t, "MICROSECOND", cfg["ApproximateCreationDateTimePrecision"])
	})

	t.Run("re-enabling DISABLED destination returns ENABLING", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table", "StreamArn": %q
		}`, streamARN))
		dynamo(t, ro, "DisableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table", "StreamArn": %q
		}`, streamARN))
		w := dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table", "StreamArn": %q
		}`, streamARN))
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "ENABLING", resp["DestinationStatus"])
	})

	t.Run("updating precision on ACTIVE destination returns UPDATING", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table", "StreamArn": %q
		}`, streamARN))
		w := dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table",
			"StreamArn": %q,
			"EnableKinesisStreamingConfiguration": {"ApproximateCreationDateTimePrecision": "MICROSECOND"}
		}`, streamARN))
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "UPDATING", resp["DestinationStatus"])
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "EnableKinesisStreamingDestination", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(
			t,
			ro,
			"EnableKinesisStreamingDestination",
			fmt.Sprintf(`{"StreamArn":%q}`, streamARN),
		)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing StreamArn", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "EnableKinesisStreamingDestination", `{"TableName":"t"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for invalid StreamArn format", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "EnableKinesisStreamingDestination", `{
			"TableName": "t", "StreamArn": "not-an-arn"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("describe after enable returns ACTIVE", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table", "StreamArn": %q
		}`, streamARN))
		w := dynamo(t, ro, "DescribeKinesisStreamingDestination", `{"TableName":"test-table"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		dests := resp["KinesisDataStreamDestinations"].([]any)
		require.Len(t, dests, 1)
		assert.Equal(t, "ACTIVE", dests[0].(map[string]any)["DestinationStatus"])
	})

	t.Run("400 for invalid precision", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		w := dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table",
			"StreamArn": %q,
			"EnableKinesisStreamingConfiguration": {"ApproximateCreationDateTimePrecision": "NANOSECOND"}
		}`, streamARN))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for unknown table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "no-such-table", "StreamArn": %q
		}`, streamARN))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 when limit exceeded", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			enableKinesisStreamingDestinationFn: func(string, string, string) (KinesisDestination, bool, error) {
				return KinesisDestination{}, false, ErrKinesisLimitExceeded
			},
		}}
		w := dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "t", "StreamArn": %q
		}`, streamARN))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#LimitExceededException")
	})

	t.Run("500 for unexpected storage error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			enableKinesisStreamingDestinationFn: func(string, string, string) (KinesisDestination, bool, error) {
				return KinesisDestination{}, false, errors.New("disk failure")
			},
		}}
		w := dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "t", "StreamArn": %q
		}`, streamARN))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleDisableKinesisStreamingDestination(t *testing.T) {
	const streamARN = "arn:aws:kinesis:us-east-1:000000000000:stream/my-stream"

	t.Run("disables destination and returns DISABLED", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table", "StreamArn": %q
		}`, streamARN))
		w := dynamo(t, ro, "DisableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table", "StreamArn": %q
		}`, streamARN))
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "DISABLING", resp["DestinationStatus"])
	})

	t.Run("400 for invalid JSON", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DisableKinesisStreamingDestination", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(
			t,
			ro,
			"DisableKinesisStreamingDestination",
			fmt.Sprintf(`{"StreamArn":%q}`, streamARN),
		)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for missing StreamArn", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DisableKinesisStreamingDestination", `{"TableName":"t"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 for invalid StreamArn format", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DisableKinesisStreamingDestination", `{
			"TableName": "t", "StreamArn": "not-an-arn"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("describe after disable returns DISABLED", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		dynamo(t, ro, "EnableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table", "StreamArn": %q
		}`, streamARN))
		dynamo(t, ro, "DisableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table", "StreamArn": %q
		}`, streamARN))
		w := dynamo(t, ro, "DescribeKinesisStreamingDestination", `{"TableName":"test-table"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		dests := resp["KinesisDataStreamDestinations"].([]any)
		require.Len(t, dests, 1)
		assert.Equal(t, "DISABLED", dests[0].(map[string]any)["DestinationStatus"])
	})

	t.Run("400 for unknown table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DisableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "no-such-table", "StreamArn": %q
		}`, streamARN))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 for destination not found", func(t *testing.T) {
		ro := newTestRouter(t)
		dynamo(t, ro, "CreateTable", createTableBody)
		w := dynamo(t, ro, "DisableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "test-table", "StreamArn": %q
		}`, streamARN))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("500 for unexpected storage error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			disableKinesisStreamingDestinationFn: func(string, string) (KinesisDestination, error) {
				return KinesisDestination{}, errors.New("disk failure")
			},
		}}
		w := dynamo(t, ro, "DisableKinesisStreamingDestination", fmt.Sprintf(`{
			"TableName": "t", "StreamArn": %q
		}`, streamARN))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestUnusedExpressionAttributeRefs(t *testing.T) {
	setup := func(t *testing.T) *Router {
		t.Helper()
		ro := newTestRouter(t)
		require.Equal(t, 200, dynamo(t, ro, "CreateTable", createTableBody).Code)
		require.Equal(t, 200, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"1"},"status":{"S":"active"}}}`).Code)
		return ro
	}

	// Scan
	t.Run("Scan: unused ExpressionAttributeNames returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "Scan", `{
			"TableName":"test-table",
			"FilterExpression":"status = :active",
			"ExpressionAttributeNames":{"#unused":"something"},
			"ExpressionAttributeValues":{":active":{"S":"active"}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
	t.Run("Scan: unused ExpressionAttributeValues returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "Scan", `{
			"TableName":"test-table",
			"FilterExpression":"#s = :active",
			"ExpressionAttributeNames":{"#s":"status"},
			"ExpressionAttributeValues":{":active":{"S":"active"},":unused":{"S":"x"}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
	t.Run("Scan: ExpressionAttributeNames with no expression returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "Scan", `{
			"TableName":"test-table",
			"ExpressionAttributeNames":{"#s":"status"}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
	t.Run("Scan: ExpressionAttributeValues with no expression returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "Scan", `{
			"TableName":"test-table",
			"ExpressionAttributeValues":{":active":{"S":"active"}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	// Query
	t.Run("Query: unused ExpressionAttributeNames returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "Query", `{
			"TableName":"test-table",
			"KeyConditionExpression":"pk = :pk",
			"ExpressionAttributeNames":{"#unused":"something"},
			"ExpressionAttributeValues":{":pk":{"S":"1"}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
	t.Run("Query: unused ExpressionAttributeValues returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "Query", `{
			"TableName":"test-table",
			"KeyConditionExpression":"pk = :pk",
			"ExpressionAttributeValues":{":pk":{"S":"1"},":unused":{"S":"x"}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	// GetItem
	t.Run("GetItem: unused ExpressionAttributeNames returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "GetItem", `{
			"TableName":"test-table",
			"Key":{"pk":{"S":"1"}},
			"ProjectionExpression":"status",
			"ExpressionAttributeNames":{"#unused":"something"}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
	t.Run("GetItem: ExpressionAttributeNames with no expression returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "GetItem", `{
			"TableName":"test-table",
			"Key":{"pk":{"S":"1"}},
			"ExpressionAttributeNames":{"#s":"status"}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	// PutItem
	t.Run("PutItem: unused ExpressionAttributeValues returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "PutItem", `{
			"TableName":"test-table",
			"Item":{"pk":{"S":"2"}},
			"ConditionExpression":"attribute_not_exists(pk)",
			"ExpressionAttributeValues":{":unused":{"S":"x"}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
	t.Run("PutItem: ExpressionAttributeValues with no expression returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "PutItem", `{
			"TableName":"test-table",
			"Item":{"pk":{"S":"2"}},
			"ExpressionAttributeValues":{":unused":{"S":"x"}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	// DeleteItem
	t.Run("DeleteItem: unused ExpressionAttributeNames returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "DeleteItem", `{
			"TableName":"test-table",
			"Key":{"pk":{"S":"1"}},
			"ConditionExpression":"attribute_exists(pk)",
			"ExpressionAttributeNames":{"#unused":"something"}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	// UpdateItem
	t.Run("UpdateItem: unused ExpressionAttributeValues returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName":"test-table",
			"Key":{"pk":{"S":"1"}},
			"UpdateExpression":"SET #s = :active",
			"ExpressionAttributeNames":{"#s":"status"},
			"ExpressionAttributeValues":{":active":{"S":"active"},":unused":{"S":"x"}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
	t.Run(
		"UpdateItem: ExpressionAttributeNames with no expression returns 400",
		func(t *testing.T) {
			ro := setup(t)
			w := dynamo(t, ro, "UpdateItem", `{
			"TableName":"test-table",
			"Key":{"pk":{"S":"1"}},
			"ExpressionAttributeNames":{"#s":"status"}
		}`)
			assert.Equal(t, 400, w.Code)
			assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
		},
	)

	// TransactGetItems
	t.Run("TransactGetItems: unused ExpressionAttributeNames returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "TransactGetItems", `{
			"TransactItems":[{
				"Get":{
					"TableName":"test-table",
					"Key":{"pk":{"S":"1"}},
					"ProjectionExpression":"status",
					"ExpressionAttributeNames":{"#unused":"something"}
				}
			}]
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	// TransactWriteItems
	t.Run(
		"TransactWriteItems Put: unused ExpressionAttributeValues returns 400",
		func(t *testing.T) {
			ro := setup(t)
			w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems":[{
				"Put":{
					"TableName":"test-table",
					"Item":{"pk":{"S":"2"}},
					"ConditionExpression":"attribute_not_exists(pk)",
					"ExpressionAttributeValues":{":unused":{"S":"x"}}
				}
			}]
		}`)
			assert.Equal(t, 400, w.Code)
			assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
		},
	)
	t.Run(
		"TransactWriteItems Delete: unused ExpressionAttributeNames returns 400",
		func(t *testing.T) {
			ro := setup(t)
			w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems":[{
				"Delete":{
					"TableName":"test-table",
					"Key":{"pk":{"S":"1"}},
					"ConditionExpression":"attribute_exists(pk)",
					"ExpressionAttributeNames":{"#unused":"something"}
				}
			}]
		}`)
			assert.Equal(t, 400, w.Code)
			assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
		},
	)
	t.Run(
		"TransactWriteItems Update: unused ExpressionAttributeValues returns 400",
		func(t *testing.T) {
			ro := setup(t)
			w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems":[{
				"Update":{
					"TableName":"test-table",
					"Key":{"pk":{"S":"1"}},
					"UpdateExpression":"SET #s = :active",
					"ExpressionAttributeNames":{"#s":"status"},
					"ExpressionAttributeValues":{":active":{"S":"active"},":unused":{"S":"x"}}
				}
			}]
		}`)
			assert.Equal(t, 400, w.Code)
			assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
		},
	)
	t.Run(
		"TransactWriteItems ConditionCheck: unused ExpressionAttributeNames returns 400",
		func(t *testing.T) {
			ro := setup(t)
			w := dynamo(t, ro, "TransactWriteItems", `{
			"TransactItems":[{
				"ConditionCheck":{
					"TableName":"test-table",
					"Key":{"pk":{"S":"1"}},
					"ConditionExpression":"attribute_exists(pk)",
					"ExpressionAttributeNames":{"#unused":"something"}
				}
			}]
		}`)
			assert.Equal(t, 400, w.Code)
			assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
		},
	)

	// BatchGetItem
	t.Run("BatchGetItem: unused ExpressionAttributeNames returns 400", func(t *testing.T) {
		ro := setup(t)
		w := dynamo(t, ro, "BatchGetItem", `{
			"RequestItems":{
				"test-table":{
					"Keys":[{"pk":{"S":"1"}}],
					"ProjectionExpression":"status",
					"ExpressionAttributeNames":{"#unused":"something"}
				}
			}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
	t.Run(
		"BatchGetItem: ExpressionAttributeNames with no expression returns 400",
		func(t *testing.T) {
			ro := setup(t)
			w := dynamo(t, ro, "BatchGetItem", `{
			"RequestItems":{
				"test-table":{
					"Keys":[{"pk":{"S":"1"}}],
					"ExpressionAttributeNames":{"#s":"status"}
				}
			}
		}`)
			assert.Equal(t, 400, w.Code)
			assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
		},
	)
}

func TestHandleUpdateItem_IfNotExists(t *testing.T) {
	setup := func(t *testing.T) *Router {
		t.Helper()
		ro := newTestRouter(t)
		require.Equal(t, 200, dynamo(t, ro, "CreateTable", createTableBody).Code)
		return ro
	}

	t.Run("sets attribute to default when it does not exist", func(t *testing.T) {
		ro := setup(t)
		require.Equal(t, 200, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k1"}},
			"UpdateExpression": "SET #c = if_not_exists(#c, :zero)",
			"ExpressionAttributeNames": {"#c": "count"},
			"ExpressionAttributeValues": {":zero": {"N": "0"}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"N": "0"}, attrs["count"])
	})

	t.Run("keeps existing value when attribute already exists", func(t *testing.T) {
		ro := setup(t)
		require.Equal(t, 200, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k2"},"count":{"N":"5"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k2"}},
			"UpdateExpression": "SET #c = if_not_exists(#c, :zero)",
			"ExpressionAttributeNames": {"#c": "count"},
			"ExpressionAttributeValues": {":zero": {"N": "0"}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"N": "5"}, attrs["count"])
	})

	t.Run(
		"sets attribute from different check attr when check attr does not exist",
		func(t *testing.T) {
			ro := setup(t)
			require.Equal(t, 200, dynamo(t, ro, "PutItem",
				`{"TableName":"test-table","Item":{"pk":{"S":"k3"}}}`).Code)
			w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k3"}},
			"UpdateExpression": "SET #a = if_not_exists(#b, :default)",
			"ExpressionAttributeNames": {"#a": "target", "#b": "source"},
			"ExpressionAttributeValues": {":default": {"S": "fallback"}},
			"ReturnValues": "ALL_NEW"
		}`)
			require.Equal(t, 200, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			attrs := resp["Attributes"].(map[string]any)
			assert.Equal(t, map[string]any{"S": "fallback"}, attrs["target"])
		},
	)

	t.Run("uses fallback attr ref when check attr does not exist", func(t *testing.T) {
		ro := setup(t)
		require.Equal(t, 200, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k4"},"src":{"N":"42"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k4"}},
			"UpdateExpression": "SET #a = if_not_exists(#a, #src)",
			"ExpressionAttributeNames": {"#a": "target", "#src": "src"},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"N": "42"}, attrs["target"])
	})

	t.Run("400 when default value placeholder is missing", func(t *testing.T) {
		ro := setup(t)
		require.Equal(t, 200, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k5"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k5"}},
			"UpdateExpression": "SET #c = if_not_exists(#c, :missing)",
			"ExpressionAttributeNames": {"#c": "count"}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandleUpdateItem_ListAppend(t *testing.T) {
	setup := func(t *testing.T) *Router {
		t.Helper()
		ro := newTestRouter(t)
		require.Equal(t, 200, dynamo(t, ro, "CreateTable", createTableBody).Code)
		return ro
	}

	t.Run("appends items to existing list", func(t *testing.T) {
		ro := setup(t)
		require.Equal(t, 200, dynamo(
			t,
			ro,
			"PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k1"},"tags":{"L":[{"S":"a"},{"S":"b"}]}}}`,
		).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k1"}},
			"UpdateExpression": "SET #t = list_append(#t, :new)",
			"ExpressionAttributeNames": {"#t": "tags"},
			"ExpressionAttributeValues": {":new": {"L": [{"S": "c"}]}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		tags := attrs["tags"].(map[string]any)["L"].([]any)
		require.Len(t, tags, 3)
		assert.Equal(t, map[string]any{"S": "a"}, tags[0])
		assert.Equal(t, map[string]any{"S": "b"}, tags[1])
		assert.Equal(t, map[string]any{"S": "c"}, tags[2])
	})

	t.Run("prepends items when value is on the left", func(t *testing.T) {
		ro := setup(t)
		require.Equal(t, 200, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k2"},"tags":{"L":[{"S":"b"}]}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k2"}},
			"UpdateExpression": "SET #t = list_append(:new, #t)",
			"ExpressionAttributeNames": {"#t": "tags"},
			"ExpressionAttributeValues": {":new": {"L": [{"S": "a"}]}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		tags := attrs["tags"].(map[string]any)["L"].([]any)
		require.Len(t, tags, 2)
		assert.Equal(t, map[string]any{"S": "a"}, tags[0])
		assert.Equal(t, map[string]any{"S": "b"}, tags[1])
	})

	t.Run("creates list from two value refs when attribute does not exist", func(t *testing.T) {
		ro := setup(t)
		require.Equal(t, 200, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k3"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k3"}},
			"UpdateExpression": "SET #t = list_append(:a, :b)",
			"ExpressionAttributeNames": {"#t": "tags"},
			"ExpressionAttributeValues": {
				":a": {"L": [{"S": "x"}]},
				":b": {"L": [{"S": "y"}]}
			},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		attrs := resp["Attributes"].(map[string]any)
		tags := attrs["tags"].(map[string]any)["L"].([]any)
		require.Len(t, tags, 2)
		assert.Equal(t, map[string]any{"S": "x"}, tags[0])
		assert.Equal(t, map[string]any{"S": "y"}, tags[1])
	})

	t.Run("400 when list_append value placeholder is missing", func(t *testing.T) {
		ro := setup(t)
		require.Equal(t, 200, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k4"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k4"}},
			"UpdateExpression": "SET #t = list_append(#t, :missing)",
			"ExpressionAttributeNames": {"#t": "tags"}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 when list_append left arg is not a List type", func(t *testing.T) {
		ro := setup(t)
		require.Equal(t, 200, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k5"},"tags":{"S":"not-a-list"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k5"}},
			"UpdateExpression": "SET #t = list_append(#t, :new)",
			"ExpressionAttributeNames": {"#t": "tags"},
			"ExpressionAttributeValues": {":new": {"L": [{"S": "x"}]}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 when list_append right arg is not a List type", func(t *testing.T) {
		ro := setup(t)
		require.Equal(t, 200, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k6"},"tags":{"L":[{"S":"a"}]}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k6"}},
			"UpdateExpression": "SET #t = list_append(#t, :notlist)",
			"ExpressionAttributeNames": {"#t": "tags"},
			"ExpressionAttributeValues": {":notlist": {"S": "not-a-list"}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 when left attr is absent (not a List type)", func(t *testing.T) {
		ro := setup(t)
		require.Equal(t, 200, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"k7"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "k7"}},
			"UpdateExpression": "SET #t = list_append(#t, :new)",
			"ExpressionAttributeNames": {"#t": "tags"},
			"ExpressionAttributeValues": {":new": {"L": [{"S": "x"}]}}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandleUpdateItem_NestedFunctions(t *testing.T) {
	setup := func(t *testing.T) *Router {
		t.Helper()
		ro := newTestRouter(t)
		require.Equal(t, 200, dynamo(t, ro, "CreateTable", createTableBody).Code)
		return ro
	}

	t.Run(
		"list_append(if_not_exists(#t, :empty), :new) initialises absent list",
		func(t *testing.T) {
			ro := setup(t)
			require.Equal(t, 200, dynamo(t, ro, "PutItem",
				`{"TableName":"test-table","Item":{"pk":{"S":"n1"}}}`).Code)
			w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n1"}},
			"UpdateExpression": "SET #t = list_append(if_not_exists(#t, :empty), :new)",
			"ExpressionAttributeNames": {"#t": "tags"},
			"ExpressionAttributeValues": {
				":empty": {"L": []},
				":new":   {"L": [{"S": "a"}]}
			},
			"ReturnValues": "ALL_NEW"
		}`)
			require.Equal(t, 200, w.Code)
			var out map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
			tags := out["Attributes"].(map[string]any)["tags"].(map[string]any)["L"].([]any)
			require.Len(t, tags, 1)
			assert.Equal(t, map[string]any{"S": "a"}, tags[0])
		},
	)

	t.Run(
		"list_append(if_not_exists(#t, :empty), :new) appends to existing list",
		func(t *testing.T) {
			ro := setup(t)
			require.Equal(t, 200, dynamo(
				t,
				ro,
				"PutItem",
				`{"TableName":"test-table","Item":{"pk":{"S":"n2"},"tags":{"L":[{"S":"x"}]}}}`,
			).Code)
			w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n2"}},
			"UpdateExpression": "SET #t = list_append(if_not_exists(#t, :empty), :new)",
			"ExpressionAttributeNames": {"#t": "tags"},
			"ExpressionAttributeValues": {
				":empty": {"L": []},
				":new":   {"L": [{"S": "y"}]}
			},
			"ReturnValues": "ALL_NEW"
		}`)
			require.Equal(t, 200, w.Code)
			var out map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
			tags := out["Attributes"].(map[string]any)["tags"].(map[string]any)["L"].([]any)
			require.Len(t, tags, 2)
			assert.Equal(t, map[string]any{"S": "x"}, tags[0])
			assert.Equal(t, map[string]any{"S": "y"}, tags[1])
		},
	)

	t.Run("400 when if_not_exists first arg is a value placeholder", func(t *testing.T) {
		ro := setup(t)
		require.Equal(t, 200, dynamo(t, ro, "PutItem",
			`{"TableName":"test-table","Item":{"pk":{"S":"n3"}}}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n3"}},
			"UpdateExpression": "SET #c = if_not_exists(:v, :fallback)",
			"ExpressionAttributeValues": {
				":v":        {"N": "1"},
				":fallback": {"N": "0"}
			},
			"ExpressionAttributeNames": {"#c": "count"}
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestHandleUpdateItem_MultipleSetAssignments(t *testing.T) {
	ro := newTestRouter(t)
	require.Equal(t, 200, dynamo(t, ro, "CreateTable", createTableBody).Code)
	require.Equal(t, 200, dynamo(t, ro, "PutItem",
		`{"TableName":"test-table","Item":{"pk":{"S":"m1"}}}`).Code)

	w := dynamo(t, ro, "UpdateItem", `{
		"TableName": "test-table",
		"Key": {"pk": {"S": "m1"}},
		"UpdateExpression": "SET a = :v1, b = :v2",
		"ExpressionAttributeValues": {":v1": {"S": "foo"}, ":v2": {"S": "bar"}},
		"ReturnValues": "ALL_NEW"
	}`)
	require.Equal(t, 200, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	attrs := out["Attributes"].(map[string]any)
	assert.Equal(t, map[string]any{"S": "foo"}, attrs["a"])
	assert.Equal(t, map[string]any{"S": "bar"}, attrs["b"])
}

// --- Nested document path tests (#207, #201) ---

func TestNestedPathUpdateItem(t *testing.T) {
	ro := newTestRouter(t)
	require.Equal(t, 200, dynamo(t, ro, "CreateTable", createTableBody).Code)

	// Seed an item with a nested map and a list.
	require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
		"TableName": "test-table",
		"Item": {
			"pk": {"S": "n1"},
			"meta": {"M": {"count": {"N": "0"}, "label": {"S": "old"}}},
			"tags": {"L": [{"S": "a"}, {"S": "b"}]}
		}
	}`).Code)

	t.Run("SET nested map attribute via dot notation", func(t *testing.T) {
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n1"}},
			"UpdateExpression": "SET #meta.#count = :val",
			"ExpressionAttributeNames": {"#meta": "meta", "#count": "count"},
			"ExpressionAttributeValues": {":val": {"N": "99"}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		metaM := attrs["meta"].(map[string]any)["M"].(map[string]any)
		assert.Equal(t, map[string]any{"N": "99"}, metaM["count"])
		assert.Equal(t, map[string]any{"S": "old"}, metaM["label"]) // unchanged
	})

	t.Run("SET list element by index", func(t *testing.T) {
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n1"}},
			"UpdateExpression": "SET #tags[1] = :val",
			"ExpressionAttributeNames": {"#tags": "tags"},
			"ExpressionAttributeValues": {":val": {"S": "replaced"}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		list := attrs["tags"].(map[string]any)["L"].([]any)
		assert.Equal(t, map[string]any{"S": "a"}, list[0])
		assert.Equal(t, map[string]any{"S": "replaced"}, list[1])
	})

	t.Run("REMOVE nested map attribute", func(t *testing.T) {
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n1"}},
			"UpdateExpression": "REMOVE #meta.#label",
			"ExpressionAttributeNames": {"#meta": "meta", "#label": "label"},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		metaM := attrs["meta"].(map[string]any)["M"].(map[string]any)
		_, hasLabel := metaM["label"]
		assert.False(t, hasLabel)
		// count should still be there
		_, hasCount := metaM["count"]
		assert.True(t, hasCount)
	})

	t.Run("REMOVE list element shifts remaining", func(t *testing.T) {
		// Re-seed tags to a known state.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n2"},
				"tags": {"L": [{"S": "x"}, {"S": "y"}, {"S": "z"}]}
			}
		}`).Code)

		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n2"}},
			"UpdateExpression": "REMOVE tags[1]",
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		list := attrs["tags"].(map[string]any)["L"].([]any)
		require.Len(t, list, 2)
		assert.Equal(t, map[string]any{"S": "x"}, list[0])
		assert.Equal(t, map[string]any{"S": "z"}, list[1])
	})

	t.Run("SET nested path parent missing returns ValidationException", func(t *testing.T) {
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "n3"}}
		}`).Code)

		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n3"}},
			"UpdateExpression": "SET missing.field = :val",
			"ExpressionAttributeValues": {":val": {"S": "x"}},
			"ReturnValues": "ALL_NEW"
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("SET list element beyond end appends", func(t *testing.T) {
		// AWS appends when the list index exceeds the current length.
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n1"}},
			"UpdateExpression": "SET tags[99] = :val",
			"ExpressionAttributeValues": {":val": {"S": "appended"}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		list := attrs["tags"].(map[string]any)["L"].([]any)
		assert.Equal(t, map[string]any{"S": "appended"}, list[len(list)-1])
	})

	t.Run(
		"SET into intermediate list element returns ValidationException when non-leaf out-of-bounds",
		func(t *testing.T) {
			// SET tags[99].field = :val — the list index is out of bounds and is not the leaf.
			w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n1"}},
			"UpdateExpression": "SET tags[99].field = :val",
			"ExpressionAttributeValues": {":val": {"S": "x"}},
			"ReturnValues": "ALL_NEW"
		}`)
			assert.Equal(t, 400, w.Code)
			assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
		},
	)

	t.Run("SET parent is not a map returns ValidationException", func(t *testing.T) {
		// "name" is a scalar S attribute; SET name.field = :val must fail.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "n4"}, "name": {"S": "Alice"}}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n4"}},
			"UpdateExpression": "SET name.field = :val",
			"ExpressionAttributeValues": {":val": {"S": "x"}},
			"ReturnValues": "ALL_NEW"
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("REMOVE nested map key that does not exist is a no-op", func(t *testing.T) {
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n5"},
				"meta": {"M": {"count": {"N": "1"}}}
			}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n5"}},
			"UpdateExpression": "REMOVE meta.#missing",
			"ExpressionAttributeNames": {"#missing": "nosuchkey"},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		metaM := attrs["meta"].(map[string]any)["M"].(map[string]any)
		_, hasCount := metaM["count"]
		assert.True(t, hasCount) // unchanged
	})

	t.Run("REMOVE list element out-of-bounds is a no-op", func(t *testing.T) {
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n6"},
				"tags": {"L": [{"S": "only"}]}
			}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n6"}},
			"UpdateExpression": "REMOVE tags[99]",
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		list := attrs["tags"].(map[string]any)["L"].([]any)
		require.Len(t, list, 1) // still 1 element
	})

	t.Run("REMOVE into parent that is not a map is a no-op", func(t *testing.T) {
		// "name" is a scalar; REMOVE name.field is a no-op per AWS spec.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "n7"}, "name": {"S": "Alice"}}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n7"}},
			"UpdateExpression": "REMOVE name.#field",
			"ExpressionAttributeNames": {"#field": "first"},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "Alice"}, attrs["name"]) // unchanged
	})

	t.Run("SET into list element attribute (3-level: attr→list[N]→attr)", func(t *testing.T) {
		// Covers the non-leaf list index navigation path in setAtDynamoValue.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n8"},
				"items": {"L": [{"M": {"city": {"S": "Tokyo"}}}]}
			}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n8"}},
			"UpdateExpression": "SET #items[0].#city = :val",
			"ExpressionAttributeNames": {"#items": "items", "#city": "city"},
			"ExpressionAttributeValues": {":val": {"S": "Osaka"}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		elem := attrs["items"].(map[string]any)["L"].([]any)[0].(map[string]any)["M"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "Osaka"}, elem["city"])
	})

	t.Run(
		"SET 3-level nested path intermediate missing returns ValidationException",
		func(t *testing.T) {
			// SET a.nonexistent.c: covers mMap[seg.attr] not found for non-leaf in setAtDynamoValue.
			require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n9"},
				"a": {"M": {}}
			}
		}`).Code)
			w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n9"}},
			"UpdateExpression": "SET a.#b.#c = :val",
			"ExpressionAttributeNames": {"#b": "nonexistent", "#c": "c"},
			"ExpressionAttributeValues": {":val": {"S": "x"}},
			"ReturnValues": "ALL_NEW"
		}`)
			assert.Equal(t, 400, w.Code)
			assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
		},
	)

	t.Run("SET list index on scalar value returns ValidationException", func(t *testing.T) {
		// SET name[0] = :val where name is a scalar covers the 'L' key missing path.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "n10"}, "name": {"S": "Alice"}}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n10"}},
			"UpdateExpression": "SET #name[0] = :val",
			"ExpressionAttributeNames": {"#name": "name"},
			"ExpressionAttributeValues": {":val": {"S": "x"}},
			"ReturnValues": "ALL_NEW"
		}`)
		assert.Equal(t, 400, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run(
		"REMOVE list[N].attr is a no-op when element missing (3-level via list)",
		func(t *testing.T) {
			// Covers the non-leaf list navigation path in removeAtDynamoValue.
			require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n11"},
				"items": {"L": [{"M": {"city": {"S": "Tokyo"}, "zip": {"S": "100"}}}]}
			}
		}`).Code)
			w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n11"}},
			"UpdateExpression": "REMOVE #items[0].#zip",
			"ExpressionAttributeNames": {"#items": "items", "#zip": "zip"},
			"ReturnValues": "ALL_NEW"
		}`)
			require.Equal(t, 200, w.Code)
			var out map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
			attrs := out["Attributes"].(map[string]any)
			elem := attrs["items"].(map[string]any)["L"].([]any)[0].(map[string]any)["M"].(map[string]any)
			_, hasZip := elem["zip"]
			assert.False(t, hasZip) // zip removed
			_, hasCity := elem["city"]
			assert.True(t, hasCity) // city unchanged
		},
	)

	t.Run("REMOVE 3-level nested path intermediate missing is a no-op", func(t *testing.T) {
		// Covers mMap[seg.attr] not found for non-leaf in removeAtDynamoValue.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n12"},
				"a": {"M": {}}
			}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n12"}},
			"UpdateExpression": "REMOVE a.#b.#c",
			"ExpressionAttributeNames": {"#b": "nonexistent", "#c": "c"},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code) // no-op, no error
	})

	t.Run("REMOVE list index on scalar value is a no-op", func(t *testing.T) {
		// Covers the 'L' key missing path in removeAtDynamoValue.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "n13"}, "name": {"S": "Alice"}}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n13"}},
			"UpdateExpression": "REMOVE #name[0]",
			"ExpressionAttributeNames": {"#name": "name"},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "Alice"}, attrs["name"]) // unchanged
	})

	t.Run("REMOVE nested path where top-level attribute is absent is a no-op", func(t *testing.T) {
		// REMOVE nonexistent.field — top-level key missing → applyNestedRemove returns nil.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "n14"}}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n14"}},
			"UpdateExpression": "REMOVE noattr.#field",
			"ExpressionAttributeNames": {"#field": "x"},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code) // no-op, no error
	})

	t.Run("SET nested path with if_not_exists uses fallback when absent", func(t *testing.T) {
		// nestedSetOp with ifNotExistsOp as val; first arg of if_not_exists is a top-level ref
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n15"},
				"meta": {"M": {}}
			}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n15"}},
			"UpdateExpression": "SET #meta.#count = if_not_exists(#count, :zero)",
			"ExpressionAttributeNames": {"#meta": "meta", "#count": "count"},
			"ExpressionAttributeValues": {":zero": {"N": "0"}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		metaM := attrs["meta"].(map[string]any)["M"].(map[string]any)
		assert.Equal(
			t,
			map[string]any{"N": "0"},
			metaM["count"],
		) // fallback used because count absent
	})

	t.Run("SET nested path with list_append appends to nested list", func(t *testing.T) {
		// nestedSetOp with listAppendOp as val; left arg of list_append is a top-level ref
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n16"},
				"tags": {"L": [{"S": "a"}]},
				"meta": {"M": {}}
			}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n16"}},
			"UpdateExpression": "SET #meta.#tags = list_append(#tags, :new)",
			"ExpressionAttributeNames": {"#meta": "meta", "#tags": "tags"},
			"ExpressionAttributeValues": {":new": {"L": [{"S": "b"}]}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		metaM := attrs["meta"].(map[string]any)["M"].(map[string]any)
		list := metaM["tags"].(map[string]any)["L"].([]any)
		require.Len(t, list, 2)
		assert.Equal(t, map[string]any{"S": "a"}, list[0])
		assert.Equal(t, map[string]any{"S": "b"}, list[1])
	})

	t.Run("SET 3-level deep M map path (a.b.c) succeeds", func(t *testing.T) {
		// Exercises setAtDynamoValue's recursive call: attr→M→attr→M→attr (3-level M nesting).
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n17"},
				"a": {"M": {"b": {"M": {"c": {"N": "1"}}}}}
			}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n17"}},
			"UpdateExpression": "SET a.b.c = :new",
			"ExpressionAttributeValues": {":new": {"N": "2"}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		aM := attrs["a"].(map[string]any)["M"].(map[string]any)
		bM := aM["b"].(map[string]any)["M"].(map[string]any)
		assert.Equal(t, map[string]any{"N": "2"}, bM["c"])
	})

	t.Run("REMOVE 3-level deep M map path (a.b.c) succeeds", func(t *testing.T) {
		// Exercises removeAtDynamoValue's recursive call: attr→M→attr→M→attr (3-level M nesting).
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n18"},
				"a": {"M": {"b": {"M": {"c": {"N": "1"}, "d": {"N": "2"}}}}}
			}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n18"}},
			"UpdateExpression": "REMOVE a.b.c",
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		aM := attrs["a"].(map[string]any)["M"].(map[string]any)
		bM := aM["b"].(map[string]any)["M"].(map[string]any)
		_, hasC := bM["c"]
		assert.False(t, hasC)
		assert.Equal(t, map[string]any{"N": "2"}, bM["d"])
	})

	t.Run("400 when nested SET list_append left arg is not a list", func(t *testing.T) {
		// Exercises the error path in nestedSetOp + listAppendOp in UpdateItem.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n19"},
				"a": {"M": {"b": {"N": "1"}}}
			}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n19"}},
			"UpdateExpression": "SET #a.#b = list_append(#a, :new)",
			"ExpressionAttributeNames": {"#a": "a", "#b": "b"},
			"ExpressionAttributeValues": {":new": {"L": [{"S": "x"}]}}
		}`)
		require.Equal(t, 400, w.Code)
	})

	t.Run("SET nested path with if_not_exists nested path arg uses fallback", func(t *testing.T) {
		// AWS spec: if_not_exists(a.b, :v) is valid — first arg may be a nested path.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n20"},
				"meta": {"M": {}}
			}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n20"}},
			"UpdateExpression": "SET #meta.#count = if_not_exists(#meta.#count, :zero)",
			"ExpressionAttributeNames": {"#meta": "meta", "#count": "count"},
			"ExpressionAttributeValues": {":zero": {"N": "0"}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		metaM := attrs["meta"].(map[string]any)["M"].(map[string]any)
		assert.Equal(t, map[string]any{"N": "0"}, metaM["count"]) // fallback: meta.count absent
	})

	t.Run(
		"SET nested path with if_not_exists nested path arg preserves existing value",
		func(t *testing.T) {
			// When meta.count already exists, if_not_exists(#meta.#count, :zero) must return the existing value.
			require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n21"},
				"meta": {"M": {"count": {"N": "5"}}}
			}
		}`).Code)
			w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n21"}},
			"UpdateExpression": "SET #meta.#count = if_not_exists(#meta.#count, :zero)",
			"ExpressionAttributeNames": {"#meta": "meta", "#count": "count"},
			"ExpressionAttributeValues": {":zero": {"N": "0"}},
			"ReturnValues": "ALL_NEW"
		}`)
			require.Equal(t, 200, w.Code)
			var out map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
			attrs := out["Attributes"].(map[string]any)
			metaM := attrs["meta"].(map[string]any)["M"].(map[string]any)
			assert.Equal(t, map[string]any{"N": "5"}, metaM["count"]) // existing value preserved
		},
	)

	t.Run("SET nested path with list_append nested path arg appends", func(t *testing.T) {
		// AWS spec: list_append(a.b, :v) is valid — first arg may be a nested path.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {
				"pk": {"S": "n22"},
				"meta": {"M": {"tags": {"L": [{"S": "a"}]}}}
			}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n22"}},
			"UpdateExpression": "SET #meta.#tags = list_append(#meta.#tags, :new)",
			"ExpressionAttributeNames": {"#meta": "meta", "#tags": "tags"},
			"ExpressionAttributeValues": {":new": {"L": [{"S": "b"}]}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		metaM := attrs["meta"].(map[string]any)["M"].(map[string]any)
		list := metaM["tags"].(map[string]any)["L"].([]any)
		require.Len(t, list, 2)
		assert.Equal(t, map[string]any{"S": "a"}, list[0])
		assert.Equal(t, map[string]any{"S": "b"}, list[1])
	})

	t.Run(
		"if_not_exists with nested path where intermediate is wrong type uses fallback",
		func(t *testing.T) {
			// resolveUpdatePath: m["M"] not found when intermediate DynamoDB value is not M type.
			require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "n23"}, "cnt": {"N": "5"}}
		}`).Code)
			w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n23"}},
			"UpdateExpression": "SET x = if_not_exists(cnt.field, :default)",
			"ExpressionAttributeValues": {":default": {"N": "0"}},
			"ReturnValues": "ALL_NEW"
		}`)
			require.Equal(t, 200, w.Code)
			var out map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
			attrs := out["Attributes"].(map[string]any)
			assert.Equal(t, map[string]any{"N": "0"}, attrs["x"]) // fallback: cnt is not M type
		},
	)

	t.Run("if_not_exists with list-index nested path returns existing element", func(t *testing.T) {
		// resolveUpdatePath: L path success — returns existing list element.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "n24"}, "tags": {"L": [{"S": "first"}]}}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n24"}},
			"UpdateExpression": "SET x = if_not_exists(tags[0], :default)",
			"ExpressionAttributeValues": {":default": {"S": "fallback"}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "first"}, attrs["x"]) // existing element returned
	})

	t.Run("if_not_exists with list-index out of bounds uses fallback", func(t *testing.T) {
		// resolveUpdatePath: seg.index >= len(lSlice) → return nil.
		require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "n25"}, "tags": {"L": [{"S": "a"}]}}
		}`).Code)
		w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n25"}},
			"UpdateExpression": "SET x = if_not_exists(tags[5], :default)",
			"ExpressionAttributeValues": {":default": {"S": "fallback"}},
			"ReturnValues": "ALL_NEW"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		attrs := out["Attributes"].(map[string]any)
		assert.Equal(
			t,
			map[string]any{"S": "fallback"},
			attrs["x"],
		) // index out of bounds → fallback
	})

	t.Run(
		"if_not_exists with list-index where intermediate is not L type uses fallback",
		func(t *testing.T) {
			// resolveUpdatePath: m["L"] not found when DynamoDB value is M not L.
			require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
			"TableName": "test-table",
			"Item": {"pk": {"S": "n26"}, "meta": {"M": {"x": {"N": "1"}}}}
		}`).Code)
			w := dynamo(t, ro, "UpdateItem", `{
			"TableName": "test-table",
			"Key": {"pk": {"S": "n26"}},
			"UpdateExpression": "SET x = if_not_exists(meta[0], :default)",
			"ExpressionAttributeValues": {":default": {"N": "0"}},
			"ReturnValues": "ALL_NEW"
		}`)
			require.Equal(t, 200, w.Code)
			var out map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
			attrs := out["Attributes"].(map[string]any)
			assert.Equal(t, map[string]any{"N": "0"}, attrs["x"]) // meta is M not L → fallback
		},
	)
}

func TestNestedPathFilterExpression(t *testing.T) {
	ro := newTestRouter(t)
	require.Equal(t, 200, dynamo(t, ro, "CreateTable", createTableBody).Code)

	require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
		"TableName": "test-table",
		"Item": {
			"pk": {"S": "p1"},
			"address": {"M": {"city": {"S": "NYC"}, "zip": {"S": "10001"}}}
		}
	}`).Code)
	require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
		"TableName": "test-table",
		"Item": {
			"pk": {"S": "p2"},
			"address": {"M": {"city": {"S": "LA"}, "zip": {"S": "90001"}}}
		}
	}`).Code)
	require.Equal(t, 200, dynamo(t, ro, "PutItem", `{
		"TableName": "test-table",
		"Item": {"pk": {"S": "p3"}}
	}`).Code)

	t.Run("Scan FilterExpression dot notation match", func(t *testing.T) {
		w := dynamo(t, ro, "Scan", `{
			"TableName": "test-table",
			"FilterExpression": "#addr.#city = :nyc",
			"ExpressionAttributeNames": {"#addr": "address", "#city": "city"},
			"ExpressionAttributeValues": {":nyc": {"S": "NYC"}}
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		items := out["Items"].([]any)
		require.Len(t, items, 1)
		assert.Equal(t, "p1", items[0].(map[string]any)["pk"].(map[string]any)["S"])
	})

	t.Run("Scan FilterExpression attribute_exists nested", func(t *testing.T) {
		w := dynamo(t, ro, "Scan", `{
			"TableName": "test-table",
			"FilterExpression": "attribute_exists(address.city)"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		items := out["Items"].([]any)
		require.Len(t, items, 2) // p1 and p2
	})

	t.Run("Scan FilterExpression attribute_not_exists nested", func(t *testing.T) {
		w := dynamo(t, ro, "Scan", `{
			"TableName": "test-table",
			"FilterExpression": "attribute_not_exists(address.city)"
		}`)
		require.Equal(t, 200, w.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		items := out["Items"].([]any)
		require.Len(t, items, 1)
		assert.Equal(t, "p3", items[0].(map[string]any)["pk"].(map[string]any)["S"])
	})
}
