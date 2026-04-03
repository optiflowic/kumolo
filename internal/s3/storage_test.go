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
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newTestStorageWithRoot(t *testing.T) (*Storage, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStorage(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
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

// badCloseWriter wraps an io.WriteCloser and returns an error on Close.
type badCloseWriter struct {
	io.WriteCloser
}

func (b badCloseWriter) Close() error {
	_ = b.WriteCloser.Close()
	return errors.New("simulated close failure")
}

func TestClose(t *testing.T) {
	t.Run("closes storage without error", func(t *testing.T) {
		s, err := NewStorage(t.TempDir())
		require.NoError(t, err)
		assert.NoError(t, s.Close())
	})
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
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		assert.True(t, s.BucketExists("my-bucket"))
	})

	t.Run("persists region", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "ap-northeast-1"))
		region, err := s.GetBucketRegion("my-bucket")
		require.NoError(t, err)
		assert.Equal(t, "ap-northeast-1", region)
	})

	t.Run("returns error and does not create bucket when meta write fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		// Place a directory where the metadata file should be written to force a failure.
		require.NoError(t, os.MkdirAll(filepath.Join(rootPath, "my-bucket.bucket.json"), 0o750))

		err := s.CreateBucket("my-bucket", "us-west-2")
		assert.Error(t, err)
		assert.False(t, s.BucketExists("my-bucket"))
	})

	t.Run(
		"logs warning when rollback remove also fails after meta write failure",
		func(t *testing.T) {
			s, rootPath := newTestStorageWithRoot(t)
			// Make openFile create a child inside the bucket dir before returning an error, so
			// that the rollback Remove also fails (non-empty directory).
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				_ = os.MkdirAll(filepath.Join(rootPath, "my-bucket", "child"), 0o750)
				return nil, errors.New("simulated write failure")
			}

			err := s.CreateBucket("my-bucket", "us-west-2")
			assert.Error(t, err)
		},
	)

	t.Run("returns close error when bucket meta file close fails", func(t *testing.T) {
		s := newTestStorage(t)
		realOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			wc, err := realOpenFile(name, flag, perm)
			if err != nil {
				return nil, err
			}
			return badCloseWriter{wc}, nil
		}

		err := s.CreateBucket("my-bucket", "ap-northeast-1")
		assert.Error(t, err)
	})
}

func TestGetBucketRegion(t *testing.T) {
	t.Run("returns region the bucket was created with", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "eu-west-1"))
		region, err := s.GetBucketRegion("my-bucket")
		require.NoError(t, err)
		assert.Equal(t, "eu-west-1", region)
	})

	t.Run("returns empty string for bucket created without region", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		region, err := s.GetBucketRegion("my-bucket")
		require.NoError(t, err)
		assert.Equal(t, "", region)
	})

	t.Run("returns ErrBucketNotFound for nonexistent bucket", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.GetBucketRegion("no-such-bucket")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("returns empty string when meta file does not exist", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		require.NoError(t, os.Remove(filepath.Join(rootPath, "my-bucket.bucket.json")))

		region, err := s.GetBucketRegion("my-bucket")
		require.NoError(t, err)
		assert.Equal(t, "", region)
	})

	t.Run("returns error when metadata is corrupt", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		require.NoError(t, os.WriteFile(
			filepath.Join(rootPath, "my-bucket.bucket.json"),
			[]byte("not-json"),
			0o600,
		))

		_, err := s.GetBucketRegion("my-bucket")
		assert.Error(t, err)
	})

	t.Run("returns error when metadata read fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "us-east-2"))

		s.readAll = func(io.Reader) ([]byte, error) {
			return nil, errors.New("simulated read failure")
		}

		_, err := s.GetBucketRegion("my-bucket")
		assert.Error(t, err)
	})
}

func TestDeleteBucket(t *testing.T) {
	t.Run("deletes empty bucket successfully", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		require.NoError(t, s.DeleteBucket("my-bucket"))
		assert.False(t, s.BucketExists("my-bucket"))
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		assert.ErrorIs(t, s.DeleteBucket("no-such-bucket"), ErrBucketNotFound)
	})

	t.Run("returns ErrBucketNotEmpty when bucket has objects", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("hello"), "text/plain")
		require.NoError(t, err)
		assert.ErrorIs(t, s.DeleteBucket("my-bucket"), ErrBucketNotEmpty)
	})

	t.Run("removes bucket metadata on delete", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "ap-northeast-1"))
		require.NoError(t, s.DeleteBucket("my-bucket"))
		assert.NoFileExists(t, filepath.Join(rootPath, "my-bucket.bucket.json"))
	})

	t.Run("logs warning when metadata removal fails but still deletes bucket", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		// Replace the .bucket.json file with a non-empty directory so Remove fails.
		metaPath := filepath.Join(rootPath, "my-bucket.bucket.json")
		require.NoError(t, os.Remove(metaPath))
		require.NoError(t, os.MkdirAll(filepath.Join(metaPath, "child"), 0o750))

		require.NoError(t, s.DeleteBucket("my-bucket"))
		assert.False(t, s.BucketExists("my-bucket"))
	})

	t.Run("returns error when directory listing fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		require.NoError(t, os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o000))
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) })

		err := s.DeleteBucket("my-bucket")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrBucketNotFound)
	})
}

func TestListBuckets(t *testing.T) {
	t.Run("lists all buckets in lexicographic order", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("bucket-c", ""))
		require.NoError(t, s.CreateBucket("bucket-a", ""))
		require.NoError(t, s.CreateBucket("bucket-b", ""))

		buckets, err := s.ListBuckets()
		require.NoError(t, err)
		require.Len(t, buckets, 3)
		assert.Equal(t, "bucket-a", buckets[0].Name)
		assert.Equal(t, "bucket-b", buckets[1].Name)
		assert.Equal(t, "bucket-c", buckets[2].Name)
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
		require.NoError(t, s.CreateBucket("bucket-a", ""))
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
		require.NoError(t, s.CreateBucket("my-bucket", ""))

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
		require.NoError(t, s.CreateBucket("my-bucket", ""))

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
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		require.NoError(t, os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o000))
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) })

		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		assert.Error(t, err)
	})

	t.Run("returns error when nested directory cannot be created", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
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
		require.NoError(t, s.CreateBucket("my-bucket", ""))

		_, err := s.PutObject("my-bucket", "obj.txt", errReader{}, "text/plain")
		assert.Error(t, err)
	})

	t.Run("cleans up object file when meta write fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
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
		require.NoError(t, s.CreateBucket("my-bucket", ""))
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

	t.Run(
		"returns close error when meta file close fails after successful write",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("my-bucket", ""))

			// Wrap only the second openFile call (the meta file); the object file must
			// close successfully so retErr is nil when the deferred meta-file Close fires.
			callCount := 0
			realOpenFile := s.openFile
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				callCount++
				wc, err := realOpenFile(name, flag, perm)
				if err != nil {
					return nil, err
				}
				if callCount == 2 {
					return badCloseWriter{wc}, nil
				}
				return wc, nil
			}

			_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
			assert.Error(t, err)
		},
	)

	t.Run(
		"returns close error when object file close fails after successful write",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("my-bucket", ""))

			// Wrap only the first openFile call (the object file); the meta file must
			// close successfully so writeMeta returns nil, leaving retErr==nil when the
			// deferred object-file Close fires.
			callCount := 0
			realOpenFile := s.openFile
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				callCount++
				wc, err := realOpenFile(name, flag, perm)
				if err != nil {
					return nil, err
				}
				if callCount == 1 {
					return badCloseWriter{wc}, nil
				}
				return wc, nil
			}

			_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
			assert.Error(t, err)
		},
	)
}

func TestGetObject(t *testing.T) {
	t.Run("returns file and metadata for existing object", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		meta, err := s.PutObject(
			"my-bucket",
			"hello.txt",
			strings.NewReader("hello world"),
			"text/plain",
		)
		require.NoError(t, err)

		f, gotMeta, err := s.GetObject("my-bucket", "hello.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		assert.Equal(t, meta.Size, gotMeta.Size)
	})

	t.Run("returns ErrObjectNotFound when object does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))

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
		require.NoError(t, s.CreateBucket("my-bucket", ""))
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
		require.NoError(t, s.CreateBucket("my-bucket", ""))
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
		require.NoError(t, s.CreateBucket("my-bucket", ""))

		meta := ObjectMetadata{ContentType: "text/plain", ETag: `"abc"`, Size: 3}
		data, err := json.Marshal(meta)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"),
			data,
			0o600,
		))

		_, _, err = s.GetObject("my-bucket", "obj.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})
}

func TestDeleteObject(t *testing.T) {
	t.Run("deletes object and metadata successfully", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		require.NoError(t, err)

		require.NoError(t, s.DeleteObject("my-bucket", "obj.txt"))

		_, _, err = s.GetObject("my-bucket", "obj.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns ErrObjectNotFound when object does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		assert.ErrorIs(t, s.DeleteObject("my-bucket", "missing.txt"), ErrObjectNotFound)
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		assert.ErrorIs(t, s.DeleteObject("no-bucket", "obj.txt"), ErrBucketNotFound)
	})

	t.Run("returns error when object removal fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
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
		require.NoError(t, s.CreateBucket("my-bucket", ""))
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
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		require.NoError(t, err)

		meta, err := s.HeadObject("my-bucket", "obj.txt")
		require.NoError(t, err)
		assert.Equal(t, int64(4), meta.Size)
	})

	t.Run("returns ErrObjectNotFound when object does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
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
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		require.NoError(t, os.WriteFile(
			filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"),
			[]byte("not-json"),
			0o600,
		))

		_, err := s.HeadObject("my-bucket", "obj.txt")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns error when metadata read fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		require.NoError(t, err)

		s.readAll = func(io.Reader) ([]byte, error) {
			return nil, errors.New("simulated read failure")
		}

		_, err = s.HeadObject("my-bucket", "obj.txt")
		assert.Error(t, err)
	})
}

func TestListObjects(t *testing.T) {
	t.Run("lists all objects in lexicographic order", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		_, err := s.PutObject("my-bucket", "c.txt", strings.NewReader("c"), "text/plain")
		require.NoError(t, err)
		_, err = s.PutObject("my-bucket", "a.txt", strings.NewReader("a"), "text/plain")
		require.NoError(t, err)
		_, err = s.PutObject("my-bucket", "b.txt", strings.NewReader("b"), "text/plain")
		require.NoError(t, err)

		objects, err := s.ListObjects("my-bucket")
		require.NoError(t, err)
		require.Len(t, objects, 3)
		assert.Equal(t, "a.txt", objects[0].Key)
		assert.Equal(t, "b.txt", objects[1].Key)
		assert.Equal(t, "c.txt", objects[2].Key)
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.ListObjects("no-bucket")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("skips orphan files without metadata", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
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
		require.NoError(t, s.CreateBucket("my-bucket", ""))
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

	t.Run("includes .json objects in listing", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		_, err := s.PutObject("my-bucket", "data.json", strings.NewReader("{}"), "application/json")
		require.NoError(t, err)

		objects, err := s.ListObjects("my-bucket")
		require.NoError(t, err)
		require.Len(t, objects, 1)
		assert.Equal(t, "data.json", objects[0].Key)
	})
}
