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
		w := dynamo(t, ro, "UpdateItem", `{}`)
		assert.Equal(t, http.StatusNotImplemented, w.Code)
		assertErrorType(t, w, "NotImplemented")
	})
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
	createTableFn   func(meta TableMetadata) error
	deleteTableFn   func(name string) error
	describeTableFn func(name string) (TableMetadata, error)
	listTablesFn    func() ([]string, error)
	putItemFn       func(tableName string, item map[string]any) error
	getItemFn       func(tableName string, key map[string]any) (map[string]any, error)
	deleteItemFn    func(tableName string, key map[string]any) error
	scanFn          func(tableName string) ([]map[string]any, error)
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
