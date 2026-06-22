package cognito

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Storage is a filesystem-backed Cognito backend. os.Root scopes all access to
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
}

// NewStorage roots the storage at dataDir/cognito, creating the directory if needed.
func NewStorage(dataDir string) (*Storage, error) {
	return newStorage(dataDir, os.OpenRoot)
}

// Close releases the os.Root handle held by the storage.
func (s *Storage) Close() error {
	return s.root.Close()
}

func newStorage(dataDir string, openRoot func(string) (*os.Root, error)) (*Storage, error) {
	rootPath := filepath.Join(dataDir, "cognito")
	if err := os.MkdirAll(filepath.Join(rootPath, "pools"), 0o750); err != nil {
		return nil, fmt.Errorf("create cognito pools dir: %w", err)
	}
	root, err := openRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open cognito storage root: %w", err)
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
	return s, nil
}

func (s *Storage) writeJSON(path string, v any) (retErr error) {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	f, err := s.openFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); retErr == nil {
			retErr = cerr
		}
	}()
	_, err = f.Write(data)
	return err
}

func readJSON[T any](s *Storage, path string) (T, error) {
	f, err := s.root.Open(path)
	var zero T
	if err != nil {
		return zero, err
	}
	defer func() { _ = f.Close() }()
	data, err := s.readAll(f)
	if err != nil {
		return zero, fmt.Errorf("read %s: %w", path, err)
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return zero, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	return v, nil
}
