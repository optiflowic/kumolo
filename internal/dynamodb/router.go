package dynamodb

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// resolveAttrName resolves an expression attribute name reference.
func resolveAttrName(ref string, attrNames map[string]string) (string, error) {
	if !strings.HasPrefix(ref, "#") {
		return ref, nil
	}
	actual, ok := attrNames[ref]
	if !ok {
		return "", fmt.Errorf("ExpressionAttributeNames missing %q", ref)
	}
	return actual, nil
}

// addOp is a sentinel stored in the updates map for an ADD clause operation.
type addOp struct{ val any }

// deleteOp is a sentinel stored in the updates map for a DELETE clause operation.
type deleteOp struct{ val any }

// parseUpdateExpression converts an UpdateExpression + attribute maps into a
// flat updates map (attribute name → operation):
//   - nil value   → REMOVE the attribute
//   - addOp       → ADD (numeric increment or set union)
//   - deleteOp    → DELETE (set difference)
//   - other value → SET the attribute to that value
func parseUpdateExpression(
	expr string,
	attrNames map[string]string,
	attrValues map[string]any,
) (map[string]any, error) {
	upper := strings.ToUpper(expr)
	updates := map[string]any{}

	updateExprKeywords := []string{"SET", "REMOVE", "ADD", "DELETE"}
	type section struct{ keyword, content string }
	var sections []section
	for _, kw := range updateExprKeywords {
		idx := strings.Index(upper, kw+" ")
		if idx < 0 {
			continue
		}
		// find where this section ends (at the next keyword)
		end := len(expr)
		for _, kw2 := range updateExprKeywords {
			if j := strings.Index(upper[idx+len(kw)+1:], kw2+" "); j >= 0 {
				if candidate := idx + len(kw) + 1 + j; candidate < end {
					end = candidate
				}
			}
		}
		sections = append(sections, section{kw, strings.TrimSpace(expr[idx+len(kw)+1 : end])})
	}
	if len(sections) == 0 {
		return nil, fmt.Errorf("unsupported UpdateExpression: %q", expr)
	}

	for _, sec := range sections {
		switch sec.keyword {
		case "SET":
			for _, assignment := range strings.Split(sec.content, ",") {
				parts := strings.SplitN(strings.TrimSpace(assignment), "=", 2)
				if len(parts) != 2 {
					return nil, fmt.Errorf("invalid SET clause: %q", assignment)
				}
				name, err := resolveAttrName(strings.TrimSpace(parts[0]), attrNames)
				if err != nil {
					return nil, err
				}
				placeholder := strings.TrimSpace(parts[1])
				val, ok := attrValues[placeholder]
				if !ok {
					return nil, fmt.Errorf("ExpressionAttributeValues missing %q", placeholder)
				}
				updates[name] = val
			}
		case "REMOVE":
			for _, token := range strings.Split(sec.content, ",") {
				name, err := resolveAttrName(strings.TrimSpace(token), attrNames)
				if err != nil {
					return nil, err
				}
				updates[name] = nil
			}
		case "ADD":
			for _, pair := range strings.Split(sec.content, ",") {
				parts := strings.Fields(strings.TrimSpace(pair))
				if len(parts) != 2 {
					return nil, fmt.Errorf("invalid ADD clause: %q", pair)
				}
				name, err := resolveAttrName(parts[0], attrNames)
				if err != nil {
					return nil, err
				}
				val, ok := attrValues[parts[1]]
				if !ok {
					return nil, fmt.Errorf("ExpressionAttributeValues missing %q", parts[1])
				}
				updates[name] = addOp{val}
			}
		case "DELETE":
			for _, pair := range strings.Split(sec.content, ",") {
				parts := strings.Fields(strings.TrimSpace(pair))
				if len(parts) != 2 {
					return nil, fmt.Errorf("invalid DELETE clause: %q", pair)
				}
				name, err := resolveAttrName(parts[0], attrNames)
				if err != nil {
					return nil, err
				}
				val, ok := attrValues[parts[1]]
				if !ok {
					return nil, fmt.Errorf("ExpressionAttributeValues missing %q", parts[1])
				}
				updates[name] = deleteOp{val}
			}
		}
	}
	return updates, nil
}

// applyAddOp applies an ADD clause delta to the current attribute value.
// For Number types, adds the numeric delta (creates the attribute if absent).
// For set types (SS/NS/BS), unions the current set with the delta elements.
func applyAddOp(current, delta any) (any, error) {
	dm, ok := delta.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ADD: delta must be a DynamoDB typed value")
	}
	if dn, ok := dm["N"].(string); ok {
		dv, err := strconv.ParseFloat(dn, 64)
		if err != nil {
			return nil, fmt.Errorf("ADD: invalid number %q", dn)
		}
		var cv float64
		if current != nil {
			cm, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("ADD: existing attribute is not a typed value")
			}
			cn, ok := cm["N"].(string)
			if !ok {
				return nil, fmt.Errorf("ADD: existing attribute is not a Number")
			}
			cv, err = strconv.ParseFloat(cn, 64)
			if err != nil {
				return nil, fmt.Errorf("ADD: invalid existing number %q", cn)
			}
		}
		return map[string]any{"N": strconv.FormatFloat(cv+dv, 'f', -1, 64)}, nil
	}
	for _, setKey := range []string{"SS", "NS", "BS"} {
		if deltaElems, ok := dm[setKey]; ok {
			var currentElems []any
			if current != nil {
				cm, ok := current.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("ADD: existing attribute is not a typed value")
				}
				ce, ok := cm[setKey].([]any)
				if !ok {
					return nil, fmt.Errorf(
						"ADD: existing attribute type mismatch: expected %s",
						setKey,
					)
				}
				currentElems = ce
			}
			deltaSlice, _ := deltaElems.([]any)
			return map[string]any{setKey: setUnion(currentElems, deltaSlice)}, nil
		}
	}
	return nil, fmt.Errorf("ADD: unsupported type in delta value")
}

// applyDeleteOp applies a DELETE clause delta to the current attribute value.
// Only set types (SS/NS/BS) are supported. Returns nil when the result is an empty set.
func applyDeleteOp(current, delta any) (any, error) {
	if current == nil {
		return nil, nil
	}
	dm, ok := delta.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("DELETE: delta must be a DynamoDB typed value")
	}
	for _, setKey := range []string{"SS", "NS", "BS"} {
		if deltaElems, ok := dm[setKey]; ok {
			cm, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("DELETE: existing attribute is not a typed value")
			}
			ce, ok := cm[setKey].([]any)
			if !ok {
				return nil, fmt.Errorf("DELETE: existing attribute is not a %s", setKey)
			}
			deltaSlice, _ := deltaElems.([]any)
			result := setDifference(ce, deltaSlice)
			if len(result) == 0 {
				return nil, nil // empty set → remove the attribute
			}
			return map[string]any{setKey: result}, nil
		}
	}
	return nil, fmt.Errorf("DELETE: unsupported type; only set types (SS/NS/BS) are valid")
}

func setUnion(a, b []any) []any {
	seen := make(map[string]bool, len(a)+len(b))
	result := make([]any, 0, len(a)+len(b))
	for _, v := range a {
		key, _ := json.Marshal(v)
		s := string(key)
		if !seen[s] {
			seen[s] = true
			result = append(result, v)
		}
	}
	for _, v := range b {
		key, _ := json.Marshal(v)
		s := string(key)
		if !seen[s] {
			seen[s] = true
			result = append(result, v)
		}
	}
	return result
}

func setDifference(a, b []any) []any {
	remove := make(map[string]bool, len(b))
	for _, v := range b {
		key, _ := json.Marshal(v)
		remove[string(key)] = true
	}
	result := make([]any, 0, len(a))
	for _, v := range a {
		key, _ := json.Marshal(v)
		if !remove[string(key)] {
			result = append(result, v)
		}
	}
	return result
}

// parseKeyConditionExpression extracts the hash key name, its equality value,
// and an optional sort key condition from a KeyConditionExpression.
// The hash key condition must be an equality; the sort key condition supports
// =, <, <=, >, >=, BETWEEN, and begins_with.
func parseKeyConditionExpression(
	expr string,
	attrNames map[string]string,
	attrValues map[string]any,
) (string, any, *SortKeyCondition, error) {
	parts := strings.SplitN(strings.TrimSpace(expr), " AND ", 2)

	// Parse hash key equality condition
	tokens := strings.Fields(strings.TrimSpace(parts[0]))
	if len(tokens) != 3 || tokens[1] != "=" {
		return "", nil, nil, fmt.Errorf("unsupported KeyConditionExpression: %q", expr)
	}
	name, err := resolveAttrName(tokens[0], attrNames)
	if err != nil {
		return "", nil, nil, err
	}
	val, ok := attrValues[tokens[2]]
	if !ok {
		return "", nil, nil, fmt.Errorf("ExpressionAttributeValues missing %q", tokens[2])
	}

	// Parse optional sort key condition
	var skCond *SortKeyCondition
	if len(parts) == 2 {
		var err error
		skCond, err = parseSortKeyCondition(strings.TrimSpace(parts[1]), attrNames, attrValues)
		if err != nil {
			return "", nil, nil, err
		}
	}

	return name, val, skCond, nil
}

// parseSortKeyCondition parses a single sort key condition expression.
// Supported forms: attr OP :val (OP = =/</<=/>/>=),
// attr BETWEEN :lo AND :hi, begins_with(attr, :prefix).
func parseSortKeyCondition(
	expr string,
	attrNames map[string]string,
	attrValues map[string]any,
) (*SortKeyCondition, error) {
	resolveValue := func(ref string) (any, error) {
		v, ok := attrValues[ref]
		if !ok {
			return nil, fmt.Errorf("ExpressionAttributeValues missing %q", ref)
		}
		return v, nil
	}

	if strings.HasPrefix(expr, "begins_with(") {
		inner := strings.TrimSuffix(strings.TrimPrefix(expr, "begins_with("), ")")
		argParts := strings.SplitN(inner, ",", 2)
		if len(argParts) != 2 {
			return nil, fmt.Errorf("invalid begins_with: %q", expr)
		}
		skName, err := resolveAttrName(strings.TrimSpace(argParts[0]), attrNames)
		if err != nil {
			return nil, err
		}
		skVal, err := resolveValue(strings.TrimSpace(argParts[1]))
		if err != nil {
			return nil, err
		}
		return &SortKeyCondition{Name: skName, Operator: OpBeginsWith, Value: skVal}, nil
	}

	tokens := strings.Fields(expr)

	if len(tokens) == 5 &&
		strings.ToUpper(tokens[1]) == OpBETWEEN &&
		strings.ToUpper(tokens[3]) == "AND" {
		skName, err := resolveAttrName(tokens[0], attrNames)
		if err != nil {
			return nil, err
		}
		lo, err := resolveValue(tokens[2])
		if err != nil {
			return nil, err
		}
		hi, err := resolveValue(tokens[4])
		if err != nil {
			return nil, err
		}
		return &SortKeyCondition{Name: skName, Operator: OpBETWEEN, Value: lo, Value2: hi}, nil
	}

	if len(tokens) == 3 {
		skName, err := resolveAttrName(tokens[0], attrNames)
		if err != nil {
			return nil, err
		}
		op := tokens[1]
		switch op {
		case OpEQ, OpLT, OpLTE, OpGT, OpGTE:
		default:
			return nil, fmt.Errorf("unsupported sort key operator: %q", op)
		}
		skVal, err := resolveValue(tokens[2])
		if err != nil {
			return nil, err
		}
		return &SortKeyCondition{Name: skName, Operator: op, Value: skVal}, nil
	}

	return nil, fmt.Errorf("unsupported sort key condition: %q", expr)
}

type store interface {
	CreateTable(meta TableMetadata) error
	DeleteTable(name string) error
	DescribeTable(name string) (TableMetadata, error)
	ListTables() ([]string, error)
	PutItem(tableName string, item map[string]any, cond *ConditionCheck) (map[string]any, error)
	GetItem(tableName string, key map[string]any) (map[string]any, error)
	DeleteItem(tableName string, key map[string]any, cond *ConditionCheck) (map[string]any, error)
	Scan(tableName string, opts ScanOptions) ([]map[string]any, map[string]any, error)
	UpdateItem(
		tableName string,
		key map[string]any,
		updates map[string]any,
		cond *ConditionCheck,
	) (map[string]any, map[string]any, error)
	Query(
		tableName, hashKeyName string,
		hashKeyValue any,
		skCond *SortKeyCondition,
		opts QueryOptions,
	) ([]map[string]any, map[string]any, error)
	BatchGetItems(tableName string, keys []map[string]any) ([]map[string]any, error)
	BatchWriteItems(tableName string, puts []map[string]any, deletes []map[string]any) error
	UpdateTimeToLive(tableName string, spec TTLSpec) (TTLSpec, error)
	DescribeTimeToLive(tableName string) (string, *TTLSpec, error)
	TagResource(resourceARN string, tags map[string]string) error
	UntagResource(resourceARN string, tagKeys []string) error
	ListTagsOfResource(resourceARN string) (map[string]string, error)
	UpdateTable(tableName string, in UpdateTableInput) (TableMetadata, error)
	TransactGetItems(gets []TransactGetInput) ([]map[string]any, error)
	TransactWriteItems(actions []TransactWriteAction) error
}

// billingModeSummary mirrors the AWS BillingModeSummary shape.
type billingModeSummary struct {
	BillingMode                       string  `json:"BillingMode"`
	LastUpdateToPayPerRequestDateTime float64 `json:"LastUpdateToPayPerRequestDateTime,omitempty"`
}

// tableDescription is the DynamoDB API representation of a table.
type tableDescription struct {
	TableName              string                 `json:"TableName"`
	TableStatus            string                 `json:"TableStatus"`
	TableArn               string                 `json:"TableArn"`
	CreationDateTime       float64                `json:"CreationDateTime"`
	KeySchema              []KeySchemaElement     `json:"KeySchema"`
	AttributeDefinitions   []AttributeDefinition  `json:"AttributeDefinitions"`
	BillingModeSummary     *billingModeSummary    `json:"BillingModeSummary,omitempty"`
	ProvisionedThroughput  *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
	GlobalSecondaryIndexes []gsiDescription       `json:"GlobalSecondaryIndexes,omitempty"`
	LocalSecondaryIndexes  []lsiDescription       `json:"LocalSecondaryIndexes,omitempty"`
	ItemCount              int64                  `json:"ItemCount"`
	TableSizeBytes         int64                  `json:"TableSizeBytes"`
}

type gsiDescription struct {
	IndexName             string                 `json:"IndexName"`
	IndexStatus           string                 `json:"IndexStatus"`
	KeySchema             []KeySchemaElement     `json:"KeySchema"`
	Projection            map[string]any         `json:"Projection,omitempty"`
	ProvisionedThroughput *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
	IndexSizeBytes        int64                  `json:"IndexSizeBytes"`
	ItemCount             int64                  `json:"ItemCount"`
}

type lsiDescription struct {
	IndexName             string                 `json:"IndexName"`
	IndexStatus           string                 `json:"IndexStatus"`
	KeySchema             []KeySchemaElement     `json:"KeySchema"`
	Projection            map[string]any         `json:"Projection,omitempty"`
	ProvisionedThroughput *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
	IndexSizeBytes        int64                  `json:"IndexSizeBytes"`
	ItemCount             int64                  `json:"ItemCount"`
}

func toTableDescription(m TableMetadata) tableDescription {
	desc := tableDescription{
		TableName:   m.Name,
		TableStatus: m.Status,
		TableArn: fmt.Sprintf(
			"arn:aws:dynamodb:us-east-1:000000000000:table/%s",
			m.Name,
		),
		CreationDateTime:      float64(m.CreatedAt.Unix()),
		KeySchema:             m.KeySchema,
		AttributeDefinitions:  m.AttributeDefinitions,
		ProvisionedThroughput: m.ProvisionedThroughput,
	}
	if m.BillingMode != "" {
		bms := &billingModeSummary{BillingMode: m.BillingMode}
		if m.BillingModeUpdatedAt != nil {
			bms.LastUpdateToPayPerRequestDateTime = float64(m.BillingModeUpdatedAt.Unix())
		}
		desc.BillingModeSummary = bms
	}
	for _, gsi := range m.GlobalSecondaryIndexes {
		desc.GlobalSecondaryIndexes = append(desc.GlobalSecondaryIndexes, gsiDescription{
			IndexName:             gsi.IndexName,
			IndexStatus:           "ACTIVE",
			KeySchema:             gsi.KeySchema,
			Projection:            gsi.Projection,
			ProvisionedThroughput: gsi.ProvisionedThroughput,
		})
	}
	for _, lsi := range m.LocalSecondaryIndexes {
		desc.LocalSecondaryIndexes = append(desc.LocalSecondaryIndexes, lsiDescription{
			IndexName:             lsi.IndexName,
			IndexStatus:           "ACTIVE",
			KeySchema:             lsi.KeySchema,
			Projection:            lsi.Projection,
			ProvisionedThroughput: m.ProvisionedThroughput,
		})
	}
	return desc
}

// Router handles DynamoDB API requests dispatched via the X-Amz-Target header.
type Router struct {
	storage store
}

func NewRouter(storage *Storage) *Router {
	return &Router{storage: storage}
}

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	op := strings.TrimPrefix(target, "DynamoDB_20120810.")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"failed to read request body",
		)
		return
	}

	switch op {
	case "CreateTable":
		ro.handleCreateTable(w, body)
	case "DeleteTable":
		ro.handleDeleteTable(w, body)
	case "DescribeTable":
		ro.handleDescribeTable(w, body)
	case "ListTables":
		ro.handleListTables(w, body)
	case "PutItem":
		ro.handlePutItem(w, body)
	case "GetItem":
		ro.handleGetItem(w, body)
	case "DeleteItem":
		ro.handleDeleteItem(w, body)
	case "Scan":
		ro.handleScan(w, body)
	case "UpdateItem":
		ro.handleUpdateItem(w, body)
	case "Query":
		ro.handleQuery(w, body)
	case "BatchGetItem":
		ro.handleBatchGetItem(w, body)
	case "BatchWriteItem":
		ro.handleBatchWriteItem(w, body)
	case "UpdateTable":
		ro.handleUpdateTable(w, body)
	case "UpdateTimeToLive":
		ro.handleUpdateTimeToLive(w, body)
	case "DescribeTimeToLive":
		ro.handleDescribeTimeToLive(w, body)
	case "TagResource":
		ro.handleTagResource(w, body)
	case "UntagResource":
		ro.handleUntagResource(w, body)
	case "ListTagsOfResource":
		ro.handleListTagsOfResource(w, body)
	case "TransactGetItems":
		ro.handleTransactGetItems(w, body)
	case "TransactWriteItems":
		ro.handleTransactWriteItems(w, body)
	case "DescribeLimits":
		ro.handleDescribeLimits(w)
	case "DescribeEndpoints":
		ro.handleDescribeEndpoints(w)
	default:
		slog.Debug( // #nosec G706 -- target comes from the X-Amz-Target header; log injection risk accepted for a local dev emulator
			"DynamoDB operation not implemented",
			"target",
			target,
		)
		writeError(
			w,
			http.StatusNotImplemented,
			"com.amazonaws.dynamodb.v20120810#NotImplemented",
			"Operation not implemented: "+op,
		)
	}
}

func validateTableIndexes(
	tableKeySchema []KeySchemaElement,
	attrDefs []AttributeDefinition,
	gsis []GlobalSecondaryIndex,
	lsis []LocalSecondaryIndex,
) error {
	defined := make(map[string]bool, len(attrDefs))
	for _, a := range attrDefs {
		defined[a.AttributeName] = true
	}

	tableHashKey := ""
	for _, k := range tableKeySchema {
		if !defined[k.AttributeName] {
			return fmt.Errorf(
				"%w: attribute '%s' is used in table key schema but not defined in AttributeDefinitions",
				ErrValidationException,
				k.AttributeName,
			)
		}
		if k.KeyType == "HASH" {
			tableHashKey = k.AttributeName
		}
	}

	if len(lsis) > 5 {
		return fmt.Errorf(
			"%w: number of local secondary indexes exceeds per-table limit of 5",
			ErrValidationException,
		)
	}

	for _, gsi := range gsis {
		hasHash := false
		for _, k := range gsi.KeySchema {
			if k.KeyType == "HASH" {
				hasHash = true
			}
			if !defined[k.AttributeName] {
				return fmt.Errorf(
					"%w: attribute '%s' is used in index '%s' but not defined in AttributeDefinitions",
					ErrValidationException,
					k.AttributeName,
					gsi.IndexName,
				)
			}
		}
		if !hasHash {
			return fmt.Errorf(
				"%w: GlobalSecondaryIndex '%s' must have a HASH key element",
				ErrValidationException, gsi.IndexName,
			)
		}
	}

	indexNames := make(map[string]bool, len(gsis)+len(lsis))
	for _, gsi := range gsis {
		if indexNames[gsi.IndexName] {
			return fmt.Errorf(
				"%w: duplicate index name '%s'",
				ErrValidationException, gsi.IndexName,
			)
		}
		indexNames[gsi.IndexName] = true
	}

	for _, lsi := range lsis {
		if indexNames[lsi.IndexName] {
			return fmt.Errorf(
				"%w: duplicate index name '%s'",
				ErrValidationException, lsi.IndexName,
			)
		}
		indexNames[lsi.IndexName] = true

		lsiHashKey := ""
		hasRange := false
		for _, k := range lsi.KeySchema {
			switch k.KeyType {
			case "HASH":
				lsiHashKey = k.AttributeName
			case "RANGE":
				hasRange = true
			}
			if !defined[k.AttributeName] {
				return fmt.Errorf(
					"%w: attribute '%s' is used in index '%s' but not defined in AttributeDefinitions",
					ErrValidationException,
					k.AttributeName,
					lsi.IndexName,
				)
			}
		}
		if lsiHashKey != tableHashKey {
			return fmt.Errorf(
				"%w: LocalSecondaryIndex '%s' must have the same HASH key as the table (expected '%s')",
				ErrValidationException,
				lsi.IndexName,
				tableHashKey,
			)
		}
		if !hasRange {
			return fmt.Errorf(
				"%w: LocalSecondaryIndex '%s' must have a RANGE key element",
				ErrValidationException, lsi.IndexName,
			)
		}
	}

	return nil
}

func (ro *Router) handleCreateTable(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName              string                `json:"TableName"`
		KeySchema              []KeySchemaElement    `json:"KeySchema"`
		AttributeDefinitions   []AttributeDefinition `json:"AttributeDefinitions"`
		BillingMode            string                `json:"BillingMode"`
		GlobalSecondaryIndexes []struct {
			IndexName             string                 `json:"IndexName"`
			KeySchema             []KeySchemaElement     `json:"KeySchema"`
			Projection            map[string]any         `json:"Projection,omitempty"`
			ProvisionedThroughput *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
		} `json:"GlobalSecondaryIndexes"`
		LocalSecondaryIndexes []struct {
			IndexName  string             `json:"IndexName"`
			KeySchema  []KeySchemaElement `json:"KeySchema"`
			Projection map[string]any     `json:"Projection,omitempty"`
		} `json:"LocalSecondaryIndexes"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}
	meta := TableMetadata{
		Name:                 req.TableName,
		KeySchema:            req.KeySchema,
		AttributeDefinitions: req.AttributeDefinitions,
		BillingMode:          req.BillingMode,
	}
	for _, g := range req.GlobalSecondaryIndexes {
		meta.GlobalSecondaryIndexes = append(meta.GlobalSecondaryIndexes, GlobalSecondaryIndex{
			IndexName:             g.IndexName,
			KeySchema:             g.KeySchema,
			Projection:            g.Projection,
			ProvisionedThroughput: g.ProvisionedThroughput,
		})
	}
	for _, l := range req.LocalSecondaryIndexes {
		meta.LocalSecondaryIndexes = append(meta.LocalSecondaryIndexes, LocalSecondaryIndex{
			IndexName:  l.IndexName,
			KeySchema:  l.KeySchema,
			Projection: l.Projection,
		})
	}
	if err := validateTableIndexes(meta.KeySchema, meta.AttributeDefinitions, meta.GlobalSecondaryIndexes, meta.LocalSecondaryIndexes); err != nil {
		slog.Debug("CreateTable: validation failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			err.Error(),
		)
		return
	}
	if err := ro.storage.CreateTable(meta); err != nil {
		if errors.Is(err, ErrTableAlreadyExists) {
			slog.Debug("CreateTable: table already exists", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceInUseException",
				"Table already exists: "+req.TableName,
			)
			return
		}
		slog.Error("CreateTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	desc, err := ro.storage.DescribeTable(req.TableName)
	if err != nil {
		slog.Error("DescribeTable after CreateTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("created DynamoDB table", "table", req.TableName)
	writeJSON(w, http.StatusOK, map[string]any{"TableDescription": toTableDescription(desc)})
}

func (ro *Router) handleDeleteTable(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName string `json:"TableName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}
	desc, err := ro.storage.DescribeTable(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("DeleteTable: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		slog.Error("DescribeTable before DeleteTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	if err := ro.storage.DeleteTable(req.TableName); err != nil {
		slog.Error("DeleteTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("deleted DynamoDB table", "table", req.TableName)
	d := toTableDescription(desc)
	d.TableStatus = "DELETING"
	writeJSON(w, http.StatusOK, map[string]any{"TableDescription": d})
}

func (ro *Router) handleDescribeTable(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName string `json:"TableName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}
	meta, err := ro.storage.DescribeTable(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("DescribeTable: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		slog.Error("DescribeTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Debug("described DynamoDB table", "table", req.TableName)
	writeJSON(w, http.StatusOK, map[string]any{"Table": toTableDescription(meta)})
}

func (ro *Router) handleListTables(w http.ResponseWriter, body []byte) {
	var req struct {
		Limit                   int    `json:"Limit"`
		ExclusiveStartTableName string `json:"ExclusiveStartTableName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	names, err := ro.storage.ListTables()
	if err != nil {
		slog.Error("ListTables failed", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	if names == nil {
		names = []string{}
	}
	slog.Debug("listed DynamoDB tables", "count", len(names))
	writeJSON(w, http.StatusOK, map[string]any{"TableNames": names})
}

func (ro *Router) handlePutItem(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                 string            `json:"TableName"`
		Item                      map[string]any    `json:"Item"`
		ReturnValues              string            `json:"ReturnValues"`
		ConditionExpression       string            `json:"ConditionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}
	switch req.ReturnValues {
	case "", "NONE", "ALL_OLD":
	default:
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Value '"+req.ReturnValues+"' at 'returnValues' failed to satisfy constraint: Member must satisfy enum value set: [ALL_OLD, NONE]",
		)
		return
	}
	var cond *ConditionCheck
	if req.ConditionExpression != "" {
		cond = &ConditionCheck{
			Expr:   req.ConditionExpression,
			Names:  req.ExpressionAttributeNames,
			Values: req.ExpressionAttributeValues,
		}
	}
	old, err := ro.storage.PutItem(req.TableName, req.Item, cond)
	if err != nil {
		if errors.Is(err, ErrConditionalCheckFailed) {
			slog.Debug("PutItem: condition check failed", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException",
				"The conditional request failed",
			)
			return
		}
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("PutItem: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("PutItem: validation error", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("PutItem failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("put DynamoDB item", "table", req.TableName)
	resp := map[string]any{}
	if req.ReturnValues == "ALL_OLD" && old != nil {
		resp["Attributes"] = old
	}
	writeJSON(w, http.StatusOK, resp)
}

func (ro *Router) handleGetItem(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                string            `json:"TableName"`
		Key                      map[string]any    `json:"Key"`
		ProjectionExpression     string            `json:"ProjectionExpression"`
		ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}
	item, err := ro.storage.GetItem(req.TableName, req.Key)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("GetItem: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("GetItem: validation error", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("GetItem failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	if item != nil && req.ProjectionExpression != "" {
		item, err = applyProjection(item, req.ProjectionExpression, req.ExpressionAttributeNames)
		if err != nil {
			slog.Debug("GetItem: invalid ProjectionExpression", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
	}
	slog.Debug("got DynamoDB item", "table", req.TableName)
	resp := map[string]any{}
	if item != nil {
		resp["Item"] = item
	}
	writeJSON(w, http.StatusOK, resp)
}

func (ro *Router) handleDeleteItem(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                 string            `json:"TableName"`
		Key                       map[string]any    `json:"Key"`
		ReturnValues              string            `json:"ReturnValues"`
		ConditionExpression       string            `json:"ConditionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}
	switch req.ReturnValues {
	case "", "NONE", "ALL_OLD":
	default:
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Value '"+req.ReturnValues+"' at 'returnValues' failed to satisfy constraint: Member must satisfy enum value set: [ALL_OLD, NONE]",
		)
		return
	}
	var cond *ConditionCheck
	if req.ConditionExpression != "" {
		cond = &ConditionCheck{
			Expr:   req.ConditionExpression,
			Names:  req.ExpressionAttributeNames,
			Values: req.ExpressionAttributeValues,
		}
	}
	old, err := ro.storage.DeleteItem(req.TableName, req.Key, cond)
	if err != nil {
		if errors.Is(err, ErrConditionalCheckFailed) {
			slog.Debug("DeleteItem: condition check failed", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException",
				"The conditional request failed",
			)
			return
		}
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("DeleteItem: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("DeleteItem: validation error", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("DeleteItem failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("deleted DynamoDB item", "table", req.TableName)
	resp := map[string]any{}
	if req.ReturnValues == "ALL_OLD" && old != nil {
		resp["Attributes"] = old
	}
	writeJSON(w, http.StatusOK, resp)
}

func (ro *Router) handleScan(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                 string            `json:"TableName"`
		FilterExpression          string            `json:"FilterExpression"`
		ProjectionExpression      string            `json:"ProjectionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		Limit                     *int              `json:"Limit"`
		ExclusiveStartKey         map[string]any    `json:"ExclusiveStartKey"`
		Segment                   *int              `json:"Segment"`
		TotalSegments             *int              `json:"TotalSegments"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}
	if req.Limit != nil && *req.Limit < 1 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			fmt.Sprintf(
				"Value %d at 'limit' failed to satisfy constraint: Member must have value greater than or equal to 1",
				*req.Limit,
			),
		)
		return
	}
	if (req.Segment == nil) != (req.TotalSegments == nil) {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Segment and TotalSegments must both be specified for parallel scan",
		)
		return
	}
	if req.Segment != nil {
		seg := *req.Segment
		total := *req.TotalSegments
		switch {
		case seg < 0 || seg > 999999:
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				fmt.Sprintf(
					"Value %d at 'segment' failed to satisfy constraint: Member must have value between 0 and 999999, inclusive",
					seg,
				),
			)
			return
		case total < 1 || total > 1000000:
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				fmt.Sprintf(
					"Value %d at 'totalSegments' failed to satisfy constraint: Member must have value between 1 and 1000000, inclusive",
					total,
				),
			)
			return
		case seg >= total:
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				fmt.Sprintf(
					"Value %d at 'segment' failed to satisfy constraint: Member must have value less than TotalSegments (%d)",
					seg,
					total,
				),
			)
			return
		}
	}
	opts := ScanOptions{
		Limit:             req.Limit,
		ExclusiveStartKey: req.ExclusiveStartKey,
		Segment:           req.Segment,
		TotalSegments:     req.TotalSegments,
	}
	items, lastEvaluatedKey, err := ro.storage.Scan(req.TableName, opts)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("Scan: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("Scan: validation error", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("Scan failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	if items == nil {
		items = []map[string]any{}
	}
	scannedCount := len(items)
	if req.FilterExpression != "" {
		items, err = applyFilterExpression(
			items,
			req.FilterExpression,
			req.ExpressionAttributeNames,
			req.ExpressionAttributeValues,
		)
		if err != nil {
			slog.Debug("Scan: invalid FilterExpression", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
	}
	if req.ProjectionExpression != "" {
		items, err = applyProjectionToItems(
			items,
			req.ProjectionExpression,
			req.ExpressionAttributeNames,
		)
		if err != nil {
			slog.Debug("Scan: invalid ProjectionExpression", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
	}
	slog.Debug("scanned DynamoDB table", "table", req.TableName, "count", len(items))
	resp := map[string]any{
		"Items":        items,
		"Count":        len(items),
		"ScannedCount": scannedCount,
	}
	if lastEvaluatedKey != nil {
		resp["LastEvaluatedKey"] = lastEvaluatedKey
	}
	writeJSON(w, http.StatusOK, resp)
}

func (ro *Router) handleUpdateItem(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                 string            `json:"TableName"`
		Key                       map[string]any    `json:"Key"`
		UpdateExpression          string            `json:"UpdateExpression"`
		ConditionExpression       string            `json:"ConditionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ReturnValues              string            `json:"ReturnValues"`
		AttributeUpdates          map[string]struct {
			Action string `json:"Action"`
			Value  any    `json:"Value"`
		} `json:"AttributeUpdates"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}

	switch req.ReturnValues {
	case "", "NONE", "ALL_OLD", "ALL_NEW", "UPDATED_OLD", "UPDATED_NEW":
	default:
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Value '"+req.ReturnValues+"' at 'returnValues' failed to satisfy constraint: Member must satisfy enum value set: [ALL_NEW, UPDATED_OLD, ALL_OLD, NONE, UPDATED_NEW]",
		)
		return
	}

	var updates map[string]any
	switch {
	case req.UpdateExpression != "":
		var err error
		updates, err = parseUpdateExpression(
			req.UpdateExpression,
			req.ExpressionAttributeNames,
			req.ExpressionAttributeValues,
		)
		if err != nil {
			slog.Debug("UpdateItem: invalid UpdateExpression", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
	case len(req.AttributeUpdates) > 0:
		updates = make(map[string]any, len(req.AttributeUpdates))
		for name, au := range req.AttributeUpdates {
			switch au.Action {
			case "PUT", "":
				updates[name] = au.Value
			case "DELETE":
				updates[name] = nil
			default:
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					"unsupported AttributeUpdates Action: "+au.Action,
				)
				return
			}
		}
	default:
		updates = map[string]any{}
	}

	var cond *ConditionCheck
	if req.ConditionExpression != "" {
		cond = &ConditionCheck{
			Expr:   req.ConditionExpression,
			Names:  req.ExpressionAttributeNames,
			Values: req.ExpressionAttributeValues,
		}
	}
	before, after, err := ro.storage.UpdateItem(req.TableName, req.Key, updates, cond)
	if err != nil {
		if errors.Is(err, ErrConditionalCheckFailed) {
			slog.Debug("UpdateItem: condition check failed", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException",
				"The conditional request failed",
			)
			return
		}
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("UpdateItem: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("UpdateItem: validation error", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("UpdateItem failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("updated DynamoDB item", "table", req.TableName)
	resp := map[string]any{}
	switch req.ReturnValues {
	case "ALL_NEW":
		resp["Attributes"] = after
	case "ALL_OLD":
		if before != nil {
			resp["Attributes"] = before
		}
	case "UPDATED_NEW":
		attrs := make(map[string]any, len(updates))
		for k := range updates {
			if v, ok := after[k]; ok {
				attrs[k] = v
			}
		}
		if len(attrs) > 0 {
			resp["Attributes"] = attrs
		}
	case "UPDATED_OLD":
		if before != nil {
			attrs := make(map[string]any, len(updates))
			for k := range updates {
				if v, ok := before[k]; ok {
					attrs[k] = v
				}
			}
			if len(attrs) > 0 {
				resp["Attributes"] = attrs
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (ro *Router) handleQuery(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                 string            `json:"TableName"`
		IndexName                 string            `json:"IndexName"`
		KeyConditionExpression    string            `json:"KeyConditionExpression"`
		FilterExpression          string            `json:"FilterExpression"`
		ProjectionExpression      string            `json:"ProjectionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ScanIndexForward          *bool             `json:"ScanIndexForward"`
		Limit                     *int              `json:"Limit"`
		ExclusiveStartKey         map[string]any    `json:"ExclusiveStartKey"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}
	if req.KeyConditionExpression == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"KeyConditionExpression is required",
		)
		return
	}

	hashKeyName, hashKeyValue, skCond, err := parseKeyConditionExpression(
		req.KeyConditionExpression,
		req.ExpressionAttributeNames,
		req.ExpressionAttributeValues,
	)
	if err != nil {
		slog.Debug("Query: invalid KeyConditionExpression", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			err.Error(),
		)
		return
	}

	if req.Limit != nil && *req.Limit < 1 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			fmt.Sprintf(
				"Value %d at 'limit' failed to satisfy constraint: Member must have value greater than or equal to 1",
				*req.Limit,
			),
		)
		return
	}
	scanIndexForward := true
	if req.ScanIndexForward != nil {
		scanIndexForward = *req.ScanIndexForward
	}
	opts := QueryOptions{
		ScanIndexForward:  scanIndexForward,
		Limit:             req.Limit,
		ExclusiveStartKey: req.ExclusiveStartKey,
		IndexName:         req.IndexName,
	}

	items, lastEvaluatedKey, err := ro.storage.Query(
		req.TableName,
		hashKeyName,
		hashKeyValue,
		skCond,
		opts,
	)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("Query: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("Query: validation error", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("Query failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	if items == nil {
		items = []map[string]any{}
	}
	scannedCount := len(items)
	if req.FilterExpression != "" {
		items, err = applyFilterExpression(
			items,
			req.FilterExpression,
			req.ExpressionAttributeNames,
			req.ExpressionAttributeValues,
		)
		if err != nil {
			slog.Debug("Query: invalid FilterExpression", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
	}
	if req.ProjectionExpression != "" {
		items, err = applyProjectionToItems(
			items,
			req.ProjectionExpression,
			req.ExpressionAttributeNames,
		)
		if err != nil {
			slog.Debug("Query: invalid ProjectionExpression", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
	}
	slog.Debug("queried DynamoDB table", "table", req.TableName, "count", len(items))
	resp := map[string]any{
		"Items":        items,
		"Count":        len(items),
		"ScannedCount": scannedCount,
	}
	if lastEvaluatedKey != nil {
		resp["LastEvaluatedKey"] = lastEvaluatedKey
	}
	writeJSON(w, http.StatusOK, resp)
}

func (ro *Router) handleBatchGetItem(w http.ResponseWriter, body []byte) {
	var req struct {
		RequestItems map[string]struct {
			Keys                     []map[string]any  `json:"Keys"`
			ProjectionExpression     string            `json:"ProjectionExpression"`
			ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames"`
		} `json:"RequestItems"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if len(req.RequestItems) == 0 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"RequestItems is required",
		)
		return
	}
	responses := make(map[string][]map[string]any, len(req.RequestItems))
	for tableName, tableReq := range req.RequestItems {
		items, err := ro.storage.BatchGetItems(tableName, tableReq.Keys)
		if err != nil {
			if errors.Is(err, ErrTableNotFound) {
				slog.Debug("BatchGetItem: table not found", "table", tableName)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
					"Requested resource not found: Table: "+tableName+" not found",
				)
				return
			}
			if errors.Is(err, ErrValidationException) {
				slog.Debug("BatchGetItem: validation error", "table", tableName, "err", err)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					err.Error(),
				)
				return
			}
			slog.Error("BatchGetItem failed", "table", tableName, "err", err)
			writeError(
				w,
				http.StatusInternalServerError,
				"com.amazonaws.dynamodb.v20120810#InternalServerError",
				"internal server error",
			)
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
		if tableReq.ProjectionExpression != "" {
			items, err = applyProjectionToItems(
				items,
				tableReq.ProjectionExpression,
				tableReq.ExpressionAttributeNames,
			)
			if err != nil {
				slog.Debug(
					"BatchGetItem: invalid ProjectionExpression",
					"table",
					tableName,
					"err",
					err,
				)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					err.Error(),
				)
				return
			}
		}
		responses[tableName] = items
	}
	slog.Debug("batch got DynamoDB items", "tables", len(req.RequestItems))
	writeJSON(w, http.StatusOK, map[string]any{
		"Responses":       responses,
		"UnprocessedKeys": map[string]any{},
	})
}

func (ro *Router) handleBatchWriteItem(w http.ResponseWriter, body []byte) {
	var req struct {
		RequestItems map[string][]struct {
			PutRequest *struct {
				Item map[string]any `json:"Item"`
			} `json:"PutRequest"`
			DeleteRequest *struct {
				Key map[string]any `json:"Key"`
			} `json:"DeleteRequest"`
		} `json:"RequestItems"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if len(req.RequestItems) == 0 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"RequestItems is required",
		)
		return
	}
	for tableName, writeReqs := range req.RequestItems {
		var puts []map[string]any
		var deletes []map[string]any
		for _, wr := range writeReqs {
			if wr.PutRequest != nil {
				puts = append(puts, wr.PutRequest.Item)
			} else if wr.DeleteRequest != nil {
				deletes = append(deletes, wr.DeleteRequest.Key)
			}
		}
		if err := ro.storage.BatchWriteItems(tableName, puts, deletes); err != nil {
			if errors.Is(err, ErrTableNotFound) {
				slog.Debug("BatchWriteItem: table not found", "table", tableName)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
					"Requested resource not found: Table: "+tableName+" not found",
				)
				return
			}
			if errors.Is(err, ErrValidationException) {
				slog.Debug("BatchWriteItem: validation error", "table", tableName, "err", err)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					err.Error(),
				)
				return
			}
			slog.Error("BatchWriteItem failed", "table", tableName, "err", err)
			writeError(
				w,
				http.StatusInternalServerError,
				"com.amazonaws.dynamodb.v20120810#InternalServerError",
				"internal server error",
			)
			return
		}
	}
	slog.Info("batch wrote DynamoDB items", "tables", len(req.RequestItems))
	writeJSON(w, http.StatusOK, map[string]any{
		"UnprocessedItems": map[string]any{},
	})
}

func (ro *Router) handleUpdateTable(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName             string                `json:"TableName"`
		BillingMode           string                `json:"BillingMode"`
		AttributeDefinitions  []AttributeDefinition `json:"AttributeDefinitions"`
		ProvisionedThroughput *struct {
			ReadCapacityUnits  int64 `json:"ReadCapacityUnits"`
			WriteCapacityUnits int64 `json:"WriteCapacityUnits"`
		} `json:"ProvisionedThroughput"`
		GlobalSecondaryIndexUpdates []struct {
			Create *struct {
				IndexName             string             `json:"IndexName"`
				KeySchema             []KeySchemaElement `json:"KeySchema"`
				Projection            map[string]any     `json:"Projection"`
				ProvisionedThroughput *struct {
					ReadCapacityUnits  int64 `json:"ReadCapacityUnits"`
					WriteCapacityUnits int64 `json:"WriteCapacityUnits"`
				} `json:"ProvisionedThroughput"`
			} `json:"Create"`
			Update *struct {
				IndexName             string `json:"IndexName"`
				ProvisionedThroughput struct {
					ReadCapacityUnits  int64 `json:"ReadCapacityUnits"`
					WriteCapacityUnits int64 `json:"WriteCapacityUnits"`
				} `json:"ProvisionedThroughput"`
			} `json:"Update"`
			Delete *struct {
				IndexName string `json:"IndexName"`
			} `json:"Delete"`
		} `json:"GlobalSecondaryIndexUpdates"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}

	in := UpdateTableInput{
		BillingMode:          req.BillingMode,
		AttributeDefinitions: req.AttributeDefinitions,
	}
	if req.ProvisionedThroughput != nil {
		in.ProvisionedThroughput = &ProvisionedThroughput{
			ReadCapacityUnits:  req.ProvisionedThroughput.ReadCapacityUnits,
			WriteCapacityUnits: req.ProvisionedThroughput.WriteCapacityUnits,
		}
	}
	for _, update := range req.GlobalSecondaryIndexUpdates {
		switch {
		case update.Create != nil:
			gsi := GlobalSecondaryIndex{
				IndexName:  update.Create.IndexName,
				KeySchema:  update.Create.KeySchema,
				Projection: update.Create.Projection,
			}
			if update.Create.ProvisionedThroughput != nil {
				gsi.ProvisionedThroughput = &ProvisionedThroughput{
					ReadCapacityUnits:  update.Create.ProvisionedThroughput.ReadCapacityUnits,
					WriteCapacityUnits: update.Create.ProvisionedThroughput.WriteCapacityUnits,
				}
			}
			in.GSICreates = append(in.GSICreates, gsi)
		case update.Update != nil:
			if in.GSIUpdates == nil {
				in.GSIUpdates = make(map[string]*ProvisionedThroughput)
			}
			in.GSIUpdates[update.Update.IndexName] = &ProvisionedThroughput{
				ReadCapacityUnits:  update.Update.ProvisionedThroughput.ReadCapacityUnits,
				WriteCapacityUnits: update.Update.ProvisionedThroughput.WriteCapacityUnits,
			}
		case update.Delete != nil:
			in.GSIDeletes = append(in.GSIDeletes, update.Delete.IndexName)
		}
	}

	meta, err := ro.storage.UpdateTable(req.TableName, in)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("UpdateTable: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		slog.Error("UpdateTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("updated DynamoDB table", "table", req.TableName)
	writeJSON(w, http.StatusOK, map[string]any{"TableDescription": toTableDescription(meta)})
}

func (ro *Router) handleTagResource(w http.ResponseWriter, body []byte) {
	var req struct {
		ResourceArn string `json:"ResourceArn"`
		Tags        []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.ResourceArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"ResourceArn is required",
		)
		return
	}
	tags := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		tags[t.Key] = t.Value
	}
	if err := ro.storage.TagResource(req.ResourceArn, tags); err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("TagResource: resource not found", "arn", req.ResourceArn)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: "+req.ResourceArn,
			)
			return
		}
		slog.Error("TagResource failed", "arn", req.ResourceArn, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("tagged DynamoDB resource", "arn", req.ResourceArn, "count", len(tags))
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (ro *Router) handleUntagResource(w http.ResponseWriter, body []byte) {
	var req struct {
		ResourceArn string   `json:"ResourceArn"`
		TagKeys     []string `json:"TagKeys"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.ResourceArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"ResourceArn is required",
		)
		return
	}
	if err := ro.storage.UntagResource(req.ResourceArn, req.TagKeys); err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("UntagResource: resource not found", "arn", req.ResourceArn)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: "+req.ResourceArn,
			)
			return
		}
		slog.Error("UntagResource failed", "arn", req.ResourceArn, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("untagged DynamoDB resource", "arn", req.ResourceArn)
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (ro *Router) handleListTagsOfResource(w http.ResponseWriter, body []byte) {
	var req struct {
		ResourceArn string `json:"ResourceArn"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.ResourceArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"ResourceArn is required",
		)
		return
	}
	tags, err := ro.storage.ListTagsOfResource(req.ResourceArn)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("ListTagsOfResource: resource not found", "arn", req.ResourceArn)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: "+req.ResourceArn,
			)
			return
		}
		slog.Error("ListTagsOfResource failed", "arn", req.ResourceArn, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	tagList := make([]map[string]string, 0, len(tags))
	for k, v := range tags {
		tagList = append(tagList, map[string]string{"Key": k, "Value": v})
	}
	slog.Debug("listed DynamoDB resource tags", "arn", req.ResourceArn, "count", len(tagList))
	writeJSON(w, http.StatusOK, map[string]any{"Tags": tagList})
}

func (ro *Router) handleUpdateTimeToLive(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName               string `json:"TableName"`
		TimeToLiveSpecification struct {
			AttributeName string `json:"AttributeName"`
			Enabled       bool   `json:"Enabled"`
		} `json:"TimeToLiveSpecification"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}
	spec, err := ro.storage.UpdateTimeToLive(req.TableName, TTLSpec{
		AttributeName: req.TimeToLiveSpecification.AttributeName,
		Enabled:       req.TimeToLiveSpecification.Enabled,
	})
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("UpdateTimeToLive: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		slog.Error("UpdateTimeToLive failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("updated TTL", "table", req.TableName, "enabled", spec.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{
		"TimeToLiveSpecification": map[string]any{
			"AttributeName": spec.AttributeName,
			"Enabled":       spec.Enabled,
		},
	})
}

func (ro *Router) handleDescribeTimeToLive(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName string `json:"TableName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}
	status, spec, err := ro.storage.DescribeTimeToLive(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("DescribeTimeToLive: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		slog.Error("DescribeTimeToLive failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	ttlDesc := map[string]any{"TimeToLiveStatus": status}
	if spec != nil {
		ttlDesc["AttributeName"] = spec.AttributeName
	}
	slog.Debug("described TTL", "table", req.TableName, "status", status)
	writeJSON(w, http.StatusOK, map[string]any{"TimeToLiveDescription": ttlDesc})
}

func (ro *Router) handleTransactGetItems(w http.ResponseWriter, body []byte) {
	var req struct {
		TransactItems []struct {
			Get *struct {
				TableName                string            `json:"TableName"`
				Key                      map[string]any    `json:"Key"`
				ProjectionExpression     string            `json:"ProjectionExpression"`
				ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames"`
			} `json:"Get"`
		} `json:"TransactItems"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if len(req.TransactItems) == 0 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TransactItems is required",
		)
		return
	}
	if len(req.TransactItems) > 100 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			fmt.Sprintf(
				"Member must have length less than or equal to 100, but received length %d",
				len(req.TransactItems),
			),
		)
		return
	}
	gets := make([]TransactGetInput, 0, len(req.TransactItems))
	projections := make([]struct {
		expr  string
		names map[string]string
	}, len(req.TransactItems))
	for i, ti := range req.TransactItems {
		if ti.Get == nil {
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				"each TransactItems entry must contain a Get",
			)
			return
		}
		gets = append(gets, TransactGetInput{
			TableName: ti.Get.TableName,
			Key:       ti.Get.Key,
		})
		projections[i].expr = ti.Get.ProjectionExpression
		projections[i].names = ti.Get.ExpressionAttributeNames
	}
	items, err := ro.storage.TransactGetItems(gets)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("TransactGetItems: table not found")
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found",
			)
			return
		}
		slog.Error("TransactGetItems failed", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	responses := make([]map[string]any, len(items))
	for i, item := range items {
		if item == nil {
			responses[i] = map[string]any{}
			continue
		}
		if projections[i].expr != "" {
			var projErr error
			item, projErr = applyProjection(item, projections[i].expr, projections[i].names)
			if projErr != nil {
				slog.Debug("TransactGetItems: invalid ProjectionExpression", "err", projErr)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					projErr.Error(),
				)
				return
			}
		}
		responses[i] = map[string]any{"Item": item}
	}
	slog.Debug("TransactGetItems", "count", len(items))
	writeJSON(w, http.StatusOK, map[string]any{"Responses": responses})
}

func (ro *Router) handleTransactWriteItems(w http.ResponseWriter, body []byte) {
	var req struct {
		TransactItems []struct {
			Put *struct {
				TableName                           string            `json:"TableName"`
				Item                                map[string]any    `json:"Item"`
				ConditionExpression                 string            `json:"ConditionExpression"`
				ExpressionAttributeNames            map[string]string `json:"ExpressionAttributeNames"`
				ExpressionAttributeValues           map[string]any    `json:"ExpressionAttributeValues"`
				ReturnValuesOnConditionCheckFailure string            `json:"ReturnValuesOnConditionCheckFailure"`
			} `json:"Put"`
			Delete *struct {
				TableName                           string            `json:"TableName"`
				Key                                 map[string]any    `json:"Key"`
				ConditionExpression                 string            `json:"ConditionExpression"`
				ExpressionAttributeNames            map[string]string `json:"ExpressionAttributeNames"`
				ExpressionAttributeValues           map[string]any    `json:"ExpressionAttributeValues"`
				ReturnValuesOnConditionCheckFailure string            `json:"ReturnValuesOnConditionCheckFailure"`
			} `json:"Delete"`
			Update *struct {
				TableName                           string            `json:"TableName"`
				Key                                 map[string]any    `json:"Key"`
				UpdateExpression                    string            `json:"UpdateExpression"`
				ConditionExpression                 string            `json:"ConditionExpression"`
				ExpressionAttributeNames            map[string]string `json:"ExpressionAttributeNames"`
				ExpressionAttributeValues           map[string]any    `json:"ExpressionAttributeValues"`
				ReturnValuesOnConditionCheckFailure string            `json:"ReturnValuesOnConditionCheckFailure"`
			} `json:"Update"`
			ConditionCheck *struct {
				TableName                           string            `json:"TableName"`
				Key                                 map[string]any    `json:"Key"`
				ConditionExpression                 string            `json:"ConditionExpression"`
				ExpressionAttributeNames            map[string]string `json:"ExpressionAttributeNames"`
				ExpressionAttributeValues           map[string]any    `json:"ExpressionAttributeValues"`
				ReturnValuesOnConditionCheckFailure string            `json:"ReturnValuesOnConditionCheckFailure"`
			} `json:"ConditionCheck"`
		} `json:"TransactItems"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if len(req.TransactItems) == 0 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TransactItems is required",
		)
		return
	}
	if len(req.TransactItems) > 100 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			fmt.Sprintf(
				"Member must have length less than or equal to 100, but received length %d",
				len(req.TransactItems),
			),
		)
		return
	}

	actions := make([]TransactWriteAction, 0, len(req.TransactItems))
	for _, ti := range req.TransactItems {
		switch {
		case ti.Put != nil:
			var cond *ConditionCheck
			if ti.Put.ConditionExpression != "" {
				cond = &ConditionCheck{
					Expr:   ti.Put.ConditionExpression,
					Names:  ti.Put.ExpressionAttributeNames,
					Values: ti.Put.ExpressionAttributeValues,
				}
			}
			actions = append(actions, TransactWriteAction{
				Put: &TransactPut{
					TableName:                      ti.Put.TableName,
					Item:                           ti.Put.Item,
					Cond:                           cond,
					ReturnValuesOnConditionFailure: ti.Put.ReturnValuesOnConditionCheckFailure,
				},
			})

		case ti.Delete != nil:
			var cond *ConditionCheck
			if ti.Delete.ConditionExpression != "" {
				cond = &ConditionCheck{
					Expr:   ti.Delete.ConditionExpression,
					Names:  ti.Delete.ExpressionAttributeNames,
					Values: ti.Delete.ExpressionAttributeValues,
				}
			}
			actions = append(actions, TransactWriteAction{
				Delete: &TransactDelete{
					TableName:                      ti.Delete.TableName,
					Key:                            ti.Delete.Key,
					Cond:                           cond,
					ReturnValuesOnConditionFailure: ti.Delete.ReturnValuesOnConditionCheckFailure,
				},
			})

		case ti.Update != nil:
			updates, err := parseUpdateExpression(
				ti.Update.UpdateExpression,
				ti.Update.ExpressionAttributeNames,
				ti.Update.ExpressionAttributeValues,
			)
			if err != nil {
				slog.Debug("TransactWriteItems: invalid UpdateExpression", "err", err)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					err.Error(),
				)
				return
			}
			var cond *ConditionCheck
			if ti.Update.ConditionExpression != "" {
				cond = &ConditionCheck{
					Expr:   ti.Update.ConditionExpression,
					Names:  ti.Update.ExpressionAttributeNames,
					Values: ti.Update.ExpressionAttributeValues,
				}
			}
			actions = append(actions, TransactWriteAction{
				Update: &TransactUpdate{
					TableName:                      ti.Update.TableName,
					Key:                            ti.Update.Key,
					Updates:                        updates,
					Cond:                           cond,
					ReturnValuesOnConditionFailure: ti.Update.ReturnValuesOnConditionCheckFailure,
				},
			})

		case ti.ConditionCheck != nil:
			if ti.ConditionCheck.ConditionExpression == "" {
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					"ConditionExpression is required for ConditionCheck",
				)
				return
			}
			cond := &ConditionCheck{
				Expr:   ti.ConditionCheck.ConditionExpression,
				Names:  ti.ConditionCheck.ExpressionAttributeNames,
				Values: ti.ConditionCheck.ExpressionAttributeValues,
			}
			actions = append(actions, TransactWriteAction{
				ConditionCheck: &TransactConditionCheck{
					TableName:                      ti.ConditionCheck.TableName,
					Key:                            ti.ConditionCheck.Key,
					Cond:                           cond,
					ReturnValuesOnConditionFailure: ti.ConditionCheck.ReturnValuesOnConditionCheckFailure,
				},
			})

		default:
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				"each TransactItems entry must contain Put, Delete, Update, or ConditionCheck",
			)
			return
		}
	}

	err := ro.storage.TransactWriteItems(actions)
	if err != nil {
		var txErr *TransactionCanceledError
		if errors.As(err, &txErr) {
			slog.Debug("TransactWriteItems: transaction canceled", "reasons", len(txErr.Reasons))
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
				slog.Warn("failed to encode TransactionCanceledException", "err", encErr)
			}
			return
		}
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("TransactWriteItems: table not found")
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("TransactWriteItems: validation error", "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("TransactWriteItems failed", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("TransactWriteItems succeeded", "count", len(actions))
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (ro *Router) handleDescribeLimits(w http.ResponseWriter) {
	slog.Debug("DescribeLimits")
	writeJSON(w, http.StatusOK, map[string]any{
		"AccountMaxReadCapacityUnits":  80000,
		"AccountMaxWriteCapacityUnits": 80000,
		"TableMaxReadCapacityUnits":    40000,
		"TableMaxWriteCapacityUnits":   40000,
	})
}

func (ro *Router) handleDescribeEndpoints(w http.ResponseWriter) {
	slog.Debug("DescribeEndpoints")
	writeJSON(w, http.StatusOK, map[string]any{
		"Endpoints": []map[string]any{
			{
				"Address":              "localhost:5566",
				"CachePeriodInMinutes": 1440,
			},
		},
	})
}
