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
	createTableFn        func(meta TableMetadata) error
	deleteTableFn        func(name string) error
	describeTableFn      func(name string) (TableMetadata, error)
	listTablesFn         func() ([]string, error)
	putItemFn            func(tableName string, item map[string]any) (map[string]any, error)
	getItemFn            func(tableName string, key map[string]any) (map[string]any, error)
	deleteItemFn         func(tableName string, key map[string]any) (map[string]any, error)
	scanFn               func(tableName string, opts ScanOptions) ([]map[string]any, map[string]any, error)
	updateItemFn         func(tableName string, key map[string]any, updates map[string]any) (map[string]any, map[string]any, error)
	queryFn              func(tableName, hashKeyName string, hashKeyValue any) ([]map[string]any, error)
	batchGetItemsFn      func(tableName string, keys []map[string]any) ([]map[string]any, error)
	batchWriteItemsFn    func(tableName string, puts []map[string]any, deletes []map[string]any) error
	updateTimeToLiveFn   func(tableName string, spec TTLSpec) (TTLSpec, error)
	describeTimeToLiveFn func(tableName string) (string, *TTLSpec, error)
	tagResourceFn        func(resourceARN string, tags map[string]string) error
	untagResourceFn      func(resourceARN string, tagKeys []string) error
	listTagsOfResourceFn func(resourceARN string) (map[string]string, error)
	updateTableFn        func(tableName string, in UpdateTableInput) (TableMetadata, error)
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
		body := `{"TableName":"gsi-table","Item":{` +
			`"pk":{"S":"` + it.pk + `"},` +
			`"sk":{"S":"` + it.sk + `"},` +
			`"gsi_pk":{"S":"` + it.gsiPK + `"},` +
			`"gsi_sk":{"S":"` + it.gsiSK + `"}` +
			`}}`
		require.Equal(t, http.StatusOK, dynamo(t, ro, "PutItem", body).Code)
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
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["__type"].(string), "ValidationException")
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
		// sorted by lsi sort key (gsi_sk), ascending: "a" < "b"
		assert.Equal(t, "a", items[0].(map[string]any)["gsi_sk"].(map[string]any)["S"])
		assert.Equal(t, "b", items[1].(map[string]any)["gsi_sk"].(map[string]any)["S"])
	})
}
