package dynamodb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- parser tests ----

func TestParsePartiQL(t *testing.T) {
	tests := []struct {
		name      string
		stmt      string
		params    []map[string]any
		wantKind  pqStmtKind
		wantTable string
		wantErr   bool
	}{
		{
			name:      "SELECT star from table",
			stmt:      `SELECT * FROM "mytable"`,
			wantKind:  pqSelect,
			wantTable: "mytable",
		},
		{
			name:      "SELECT with WHERE equality",
			stmt:      `SELECT * FROM "t" WHERE pk = ?`,
			params:    []map[string]any{{"S": "k1"}},
			wantKind:  pqSelect,
			wantTable: "t",
		},
		{
			name:      "SELECT with LIMIT",
			stmt:      `SELECT * FROM "t" LIMIT 5`,
			wantKind:  pqSelect,
			wantTable: "t",
		},
		{
			name:      "INSERT",
			stmt:      `INSERT INTO "t" VALUE {'pk': ?, 'name': ?}`,
			params:    []map[string]any{{"S": "k1"}, {"S": "alice"}},
			wantKind:  pqInsert,
			wantTable: "t",
		},
		{
			name:      "UPDATE",
			stmt:      `UPDATE "t" SET name = ? WHERE pk = ?`,
			params:    []map[string]any{{"S": "bob"}, {"S": "k1"}},
			wantKind:  pqUpdate,
			wantTable: "t",
		},
		{
			name:      "DELETE",
			stmt:      `DELETE FROM "t" WHERE pk = ?`,
			params:    []map[string]any{{"S": "k1"}},
			wantKind:  pqDelete,
			wantTable: "t",
		},
		{
			name:    "empty statement",
			stmt:    ``,
			wantErr: true,
		},
		{
			name:    "unknown statement type",
			stmt:    `REPLACE INTO t VALUES (1)`,
			wantErr: true,
		},
		{
			name:    "too few parameters",
			stmt:    `SELECT * FROM "t" WHERE pk = ?`,
			params:  nil,
			wantErr: true,
		},
		{
			name:      "unquoted table name",
			stmt:      `SELECT * FROM mytable`,
			wantKind:  pqSelect,
			wantTable: "mytable",
		},
		{
			name:      "backtick-quoted table name",
			stmt:      "SELECT * FROM `mytable`",
			wantKind:  pqSelect,
			wantTable: "mytable",
		},
		{
			name:      "literal string value",
			stmt:      `SELECT * FROM "t" WHERE pk = 'hello'`,
			wantKind:  pqSelect,
			wantTable: "t",
		},
		{
			name:      "literal number value",
			stmt:      `SELECT * FROM "t" WHERE pk = 42`,
			wantKind:  pqSelect,
			wantTable: "t",
		},
		{
			name:      "BETWEEN condition",
			stmt:      `SELECT * FROM "t" WHERE sk BETWEEN ? AND ?`,
			params:    []map[string]any{{"N": "1"}, {"N": "10"}},
			wantKind:  pqSelect,
			wantTable: "t",
		},
		{
			name:      "IN condition",
			stmt:      `SELECT * FROM "t" WHERE pk IN (?, ?)`,
			params:    []map[string]any{{"S": "a"}, {"S": "b"}},
			wantKind:  pqSelect,
			wantTable: "t",
		},
		{
			name:      "multi-SET update",
			stmt:      `UPDATE "t" SET a = ?, b = ? WHERE pk = ?`,
			params:    []map[string]any{{"S": "x"}, {"N": "1"}, {"S": "k"}},
			wantKind:  pqUpdate,
			wantTable: "t",
		},
		{
			name:      "INSERT with literal values",
			stmt:      `INSERT INTO "t" VALUE {'pk': 'mykey', 'active': TRUE, 'count': 0}`,
			wantKind:  pqInsert,
			wantTable: "t",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := parsePartiQL(tc.stmt, tc.params)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantKind, stmt.kind)
			assert.Equal(t, tc.wantTable, stmt.tableName)
		})
	}
}

func TestParsePartiQL_Values(t *testing.T) {
	t.Run("INSERT item has correct DynamoDB types", func(t *testing.T) {
		stmt, err := parsePartiQL(
			`INSERT INTO "t" VALUE {'pk': ?, 'active': TRUE, 'score': 42, 'name': 'alice', 'nothing': NULL}`,
			[]map[string]any{{"S": "k1"}},
		)
		require.NoError(t, err)
		require.Equal(t, pqInsert, stmt.kind)
		assert.Equal(t, map[string]any{"S": "k1"}, stmt.item["pk"])
		assert.Equal(t, map[string]any{"BOOL": true}, stmt.item["active"])
		assert.Equal(t, map[string]any{"N": "42"}, stmt.item["score"])
		assert.Equal(t, map[string]any{"S": "alice"}, stmt.item["name"])
		assert.Equal(t, map[string]any{"NULL": true}, stmt.item["nothing"])
	})

	t.Run("SELECT WHERE conditions parsed correctly", func(t *testing.T) {
		stmt, err := parsePartiQL(
			`SELECT * FROM "t" WHERE pk = ? AND sk = ?`,
			[]map[string]any{{"S": "hash"}, {"N": "1"}},
		)
		require.NoError(t, err)
		require.Len(t, stmt.where, 2)
		assert.Equal(t, "pk", stmt.where[0].attr)
		assert.Equal(t, "=", stmt.where[0].op)
		assert.Equal(t, map[string]any{"S": "hash"}, stmt.where[0].val)
		assert.Equal(t, "sk", stmt.where[1].attr)
		assert.Equal(t, map[string]any{"N": "1"}, stmt.where[1].val)
	})

	t.Run("SELECT BETWEEN condition", func(t *testing.T) {
		stmt, err := parsePartiQL(
			`SELECT * FROM "t" WHERE pk = ? AND sk BETWEEN ? AND ?`,
			[]map[string]any{{"S": "hash"}, {"N": "1"}, {"N": "10"}},
		)
		require.NoError(t, err)
		require.Len(t, stmt.where, 2)
		assert.Equal(t, "BETWEEN", stmt.where[1].op)
		assert.Equal(t, map[string]any{"N": "1"}, stmt.where[1].val)
		assert.Equal(t, map[string]any{"N": "10"}, stmt.where[1].val2)
	})

	t.Run("UPDATE sets parsed correctly", func(t *testing.T) {
		stmt, err := parsePartiQL(
			`UPDATE "t" SET col1 = ?, col2 = ? WHERE pk = ?`,
			[]map[string]any{{"S": "v1"}, {"N": "2"}, {"S": "k"}},
		)
		require.NoError(t, err)
		require.Len(t, stmt.sets, 2)
		assert.Equal(t, "col1", stmt.sets[0].attr)
		assert.Equal(t, map[string]any{"S": "v1"}, stmt.sets[0].val)
		assert.Equal(t, "col2", stmt.sets[1].attr)
		assert.Equal(t, map[string]any{"N": "2"}, stmt.sets[1].val)
		require.Len(t, stmt.where, 1)
		assert.Equal(t, "pk", stmt.where[0].attr)
	})

	t.Run("SELECT LIMIT stored", func(t *testing.T) {
		stmt, err := parsePartiQL(`SELECT * FROM "t" LIMIT 10`, nil)
		require.NoError(t, err)
		require.NotNil(t, stmt.stmtLimit)
		assert.Equal(t, 10, *stmt.stmtLimit)
	})
}

// ---- ExecuteStatement handler tests ----

const createHashTable = `{
	"TableName": "t",
	"KeySchema": [{"AttributeName":"pk","KeyType":"HASH"}],
	"AttributeDefinitions": [{"AttributeName":"pk","AttributeType":"S"}],
	"BillingMode": "PAY_PER_REQUEST"
}`

const createCompositeTable = `{
	"TableName": "t2",
	"KeySchema": [
		{"AttributeName":"pk","KeyType":"HASH"},
		{"AttributeName":"sk","KeyType":"RANGE"}
	],
	"AttributeDefinitions": [
		{"AttributeName":"pk","AttributeType":"S"},
		{"AttributeName":"sk","AttributeType":"N"}
	],
	"BillingMode": "PAY_PER_REQUEST"
}`

func setup(t *testing.T) *Router {
	t.Helper()
	ro := newTestRouter(t)
	require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createHashTable).Code)
	require.Equal(t, http.StatusOK, dynamo(t, ro, "CreateTable", createCompositeTable).Code)
	return ro
}

func TestExecuteStatement_INSERT(t *testing.T) {
	ro := setup(t)

	t.Run("inserts item with ? param", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': ?}",
			"Parameters": [{"S":"k1"}]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("inserts item with literal value", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'k2', 'score': 99}"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("400 on missing Statement", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 on invalid JSON", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 on parse error", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{"Statement":"BADOP FROM t"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 on table not found", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"nosuchtable\" VALUE {'pk': 'x'}"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("400 on invalid Limit", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\"",
			"Limit": 0
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestExecuteStatement_SELECT(t *testing.T) {
	ro := setup(t)

	// Pre-populate items
	require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
		"Statement": "INSERT INTO \"t\" VALUE {'pk': 'a', 'val': 'alpha'}"
	}`).Code)
	require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
		"Statement": "INSERT INTO \"t\" VALUE {'pk': 'b', 'val': 'beta'}"
	}`).Code)

	t.Run("SELECT all via Scan", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{"Statement":"SELECT * FROM \"t\""}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		assert.Len(t, items, 2)
	})

	t.Run("SELECT with hash key equality via Query", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\" WHERE pk = ?",
			"Parameters": [{"S":"a"}]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		assert.Len(t, items, 1)
		item := items[0].(map[string]any)
		assert.Equal(t, map[string]any{"S": "a"}, item["pk"])
	})

	t.Run("SELECT returns empty Items for no match", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\" WHERE pk = ?",
			"Parameters": [{"S":"notfound"}]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		assert.Empty(t, items)
	})

	t.Run("SELECT with LIMIT", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\" LIMIT 1"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		assert.Len(t, items, 1)
		assert.NotEmpty(t, resp["NextToken"])
	})

	t.Run("SELECT with pagination", func(t *testing.T) {
		// page 1
		w1 := dynamo(t, ro, "ExecuteStatement", `{"Statement":"SELECT * FROM \"t\"","Limit":1}`)
		assert.Equal(t, http.StatusOK, w1.Code)
		var resp1 map[string]any
		require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
		token, ok := resp1["NextToken"].(string)
		require.True(t, ok, "expected NextToken on page 1")

		// page 2
		body, _ := json.Marshal(map[string]any{
			"Statement": `SELECT * FROM "t"`,
			"Limit":     1,
			"NextToken": token,
		})
		w2 := dynamo(t, ro, "ExecuteStatement", string(body))
		assert.Equal(t, http.StatusOK, w2.Code)
		var resp2 map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
		items2 := resp2["Items"].([]any)
		assert.Len(t, items2, 1)
	})
}

func TestExecuteStatement_SELECT_CompositeKey(t *testing.T) {
	ro := setup(t)

	// Pre-populate t2
	for _, body := range []string{
		`{"Statement":"INSERT INTO \"t2\" VALUE {'pk': 'p', 'sk': 1, 'data': 'one'}"}`,
		`{"Statement":"INSERT INTO \"t2\" VALUE {'pk': 'p', 'sk': 2, 'data': 'two'}"}`,
		`{"Statement":"INSERT INTO \"t2\" VALUE {'pk': 'p', 'sk': 3, 'data': 'three'}"}`,
	} {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", body).Code)
	}

	t.Run("point lookup with both keys via GetItem", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t2\" WHERE pk = ? AND sk = ?",
			"Parameters": [{"S":"p"}, {"N":"2"}]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		assert.Len(t, items, 1)
		item := items[0].(map[string]any)
		assert.Equal(t, map[string]any{"S": "two"}, item["data"])
	})

	t.Run("Query with hash key only returns multiple items", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t2\" WHERE pk = ?",
			"Parameters": [{"S":"p"}]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		assert.Len(t, items, 3)
	})

	t.Run("Query with BETWEEN sort key condition", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t2\" WHERE pk = ? AND sk BETWEEN ? AND ?",
			"Parameters": [{"S":"p"}, {"N":"1"}, {"N":"2"}]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		assert.Len(t, items, 2)
	})
}

func TestExecuteStatement_UPDATE(t *testing.T) {
	ro := setup(t)

	require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
		"Statement": "INSERT INTO \"t\" VALUE {'pk': 'u1', 'val': 'original'}"
	}`).Code)

	t.Run("updates attribute", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "UPDATE \"t\" SET val = ? WHERE pk = ?",
			"Parameters": [{"S":"updated"}, {"S":"u1"}]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)

		// Verify
		w2 := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\" WHERE pk = ?",
			"Parameters": [{"S":"u1"}]
		}`)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 1)
		assert.Equal(t, map[string]any{"S": "updated"}, items[0].(map[string]any)["val"])
	})

	t.Run("400 when key not in WHERE", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "UPDATE \"t\" SET val = ? WHERE val = ?",
			"Parameters": [{"S":"x"}, {"S":"y"}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestExecuteStatement_DELETE(t *testing.T) {
	ro := setup(t)

	require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
		"Statement": "INSERT INTO \"t\" VALUE {'pk': 'd1'}"
	}`).Code)

	t.Run("deletes item", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "DELETE FROM \"t\" WHERE pk = ?",
			"Parameters": [{"S":"d1"}]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)

		// Verify deleted
		w2 := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\" WHERE pk = ?",
			"Parameters": [{"S":"d1"}]
		}`)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
		assert.Empty(t, resp["Items"].([]any))
	})
}

func TestExecuteStatement_ConsumedCapacity(t *testing.T) {
	ro := setup(t)

	t.Run("returns ConsumedCapacity when TOTAL", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\"",
			"ReturnConsumedCapacity": "TOTAL"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotNil(t, resp["ConsumedCapacity"])
	})

	t.Run("no ConsumedCapacity when NONE", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\"",
			"ReturnConsumedCapacity": "NONE"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Nil(t, resp["ConsumedCapacity"])
	})

	t.Run("400 on invalid ReturnConsumedCapacity", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\"",
			"ReturnConsumedCapacity": "INVALID"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// ---- BatchExecuteStatement handler tests ----

func TestBatchExecuteStatement_Reads(t *testing.T) {
	ro := setup(t)

	require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
		"Statement": "INSERT INTO \"t\" VALUE {'pk': 'b1', 'val': 'v1'}"
	}`).Code)
	require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
		"Statement": "INSERT INTO \"t\" VALUE {'pk': 'b2', 'val': 'v2'}"
	}`).Code)

	t.Run("batch read returns per-statement results", func(t *testing.T) {
		w := dynamo(t, ro, "BatchExecuteStatement", `{
			"Statements": [
				{"Statement": "SELECT * FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"b1"}]},
				{"Statement": "SELECT * FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"notfound"}]}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		responses := resp["Responses"].([]any)
		assert.Len(t, responses, 2)
		// First: found
		r0 := responses[0].(map[string]any)
		assert.Nil(t, r0["Error"])
		assert.NotNil(t, r0["Item"])
		// Second: not found (no item, no error)
		r1 := responses[1].(map[string]any)
		assert.Nil(t, r1["Error"])
		assert.Nil(t, r1["Item"])
	})

	t.Run("400 on empty Statements", func(t *testing.T) {
		w := dynamo(t, ro, "BatchExecuteStatement", `{"Statements":[]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 on >25 statements", func(t *testing.T) {
		stmts := make([]map[string]any, 26)
		for i := range stmts {
			stmts[i] = map[string]any{
				"Statement":  `SELECT * FROM "t"`,
				"Parameters": []any{},
			}
		}
		body, _ := json.Marshal(map[string]any{"Statements": stmts})
		w := dynamo(t, ro, "BatchExecuteStatement", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 on mixed reads and writes", func(t *testing.T) {
		w := dynamo(t, ro, "BatchExecuteStatement", `{
			"Statements": [
				{"Statement": "SELECT * FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"b1"}]},
				{"Statement": "INSERT INTO \"t\" VALUE {'pk': 'x'}"}
			]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

func TestBatchExecuteStatement_Writes(t *testing.T) {
	ro := setup(t)

	t.Run("batch write executes all statements", func(t *testing.T) {
		w := dynamo(t, ro, "BatchExecuteStatement", `{
			"Statements": [
				{"Statement": "INSERT INTO \"t\" VALUE {'pk': 'w1'}"},
				{"Statement": "INSERT INTO \"t\" VALUE {'pk': 'w2'}"}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		responses := resp["Responses"].([]any)
		assert.Len(t, responses, 2)
		for _, r := range responses {
			assert.Nil(t, r.(map[string]any)["Error"])
		}
	})

	t.Run("per-statement error captured without failing whole batch", func(t *testing.T) {
		w := dynamo(t, ro, "BatchExecuteStatement", `{
			"Statements": [
				{"Statement": "INSERT INTO \"nosuchtable\" VALUE {'pk': 'x'}"},
				{"Statement": "INSERT INTO \"t\" VALUE {'pk': 'w3'}"}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		responses := resp["Responses"].([]any)
		assert.Len(t, responses, 2)
		// First stmt failed
		r0 := responses[0].(map[string]any)
		assert.NotNil(t, r0["Error"])
		// Second stmt succeeded
		r1 := responses[1].(map[string]any)
		assert.Nil(t, r1["Error"])
	})
}

// ---- ExecuteTransaction handler tests ----

func TestExecuteTransaction_Reads(t *testing.T) {
	ro := setup(t)

	require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
		"Statement": "INSERT INTO \"t\" VALUE {'pk': 'tx1', 'val': 'one'}"
	}`).Code)
	require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
		"Statement": "INSERT INTO \"t\" VALUE {'pk': 'tx2', 'val': 'two'}"
	}`).Code)

	t.Run("transact reads return items", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "SELECT * FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"tx1"}]},
				{"Statement": "SELECT * FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"tx2"}]}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		responses := resp["Responses"].([]any)
		assert.Len(t, responses, 2)
		r0 := responses[0].(map[string]any)
		item0 := r0["Item"].(map[string]any)
		assert.Equal(t, map[string]any{"S": "one"}, item0["val"])
	})

	t.Run("transact read missing item returns empty response", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "SELECT * FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"notfound"}]}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		responses := resp["Responses"].([]any)
		assert.Len(t, responses, 1)
		assert.Empty(t, responses[0].(map[string]any)["Item"])
	})
}

func TestExecuteTransaction_Writes(t *testing.T) {
	ro := setup(t)

	t.Run("transact writes succeed atomically", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "INSERT INTO \"t\" VALUE {'pk': 'ttx1', 'v': 'a'}"},
				{"Statement": "INSERT INTO \"t\" VALUE {'pk': 'ttx2', 'v': 'b'}"}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)

		// Verify both items exist
		w2 := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\" WHERE pk = ?",
			"Parameters": [{"S":"ttx1"}]
		}`)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
		assert.Len(t, resp["Items"].([]any), 1)
	})

	t.Run("400 on empty TransactStatements", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{"TransactStatements":[]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("400 on >100 statements", func(t *testing.T) {
		stmts := make([]map[string]any, 101)
		for i := range stmts {
			stmts[i] = map[string]any{"Statement": `INSERT INTO "t" VALUE {'pk': 'x'}`}
		}
		body, _ := json.Marshal(map[string]any{"TransactStatements": stmts})
		w := dynamo(t, ro, "ExecuteTransaction", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 on mixed reads and writes", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "SELECT * FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"x"}]},
				{"Statement": "INSERT INTO \"t\" VALUE {'pk': 'y'}"}
			]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

// ---- helper tests ----

func TestExtractExactKey(t *testing.T) {
	meta := TableMetadata{
		KeySchema: []KeySchemaElement{
			{AttributeName: "pk", KeyType: "HASH"},
			{AttributeName: "sk", KeyType: "RANGE"},
		},
	}
	t.Run("extracts both key attributes", func(t *testing.T) {
		where := []pqCond{
			{attr: "pk", op: "=", val: map[string]any{"S": "a"}},
			{attr: "sk", op: "=", val: map[string]any{"N": "1"}},
		}
		key, err := extractExactKey(where, meta)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"S": "a"}, key["pk"])
		assert.Equal(t, map[string]any{"N": "1"}, key["sk"])
	})

	t.Run("error when hash key missing", func(t *testing.T) {
		where := []pqCond{
			{attr: "sk", op: "=", val: map[string]any{"N": "1"}},
		}
		_, err := extractExactKey(where, meta)
		require.Error(t, err)
	})

	t.Run("error when key condition is not equality", func(t *testing.T) {
		where := []pqCond{
			{attr: "pk", op: ">", val: map[string]any{"S": "a"}},
			{attr: "sk", op: "=", val: map[string]any{"N": "1"}},
		}
		_, err := extractExactKey(where, meta)
		require.Error(t, err)
	})
}

func TestPQCondsToFilterExpr(t *testing.T) {
	t.Run("empty returns empty", func(t *testing.T) {
		expr, names, values := pqCondsToFilterExpr(nil)
		assert.Empty(t, expr)
		assert.Nil(t, names)
		assert.Nil(t, values)
	})

	t.Run("equality condition", func(t *testing.T) {
		conds := []pqCond{{attr: "name", op: "=", val: map[string]any{"S": "alice"}}}
		expr, names, values := pqCondsToFilterExpr(conds)
		assert.Contains(t, expr, "#pqf0 = :pqf0")
		assert.Equal(t, "name", names["#pqf0"])
		assert.Equal(t, map[string]any{"S": "alice"}, values[":pqf0"])
	})

	t.Run("BETWEEN condition", func(t *testing.T) {
		conds := []pqCond{{attr: "score", op: "BETWEEN",
			val:  map[string]any{"N": "1"},
			val2: map[string]any{"N": "10"},
		}}
		expr, _, values := pqCondsToFilterExpr(conds)
		assert.Contains(t, expr, "BETWEEN")
		assert.Contains(t, values, ":pqf0lo")
		assert.Contains(t, values, ":pqf0hi")
	})

	t.Run("IN condition", func(t *testing.T) {
		conds := []pqCond{{attr: "status", op: "IN",
			vals: []map[string]any{{"S": "a"}, {"S": "b"}},
		}}
		expr, _, values := pqCondsToFilterExpr(conds)
		assert.Contains(t, expr, "IN (")
		assert.Contains(t, values, ":pqf0_0")
		assert.Contains(t, values, ":pqf0_1")
	})
}

func TestPQMinLimit(t *testing.T) {
	n5, n10 := 5, 10
	assert.Equal(t, &n5, pqMinLimit(&n5, &n10))
	assert.Equal(t, &n5, pqMinLimit(&n10, &n5))
	assert.Equal(t, &n5, pqMinLimit(&n5, nil))
	assert.Equal(t, &n5, pqMinLimit(nil, &n5))
	assert.Nil(t, pqMinLimit(nil, nil))
}

// ---- tokenizer edge cases ----

func TestPQTokenize_EdgeCases(t *testing.T) {
	t.Run("unterminated single-quoted string", func(t *testing.T) {
		_, err := pqTokenize("'hello")
		require.Error(t, err)
	})
	t.Run("unterminated double-quoted identifier", func(t *testing.T) {
		_, err := pqTokenize(`"hello`)
		require.Error(t, err)
	})
	t.Run("unterminated backtick identifier", func(t *testing.T) {
		_, err := pqTokenize("`hello")
		require.Error(t, err)
	})
	t.Run("! without = is an error", func(t *testing.T) {
		_, err := pqTokenize("a ! b")
		require.Error(t, err)
	})
	t.Run("unexpected character", func(t *testing.T) {
		_, err := pqTokenize("SELECT @ FROM t")
		require.Error(t, err)
	})
	t.Run("negative number literal", func(t *testing.T) {
		toks, err := pqTokenize("sk = -42")
		require.NoError(t, err)
		found := false
		for _, tok := range toks {
			if tok.kind == pqTokNum && tok.val == "-42" {
				found = true
			}
		}
		assert.True(t, found)
	})
	t.Run("!= operator produces Ne token", func(t *testing.T) {
		toks, err := pqTokenize("a != b")
		require.NoError(t, err)
		require.True(t, len(toks) >= 3)
		assert.Equal(t, pqTokNe, toks[1].kind)
		assert.Equal(t, "!=", toks[1].val)
	})
	t.Run("<> operator produces Ne token", func(t *testing.T) {
		toks, err := pqTokenize("a <> b")
		require.NoError(t, err)
		assert.Equal(t, pqTokNe, toks[1].kind)
	})
	t.Run("all punctuation tokens", func(t *testing.T) {
		toks, err := pqTokenize("( ) { } [ ] : ? .")
		require.NoError(t, err)
		kinds := make([]pqTokKind, 0, 9)
		for _, tok := range toks {
			if tok.kind != pqTokEOF {
				kinds = append(kinds, tok.kind)
			}
		}
		assert.Equal(t, []pqTokKind{
			pqTokLParen, pqTokRParen,
			pqTokLBrace, pqTokRBrace,
			pqTokLBrack, pqTokRBrack,
			pqTokColon, pqTokQ, pqTokDot,
		}, kinds)
	})
}

// ---- parser value type coverage ----

func TestParsePartiQL_AllValueTypes(t *testing.T) {
	t.Run("FALSE boolean literal", func(t *testing.T) {
		stmt, err := parsePartiQL(
			`INSERT INTO "t" VALUE {'pk': ?, 'flag': FALSE}`,
			[]map[string]any{{"S": "k"}},
		)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"BOOL": false}, stmt.item["flag"])
	})

	t.Run("NULL literal", func(t *testing.T) {
		stmt, err := parsePartiQL(
			`INSERT INTO "t" VALUE {'pk': ?, 'x': NULL}`,
			[]map[string]any{{"S": "k"}},
		)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"NULL": true}, stmt.item["x"])
	})

	t.Run("MISSING treated as NULL", func(t *testing.T) {
		stmt, err := parsePartiQL(
			`INSERT INTO "t" VALUE {'pk': ?, 'x': MISSING}`,
			[]map[string]any{{"S": "k"}},
		)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"NULL": true}, stmt.item["x"])
	})

	t.Run("list literal in INSERT", func(t *testing.T) {
		stmt, err := parsePartiQL(
			`INSERT INTO "t" VALUE {'pk': ?, 'tags': [?, ?]}`,
			[]map[string]any{{"S": "k"}, {"S": "a"}, {"S": "b"}},
		)
		require.NoError(t, err)
		listWrapped, ok := stmt.item["tags"].(map[string]any)
		require.True(t, ok)
		items, ok := listWrapped["L"].([]any)
		require.True(t, ok)
		assert.Len(t, items, 2)
		assert.Equal(t, map[string]any{"S": "a"}, items[0])
		assert.Equal(t, map[string]any{"S": "b"}, items[1])
	})

	t.Run("nested map literal in INSERT", func(t *testing.T) {
		stmt, err := parsePartiQL(
			`INSERT INTO "t" VALUE {'pk': ?, 'meta': {'k': ?}}`,
			[]map[string]any{{"S": "pk"}, {"N": "1"}},
		)
		require.NoError(t, err)
		mapWrapped, ok := stmt.item["meta"].(map[string]any)
		require.True(t, ok)
		inner, ok := mapWrapped["M"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, map[string]any{"N": "1"}, inner["k"])
	})

	t.Run("unknown identifier in value position fails", func(t *testing.T) {
		_, err := parsePartiQL(`INSERT INTO "t" VALUE {'pk': ORDER}`, nil)
		require.Error(t, err)
	})

	t.Run("ORDER BY is skipped in SELECT", func(t *testing.T) {
		stmt, err := parsePartiQL(`SELECT * FROM "t" ORDER BY sk DESC`, nil)
		require.NoError(t, err)
		assert.Equal(t, pqSelect, stmt.kind)
	})

	t.Run("INSERT missing VALUE keyword", func(t *testing.T) {
		_, err := parsePartiQL(`INSERT INTO "t" {'pk': ?}`, []map[string]any{{"S": "k"}})
		require.Error(t, err)
	})

	t.Run("non-identifier after FROM", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT * FROM 123`, nil)
		require.Error(t, err)
	})

	t.Run("parseName with unexpected token type", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT * FROM =`, nil)
		require.Error(t, err)
	})

	t.Run("parseDocMap with missing closing brace", func(t *testing.T) {
		_, err := parsePartiQL(`INSERT INTO "t" VALUE {'pk': ?`, []map[string]any{{"S": "k"}})
		require.Error(t, err)
	})

	t.Run("parseList with missing closing bracket", func(t *testing.T) {
		_, err := parsePartiQL(`INSERT INTO "t" VALUE {'pk': ?, 'x': [?}`,
			[]map[string]any{{"S": "k"}, {"S": "v"}})
		require.Error(t, err)
	})
}

// ---- sort key range operator coverage ----

func TestExecuteStatement_SELECT_SortKeyRangeOps(t *testing.T) {
	ro := setup(t)
	for _, body := range []string{
		`{"Statement":"INSERT INTO \"t2\" VALUE {'pk': 'p', 'sk': 1, 'data': 'one'}"}`,
		`{"Statement":"INSERT INTO \"t2\" VALUE {'pk': 'p', 'sk': 2, 'data': 'two'}"}`,
		`{"Statement":"INSERT INTO \"t2\" VALUE {'pk': 'p', 'sk': 3, 'data': 'three'}"}`,
		`{"Statement":"INSERT INTO \"t2\" VALUE {'pk': 'p', 'sk': 4, 'data': 'four'}"}`,
	} {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", body).Code)
	}

	tests := []struct {
		name      string
		op        string
		param     string
		wantCount int
	}{
		{"sk >= 3 returns 2 items", ">=", `{"N":"3"}`, 2},
		{"sk > 2 returns 2 items", ">", `{"N":"2"}`, 2},
		{"sk <= 2 returns 2 items", "<=", `{"N":"2"}`, 2},
		{"sk < 3 returns 2 items", "<", `{"N":"3"}`, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"Statement":  `SELECT * FROM "t2" WHERE pk = ? AND sk ` + tc.op + ` ?`,
				"Parameters": []any{map[string]any{"S": "p"}, json.RawMessage(tc.param)},
			})
			w := dynamo(t, ro, "ExecuteStatement", string(body))
			assert.Equal(t, http.StatusOK, w.Code)
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			items := resp["Items"].([]any)
			assert.Len(t, items, tc.wantCount, tc.name)
		})
	}
}

// ---- ExecuteStatement additional error paths ----

func TestExecuteStatement_AdditionalErrorPaths(t *testing.T) {
	ro := setup(t)

	t.Run("400 on statement longer than 8192 chars", func(t *testing.T) {
		stmt := `SELECT * FROM "t" WHERE pk = '` + string(make([]byte, 8200)) + `'`
		body, _ := json.Marshal(map[string]any{"Statement": stmt})
		w := dynamo(t, ro, "ExecuteStatement", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 on invalid NextToken", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\"",
			"NextToken": "!!!not-valid-base64!!!"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	t.Run("400 on table not found for DELETE", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "DELETE FROM \"nosuchtable\" WHERE pk = ?",
			"Parameters": [{"S":"k"}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("ConsumedCapacity returned for INSERT", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'cctest'}",
			"ReturnConsumedCapacity": "TOTAL"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotNil(t, resp["ConsumedCapacity"])
	})

	t.Run("ConsumedCapacity returned for UPDATE", func(t *testing.T) {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'ccupd'}"
		}`).Code)
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "UPDATE \"t\" SET val = ? WHERE pk = ?",
			"Parameters": [{"S":"v"}, {"S":"ccupd"}],
			"ReturnConsumedCapacity": "TOTAL"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotNil(t, resp["ConsumedCapacity"])
	})

	t.Run("ConsumedCapacity returned for DELETE", func(t *testing.T) {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'ccdel'}"
		}`).Code)
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "DELETE FROM \"t\" WHERE pk = ?",
			"Parameters": [{"S":"ccdel"}],
			"ReturnConsumedCapacity": "TOTAL"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotNil(t, resp["ConsumedCapacity"])
	})
}

// ---- ExecuteTransaction write coverage (UPDATE, DELETE, cancellation) ----

func TestExecuteTransaction_WritesCoverage(t *testing.T) {
	ro := setup(t)

	t.Run("transact UPDATE succeeds", func(t *testing.T) {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'tu1', 'val': 'old'}"
		}`).Code)

		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "UPDATE \"t\" SET val = ? WHERE pk = ?", "Parameters": [{"S":"new"}, {"S":"tu1"}]}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)

		w2 := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\" WHERE pk = ?",
			"Parameters": [{"S":"tu1"}]
		}`)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		require.Len(t, items, 1)
		assert.Equal(t, map[string]any{"S": "new"}, items[0].(map[string]any)["val"])
	})

	t.Run("transact DELETE succeeds", func(t *testing.T) {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'td1'}"
		}`).Code)

		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "DELETE FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"td1"}]}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)

		w2 := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t\" WHERE pk = ?",
			"Parameters": [{"S":"td1"}]
		}`)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
		assert.Empty(t, resp["Items"].([]any))
	})

	t.Run("transact mixed INSERT+UPDATE+DELETE", func(t *testing.T) {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'tmix1', 'val': 'old'}"
		}`).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'tmix2'}"
		}`).Code)

		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "INSERT INTO \"t\" VALUE {'pk': 'tmix3'}"},
				{"Statement": "UPDATE \"t\" SET val = ? WHERE pk = ?", "Parameters": [{"S":"upd"}, {"S":"tmix1"}]},
				{"Statement": "DELETE FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"tmix2"}]}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("duplicate key in transact writes returns error", func(t *testing.T) {
		// checkDuplicateKeysLocked returns ValidationException for duplicate keys
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "INSERT INTO \"t\" VALUE {'pk': 'dupkey'}"},
				{"Statement": "INSERT INTO \"t\" VALUE {'pk': 'dupkey'}"}
			]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("transact writes with non-existent table returns error", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "UPDATE \"nosuchtable\" SET val = ? WHERE pk = ?",
				 "Parameters": [{"S":"v"}, {"S":"k"}]}
			]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	t.Run("transact reads with non-existent table returns error", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "SELECT * FROM \"nosuchtable\" WHERE pk = ?",
				 "Parameters": [{"S":"k"}]}
			]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})
}

// ---- BatchExecuteStatement additional coverage ----

func TestBatchExecuteStatement_AdditionalCoverage(t *testing.T) {
	ro := setup(t)

	t.Run("batch DELETE statements succeed", func(t *testing.T) {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'bd1'}"
		}`).Code)
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'bd2'}"
		}`).Code)

		w := dynamo(t, ro, "BatchExecuteStatement", `{
			"Statements": [
				{"Statement": "DELETE FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"bd1"}]},
				{"Statement": "DELETE FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"bd2"}]}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		responses := resp["Responses"].([]any)
		for _, r := range responses {
			assert.Nil(t, r.(map[string]any)["Error"])
		}
	})

	t.Run("batch UPDATE statements succeed", func(t *testing.T) {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'bu1', 'val': 'old'}"
		}`).Code)

		w := dynamo(t, ro, "BatchExecuteStatement", `{
			"Statements": [
				{"Statement": "UPDATE \"t\" SET val = ? WHERE pk = ?",
				 "Parameters": [{"S":"new"}, {"S":"bu1"}]}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Nil(t, resp["Responses"].([]any)[0].(map[string]any)["Error"])
	})

	t.Run("batch error ValidationException from wrong WHERE", func(t *testing.T) {
		// UPDATE on non-key attribute in WHERE → extractExactKey returns ValidationException
		w := dynamo(t, ro, "BatchExecuteStatement", `{
			"Statements": [
				{"Statement": "UPDATE \"t\" SET v = ? WHERE notakey = ?",
				 "Parameters": [{"S":"x"}, {"S":"y"}]}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code) // batch always returns 200
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		r := resp["Responses"].([]any)[0].(map[string]any)
		require.NotNil(t, r["Error"])
		errObj := r["Error"].(map[string]any)
		assert.Equal(t, "ValidationException", errObj["Code"])
	})

	t.Run("batch invalid JSON body returns 400", func(t *testing.T) {
		w := dynamo(t, ro, "BatchExecuteStatement", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("batch statement parse error returns 400", func(t *testing.T) {
		w := dynamo(t, ro, "BatchExecuteStatement", `{
			"Statements": [{"Statement": "BADOP FROM t"}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("batch returns ConsumedCapacity when TOTAL", func(t *testing.T) {
		w := dynamo(t, ro, "BatchExecuteStatement", `{
			"Statements": [{"Statement": "SELECT * FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"x"}]}],
			"ReturnConsumedCapacity": "TOTAL"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotNil(t, resp["ConsumedCapacity"])
	})
}

// ---- ExecuteTransaction additional coverage ----

func TestExecuteTransaction_AdditionalCoverage(t *testing.T) {
	ro := setup(t)

	t.Run("invalid JSON body returns 400", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{bad}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("statement parse error returns 400", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [{"Statement": "BADOP FROM t"}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("returns ConsumedCapacity when TOTAL for writes", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "INSERT INTO \"t\" VALUE {'pk': 'txcc1'}"}
			],
			"ReturnConsumedCapacity": "TOTAL"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotNil(t, resp["ConsumedCapacity"])
	})

	t.Run("returns ConsumedCapacity when TOTAL for reads", func(t *testing.T) {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'txcc2'}"
		}`).Code)
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "SELECT * FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"txcc2"}]}
			],
			"ReturnConsumedCapacity": "TOTAL"
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.NotNil(t, resp["ConsumedCapacity"])
	})
}

// ---- pqWriteTransactError / pqWriteStorageError / pqStorageErrToBatchError unit tests ----

func TestPQWriteTransactError(t *testing.T) {
	t.Run("TransactionCanceledException path", func(t *testing.T) {
		w := httptest.NewRecorder()
		txErr := &TransactionCanceledError{
			Reasons: []CancellationReason{
				{Code: "ConditionalCheckFailed", Message: "condition failed"},
			},
		}
		pqWriteTransactError(w, txErr)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(
			t,
			"com.amazonaws.dynamodb.v20120810#TransactionCanceledException",
			resp["__type"],
		)
		reasons := resp["CancellationReasons"].([]any)
		assert.Len(t, reasons, 1)
	})

	t.Run("delegates other errors to pqWriteStorageError", func(t *testing.T) {
		w := httptest.NewRecorder()
		pqWriteTransactError(w, ErrTableNotFound)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})
}

func TestPQWriteStorageError(t *testing.T) {
	t.Run("ConditionalCheckFailedException", func(t *testing.T) {
		w := httptest.NewRecorder()
		pqWriteStorageError(w, "op", ErrConditionalCheckFailed)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException")
	})

	t.Run("InternalServerError for unknown errors", func(t *testing.T) {
		w := httptest.NewRecorder()
		pqWriteStorageError(w, "op", errUnexpected)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#InternalServerError")
	})
}

func TestPQStorageErrToBatchError(t *testing.T) {
	tests := []struct {
		err      error
		wantCode string
	}{
		{ErrTableNotFound, "ResourceNotFoundException"},
		{ErrConditionalCheckFailed, "ConditionalCheckFailed"},
		{fmt.Errorf("%w: detail", ErrValidationException), "ValidationException"},
		{errUnexpected, "InternalServerError"},
	}
	for _, tc := range tests {
		be := pqStorageErrToBatchError(tc.err)
		assert.Equal(t, tc.wantCode, be.Code, "err=%v", tc.err)
	}
}

// errUnexpected is a sentinel for "none of the above" error branches.
var errUnexpected = fmt.Errorf("unexpected storage failure")

// TestExecutePartiQLSelect_ScanError uses a mockStore to trigger the storage error
// path (handler_partiql.go:428-430) that fires when Scan/Query/GetItem itself fails.
func TestExecutePartiQLSelect_ScanError(t *testing.T) {
	meta := TableMetadata{
		Name:      "t",
		KeySchema: []KeySchemaElement{{AttributeName: "pk", KeyType: "HASH"}},
		Status:    "ACTIVE",
	}
	mock := &mockStore{
		describeTableFn: func(string) (TableMetadata, error) { return meta, nil },
		scanFn: func(string, ScanOptions) ([]map[string]any, map[string]any, error) {
			return nil, nil, fmt.Errorf("simulated scan failure")
		},
	}
	ro := &Router{storage: mock}
	// SELECT * FROM "t" → no WHERE → Scan path; Scan returns error → 428-430
	w := dynamo(t, ro, "ExecuteStatement", `{"Statement":"SELECT * FROM \"t\""}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestParseOneSet_ParseNameError covers partiql.go:636-638 (parseName failure in
// parseOneSet when the SET clause has no attribute name, e.g. "SET = ?").
func TestParseOneSet_ParseNameError(t *testing.T) {
	_, err := parsePartiQL(
		`UPDATE "t" SET = ? WHERE pk = ?`,
		[]map[string]any{{"S": "v"}, {"S": "k"}},
	)
	require.Error(t, err)
}

// ---- parser error path coverage ----

func TestParsePartiQL_ErrorPaths(t *testing.T) {
	t.Run("tokenize error propagated from parsePartiQL", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT @ FROM t`, nil)
		require.Error(t, err)
	})

	t.Run("SELECT ORDER BY without attr name fails", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT * FROM "t" ORDER BY`, nil)
		require.Error(t, err)
	})

	t.Run("INSERT with numeric table name fails", func(t *testing.T) {
		_, err := parsePartiQL(`INSERT INTO 42 VALUE {'pk': ?}`, []map[string]any{{"S": "k"}})
		require.Error(t, err)
	})

	t.Run("UPDATE with numeric table name fails", func(t *testing.T) {
		_, err := parsePartiQL(`UPDATE 42 SET col = ? WHERE pk = ?`,
			[]map[string]any{{"S": "v"}, {"S": "k"}})
		require.Error(t, err)
	})

	t.Run("UPDATE WHERE conditions parse error fails", func(t *testing.T) {
		_, err := parsePartiQL(`UPDATE "t" SET col = ? WHERE ? = ?`,
			[]map[string]any{{"S": "v"}, {"S": "k"}, {"S": "k"}})
		require.Error(t, err)
	})

	t.Run("DELETE with numeric table name fails", func(t *testing.T) {
		_, err := parsePartiQL(`DELETE FROM 42 WHERE pk = ?`, []map[string]any{{"S": "k"}})
		require.Error(t, err)
	})

	t.Run("DELETE WHERE conditions parse error fails", func(t *testing.T) {
		_, err := parsePartiQL(`DELETE FROM "t" WHERE ? = ?`,
			[]map[string]any{{"S": "k"}, {"S": "k"}})
		require.Error(t, err)
	})

	t.Run("WHERE with unsupported operator (LIKE) fails", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT * FROM "t" WHERE pk LIKE ?`,
			[]map[string]any{{"S": "x"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "LIKE")
	})

	t.Run("SET with value parse error fails", func(t *testing.T) {
		// second = is not a valid value; parseOneSet's parseValue fails
		_, err := parsePartiQL(`UPDATE "t" SET col == ? WHERE pk = ?`,
			[]map[string]any{{"S": "v"}, {"S": "k"}})
		require.Error(t, err)
	})

	t.Run("SET list second assignment error propagates", func(t *testing.T) {
		_, err := parsePartiQL(`UPDATE "t" SET a = ?, b == ? WHERE pk = ?`,
			[]map[string]any{{"S": "v1"}, {"S": "v2"}, {"S": "k"}})
		require.Error(t, err)
	})
}

// ---- additional parser error paths (targeted for remaining uncovered statements) ----

func TestParsePartiQL_RemainingErrorPaths(t *testing.T) {
	t.Run("DELETE missing FROM keyword", func(t *testing.T) {
		// expectIdent("FROM") fails when next token is a quoted string, not FROM
		_, err := parsePartiQL(`DELETE "t" WHERE pk = ?`, []map[string]any{{"S": "k"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "FROM")
	})

	t.Run("UPDATE missing SET keyword", func(t *testing.T) {
		// expectIdent("SET") fails when column name is seen instead
		_, err := parsePartiQL(`UPDATE "t" col = ? WHERE pk = ?`,
			[]map[string]any{{"S": "v"}, {"S": "k"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SET")
	})

	t.Run("INSERT missing INTO keyword", func(t *testing.T) {
		_, err := parsePartiQL(`INSERT "t" VALUE {'pk': ?}`, []map[string]any{{"S": "k"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "INTO")
	})

	t.Run("SELECT LIMIT with non-numeric value", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT * FROM "t" LIMIT abc`, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "LIMIT")
	})

	t.Run("WHERE IN missing opening paren", func(t *testing.T) {
		// IN without ( fails expectPunct
		_, err := parsePartiQL(`SELECT * FROM "t" WHERE pk IN ?`,
			[]map[string]any{{"S": "k"}})
		require.Error(t, err)
	})

	t.Run("WHERE IN with invalid value in list", func(t *testing.T) {
		// parseValue inside IN list fails on unexpected token
		_, err := parsePartiQL(`SELECT * FROM "t" WHERE pk IN (ORDER)`, nil)
		require.Error(t, err)
	})

	t.Run("SET assignment with != instead of =", func(t *testing.T) {
		// expectPunct(pqTokEq) in parseOneSet fails when != (pqTokNe) is seen
		_, err := parsePartiQL(`UPDATE "t" SET col != ? WHERE pk = ?`,
			[]map[string]any{{"S": "v"}, {"S": "k"}})
		require.Error(t, err)
	})

	t.Run("doc map value parse error", func(t *testing.T) {
		// parseValue inside parseDocMap fails on unknown identifier
		_, err := parsePartiQL(`INSERT INTO "t" VALUE {'pk': ORDER}`, nil)
		require.Error(t, err)
	})

	t.Run("doc map missing colon between key and value", func(t *testing.T) {
		// expectPunct(':') in parseDocMap fails when colon is absent
		_, err := parsePartiQL(`INSERT INTO "t" VALUE {'pk' value}`, nil)
		require.Error(t, err)
	})

	t.Run("list value parse error", func(t *testing.T) {
		// parseValue inside parseList fails on unexpected token type
		_, err := parsePartiQL(`INSERT INTO "t" VALUE {'pk': ?, 'tags': [ORDER]}`,
			[]map[string]any{{"S": "k"}})
		require.Error(t, err)
	})

	t.Run("list missing closing bracket at EOF", func(t *testing.T) {
		// expectPunct(']') in parseList fails when EOF is reached without closing bracket
		_, err := parsePartiQL(`INSERT INTO "t" VALUE {'pk': ?, 'x': [?`,
			[]map[string]any{{"S": "k"}, {"S": "v"}})
		require.Error(t, err)
	})
}

// ---- sort key <> operator triggers filterConds (pqCondToSortKey default path) ----

func TestExecuteStatement_SELECT_SortKeyInFilter(t *testing.T) {
	ro := setup(t)
	for _, body := range []string{
		`{"Statement":"INSERT INTO \"t2\" VALUE {'pk': 'p', 'sk': 1, 'data': 'one'}"}`,
		`{"Statement":"INSERT INTO \"t2\" VALUE {'pk': 'p', 'sk': 2, 'data': 'two'}"}`,
	} {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", body).Code)
	}

	t.Run("sort key <> condition goes to filter", func(t *testing.T) {
		// <> on sort key is not a range op, so it falls to filterConds via pqCondToSortKey default
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"t2\" WHERE pk = ? AND sk <> ?",
			"Parameters": [{"S":"p"}, {"N":"1"}]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		items := resp["Items"].([]any)
		assert.Len(t, items, 1) // only sk=2 returned
	})
}

// ---- executePartiQLTransactWrites ValidationException path ----

func TestExecuteTransaction_TransactWritesValidationError(t *testing.T) {
	ro := setup(t)

	t.Run(
		"UPDATE in transaction with non-key WHERE fails with ValidationException",
		func(t *testing.T) {
			require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'twv1'}"
		}`).Code)

			w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "UPDATE \"t\" SET val = ? WHERE notakey = ?",
				 "Parameters": [{"S":"v"}, {"S":"k"}]}
			]
		}`)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
		},
	)
}

// ---- validatePQBatchKind edge: called with empty slice (defensive guard) ----

func TestValidatePQBatchKind_EmptySlice(t *testing.T) {
	// The guard `if len(stmts) == 0 { return nil }` is a defensive path.
	// Call it directly to confirm it returns nil.
	err := validatePQBatchKind(nil)
	assert.NoError(t, err)
}

// ---- pqDecodeToken coverage ----

// ---- comprehensive coverage tests for remaining gaps ----

func TestParsePartiQL_AllRemainingErrors(t *testing.T) {
	// parser WHERE-required guards (these were genuinely not tested before)
	t.Run("UPDATE without WHERE clause fails", func(t *testing.T) {
		_, err := parsePartiQL(`UPDATE "t" SET col = ?`, []map[string]any{{"S": "v"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "WHERE")
	})
	t.Run("DELETE without WHERE clause fails", func(t *testing.T) {
		_, err := parsePartiQL(`DELETE FROM "t"`, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "WHERE")
	})

	// SELECT missing FROM
	t.Run("SELECT without FROM fails", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT *`, nil)
		require.Error(t, err)
	})

	// SELECT ORDER BY — missing BY keyword
	t.Run("SELECT ORDER without BY fails", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT * FROM "t" ORDER 42`, nil)
		require.Error(t, err)
	})

	// LIMIT with decimal — strconv.Atoi fails on "1.5"
	t.Run("SELECT LIMIT with decimal fails", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT * FROM "t" LIMIT 1.5`, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "LIMIT")
	})

	// INSERT VALUE with non-{ token
	t.Run("INSERT VALUE with ? instead of { fails", func(t *testing.T) {
		_, err := parsePartiQL(`INSERT INTO "t" VALUE ?`, []map[string]any{{"S": "k"}})
		require.Error(t, err)
	})

	// parseDocMap with numeric key (parseName fails on pqTokNum)
	t.Run("doc map with numeric key fails", func(t *testing.T) {
		_, err := parsePartiQL(`INSERT INTO "t" VALUE {42: ?}`, []map[string]any{{"S": "v"}})
		require.Error(t, err)
	})

	// parseConditions: error in second AND condition
	t.Run("WHERE second AND condition parse error", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT * FROM "t" WHERE pk = ? AND 42`,
			[]map[string]any{{"S": "k"}})
		require.Error(t, err)
	})

	// BETWEEN lo parse error
	t.Run("BETWEEN lo parse error fails", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT * FROM "t" WHERE pk BETWEEN = ?`,
			[]map[string]any{{"S": "k"}})
		require.Error(t, err)
	})

	// BETWEEN AND error (no AND keyword after lo)
	t.Run("BETWEEN without AND fails", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT * FROM "t" WHERE pk BETWEEN ? WRONG ?`,
			[]map[string]any{{"S": "k"}, {"S": "v"}})
		require.Error(t, err)
	})

	// BETWEEN hi parse error
	t.Run("BETWEEN hi parse error fails", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT * FROM "t" WHERE pk BETWEEN ? AND =`,
			[]map[string]any{{"S": "k"}})
		require.Error(t, err)
	})

	// IN list expectPunct(')') error: list not closed, hits EOF
	t.Run("IN list not closed at EOF fails", func(t *testing.T) {
		_, err := parsePartiQL(`SELECT * FROM "t" WHERE pk IN (?`,
			[]map[string]any{{"S": "k"}})
		require.Error(t, err)
	})

	// parseValue: nested docmap error propagates through parseValue
	t.Run("nested doc map with bad key propagates error", func(t *testing.T) {
		_, err := parsePartiQL(`INSERT INTO "t" VALUE {'pk': ?, 'meta': {42: ?}}`,
			[]map[string]any{{"S": "k"}, {"S": "v"}})
		require.Error(t, err)
	})

	// parseList: value parse error in list
	t.Run("list with invalid value fails", func(t *testing.T) {
		_, err := parsePartiQL(`INSERT INTO "t" VALUE {'pk': ?, 'items': [=]}`,
			[]map[string]any{{"S": "k"}})
		require.Error(t, err)
	})

	// parseOneSet: expectPunct('=') error
	t.Run("SET assignment with != instead of = fails", func(t *testing.T) {
		_, err := parsePartiQL(`UPDATE "t" SET col!= ? WHERE pk = ?`,
			[]map[string]any{{"S": "v"}, {"S": "k"}})
		require.Error(t, err)
	})
}

func TestHandlerPartiQL_RemainingCoverage(t *testing.T) {
	ro := setup(t)

	// Batch: invalid ReturnConsumedCapacity
	t.Run("batch 400 on invalid ReturnConsumedCapacity", func(t *testing.T) {
		w := dynamo(t, ro, "BatchExecuteStatement", `{
			"Statements": [{"Statement": "SELECT * FROM \"t\" WHERE pk = ?", "Parameters": [{"S":"k"}]}],
			"ReturnConsumedCapacity": "INVALID"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	// Batch SELECT error (SELECT from non-existent table hits executePartiQLSelect DescribeTable error)
	t.Run("batch SELECT from non-existent table returns per-item error", func(t *testing.T) {
		w := dynamo(t, ro, "BatchExecuteStatement", `{
			"Statements": [
				{"Statement": "SELECT * FROM \"nosuchtable\" WHERE pk = ?", "Parameters": [{"S":"k"}]}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		r := resp["Responses"].([]any)[0].(map[string]any)
		assert.NotNil(t, r["Error"])
	})

	// Batch DELETE error (DELETE from non-existent table)
	t.Run("batch DELETE from non-existent table returns per-item error", func(t *testing.T) {
		w := dynamo(t, ro, "BatchExecuteStatement", `{
			"Statements": [
				{"Statement": "DELETE FROM \"nosuchtable\" WHERE pk = ?", "Parameters": [{"S":"k"}]}
			]
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		r := resp["Responses"].([]any)[0].(map[string]any)
		assert.NotNil(t, r["Error"])
	})

	// ExecuteTransaction: invalid ReturnConsumedCapacity
	t.Run("executeTransaction 400 on invalid ReturnConsumedCapacity", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [{"Statement": "INSERT INTO \"t\" VALUE {'pk': 'x'}"}],
			"ReturnConsumedCapacity": "INVALID"
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	// ExecuteStatement SELECT from non-existent table → executePartiQLSelect DescribeTable error
	t.Run("executeStatement SELECT from non-existent table 400", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "SELECT * FROM \"nosuchtable\" WHERE pk = ?",
			"Parameters": [{"S":"k"}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	// ExecuteStatement UPDATE from non-existent table → executePartiQLUpdate DescribeTable error
	t.Run("executeStatement UPDATE from non-existent table 400", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "UPDATE \"nosuchtable\" SET val = ? WHERE pk = ?",
			"Parameters": [{"S":"v"}, {"S":"k"}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	// ExecuteStatement DELETE with non-key WHERE → executePartiQLDelete extractExactKey error
	t.Run("executeStatement DELETE with non-key WHERE 400", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "DELETE FROM \"t\" WHERE val = ?",
			"Parameters": [{"S":"k"}]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	// ExecuteTransaction reads with non-key WHERE → extractExactKey error
	t.Run("executeTransaction reads with non-key WHERE 400", func(t *testing.T) {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", `{
			"Statement": "INSERT INTO \"t\" VALUE {'pk': 'txrk1'}"
		}`).Code)
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "SELECT * FROM \"t\" WHERE val = ?", "Parameters": [{"S":"k"}]}
			]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})

	// ExecuteTransaction writes DELETE from non-existent table → DescribeTable error
	t.Run("executeTransaction DELETE from non-existent table 400", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "DELETE FROM \"nosuchtable\" WHERE pk = ?", "Parameters": [{"S":"k"}]}
			]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException")
	})

	// ExecuteTransaction writes DELETE with non-key WHERE → extractExactKey error
	t.Run("executeTransaction DELETE with non-key WHERE 400", func(t *testing.T) {
		w := dynamo(t, ro, "ExecuteTransaction", `{
			"TransactStatements": [
				{"Statement": "DELETE FROM \"t\" WHERE val = ?", "Parameters": [{"S":"k"}]}
			]
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrorType(t, w, "com.amazonaws.dynamodb.v20120810#ValidationException")
	})
}

// ---- pqCondsToFilterExpr != normalization ----

func TestPQCondsToFilterExpr_NEOperator(t *testing.T) {
	// When the operator is "!=", pqCondsToFilterExpr normalizes it to "<>"
	conds := []pqCond{{attr: "status", op: "!=", val: map[string]any{"S": "deleted"}}}
	expr, names, values := pqCondsToFilterExpr(conds)
	assert.Contains(t, expr, "<>")
	assert.NotContains(t, expr, "!=")
	assert.Equal(t, "status", names["#pqf0"])
	assert.Equal(t, map[string]any{"S": "deleted"}, values[":pqf0"])
}

// ---- pqCondsToFilterExpr IN covers branch via SELECT filter path ----

func TestExecuteStatement_SELECT_FilterWithNE(t *testing.T) {
	ro := setup(t)
	for _, b := range []string{
		`{"Statement":"INSERT INTO \"t2\" VALUE {'pk': 'p', 'sk': 10, 'data': 'ten'}"}`,
		`{"Statement":"INSERT INTO \"t2\" VALUE {'pk': 'p', 'sk': 20, 'data': 'twenty'}"}`,
	} {
		require.Equal(t, http.StatusOK, dynamo(t, ro, "ExecuteStatement", b).Code)
	}

	// WHERE pk = ? AND sk != ? → sk != ends up in filterConds with op "!="
	// pqCondsToFilterExpr normalizes != to <> — covers lines 712-714
	w := dynamo(t, ro, "ExecuteStatement", `{
		"Statement": "SELECT * FROM \"t2\" WHERE pk = ? AND sk != ?",
		"Parameters": [{"S":"p"}, {"N":"10"}]
	}`)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	items := resp["Items"].([]any)
	assert.Len(t, items, 1) // only sk=20 returned
}

func TestPQDecodeToken(t *testing.T) {
	t.Run("invalid base64 returns error", func(t *testing.T) {
		_, err := pqDecodeToken("!!!not-base64!!!")
		require.Error(t, err)
	})

	t.Run("valid base64 but invalid JSON returns error", func(t *testing.T) {
		import64 := "bm90anNvbg==" // base64("notjson")
		_, err := pqDecodeToken(import64)
		require.Error(t, err)
	})

	t.Run("roundtrip encode/decode", func(t *testing.T) {
		lek := map[string]any{"pk": map[string]any{"S": "key"}}
		token, err := pqEncodeToken(lek)
		require.NoError(t, err)
		require.NotEmpty(t, token)
		decoded, err := pqDecodeToken(token)
		require.NoError(t, err)
		assert.Equal(t, "key", decoded["pk"].(map[string]any)["S"])
	})

	t.Run("empty lek encodes to empty string", func(t *testing.T) {
		token, err := pqEncodeToken(nil)
		require.NoError(t, err)
		assert.Empty(t, token)
	})
}
