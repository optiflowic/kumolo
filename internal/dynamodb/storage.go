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
