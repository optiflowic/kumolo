package dynamodb

import (
	"encoding/json"
	"errors"
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
		assertErrorType(t, w, "ResourceInUseException")
	})

	t.Run("400 for missing TableName", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "CreateTable", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ResourceNotFoundException")
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
		assertErrorType(t, w, "ResourceNotFoundException")
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
		assertErrorType(t, w, "ResourceNotFoundException")
	})

	t.Run("400 for missing key attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "PutItem", `{"TableName": "test-table", "Item": {"other": {"S": "x"}}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ResourceNotFoundException")
	})

	t.Run("400 for missing key attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "GetItem", `{"TableName":"test-table","Key":{}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ResourceNotFoundException")
	})

	t.Run("400 for missing key attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "DeleteItem", `{"TableName":"test-table","Key":{}}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ResourceNotFoundException")
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
}

func TestUnknownOperation(t *testing.T) {
	t.Run("501 for unknown target", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UnknownOperation", `{}`)
		assert.Equal(t, http.StatusNotImplemented, w.Code)
		assertErrorType(t, w, "NotImplemented")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ResourceNotFoundException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
	})

	t.Run("400 for missing KeyConditionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "Query", `{"TableName":"test-table"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "ValidationException")
	})

	t.Run("400 for unsupported KeyConditionExpression", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "begins_with(pk, :v)",
            "ExpressionAttributeValues": {":v": {"S": "a"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "ValidationException")
	})

	t.Run("400 for missing table", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "Query", `{
            "TableName": "no-such-table",
            "KeyConditionExpression": "pk = :pk",
            "ExpressionAttributeValues": {":pk": {"S": "a"}}
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "ResourceNotFoundException")
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
		assertErrorType(t, w, "ValidationException")
	})

	t.Run("400 for missing ExpressionAttributeValues entry", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTableBody).Code)
		w := dynamo(t, ro, "Query", `{
            "TableName": "test-table",
            "KeyConditionExpression": "pk = :pk"
        }`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "ValidationException")
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
	putItemFn            func(tableName string, item map[string]any) error
	getItemFn            func(tableName string, key map[string]any) (map[string]any, error)
	deleteItemFn         func(tableName string, key map[string]any) error
	scanFn               func(tableName string) ([]map[string]any, error)
	updateItemFn         func(tableName string, key map[string]any, updates map[string]any) (map[string]any, map[string]any, error)
	queryFn              func(tableName, hashKeyName string, hashKeyValue any) ([]map[string]any, error)
	batchGetItemsFn      func(tableName string, keys []map[string]any) ([]map[string]any, error)
	batchWriteItemsFn    func(tableName string, puts []map[string]any, deletes []map[string]any) error
	updateTimeToLiveFn   func(tableName string, spec TTLSpec) (TTLSpec, error)
	describeTimeToLiveFn func(tableName string) (string, *TTLSpec, error)
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

func (m *mockStore) PutItem(tableName string, item map[string]any) error {
	return m.putItemFn(tableName, item)
}

func (m *mockStore) GetItem(tableName string, key map[string]any) (map[string]any, error) {
	return m.getItemFn(tableName, key)
}

func (m *mockStore) DeleteItem(tableName string, key map[string]any) error {
	return m.deleteItemFn(tableName, key)
}

func (m *mockStore) Scan(tableName string) ([]map[string]any, error) {
	return m.scanFn(tableName)
}

func (m *mockStore) UpdateItem(
	tableName string,
	key map[string]any,
	updates map[string]any,
) (map[string]any, map[string]any, error) {
	return m.updateItemFn(tableName, key, updates)
}

func (m *mockStore) Query(
	tableName, hashKeyName string,
	hashKeyValue any,
	_ *SortKeyCondition,
) ([]map[string]any, error) {
	return m.queryFn(tableName, hashKeyName, hashKeyValue)
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
			putItemFn: func(string, map[string]any) error { return errInternal },
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
			deleteItemFn: func(string, map[string]any) error { return errInternal },
		}}
		w := dynamo(t, ro, "DeleteItem", `{"TableName":"t","Key":{"pk":{"S":"k"}}}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestHandleScan_InternalErrors(t *testing.T) {
	t.Run("500 when Scan fails with unexpected error", func(t *testing.T) {
		ro := &Router{storage: &mockStore{
			scanFn: func(string) ([]map[string]any, error) { return nil, errInternal },
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
		writeError(w, http.StatusBadRequest, "ValidationException", "test")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ResourceNotFoundException")
	})

	t.Run("400 for missing key attribute", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTblBody).Code)
		w := dynamo(t, ro, "BatchGetItem", `{
			"RequestItems": {"tbl": {"Keys": [{}]}}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ResourceNotFoundException")
	})

	t.Run("400 for missing key attribute in put", func(t *testing.T) {
		ro := newTestRouter(t)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createTblBody).Code)
		w := dynamo(t, ro, "BatchWriteItem", `{
			"RequestItems": {"tbl": [{"PutRequest": {"Item": {}}}]}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "ValidationException")
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
		assertErrorType(t, w, "ValidationException")
	})

	t.Run("400 for table not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "UpdateTimeToLive", `{
			"TableName": "no-such",
			"TimeToLiveSpecification": {"AttributeName": "exp", "Enabled": true}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "ResourceNotFoundException")
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
		assertErrorType(t, w, "ValidationException")
	})

	t.Run("400 for table not found", func(t *testing.T) {
		ro := newTestRouter(t)
		w := dynamo(t, ro, "DescribeTimeToLive", `{"TableName": "no-such"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "ResourceNotFoundException")
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
