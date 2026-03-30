package s3

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStorage(t *testing.T) *Storage {
	t.Helper()
	s, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	return s
}

func newTestStorageWithRoot(t *testing.T) (*Storage, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStorage(dir)
	require.NoError(t, err)
	return s, s.root.Name()
}

// errReader is an io.Reader that always returns an error after reading nothing.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

// --- NewStorage ---

func TestNewStorageErrorNotDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s3"), []byte("x"), 0o600))
	_, err := NewStorage(dir)
	assert.Error(t, err)
}

func TestNewStorageOpenRootError(t *testing.T) {
	orig := osOpenRoot
	t.Cleanup(func() { osOpenRoot = orig })
	osOpenRoot = func(string) (*os.Root, error) {
		return nil, os.ErrPermission
	}

	_, err := NewStorage(t.TempDir())
	assert.Error(t, err)
}

// --- Bucket operations ---

func TestCreateAndDeleteBucket(t *testing.T) {
	s := newTestStorage(t)

	require.NoError(t, s.CreateBucket("my-bucket"))
	assert.True(t, s.BucketExists("my-bucket"))

	require.NoError(t, s.DeleteBucket("my-bucket"))
	assert.False(t, s.BucketExists("my-bucket"))
}

func TestDeleteBucketNotFound(t *testing.T) {
	s := newTestStorage(t)
	assert.ErrorIs(t, s.DeleteBucket("no-such-bucket"), ErrBucketNotFound)
}

func TestDeleteBucketNotEmpty(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateBucket("my-bucket"))
	_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("hello"), "text/plain")
	require.NoError(t, err)

	assert.ErrorIs(t, s.DeleteBucket("my-bucket"), ErrBucketNotEmpty)
}

func TestDeleteBucketReadDirError(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)
	require.NoError(t, s.CreateBucket("my-bucket"))

	require.NoError(t, os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) })

	err := s.DeleteBucket("my-bucket")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrBucketNotFound)
}

func TestListBuckets(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateBucket("bucket-a"))
	require.NoError(t, s.CreateBucket("bucket-b"))

	buckets, err := s.ListBuckets()
	require.NoError(t, err)
	assert.Len(t, buckets, 2)
}

func TestListBucketsReadDirError(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)

	require.NoError(t, os.Chmod(rootPath, 0o000))
	t.Cleanup(func() { _ = os.Chmod(rootPath, 0o750) })

	_, err := s.ListBuckets()
	assert.Error(t, err)
}

func TestListBucketsSkipsNonDir(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)
	require.NoError(t, s.CreateBucket("bucket-a"))
	require.NoError(t, os.WriteFile(filepath.Join(rootPath, "not-a-bucket"), []byte("x"), 0o600))

	buckets, err := s.ListBuckets()
	require.NoError(t, err)
	assert.Len(t, buckets, 1)
}

// --- Object operations ---

func TestPutAndGetObject(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateBucket("my-bucket"))

	meta, err := s.PutObject(
		"my-bucket",
		"hello.txt",
		strings.NewReader("hello world"),
		"text/plain",
	)
	require.NoError(t, err)
	assert.Equal(t, int64(11), meta.Size)
	assert.Equal(t, "text/plain", meta.ContentType)
	assert.NotEmpty(t, meta.ETag)

	f, gotMeta, err := s.GetObject("my-bucket", "hello.txt")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	assert.Equal(t, meta.Size, gotMeta.Size)
}

func TestPutObjectNestedKey(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateBucket("my-bucket"))

	_, err := s.PutObject("my-bucket", "dir/sub/obj.txt", strings.NewReader("data"), "text/plain")
	require.NoError(t, err)

	objects, err := s.ListObjects("my-bucket")
	require.NoError(t, err)
	require.Len(t, objects, 1)
	assert.Equal(t, "dir/sub/obj.txt", objects[0].Key)
}

func TestPutObjectBucketNotFound(t *testing.T) {
	s := newTestStorage(t)
	_, err := s.PutObject("no-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
	assert.ErrorIs(t, err, ErrBucketNotFound)
}

func TestPutObjectOpenFileError(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)
	require.NoError(t, s.CreateBucket("my-bucket"))

	require.NoError(t, os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) })

	_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
	assert.Error(t, err)
}

func TestPutObjectMkdirAllError(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)
	require.NoError(t, s.CreateBucket("my-bucket"))

	require.NoError(t, os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o500))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) })

	_, err := s.PutObject("my-bucket", "nested/obj.txt", strings.NewReader("data"), "text/plain")
	assert.Error(t, err)
}

func TestPutObjectCopyError(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateBucket("my-bucket"))

	_, err := s.PutObject("my-bucket", "obj.txt", errReader{}, "text/plain")
	assert.Error(t, err)
}

func TestPutObjectWriteMetaError(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)
	require.NoError(t, s.CreateBucket("my-bucket"))

	require.NoError(
		t,
		os.MkdirAll(filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"), 0o750),
	)

	_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
	assert.Error(t, err)
}

func TestGetObjectNotFound(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateBucket("my-bucket"))

	_, _, err := s.GetObject("my-bucket", "missing.txt")
	assert.ErrorIs(t, err, ErrObjectNotFound)
}

func TestGetObjectBucketNotFound(t *testing.T) {
	s := newTestStorage(t)
	_, _, err := s.GetObject("no-bucket", "obj.txt")
	assert.ErrorIs(t, err, ErrBucketNotFound)
}

func TestGetObjectCorruptMeta(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)
	require.NoError(t, s.CreateBucket("my-bucket"))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"),
			[]byte("not-json"),
			0o600,
		),
	)

	_, _, err := s.GetObject("my-bucket", "obj.txt")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrObjectNotFound)
}

func TestGetObjectFileUnreadable(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)
	require.NoError(t, s.CreateBucket("my-bucket"))
	_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
	require.NoError(t, err)

	dataPath := filepath.Join(rootPath, "my-bucket", "obj.txt")
	require.NoError(t, os.Chmod(dataPath, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dataPath, 0o600) })

	_, _, err = s.GetObject("my-bucket", "obj.txt")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrObjectNotFound)
}

func TestGetObjectMetaExistsFileGone(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)
	require.NoError(t, s.CreateBucket("my-bucket"))

	meta := ObjectMetadata{ContentType: "text/plain", ETag: `"abc"`, Size: 3}
	data, _ := json.Marshal(meta)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"), data, 0o600),
	)

	_, _, err := s.GetObject("my-bucket", "obj.txt")
	assert.ErrorIs(t, err, ErrObjectNotFound)
}

func TestDeleteObject(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateBucket("my-bucket"))
	_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
	require.NoError(t, err)

	require.NoError(t, s.DeleteObject("my-bucket", "obj.txt"))

	_, _, err = s.GetObject("my-bucket", "obj.txt")
	assert.ErrorIs(t, err, ErrObjectNotFound)
}

func TestDeleteObjectNotFound(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateBucket("my-bucket"))
	assert.ErrorIs(t, s.DeleteObject("my-bucket", "missing.txt"), ErrObjectNotFound)
}

func TestDeleteObjectBucketNotFound(t *testing.T) {
	s := newTestStorage(t)
	assert.ErrorIs(t, s.DeleteObject("no-bucket", "obj.txt"), ErrBucketNotFound)
}

func TestDeleteObjectRemoveError(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)
	require.NoError(t, s.CreateBucket("my-bucket"))

	// Create a non-empty directory at the object path to make Remove fail.
	require.NoError(t, os.MkdirAll(filepath.Join(rootPath, "my-bucket", "dir-obj", "child"), 0o750))

	err := s.DeleteObject("my-bucket", "dir-obj")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrObjectNotFound)
}

func TestHeadObject(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateBucket("my-bucket"))
	_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
	require.NoError(t, err)

	meta, err := s.HeadObject("my-bucket", "obj.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(4), meta.Size)
}

func TestHeadObjectNotFound(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateBucket("my-bucket"))
	_, err := s.HeadObject("my-bucket", "missing.txt")
	assert.ErrorIs(t, err, ErrObjectNotFound)
}

func TestHeadObjectBucketNotFound(t *testing.T) {
	s := newTestStorage(t)
	_, err := s.HeadObject("no-bucket", "obj.txt")
	assert.ErrorIs(t, err, ErrBucketNotFound)
}

func TestHeadObjectCorruptMeta(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)
	require.NoError(t, s.CreateBucket("my-bucket"))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"),
			[]byte("not-json"),
			0o600,
		),
	)

	_, err := s.HeadObject("my-bucket", "obj.txt")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrObjectNotFound)
}

func TestListObjects(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateBucket("my-bucket"))
	_, err := s.PutObject("my-bucket", "a.txt", strings.NewReader("a"), "text/plain")
	require.NoError(t, err)
	_, err = s.PutObject("my-bucket", "b.txt", strings.NewReader("b"), "text/plain")
	require.NoError(t, err)

	objects, err := s.ListObjects("my-bucket")
	require.NoError(t, err)
	assert.Len(t, objects, 2)
}

func TestListObjectsBucketNotFound(t *testing.T) {
	s := newTestStorage(t)
	_, err := s.ListObjects("no-bucket")
	assert.ErrorIs(t, err, ErrBucketNotFound)
}

func TestListObjectsOrphanFile(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)
	require.NoError(t, s.CreateBucket("my-bucket"))
	_, err := s.PutObject("my-bucket", "real.txt", strings.NewReader("data"), "text/plain")
	require.NoError(t, err)

	require.NoError(
		t,
		os.WriteFile(filepath.Join(rootPath, "my-bucket", "orphan.txt"), []byte("data"), 0o600),
	)

	objects, err := s.ListObjects("my-bucket")
	require.NoError(t, err)
	assert.Len(t, objects, 1)
}

func TestListObjectsSubdirError(t *testing.T) {
	s, rootPath := newTestStorageWithRoot(t)
	require.NoError(t, s.CreateBucket("my-bucket"))
	_, err := s.PutObject("my-bucket", "subdir/obj.txt", strings.NewReader("data"), "text/plain")
	require.NoError(t, err)

	subdir := filepath.Join(rootPath, "my-bucket", "subdir")
	require.NoError(t, os.Chmod(subdir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(subdir, 0o750) })

	_, err = s.ListObjects("my-bucket")
	assert.Error(t, err)
}
