package s3

import (
	"cmp"
	"crypto/md5" // #nosec G501 -- MD5 is required by the S3 ETag specification
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// ObjectMetadata is stored as a sidecar .meta.json file alongside each object.
type ObjectMetadata struct {
	ContentType  string    `json:"contentType"`
	ETag         string    `json:"etag"`
	LastModified time.Time `json:"lastModified"`
	Size         int64     `json:"size"`
}

// Storage is a filesystem-backed S3 backend. os.Root scopes all access to the
// storage root, preventing path traversal attacks.
type Storage struct {
	mu         sync.RWMutex
	root       *os.Root
	removeFile func(name string) error
	openFile   func(name string, flag int, perm os.FileMode) (io.WriteCloser, error)
	readAll    func(r io.Reader) ([]byte, error)
}

// NewStorage roots the storage at dataDir/s3, creating the directory if needed.
func NewStorage(dataDir string) (*Storage, error) {
	return newStorage(dataDir, os.OpenRoot)
}

// Close releases the os.Root handle held by the storage.
func (s *Storage) Close() error {
	return s.root.Close()
}

func newStorage(dataDir string, openRoot func(string) (*os.Root, error)) (*Storage, error) {
	rootPath := filepath.Join(dataDir, "s3")
	if err := os.MkdirAll(rootPath, 0o750); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	root, err := openRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open storage root: %w", err)
	}
	s := &Storage{root: root}
	s.removeFile = s.root.Remove
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		return s.root.OpenFile(name, flag, perm)
	}
	s.readAll = io.ReadAll
	return s, nil
}

func (s *Storage) CreateBucket(bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Mkdir(bucket, 0o750)
}

func (s *Storage) DeleteBucket(bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readDir(bucket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrBucketNotFound
		}
		return err
	}
	if len(entries) > 0 {
		return ErrBucketNotEmpty
	}
	return s.root.Remove(bucket)
}

func (s *Storage) BucketExists(bucket string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bucketExistsLocked(bucket)
}

func (s *Storage) bucketExistsLocked(bucket string) bool {
	info, err := s.root.Stat(bucket)
	return err == nil && info.IsDir()
}

func (s *Storage) ListBuckets() ([]BucketInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := s.readDir(".")
	if err != nil {
		return nil, err
	}
	buckets := make([]BucketInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var creationDate time.Time
		if info, err := e.Info(); err == nil {
			creationDate = info.ModTime()
		}
		buckets = append(buckets, BucketInfo{
			Name:         e.Name(),
			CreationDate: creationDate,
		})
	}
	slices.SortFunc(buckets, func(a, b BucketInfo) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return buckets, nil
}

func (s *Storage) PutObject(
	bucket, key string,
	r io.Reader,
	contentType string,
) (ObjectMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ObjectMetadata{}, ErrBucketNotFound
	}

	objPath := filepath.Join(bucket, key)
	if dir := filepath.Dir(objPath); dir != bucket {
		if err := s.root.MkdirAll(dir, 0o750); err != nil {
			return ObjectMetadata{}, err
		}
	}

	return s.writeObject(objPath, r, contentType)
}

func (s *Storage) writeObject(
	objPath string,
	r io.Reader,
	contentType string,
) (retMeta ObjectMetadata, retErr error) {
	f, err := s.openFile(objPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return ObjectMetadata{}, err
	}
	defer func() {
		if err := f.Close(); err != nil && retErr == nil {
			retErr = err
		}
	}()

	h := md5.New() // #nosec G401 -- MD5 is required by the S3 ETag specification
	size, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		return ObjectMetadata{}, err
	}

	meta := ObjectMetadata{
		ContentType:  contentType,
		ETag:         `"` + hex.EncodeToString(h.Sum(nil)) + `"`,
		LastModified: time.Now().UTC(),
		Size:         size,
	}
	if err := s.writeMeta(objPath, meta); err != nil {
		if removeErr := s.removeFile(objPath); removeErr != nil &&
			!errors.Is(removeErr, os.ErrNotExist) {
			slog.Warn("failed to clean up object after meta write error", "err", removeErr)
		}
		return ObjectMetadata{}, err
	}
	return meta, nil
}

func (s *Storage) GetObject(bucket, key string) (*os.File, ObjectMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return nil, ObjectMetadata{}, ErrBucketNotFound
	}
	objPath := filepath.Join(bucket, key)
	meta, err := s.readMeta(objPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ObjectMetadata{}, ErrObjectNotFound
		}
		return nil, ObjectMetadata{}, err
	}
	f, err := s.root.Open(objPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ObjectMetadata{}, ErrObjectNotFound
		}
		return nil, ObjectMetadata{}, err
	}
	return f, meta, nil
}

func (s *Storage) DeleteObject(bucket, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	objPath := filepath.Join(bucket, key)
	if err := s.root.Remove(objPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrObjectNotFound
		}
		return err
	}
	if err := s.root.Remove(objPath + ".meta.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn( // #nosec G706 -- objPath is an internal filesystem path derived from bucket/key, not direct user input
			"failed to remove metadata",
			"path",
			objPath,
			"err",
			err,
		)
	}
	return nil
}

func (s *Storage) HeadObject(bucket, key string) (ObjectMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return ObjectMetadata{}, ErrBucketNotFound
	}
	meta, err := s.readMeta(filepath.Join(bucket, key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ObjectMetadata{}, ErrObjectNotFound
		}
		return ObjectMetadata{}, err
	}
	return meta, nil
}

func (s *Storage) ListObjects(bucket string) ([]ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return nil, ErrBucketNotFound
	}
	var objects []ObjectInfo
	if err := s.walkDir(bucket, bucket, &objects); err != nil {
		return nil, err
	}
	slices.SortFunc(objects, func(a, b ObjectInfo) int {
		return cmp.Compare(a.Key, b.Key)
	})
	return objects, nil
}

func (s *Storage) walkDir(bucket, dir string, objects *[]ObjectInfo) error {
	entries, err := s.readDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		entryPath := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if err := s.walkDir(bucket, entryPath, objects); err != nil {
				return err
			}
			continue
		}
		if strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		key, _ := filepath.Rel(bucket, entryPath)
		meta, err := s.readMeta(entryPath)
		if err != nil {
			slog.Warn( // #nosec G706 -- entryPath is an internal filesystem path, not direct user input
				"skipping object with unreadable metadata",
				"path",
				entryPath,
				"err",
				err,
			)
			continue
		}
		*objects = append(*objects, ObjectInfo{Key: key, Metadata: meta})
	}
	return nil
}

func (s *Storage) readDir(name string) ([]os.DirEntry, error) {
	f, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return f.ReadDir(-1)
}

func (s *Storage) writeMeta(objPath string, meta ObjectMetadata) (retErr error) {
	// json.Marshal never fails for ObjectMetadata: all fields are JSON-serializable
	// (string, int64, and time.Time which implements json.Marshaler without error).
	data, _ := json.Marshal(meta)
	f, err := s.openFile(objPath+".meta.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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

func (s *Storage) readMeta(objPath string) (ObjectMetadata, error) {
	f, err := s.root.Open(objPath + ".meta.json")
	if err != nil {
		return ObjectMetadata{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := s.readAll(f)
	if err != nil {
		return ObjectMetadata{}, err
	}
	var meta ObjectMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return ObjectMetadata{}, err
	}
	return meta, nil
}

type BucketInfo struct {
	Name         string
	CreationDate time.Time
}

type ObjectInfo struct {
	Key      string
	Metadata ObjectMetadata
}
