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

// TableMetadata is stored as <table>.table.json at the storage root.
type TableMetadata struct {
	Name                 string                `json:"name"`
	KeySchema            []KeySchemaElement    `json:"keySchema"`
	AttributeDefinitions []AttributeDefinition `json:"attributeDefinitions"`
	BillingMode          string                `json:"billingMode,omitempty"`
	Status               string                `json:"status"`
	CreatedAt            time.Time             `json:"createdAt"`
}

// SortKeyCondition describes an optional sort key filter applied during Query.
type SortKeyCondition struct {
	Name     string // attribute name
	Operator string // =, <, <=, >, >=, BETWEEN, begins_with
	Value    any    // comparison value (DynamoDB typed)
	Value2   any    // upper bound for BETWEEN
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
	case "=":
		a, _ := json.Marshal(
			itemVal,
		) // json.Marshal only fails for unmarshalable types (channels, funcs)
		b, _ := json.Marshal(
			cond.Value,
		) // json.Marshal only fails for unmarshalable types (channels, funcs)
		return string(a) == string(b)
	case "<":
		c, err := dynamoValueCmp(itemVal, cond.Value)
		return err == nil && c < 0
	case "<=":
		c, err := dynamoValueCmp(itemVal, cond.Value)
		return err == nil && c <= 0
	case ">":
		c, err := dynamoValueCmp(itemVal, cond.Value)
		return err == nil && c > 0
	case ">=":
		c, err := dynamoValueCmp(itemVal, cond.Value)
		return err == nil && c >= 0
	case "BETWEEN":
		c1, err1 := dynamoValueCmp(itemVal, cond.Value)
		c2, err2 := dynamoValueCmp(itemVal, cond.Value2)
		return err1 == nil && err2 == nil && c1 >= 0 && c2 <= 0
	case "begins_with":
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

func (s *Storage) PutItem(tableName string, item map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrTableNotFound
		}
		return err
	}
	key, err := itemKey(item, meta.KeySchema)
	if err != nil {
		return err
	}
	return s.writeJSON(filepath.Join(tableName, key+".json"), item)
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

func (s *Storage) DeleteItem(tableName string, key map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readTableMeta(tableName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrTableNotFound
		}
		return err
	}
	k, err := itemKey(key, meta.KeySchema)
	if err != nil {
		return err
	}
	if err := s.removeFile(filepath.Join(tableName, k+".json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
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
func (s *Storage) UpdateItem(
	tableName string,
	key map[string]any,
	updates map[string]any,
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
	itemPath := filepath.Join(tableName, k+".json")
	item, err := readJSON[map[string]any](s, itemPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		item = make(map[string]any, len(key))
		for kk, v := range key {
			item[kk] = v
		}
	}
	for attr, val := range updates {
		if val == nil {
			delete(item, attr)
		} else {
			item[attr] = val
		}
	}
	if err := s.writeJSON(itemPath, item); err != nil {
		return nil, err
	}
	return item, nil
}

// Query returns items in tableName matching the hash key equality and the optional
// sort key condition. Hash key comparison uses JSON encoding; sort key comparison
// is type-aware (S: lexicographic, N: numeric).
func (s *Storage) Query(
	tableName, hashKeyName string,
	hashKeyValue any,
	skCond *SortKeyCondition,
) ([]map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tableExistsLocked(tableName) {
		return nil, ErrTableNotFound
	}
	all, err := s.readAllItemsLocked(tableName)
	if err != nil {
		return nil, err
	}
	wantJSON, _ := json.Marshal(
		hashKeyValue,
	) // json.Marshal only fails for unmarshalable types (channels, funcs)
	var items []map[string]any
	for _, item := range all {
		val, ok := item[hashKeyName]
		if !ok {
			continue
		}
		gotJSON, _ := json.Marshal(
			val,
		) // json.Marshal only fails for unmarshalable types (channels, funcs)
		if string(gotJSON) != string(wantJSON) {
			continue
		}
		if skCond != nil {
			skVal, ok := item[skCond.Name]
			if !ok || !matchesSortKey(skVal, *skCond) {
				continue
			}
		}
		items = append(items, item)
	}
	return items, nil
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
