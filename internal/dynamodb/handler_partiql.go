package dynamodb

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// ---- ExecuteStatement ----

func (ro *Router) handleExecuteStatement(w http.ResponseWriter, body []byte) {
	var req struct {
		Statement                           string           `json:"Statement"`
		Parameters                          []map[string]any `json:"Parameters"`
		ConsistentRead                      bool             `json:"ConsistentRead"`
		Limit                               *int             `json:"Limit"`
		NextToken                           string           `json:"NextToken"`
		ReturnConsumedCapacity              string           `json:"ReturnConsumedCapacity"`
		ReturnValuesOnConditionCheckFailure string           `json:"ReturnValuesOnConditionCheckFailure"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException", "invalid request body")
		return
	}
	if req.Statement == "" {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException", "Statement is required")
		return
	}
	if len(req.Statement) > 8192 {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Statement length exceeds maximum of 8192 characters")
		return
	}
	if req.Limit != nil && *req.Limit < 1 {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			fmt.Sprintf(
				"Value %d at 'limit' failed to satisfy constraint: Member must have value greater than or equal to 1",
				*req.Limit,
			))
		return
	}
	if !validateReturnConsumedCapacity(w, req.ReturnConsumedCapacity) {
		return
	}

	stmt, err := parsePartiQL(req.Statement, req.Parameters)
	if err != nil {
		slog.Debug("ExecuteStatement: parse error", "err", err)
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException", err.Error())
		return
	}

	resp := map[string]any{}

	switch stmt.kind {
	case pqSelect:
		items, nextToken, err := ro.executePartiQLSelect(stmt, req.Limit, req.NextToken)
		if err != nil {
			pqWriteStorageError(w, "ExecuteStatement", err)
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
		resp["Items"] = items
		if nextToken != "" {
			resp["NextToken"] = nextToken
		}
		if cc := buildConsumedCapacity(stmt.tableName, req.ReturnConsumedCapacity); cc != nil {
			resp["ConsumedCapacity"] = cc
		}
		slog.Debug("ExecuteStatement SELECT", "table", stmt.tableName, "count", len(items))

	case pqInsert:
		if err := ro.executePartiQLInsert(stmt); err != nil {
			pqWriteStorageError(w, "ExecuteStatement", err)
			return
		}
		if cc := buildConsumedCapacity(stmt.tableName, req.ReturnConsumedCapacity); cc != nil {
			resp["ConsumedCapacity"] = cc
		}
		slog.Info("ExecuteStatement INSERT", "table", stmt.tableName)

	case pqUpdate:
		if err := ro.executePartiQLUpdate(stmt); err != nil {
			pqWriteStorageError(w, "ExecuteStatement", err)
			return
		}
		if cc := buildConsumedCapacity(stmt.tableName, req.ReturnConsumedCapacity); cc != nil {
			resp["ConsumedCapacity"] = cc
		}
		slog.Info("ExecuteStatement UPDATE", "table", stmt.tableName)

	case pqDelete:
		if err := ro.executePartiQLDelete(stmt); err != nil {
			pqWriteStorageError(w, "ExecuteStatement", err)
			return
		}
		if cc := buildConsumedCapacity(stmt.tableName, req.ReturnConsumedCapacity); cc != nil {
			resp["ConsumedCapacity"] = cc
		}
		slog.Info("ExecuteStatement DELETE", "table", stmt.tableName)
	}

	writeJSON(w, http.StatusOK, resp)
}

// ---- BatchExecuteStatement ----

type batchStmtReq struct {
	Statement                           string           `json:"Statement"`
	Parameters                          []map[string]any `json:"Parameters"`
	ConsistentRead                      bool             `json:"ConsistentRead"`
	ReturnValuesOnConditionCheckFailure string           `json:"ReturnValuesOnConditionCheckFailure"`
}

type batchStmtError struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

type batchStmtResp struct {
	Item      map[string]any  `json:"Item,omitempty"`
	Error     *batchStmtError `json:"Error,omitempty"`
	TableName string          `json:"TableName"`
}

func (ro *Router) handleBatchExecuteStatement(w http.ResponseWriter, body []byte) {
	var req struct {
		Statements             []batchStmtReq `json:"Statements"`
		ReturnConsumedCapacity string         `json:"ReturnConsumedCapacity"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException", "invalid request body")
		return
	}
	if len(req.Statements) == 0 {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Statements is required and must contain at least 1 item")
		return
	}
	if len(req.Statements) > 25 {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			fmt.Sprintf(
				"Member must have length less than or equal to 25, but received length %d",
				len(req.Statements),
			))
		return
	}
	if !validateReturnConsumedCapacity(w, req.ReturnConsumedCapacity) {
		return
	}

	// Parse all statements first to detect mix of reads and writes.
	stmts := make([]*pqStmt, len(req.Statements))
	for i, s := range req.Statements {
		stmt, err := parsePartiQL(s.Statement, s.Parameters)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				fmt.Sprintf("statement %d: %v", i, err))
			return
		}
		stmts[i] = stmt
	}
	if err := validatePQBatchKind(stmts); err != nil {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException", err.Error())
		return
	}

	responses := make([]batchStmtResp, len(stmts))
	seen := make(map[string]struct{})
	var consumedTables []string

	for i, stmt := range stmts {
		resp := batchStmtResp{TableName: stmt.tableName}
		if _, ok := seen[stmt.tableName]; !ok {
			seen[stmt.tableName] = struct{}{}
			consumedTables = append(consumedTables, stmt.tableName)
		}

		switch stmt.kind {
		case pqSelect:
			items, _, err := ro.executePartiQLSelect(stmt, nil, "")
			if err != nil {
				resp.Error = pqStorageErrToBatchError(err)
			} else {
				if len(items) > 0 {
					resp.Item = items[0]
				}
			}
		case pqInsert:
			if err := ro.executePartiQLInsert(stmt); err != nil {
				resp.Error = pqStorageErrToBatchError(err)
			}
		case pqUpdate:
			if err := ro.executePartiQLUpdate(stmt); err != nil {
				resp.Error = pqStorageErrToBatchError(err)
			}
		case pqDelete:
			if err := ro.executePartiQLDelete(stmt); err != nil {
				resp.Error = pqStorageErrToBatchError(err)
			}
		}

		responses[i] = resp
	}

	slog.Debug("BatchExecuteStatement", "count", len(stmts))
	out := map[string]any{"Responses": responses}
	if req.ReturnConsumedCapacity != "" && req.ReturnConsumedCapacity != "NONE" {
		ccs := make([]map[string]any, 0, len(consumedTables))
		for _, t := range consumedTables {
			if cc := buildConsumedCapacity(t, req.ReturnConsumedCapacity); cc != nil {
				ccs = append(ccs, cc)
			}
		}
		out["ConsumedCapacity"] = ccs
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- ExecuteTransaction ----

type paramStmtReq struct {
	Statement                           string           `json:"Statement"`
	Parameters                          []map[string]any `json:"Parameters"`
	ReturnValuesOnConditionCheckFailure string           `json:"ReturnValuesOnConditionCheckFailure"`
}

func (ro *Router) handleExecuteTransaction(w http.ResponseWriter, body []byte) {
	var req struct {
		TransactStatements     []paramStmtReq `json:"TransactStatements"`
		ClientRequestToken     string         `json:"ClientRequestToken"`
		ReturnConsumedCapacity string         `json:"ReturnConsumedCapacity"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException", "invalid request body")
		return
	}
	if len(req.TransactStatements) == 0 {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TransactStatements is required and must contain at least 1 item")
		return
	}
	if len(req.TransactStatements) > 100 {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			fmt.Sprintf(
				"Member must have length less than or equal to 100, but received length %d",
				len(req.TransactStatements),
			))
		return
	}
	if !validateReturnConsumedCapacity(w, req.ReturnConsumedCapacity) {
		return
	}

	stmts := make([]*pqStmt, len(req.TransactStatements))
	for i, s := range req.TransactStatements {
		stmt, err := parsePartiQL(s.Statement, s.Parameters)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				fmt.Sprintf("statement %d: %v", i, err))
			return
		}
		stmts[i] = stmt
	}
	if err := validatePQBatchKind(stmts); err != nil {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException", err.Error())
		return
	}

	resp := map[string]any{}

	if stmts[0].kind == pqSelect {
		// All reads — translate to TransactGetItems
		items, err := ro.executePartiQLTransactReads(stmts)
		if err != nil {
			pqWriteTransactError(w, err)
			return
		}
		responses := make([]map[string]any, len(items))
		for i, item := range items {
			if item != nil {
				responses[i] = map[string]any{"Item": item}
			} else {
				responses[i] = map[string]any{}
			}
		}
		resp["Responses"] = responses
	} else {
		// All writes — translate to TransactWriteItems
		if err := ro.executePartiQLTransactWrites(stmts); err != nil {
			pqWriteTransactError(w, err)
			return
		}
	}

	seen := make(map[string]struct{})
	var consumedTables []string
	for _, s := range stmts {
		if _, ok := seen[s.tableName]; !ok {
			seen[s.tableName] = struct{}{}
			consumedTables = append(consumedTables, s.tableName)
		}
	}
	if req.ReturnConsumedCapacity != "" && req.ReturnConsumedCapacity != "NONE" {
		ccs := make([]map[string]any, 0, len(consumedTables))
		for _, t := range consumedTables {
			if cc := buildConsumedCapacity(t, req.ReturnConsumedCapacity); cc != nil {
				ccs = append(ccs, cc)
			}
		}
		resp["ConsumedCapacity"] = ccs
	}

	slog.Info("ExecuteTransaction", "count", len(stmts))
	writeJSON(w, http.StatusOK, resp)
}

// ---- execution helpers ----

// executePartiQLSelect runs a SELECT statement and returns items + optional NextToken.
func (ro *Router) executePartiQLSelect(
	stmt *pqStmt,
	apiLimit *int,
	nextToken string,
) ([]map[string]any, string, error) {
	meta, err := ro.storage.DescribeTable(stmt.tableName)
	if err != nil {
		return nil, "", err
	}

	var hashKeyName, sortKeyName string
	for _, k := range meta.KeySchema {
		switch k.KeyType {
		case "HASH":
			hashKeyName = k.AttributeName
		case "RANGE":
			sortKeyName = k.AttributeName
		}
	}

	// Classify WHERE conditions into hash-key, sort-key, and filter groups.
	var hashEqCond *pqCond
	var sortCond *pqCond
	var filterConds []pqCond
	for i := range stmt.where {
		c := &stmt.where[i]
		switch {
		case c.attr == hashKeyName && c.op == "=":
			hashEqCond = c
		case sortKeyName != "" && c.attr == sortKeyName && pqCondToSortKey(c) != nil:
			sortCond = c
		default:
			filterConds = append(filterConds, *c)
		}
	}

	effectiveLimit := pqMinLimit(apiLimit, stmt.stmtLimit)

	// Decode NextToken → ExclusiveStartKey
	var esk map[string]any
	if nextToken != "" {
		esk, err = pqDecodeToken(nextToken)
		if err != nil {
			return nil, "", fmt.Errorf("%w: invalid NextToken: %v", ErrValidationException, err)
		}
	}

	var items []map[string]any
	var lek map[string]any

	switch {
	case hashEqCond == nil:
		// Full Scan
		items, lek, err = ro.storage.Scan(stmt.tableName, ScanOptions{
			Limit:             effectiveLimit,
			ExclusiveStartKey: esk,
		})
	case sortKeyName != "" && sortCond != nil && sortCond.op == "=":
		// Exact point lookup via GetItem
		key := map[string]any{
			hashKeyName: hashEqCond.val,
			sortKeyName: sortCond.val,
		}
		var item map[string]any
		item, err = ro.storage.GetItem(stmt.tableName, key)
		if item != nil {
			items = []map[string]any{item}
		}
	default:
		// Query with hash key + optional sort key range condition
		var skCond *SortKeyCondition
		if sortCond != nil {
			skCond = pqCondToSortKey(sortCond)
		}
		items, lek, err = ro.storage.Query(
			stmt.tableName,
			hashKeyName,
			hashEqCond.val,
			skCond,
			QueryOptions{
				ScanIndexForward:  true,
				Limit:             effectiveLimit,
				ExclusiveStartKey: esk,
			},
		)
	}
	if err != nil {
		return nil, "", err
	}

	// Apply remaining WHERE conditions as in-memory filter.
	if len(filterConds) > 0 && len(items) > 0 {
		filterExpr, names, values := pqCondsToFilterExpr(filterConds)
		if filterExpr != "" {
			filtered, ferr := applyFilterExpression(items, filterExpr, names, values)
			if ferr != nil {
				return nil, "", fmt.Errorf(
					"%w: %v",
					ErrValidationException,
					ferr,
				) // untestable: pqCondsToFilterExpr always generates valid DynamoDB filter expressions
			}
			items = filtered
		}
	}

	outToken, _ := pqEncodeToken(lek)
	return items, outToken, nil
}

func (ro *Router) executePartiQLInsert(stmt *pqStmt) error {
	_, err := ro.storage.PutItem(stmt.tableName, stmt.item, nil)
	return err
}

func (ro *Router) executePartiQLUpdate(stmt *pqStmt) error {
	meta, err := ro.storage.DescribeTable(stmt.tableName)
	if err != nil {
		return err
	}
	key, err := extractExactKey(stmt.where, meta)
	if err != nil {
		return err
	}
	updates := make(map[string]any, len(stmt.sets))
	for _, s := range stmt.sets {
		updates[s.attr] = s.val
	}
	_, _, err = ro.storage.UpdateItem(stmt.tableName, key, updates, nil)
	return err
}

func (ro *Router) executePartiQLDelete(stmt *pqStmt) error {
	meta, err := ro.storage.DescribeTable(stmt.tableName)
	if err != nil {
		return err
	}
	key, err := extractExactKey(stmt.where, meta)
	if err != nil {
		return err
	}
	_, err = ro.storage.DeleteItem(stmt.tableName, key, nil)
	return err
}

// executePartiQLTransactReads translates all-SELECT statements to TransactGetItems.
func (ro *Router) executePartiQLTransactReads(stmts []*pqStmt) ([]map[string]any, error) {
	gets := make([]TransactGetInput, len(stmts))
	for i, stmt := range stmts {
		meta, err := ro.storage.DescribeTable(stmt.tableName)
		if err != nil {
			return nil, err
		}
		key, err := extractExactKey(stmt.where, meta)
		if err != nil {
			return nil, err
		}
		gets[i] = TransactGetInput{TableName: stmt.tableName, Key: key}
	}
	return ro.storage.TransactGetItems(gets)
}

// executePartiQLTransactWrites translates INSERT/UPDATE/DELETE statements to TransactWriteItems.
func (ro *Router) executePartiQLTransactWrites(stmts []*pqStmt) error {
	actions := make([]TransactWriteAction, len(stmts))
	for i, stmt := range stmts {
		switch stmt.kind {
		case pqInsert:
			actions[i] = TransactWriteAction{
				Put: &TransactPut{TableName: stmt.tableName, Item: stmt.item},
			}
		case pqUpdate:
			meta, err := ro.storage.DescribeTable(stmt.tableName)
			if err != nil {
				return err
			}
			key, err := extractExactKey(stmt.where, meta)
			if err != nil {
				return err
			}
			updates := make(map[string]any, len(stmt.sets))
			for _, s := range stmt.sets {
				updates[s.attr] = s.val
			}
			actions[i] = TransactWriteAction{
				Update: &TransactUpdate{
					TableName: stmt.tableName,
					Key:       key,
					Updates:   updates,
				},
			}
		case pqDelete:
			meta, err := ro.storage.DescribeTable(stmt.tableName)
			if err != nil {
				return err
			}
			key, err := extractExactKey(stmt.where, meta)
			if err != nil {
				return err
			}
			actions[i] = TransactWriteAction{
				Delete: &TransactDelete{TableName: stmt.tableName, Key: key},
			}
		default:
			// unreachable: validatePQBatchKind already rejected mixed batches
			return fmt.Errorf(
				"%w: unexpected statement kind in write transaction",
				ErrValidationException,
			)
		}
	}
	return ro.storage.TransactWriteItems(actions)
}

// ---- validation ----

// validatePQBatchKind checks that all statements are either reads or writes.
func validatePQBatchKind(stmts []*pqStmt) error {
	if len(stmts) == 0 {
		return nil
	}
	isRead := stmts[0].kind == pqSelect
	for _, s := range stmts[1:] {
		if (s.kind == pqSelect) != isRead {
			return fmt.Errorf(
				"all statements must be reads (SELECT) or all writes (INSERT/UPDATE/DELETE), not a mix",
			)
		}
	}
	return nil
}

// ---- NextToken encoding ----

func pqEncodeToken(lek map[string]any) (string, error) {
	if len(lek) == 0 {
		return "", nil
	}
	data, err := json.Marshal(lek)
	if err != nil {
		return "", err // untestable: json.Marshal of DynamoDB-typed map[string]any never fails
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func pqDecodeToken(token string) (map[string]any, error) {
	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, err
	}
	var lek map[string]any
	if err := json.Unmarshal(data, &lek); err != nil {
		return nil, err
	}
	return lek, nil
}

// ---- error helpers ----

// pqWriteStorageError writes the appropriate HTTP error for a storage-layer error
// returned by a PartiQL execution helper.
func pqWriteStorageError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, ErrTableNotFound):
		slog.Debug(op + ": table not found")
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
			"Requested resource not found")
	case errors.Is(err, ErrConditionalCheckFailed):
		slog.Debug(op + ": condition check failed")
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException",
			"The conditional request failed")
	case errors.Is(err, ErrValidationException):
		slog.Debug(op+": validation error", "err", err)
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			err.Error())
	default:
		slog.Error(op+" failed", "err", err)
		writeError(w, http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error")
	}
}

// pqWriteTransactError writes the appropriate HTTP error for ExecuteTransaction.
func pqWriteTransactError(w http.ResponseWriter, err error) {
	var txErr *TransactionCanceledError
	if errors.As(err, &txErr) {
		slog.Debug("ExecuteTransaction: transaction canceled", "reasons", len(txErr.Reasons))
		codes := make([]string, len(txErr.Reasons))
		for i, r := range txErr.Reasons {
			codes[i] = r.Code
		}
		type cancelResp struct {
			Type                string               `json:"__type"`
			Message             string               `json:"message"`
			CancellationReasons []CancellationReason `json:"CancellationReasons"`
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.WriteHeader(http.StatusBadRequest)
		if encErr := json.NewEncoder(w).Encode(cancelResp{
			Type: "com.amazonaws.dynamodb.v20120810#TransactionCanceledException",
			Message: "Transaction cancelled, please refer cancellation reasons for specific reasons [" +
				strings.Join(codes, ", ") + "]",
			CancellationReasons: txErr.Reasons,
		}); encErr != nil {
			slog.Warn(
				"failed to encode TransactionCanceledException",
				"err",
				encErr,
			) // untestable: cancelResp contains no unencodable types
		}
		return
	}
	pqWriteStorageError(w, "ExecuteTransaction", err)
}

// pqStorageErrToBatchError converts a storage error to a BatchStatementError.
func pqStorageErrToBatchError(err error) *batchStmtError {
	switch {
	case errors.Is(err, ErrTableNotFound):
		return &batchStmtError{
			Code:    "ResourceNotFoundException",
			Message: "Requested resource not found",
		}
	case errors.Is(err, ErrConditionalCheckFailed):
		return &batchStmtError{
			Code:    "ConditionalCheckFailed",
			Message: "The conditional request failed",
		}
	case errors.Is(err, ErrValidationException):
		return &batchStmtError{
			Code:    "ValidationException",
			Message: err.Error(),
		}
	default:
		return &batchStmtError{
			Code:    "InternalServerError",
			Message: err.Error(),
		}
	}
}
