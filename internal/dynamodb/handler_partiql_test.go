package dynamodb

import (
	"encoding/json"
	"net/http"
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
