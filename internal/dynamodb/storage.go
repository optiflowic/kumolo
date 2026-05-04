package dynamodb

import (
	"crypto/md5" // #nosec G501 -- MD5 used only for deterministic filename generation, not security
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// KeySchemaElement is one element of a table's key schema.
type KeySchemaElement struct {
	AttributeName string `json:"AttributeName"`
	KeyType       string `json:"KeyType"`
}

// AttributeDefinition describes a key attribute's type.
type AttributeDefinition struct {
	AttributeName string `json:"AttributeName"`
	AttributeType string `json:"AttributeType"`
}

// TTLSpec holds the TimeToLive configuration for a table.
type TTLSpec struct {
	AttributeName string `json:"attributeName"`
	Enabled       bool   `json:"enabled"`
}

// ProvisionedThroughput holds read/write capacity units.
type ProvisionedThroughput struct {
	ReadCapacityUnits  int64 `json:"ReadCapacityUnits,omitempty"`
	WriteCapacityUnits int64 `json:"WriteCapacityUnits,omitempty"`
}

// GlobalSecondaryIndex holds the definition of a GSI.
type GlobalSecondaryIndex struct {
	IndexName             string                 `json:"indexName"`
	KeySchema             []KeySchemaElement     `json:"keySchema"`
	Projection            map[string]any         `json:"projection,omitempty"`
	ProvisionedThroughput *ProvisionedThroughput `json:"provisionedThroughput,omitempty"`
}

// TableMetadata is stored as <table>.table.json at the storage root.
type TableMetadata struct {
	Name                   string                 `json:"name"`
	KeySchema              []KeySchemaElement     `json:"keySchema"`
	AttributeDefinitions   []AttributeDefinition  `json:"attributeDefinitions"`
	BillingMode            string                 `json:"billingMode,omitempty"`
	BillingModeUpdatedAt   *time.Time             `json:"billingModeUpdatedAt,omitempty"`
	ProvisionedThroughput  *ProvisionedThroughput `json:"provisionedThroughput,omitempty"`
	GlobalSecondaryIndexes []GlobalSecondaryIndex `json:"globalSecondaryIndexes,omitempty"`
	Status                 string                 `json:"status"`
	CreatedAt              time.Time              `json:"createdAt"`
	TTL                    *TTLSpec               `json:"ttl,omitempty"`
	Tags                   map[string]string      `json:"tags,omitempty"`
}

// Sort key condition operators used in SortKeyCondition.Operator.
const (
	OpEQ         = "="
	OpLT         = "<"
	OpLTE        = "<="
	OpGT         = ">"
	OpGTE        = ">="
	OpBETWEEN    = "BETWEEN"
	OpBeginsWith = "begins_with"
)

// SortKeyCondition describes an optional sort key filter applied during Query.
type SortKeyCondition struct {
	Name     string
	Operator string // one of the Op* constants
	Value    any    // comparison value (DynamoDB typed)
	Value2   any    // upper bound for BETWEEN
}

// QueryOptions controls pagination and sort order for Query.
type QueryOptions struct {
	ScanIndexForward  bool
	Limit             *int // nil means no limit; must be >= 1 when set
	ExclusiveStartKey map[string]any
}

// dynamoValueCmp compares two DynamoDB typed attribute values.
// Returns negative, zero, or positive like strings.Compare.
func dynamoValueCmp(a, b any) (int, error) {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if !aok || !bok {
		return 0, fmt.Errorf("invalid DynamoDB typed value")
	}
	if as, ok := am["S"].(string); ok {
		bs, ok := bm["S"].(string)
		if !ok {
			return 0, fmt.Errorf("type mismatch: expected S")
		}
		return strings.Compare(as, bs), nil
	}
	if an, ok := am["N"].(string); ok {
		bn, ok := bm["N"].(string)
		if !ok {
			return 0, fmt.Errorf("type mismatch: expected N")
		}
		af, _ := strconv.ParseFloat(an, 64) // N values are always valid numerics per DynamoDB spec
		bf, _ := strconv.ParseFloat(bn, 64)
		switch {
		case af < bf:
			return -1, nil
		case af > bf:
			return 1, nil
		default:
			return 0, nil
		}
	}
	// Fallback: lexicographic JSON comparison (for B, BOOL, NULL, etc.)
	aj, _ := json.Marshal(a) // json.Marshal only fails for unmarshalable types (channels, funcs)
	bj, _ := json.Marshal(b) // json.Marshal only fails for unmarshalable types (channels, funcs)
	return strings.Compare(string(aj), string(bj)), nil
}

// matchesSortKey reports whether itemVal satisfies cond.
func matchesSortKey(itemVal any, cond SortKeyCondition) bool {
	switch cond.Operator {
	case OpEQ:
		a, _ := json.Marshal(
			itemVal,
		) // json.Marshal only fails for unmarshalable types (channels, funcs)
		b, _ := json.Marshal(
			cond.Value,
		) // json.Marshal only fails for unmarshalable types (channels, funcs)
		return string(a) == string(b)
	case OpLT:
		c, err := dynamoValueCmp(itemVal, cond.Value)
		return err == nil && c < 0
	case OpLTE:
		c, err := dynamoValueCmp(itemVal, cond.Value)
		return err == nil && c <= 0
	case OpGT:
		c, err := dynamoValueCmp(itemVal, cond.Value)
		return err == nil && c > 0
	case OpGTE:
		c, err := dynamoValueCmp(itemVal, cond.Value)
		return err == nil && c >= 0
	case OpBETWEEN:
		c1, err1 := dynamoValueCmp(itemVal, cond.Value)
		c2, err2 := dynamoValueCmp(itemVal, cond.Value2)
		return err1 == nil && err2 == nil && c1 >= 0 && c2 <= 0
	case OpBeginsWith:
		am, aok := itemVal.(map[string]any)
		bm, bok := cond.Value.(map[string]any)
		if !aok || !bok {
			return false
		}
		as, aok := am["S"].(string)
		bs, bok := bm["S"].(string)
		return aok && bok && strings.HasPrefix(as, bs)
	}
	return false
}

// Storage is a filesystem-backed DynamoDB backend. os.Root scopes all access to
// the storage root, preventing path traversal attacks.
type Storage struct {
	mu         sync.RWMutex
	root       *os.Root
	mkdirFn    func(name string, perm os.FileMode) error
	removeFile func(name string) error
	openFile   func(name string, flag int, perm os.FileMode) (io.WriteCloser, error)
	readAll    func(r io.Reader) ([]byte, error)
	listDirFn  func(name string) ([]os.DirEntry, error)
}

// NewStorage roots the storage at dataDir/dynamodb, creating the directory if needed.
func NewStorage(dataDir string) (*Storage, error) {
	return newStorage(dataDir, os.OpenRoot)
}

// Close releases the os.Root handle held by the storage.
func (s *Storage) Close() error {
	return s.root.Close()
}

func newStorage(dataDir string, openRoot func(string) (*os.Root, error)) (*Storage, error) {
	rootPath := filepath.Join(dataDir, "dynamodb")
	if err := os.MkdirAll(rootPath, 0o750); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	root, err := openRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open storage root: %w", err)
	}
	s := &Storage{root: root}
	s.mkdirFn = s.root.Mkdir
	s.removeFile = s.root.Remove
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		return s.root.OpenFile(name, flag, perm)
	}
	s.readAll = io.ReadAll
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		f, err := s.root.Open(name)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return f.ReadDir(-1)
	}
	return s, nil
}

func (s *Storage) CreateTable(meta TableMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tableExistsLocked(meta.Name) {
		return ErrTableAlreadyExists
	}
	meta.Status = "ACTIVE"
	meta.CreatedAt = time.Now().UTC()
	if err := s.mkdirFn(meta.Name, 0o750); err != nil {
		return err
	}
	if err := s.writeTableMeta(meta.Name, meta); err != nil {
		if removeErr := s.removeFile(meta.Name); removeErr != nil {
			slog.Warn(
				"failed to clean up table dir after meta write failure",
				"table", meta.Name,
				"err", removeErr,
			)
		}
		return err
	}
	return nil
}

func (s *Storage) DeleteTable(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(name) {
		return ErrTableNotFound
	}
	entries, err := s.readDir(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if err := s.removeFile(filepath.Join(name, e.Name())); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := s.removeFile(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := s.removeFile(name + ".table.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Storage) DescribeTable(name string) (TableMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(name) {
		return TableMetadata{}, ErrTableNotFound
	}
	return s.readTableMeta(name)
}

func (s *Storage) ListTables() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := s.readDir(".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

type ConditionCheck struct {
	Expr   string
	Names  map[string]string
	Values map[string]any
}

func (s *Storage) PutItem(
	tableName string,
	item map[string]any,
	cond *ConditionCheck,
) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrTableNotFound
		}
		return nil, err
	}
	key, err := itemKey(item, meta.KeySchema)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(tableName, key+".json")
	old, err := readJSON[map[string]any](s, path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if cond != nil && cond.Expr != "" {
		current := old
		if current == nil {
			current = map[string]any{}
		}
		ok, err := evalFilterExpr(cond.Expr, current, cond.Names, cond.Values)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrValidationException, err)
		}
		if !ok {
			return nil, ErrConditionalCheckFailed
		}
	}
	return old, s.writeJSON(path, item)
}

func (s *Storage) GetItem(tableName string, key map[string]any) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrTableNotFound
		}
		return nil, err
	}
	k, err := itemKey(key, meta.KeySchema)
	if err != nil {
		return nil, err
	}
	item, err := readJSON[map[string]any](s, filepath.Join(tableName, k+".json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return item, nil
}

func (s *Storage) DeleteItem(
	tableName string,
	key map[string]any,
	cond *ConditionCheck,
) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrTableNotFound
		}
		return nil, err
	}
	k, err := itemKey(key, meta.KeySchema)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(tableName, k+".json")
	old, err := readJSON[map[string]any](s, path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if cond != nil && cond.Expr != "" {
		current := old
		if current == nil {
			current = map[string]any{}
		}
		ok, err := evalFilterExpr(cond.Expr, current, cond.Names, cond.Values)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrValidationException, err)
		}
		if !ok {
			return nil, ErrConditionalCheckFailed
		}
	}
	if err := s.removeFile(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return old, nil
}

func (s *Storage) Scan(tableName string) ([]map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(tableName) {
		return nil, ErrTableNotFound
	}
	return s.readAllItemsLocked(tableName)
}

// UpdateItem reads an existing item (or seeds one from the key), applies the
// provided attribute updates, and writes the result back.
// A nil value in updates means remove the attribute.
// Returns (before, after, error): before is the item state prior to update,
// after is the item state following the update.
func (s *Storage) UpdateItem(
	tableName string,
	key map[string]any,
	updates map[string]any,
	cond *ConditionCheck,
) (map[string]any, map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrTableNotFound
		}
		return nil, nil, err
	}
	k, err := itemKey(key, meta.KeySchema)
	if err != nil {
		return nil, nil, err
	}
	itemPath := filepath.Join(tableName, k+".json")
	item, err := readJSON[map[string]any](s, itemPath)
	var before map[string]any
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		item = make(map[string]any, len(key))
		for kk, v := range key {
			item[kk] = v
		}
		// before stays nil: item did not exist prior to this update
	} else {
		before = make(map[string]any, len(item))
		for k, v := range item {
			before[k] = v
		}
	}
	if cond != nil && cond.Expr != "" {
		condItem := before
		if condItem == nil {
			condItem = map[string]any{}
		}
		ok, err := evalFilterExpr(cond.Expr, condItem, cond.Names, cond.Values)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrValidationException, err)
		}
		if !ok {
			return nil, nil, ErrConditionalCheckFailed
		}
	}
	for attr, val := range updates {
		switch op := val.(type) {
		case nil:
			delete(item, attr)
		case addOp:
			result, err := applyAddOp(item[attr], op.val)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %v", ErrValidationException, err)
			}
			item[attr] = result
		case deleteOp:
			result, err := applyDeleteOp(item[attr], op.val)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %v", ErrValidationException, err)
			}
			if result == nil {
				delete(item, attr)
			} else {
				item[attr] = result
			}
		default:
			item[attr] = val
		}
	}
	if err := s.writeJSON(itemPath, item); err != nil {
		return nil, nil, err
	}
	return before, item, nil
}

// Query returns items in tableName matching the hash key equality and the optional
// sort key condition. Hash key comparison uses JSON encoding; sort key comparison
// is type-aware (S: lexicographic, N: numeric).
//
// opts.ScanIndexForward controls ascending (true) vs descending (false) sort order.
// opts.Limit caps the number of items evaluated before FilterExpression.
// opts.ExclusiveStartKey resumes from the item after the given primary key.
// The second return value is the LastEvaluatedKey (non-nil when more pages remain).
func (s *Storage) Query(
	tableName, hashKeyName string,
	hashKeyValue any,
	skCond *SortKeyCondition,
	opts QueryOptions,
) ([]map[string]any, map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(tableName) {
		return nil, nil, ErrTableNotFound
	}
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return nil, nil, err
	}
	var skName string
	for _, k := range meta.KeySchema {
		if k.KeyType == "RANGE" {
			skName = k.AttributeName
			break
		}
	}
	all, err := s.readAllItemsLocked(tableName)
	if err != nil {
		return nil, nil, err
	}
	wantJSON, _ := json.Marshal(hashKeyValue) // only fails for channels/funcs
	var matched []map[string]any
	for _, item := range all {
		val, ok := item[hashKeyName]
		if !ok {
			continue
		}
		gotJSON, _ := json.Marshal(val) // only fails for channels/funcs
		if string(gotJSON) != string(wantJSON) {
			continue
		}
		if skCond != nil {
			skVal, ok := item[skCond.Name]
			if !ok || !matchesSortKey(skVal, *skCond) {
				continue
			}
		}
		matched = append(matched, item)
	}

	if skName != "" {
		sort.SliceStable(matched, func(i, j int) bool {
			c, err := dynamoValueCmp(matched[i][skName], matched[j][skName])
			if err != nil {
				slog.Warn(
					"Query: sort key comparison failed; order undefined",
					"table",
					tableName,
					"err",
					err,
				)
				return false
			}
			if opts.ScanIndexForward {
				return c < 0
			}
			return c > 0
		})
	}

	if len(opts.ExclusiveStartKey) > 0 {
		if skName == "" {
			// Hash-only table: Query returns at most one item per hash key.
			// Any ExclusiveStartKey means we are resuming past that item.
			matched = matched[:0]
		} else {
			eskSKVal, ok := opts.ExclusiveStartKey[skName]
			if !ok {
				matched = matched[:0]
			} else {
				// Use sort key value as a position cursor, not an exact item lookup.
				// This matches DynamoDB behavior: the cursor remains valid even when
				// the item at that key has been deleted.
				startIdx := len(matched)
				for i, item := range matched {
					c, err := dynamoValueCmp(item[skName], eskSKVal)
					if err != nil {
						continue
					}
					if opts.ScanIndexForward && c > 0 {
						startIdx = i
						break
					}
					if !opts.ScanIndexForward && c < 0 {
						startIdx = i
						break
					}
				}
				matched = matched[startIdx:]
			}
		}
	}

	var lastEvaluatedKey map[string]any
	if opts.Limit != nil && len(matched) > *opts.Limit {
		lastItem := matched[*opts.Limit-1]
		lastEvaluatedKey = extractPrimaryKey(lastItem, meta.KeySchema)
		matched = matched[:*opts.Limit]
	}

	return matched, lastEvaluatedKey, nil
}

// extractPrimaryKey builds a DynamoDB-typed primary key map from an item.
func extractPrimaryKey(item map[string]any, keySchema []KeySchemaElement) map[string]any {
	key := make(map[string]any, len(keySchema))
	for _, k := range keySchema {
		key[k.AttributeName] = item[k.AttributeName]
	}
	return key
}

// BatchGetItems retrieves items by their primary keys from tableName.
// Items not found are omitted from the result (matching DynamoDB behavior).
func (s *Storage) BatchGetItems(tableName string, keys []map[string]any) ([]map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrTableNotFound
		}
		return nil, err
	}
	var items []map[string]any
	for _, key := range keys {
		k, err := itemKey(key, meta.KeySchema)
		if err != nil {
			return nil, err
		}
		item, err := readJSON[map[string]any](s, filepath.Join(tableName, k+".json"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// BatchWriteItems applies puts and deletes to tableName under a single lock.
func (s *Storage) BatchWriteItems(
	tableName string,
	puts []map[string]any,
	deletes []map[string]any,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrTableNotFound
		}
		return err
	}
	for _, item := range puts {
		key, err := itemKey(item, meta.KeySchema)
		if err != nil {
			return err
		}
		if err := s.writeJSON(filepath.Join(tableName, key+".json"), item); err != nil {
			return err
		}
	}
	for _, key := range deletes {
		k, err := itemKey(key, meta.KeySchema)
		if err != nil {
			return err
		}
		if err := s.removeFile(filepath.Join(tableName, k+".json")); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
	}
	return nil
}

// readAllItemsLocked reads every *.json item file in tableName.
// Must be called with at least a read lock held.
func (s *Storage) readAllItemsLocked(tableName string) ([]map[string]any, error) {
	entries, err := s.readDir(tableName)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		itemPath := filepath.Join(tableName, e.Name())
		item, err := readJSON[map[string]any](s, itemPath)
		if err != nil {
			slog.Warn("skipping item with unreadable data", "path", itemPath, "err", err)
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

// itemKey computes a deterministic filesystem-safe filename for an item
// from its primary key attributes by hashing their JSON representation.
func itemKey(item map[string]any, keySchema []KeySchemaElement) (string, error) {
	type part struct {
		Name  string `json:"n"`
		Value any    `json:"v"`
	}
	parts := make([]part, 0, len(keySchema))
	for _, k := range keySchema {
		v, ok := item[k.AttributeName]
		if !ok {
			return "", fmt.Errorf(
				"%w: missing key attribute %q",
				ErrValidationException,
				k.AttributeName,
			)
		}
		parts = append(parts, part{Name: k.AttributeName, Value: v})
	}
	data, _ := json.Marshal(parts)
	h := md5.Sum(
		data,
	) // #nosec G401 -- MD5 used only for deterministic filename generation, not security
	return hex.EncodeToString(h[:]), nil
}

func (s *Storage) tableExistsLocked(name string) bool {
	info, err := s.root.Stat(name)
	return err == nil && info.IsDir()
}

func (s *Storage) readTableMeta(name string) (TableMetadata, error) {
	return readJSON[TableMetadata](s, name+".table.json")
}

func (s *Storage) writeTableMeta(name string, meta TableMetadata) error {
	return s.writeJSON(name+".table.json", meta)
}

// UpdateTableInput holds the optional fields that can be changed via UpdateTable.
type UpdateTableInput struct {
	BillingMode           string
	ProvisionedThroughput *ProvisionedThroughput
	AttributeDefinitions  []AttributeDefinition
	GSICreates            []GlobalSecondaryIndex
	GSIUpdates            map[string]*ProvisionedThroughput // indexName → new throughput
	GSIDeletes            []string                          // indexNames to remove
}

func (s *Storage) UpdateTable(tableName string, in UpdateTableInput) (TableMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(tableName) {
		return TableMetadata{}, ErrTableNotFound
	}
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return TableMetadata{}, err
	}
	if in.BillingMode != "" && in.BillingMode != meta.BillingMode {
		meta.BillingMode = in.BillingMode
		now := time.Now().UTC()
		meta.BillingModeUpdatedAt = &now
	}
	if in.ProvisionedThroughput != nil {
		meta.ProvisionedThroughput = in.ProvisionedThroughput
	}
	// Merge new AttributeDefinitions (deduplicate by AttributeName)
	existing := make(map[string]struct{}, len(meta.AttributeDefinitions))
	for _, a := range meta.AttributeDefinitions {
		existing[a.AttributeName] = struct{}{}
	}
	for _, a := range in.AttributeDefinitions {
		if _, ok := existing[a.AttributeName]; !ok {
			meta.AttributeDefinitions = append(meta.AttributeDefinitions, a)
			existing[a.AttributeName] = struct{}{}
		}
	}
	// GSI deletes
	deleteSet := make(map[string]struct{}, len(in.GSIDeletes))
	for _, name := range in.GSIDeletes {
		deleteSet[name] = struct{}{}
	}
	// GSI updates and deletes applied to existing list
	filtered := meta.GlobalSecondaryIndexes[:0:0]
	for _, gsi := range meta.GlobalSecondaryIndexes {
		if _, del := deleteSet[gsi.IndexName]; del {
			continue
		}
		if pt, ok := in.GSIUpdates[gsi.IndexName]; ok {
			gsi.ProvisionedThroughput = pt
		}
		filtered = append(filtered, gsi)
	}
	// GSI creates
	filtered = append(filtered, in.GSICreates...)
	meta.GlobalSecondaryIndexes = filtered
	if err := s.writeTableMeta(tableName, meta); err != nil {
		return TableMetadata{}, err
	}
	return meta, nil
}

// tableNameFromARN extracts the table name from a DynamoDB table ARN.
func tableNameFromARN(arn string) (string, bool) {
	const prefix = "arn:aws:dynamodb:us-east-1:000000000000:table/"
	if !strings.HasPrefix(arn, prefix) {
		return "", false
	}
	return strings.TrimPrefix(arn, prefix), true
}

func (s *Storage) TagResource(resourceARN string, tags map[string]string) error {
	name, ok := tableNameFromARN(resourceARN)
	if !ok {
		return ErrTableNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(name) {
		return ErrTableNotFound
	}
	meta, err := s.readTableMeta(name)
	if err != nil {
		return err
	}
	if meta.Tags == nil {
		meta.Tags = make(map[string]string, len(tags))
	}
	for k, v := range tags {
		meta.Tags[k] = v
	}
	return s.writeTableMeta(name, meta)
}

func (s *Storage) UntagResource(resourceARN string, tagKeys []string) error {
	name, ok := tableNameFromARN(resourceARN)
	if !ok {
		return ErrTableNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(name) {
		return ErrTableNotFound
	}
	meta, err := s.readTableMeta(name)
	if err != nil {
		return err
	}
	for _, k := range tagKeys {
		delete(meta.Tags, k)
	}
	return s.writeTableMeta(name, meta)
}

func (s *Storage) ListTagsOfResource(resourceARN string) (map[string]string, error) {
	name, ok := tableNameFromARN(resourceARN)
	if !ok {
		return nil, ErrTableNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(name) {
		return nil, ErrTableNotFound
	}
	meta, err := s.readTableMeta(name)
	if err != nil {
		return nil, err
	}
	if meta.Tags == nil {
		return map[string]string{}, nil
	}
	return meta.Tags, nil
}

func (s *Storage) UpdateTimeToLive(tableName string, spec TTLSpec) (TTLSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tableExistsLocked(tableName) {
		return TTLSpec{}, ErrTableNotFound
	}
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return TTLSpec{}, err
	}
	meta.TTL = &spec
	if err := s.writeTableMeta(tableName, meta); err != nil {
		return TTLSpec{}, err
	}
	return spec, nil
}

func (s *Storage) DescribeTimeToLive(tableName string) (string, *TTLSpec, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(tableName) {
		return "", nil, ErrTableNotFound
	}
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return "", nil, err
	}
	if meta.TTL == nil || !meta.TTL.Enabled {
		return "DISABLED", meta.TTL, nil
	}
	return "ENABLED", meta.TTL, nil
}

func (s *Storage) writeJSON(path string, v any) (retErr error) {
	data, _ := json.Marshal(v) // json.Marshal only fails for unmarshalable types (channels, funcs)
	f, err := s.openFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil && retErr == nil {
			retErr = err
		}
	}()
	_, retErr = f.Write(data)
	return
}

func readJSON[T any](s *Storage, path string) (T, error) {
	var zero T
	f, err := s.root.Open(path)
	if err != nil {
		return zero, err
	}
	defer func() { _ = f.Close() }()
	data, err := s.readAll(f)
	if err != nil {
		return zero, err
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return zero, err
	}
	return v, nil
}

func (s *Storage) readDir(name string) ([]os.DirEntry, error) {
	return s.listDirFn(name)
}
