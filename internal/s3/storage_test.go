package s3

import (
	"encoding/json"
	"errors"
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

func newTestStorageWithOpenRoot(
	t *testing.T,
	openRoot func(string) (*os.Root, error),
) (*Storage, error) {
	t.Helper()
	return newStorage(t.TempDir(), openRoot)
}

// errReader is an io.Reader that always returns an error after reading nothing.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestNewStorage(t *testing.T) {
	t.Run("returns error when s3 path exists as a file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "s3"), []byte("x"), 0o600))
		_, err := NewStorage(dir)
		assert.Error(t, err)
	})

	t.Run("returns error when OpenRoot fails", func(t *testing.T) {
		_, err := newTestStorageWithOpenRoot(t, func(string) (*os.Root, error) {
			return nil, os.ErrPermission
		})
		assert.Error(t, err)
	})
}

func TestCreateBucket(t *testing.T) {
	t.Run("creates bucket and reports it as existing", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		assert.True(t, s.BucketExists("my-bucket"))
	})
}

func TestDeleteBucket(t *testing.T) {
	t.Run("deletes empty bucket successfully", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		require.NoError(t, s.DeleteBucket("my-bucket"))
		assert.False(t, s.BucketExists("my-bucket"))
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		assert.ErrorIs(t, s.DeleteBucket("no-such-bucket"), ErrBucketNotFound)
	})

	t.Run("returns ErrBucketNotEmpty when bucket has objects", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("hello"), "text/plain")
		require.NoError(t, err)
		assert.ErrorIs(t, s.DeleteBucket("my-bucket"), ErrBucketNotEmpty)
	})

	t.Run("returns error when directory listing fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		require.NoError(t, os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o000))
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) })

		err := s.DeleteBucket("my-bucket")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrBucketNotFound)
	})
}

func TestListBuckets(t *testing.T) {
	t.Run("lists all buckets", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("bucket-a"))
		require.NoError(t, s.CreateBucket("bucket-b"))

		buckets, err := s.ListBuckets()
		require.NoError(t, err)
		assert.Len(t, buckets, 2)
	})

	t.Run("returns error when root is unreadable", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, os.Chmod(rootPath, 0o000))
		t.Cleanup(func() { _ = os.Chmod(rootPath, 0o750) })

		_, err := s.ListBuckets()
		assert.Error(t, err)
	})

	t.Run("skips non-directory entries", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("bucket-a"))
		require.NoError(
			t,
			os.WriteFile(filepath.Join(rootPath, "not-a-bucket"), []byte("x"), 0o600),
		)

		buckets, err := s.ListBuckets()
		require.NoError(t, err)
		assert.Len(t, buckets, 1)
	})
}

func TestPutObject(t *testing.T) {
	t.Run("stores object and returns metadata", func(t *testing.T) {
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
	})

	t.Run("stores object with nested key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket"))

		_, err := s.PutObject(
			"my-bucket",
			"dir/sub/obj.txt",
			strings.NewReader("data"),
			"text/plain",
		)
		require.NoError(t, err)

		objects, err := s.ListObjects("my-bucket")
		require.NoError(t, err)
		require.Len(t, objects, 1)
		assert.Equal(t, "dir/sub/obj.txt", objects[0].Key)
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.PutObject("no-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("returns error when file cannot be opened", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		require.NoError(t, os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o000))
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) })

		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		assert.Error(t, err)
	})

	t.Run("returns error when nested directory cannot be created", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		require.NoError(t, os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o500))
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) })

		_, err := s.PutObject(
			"my-bucket",
			"nested/obj.txt",
			strings.NewReader("data"),
			"text/plain",
		)
		assert.Error(t, err)
	})

	t.Run("returns error when reader fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket"))

		_, err := s.PutObject("my-bucket", "obj.txt", errReader{}, "text/plain")
		assert.Error(t, err)
	})

	t.Run("cleans up object file when meta write fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		require.NoError(
			t,
			os.MkdirAll(filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"), 0o750),
		)

		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		assert.Error(t, err)
		assert.NoFileExists(t, filepath.Join(rootPath, "my-bucket", "obj.txt"))
	})

	t.Run("logs warning when cleanup remove also fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		require.NoError(
			t,
			os.MkdirAll(filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"), 0o750),
		)

		s.removeFile = func(_ string) error {
			return errors.New("simulated remove failure")
		}

		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		assert.Error(t, err)
	})
}

func TestGetObject(t *testing.T) {
	t.Run("returns file and metadata for existing object", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		meta, err := s.PutObject(
			"my-bucket",
			"hello.txt",
			strings.NewReader("hello world"),
			"text/plain",
		)
		require.NoError(t, err)

		f, gotMeta, err := s.GetObject("my-bucket", "hello.txt")
		require.NoError(t, err)
		defer f.Close()
		assert.Equal(t, meta.Size, gotMeta.Size)
	})

	t.Run("returns ErrObjectNotFound when object does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket"))

		_, _, err := s.GetObject("my-bucket", "missing.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		_, _, err := s.GetObject("no-bucket", "obj.txt")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("returns error when metadata is corrupt", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		require.NoError(t, os.WriteFile(
			filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"),
			[]byte("not-json"),
			0o600,
		))

		_, _, err := s.GetObject("my-bucket", "obj.txt")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns error when data file is unreadable", func(t *testing.T) {
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
	})

	t.Run("returns ErrObjectNotFound when meta exists but data file is gone", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket"))

		meta := ObjectMetadata{ContentType: "text/plain", ETag: `"abc"`, Size: 3}
		data, _ := json.Marshal(meta)
		require.NoError(t, os.WriteFile(
			filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"),
			data,
			0o600,
		))

		_, _, err := s.GetObject("my-bucket", "obj.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})
}

func TestDeleteObject(t *testing.T) {
	t.Run("deletes object and metadata successfully", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		require.NoError(t, err)

		require.NoError(t, s.DeleteObject("my-bucket", "obj.txt"))

		_, _, err = s.GetObject("my-bucket", "obj.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns ErrObjectNotFound when object does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		assert.ErrorIs(t, s.DeleteObject("my-bucket", "missing.txt"), ErrObjectNotFound)
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		assert.ErrorIs(t, s.DeleteObject("no-bucket", "obj.txt"), ErrBucketNotFound)
	})

	t.Run("returns error when object removal fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		require.NoError(
			t,
			os.MkdirAll(filepath.Join(rootPath, "my-bucket", "dir-obj", "child"), 0o750),
		)

		err := s.DeleteObject("my-bucket", "dir-obj")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("logs warning when metadata removal fails but still succeeds", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		require.NoError(t, err)

		require.NoError(t, os.Remove(filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json")))
		require.NoError(t, os.MkdirAll(
			filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json", "child"),
			0o750,
		))

		assert.NoError(t, s.DeleteObject("my-bucket", "obj.txt"))
	})
}

func TestHeadObject(t *testing.T) {
	t.Run("returns metadata for existing object", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		require.NoError(t, err)

		meta, err := s.HeadObject("my-bucket", "obj.txt")
		require.NoError(t, err)
		assert.Equal(t, int64(4), meta.Size)
	})

	t.Run("returns ErrObjectNotFound when object does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		_, err := s.HeadObject("my-bucket", "missing.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.HeadObject("no-bucket", "obj.txt")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("returns error when metadata is corrupt", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		require.NoError(t, os.WriteFile(
			filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"),
			[]byte("not-json"),
			0o600,
		))

		_, err := s.HeadObject("my-bucket", "obj.txt")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrObjectNotFound)
	})
}

func TestListObjects(t *testing.T) {
	t.Run("lists all objects in bucket", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		_, err := s.PutObject("my-bucket", "a.txt", strings.NewReader("a"), "text/plain")
		require.NoError(t, err)
		_, err = s.PutObject("my-bucket", "b.txt", strings.NewReader("b"), "text/plain")
		require.NoError(t, err)

		objects, err := s.ListObjects("my-bucket")
		require.NoError(t, err)
		assert.Len(t, objects, 2)
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.ListObjects("no-bucket")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("skips orphan files without metadata", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		_, err := s.PutObject("my-bucket", "real.txt", strings.NewReader("data"), "text/plain")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			filepath.Join(rootPath, "my-bucket", "orphan.txt"),
			[]byte("data"),
			0o600,
		))

		objects, err := s.ListObjects("my-bucket")
		require.NoError(t, err)
		assert.Len(t, objects, 1)
	})

	t.Run("returns error when subdirectory listing fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket"))
		_, err := s.PutObject(
			"my-bucket",
			"subdir/obj.txt",
			strings.NewReader("data"),
			"text/plain",
		)
		require.NoError(t, err)

		subdir := filepath.Join(rootPath, "my-bucket", "subdir")
		require.NoError(t, os.Chmod(subdir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(subdir, 0o750) })

		_, err = s.ListObjects("my-bucket")
		assert.Error(t, err)
	})
}
