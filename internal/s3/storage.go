package s3

import (
	"crypto/md5" // #nosec G501 -- MD5 is required by the S3 ETag specification
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ObjectMetadata holds S3 object metadata stored as a sidecar JSON file.
type ObjectMetadata struct {
	ContentType  string    `json:"contentType"`
	ETag         string    `json:"etag"`
	LastModified time.Time `json:"lastModified"`
	Size         int64     `json:"size"`
}

// Storage is a filesystem-backed S3 storage backend using os.Root for safe path scoping.
type Storage struct {
	root *os.Root
}

// NewStorage creates a Storage rooted at dataDir/s3.
func NewStorage(dataDir string) (*Storage, error) {
	rootPath := filepath.Join(dataDir, "s3")
	if err := os.MkdirAll(rootPath, 0o750); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open storage root: %w", err)
	}
	return &Storage{root: root}, nil
}

// --- Bucket operations ---

func (s *Storage) CreateBucket(bucket string) error {
	return s.root.Mkdir(bucket, 0o750)
}

func (s *Storage) DeleteBucket(bucket string) error {
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
	info, err := s.root.Stat(bucket)
	return err == nil && info.IsDir()
}

func (s *Storage) ListBuckets() ([]BucketInfo, error) {
	entries, err := s.readDir(".")
	if err != nil {
		return nil, err
	}
	buckets := make([]BucketInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		buckets = append(buckets, BucketInfo{
			Name:         e.Name(),
			CreationDate: info.ModTime(),
		})
	}
	return buckets, nil
}

// --- Object operations ---

func (s *Storage) PutObject(
	bucket, key string,
	r io.Reader,
	contentType string,
) (*ObjectMetadata, error) {
	if !s.BucketExists(bucket) {
		return nil, ErrBucketNotFound
	}

	objPath := bucket + "/" + key
	if dir := filepath.Dir(objPath); dir != bucket {
		if err := s.root.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}

	f, err := s.root.OpenFile(objPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	h := md5.New() // #nosec G401 -- MD5 is required by the S3 ETag specification
	size, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		return nil, err
	}

	meta := &ObjectMetadata{
		ContentType:  contentType,
		ETag:         `"` + hex.EncodeToString(h.Sum(nil)) + `"`,
		LastModified: time.Now().UTC(),
		Size:         size,
	}
	if err := s.writeMeta(objPath, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func (s *Storage) GetObject(bucket, key string) (*os.File, *ObjectMetadata, error) {
	if !s.BucketExists(bucket) {
		return nil, nil, ErrBucketNotFound
	}
	objPath := bucket + "/" + key
	meta, err := s.readMeta(objPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrObjectNotFound
		}
		return nil, nil, err
	}
	f, err := s.root.Open(objPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrObjectNotFound
		}
		return nil, nil, err
	}
	return f, meta, nil
}

func (s *Storage) DeleteObject(bucket, key string) error {
	if !s.BucketExists(bucket) {
		return ErrBucketNotFound
	}
	objPath := bucket + "/" + key
	if err := s.root.Remove(objPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrObjectNotFound
		}
		return err
	}
	_ = s.root.Remove(objPath + ".meta.json")
	return nil
}

func (s *Storage) HeadObject(bucket, key string) (*ObjectMetadata, error) {
	if !s.BucketExists(bucket) {
		return nil, ErrBucketNotFound
	}
	meta, err := s.readMeta(bucket + "/" + key)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	return meta, nil
}

func (s *Storage) ListObjects(bucket string) ([]ObjectInfo, error) {
	if !s.BucketExists(bucket) {
		return nil, ErrBucketNotFound
	}
	var objects []ObjectInfo
	if err := s.walkDir(bucket, bucket, &objects); err != nil {
		return nil, err
	}
	return objects, nil
}

func (s *Storage) walkDir(bucket, dir string, objects *[]ObjectInfo) error {
	entries, err := s.readDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		entryPath := dir + "/" + e.Name()
		if e.IsDir() {
			if err := s.walkDir(bucket, entryPath, objects); err != nil {
				return err
			}
			continue
		}
		if filepath.Ext(e.Name()) == ".json" {
			continue
		}
		key := entryPath[len(bucket)+1:]
		meta, err := s.readMeta(entryPath)
		if err != nil {
			continue
		}
		*objects = append(*objects, ObjectInfo{Key: key, Metadata: meta})
	}
	return nil
}

// --- Helpers ---

func (s *Storage) readDir(name string) ([]os.DirEntry, error) {
	f, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return f.ReadDir(-1)
}

func (s *Storage) writeMeta(objPath string, meta *ObjectMetadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	f, err := s.root.OpenFile(objPath+".meta.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(data)
	return err
}

func (s *Storage) readMeta(objPath string) (*ObjectMetadata, error) {
	f, err := s.root.Open(objPath + ".meta.json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var meta ObjectMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// --- Info types ---

type BucketInfo struct {
	Name         string
	CreationDate time.Time
}

type ObjectInfo struct {
	Key      string
	Metadata *ObjectMetadata
}

// --- Errors ---

var (
	ErrBucketNotFound = errors.New("bucket not found")
	ErrBucketNotEmpty = errors.New("bucket not empty")
	ErrObjectNotFound = errors.New("object not found")
)
