package dynamodb

import (
	"crypto/md5" // #nosec G501 -- MD5 used only for deterministic filename generation, not security
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

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
	statFn     func(name string) (os.FileInfo, error)

	streamsMu sync.RWMutex
	streams   map[string]*streamBuffer // tableName → in-memory stream buffer
	seqNum    atomic.Uint64

	stopCh    chan struct{}
	trimWg    sync.WaitGroup
	closeOnce sync.Once
}

// NewStorage roots the storage at dataDir/dynamodb, creating the directory if needed.
func NewStorage(dataDir string) (*Storage, error) {
	s, err := newStorage(dataDir, os.OpenRoot)
	if err != nil {
		return nil, err
	}
	s.startTrimLoop(time.Hour)
	return s, nil
}

// Close stops the background trim goroutine and releases the os.Root handle.
// Safe to call multiple times.
func (s *Storage) Close() error {
	s.closeOnce.Do(func() { close(s.stopCh) })
	s.trimWg.Wait()
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
	s.statFn = s.root.Stat
	s.streams = make(map[string]*streamBuffer)
	s.seqNum.Store(uint64(time.Now().UnixNano() / 1e6))
	s.stopCh = make(chan struct{})
	s.loadAllStreamBuffers()
	return s, nil
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
	_, err := s.statFn(name + ".table.json")
	return err == nil
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
