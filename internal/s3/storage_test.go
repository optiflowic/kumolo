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

// badWriteWriter wraps an io.WriteCloser and returns an error on Write.
type badWriteWriter struct {
	io.WriteCloser
}

func (b badWriteWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("simulated write failure")
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
		t.Cleanup(
			func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) },
		)

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
		t.Cleanup(
			func() { _ = os.Chmod(rootPath, 0o750) },
		)

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
		t.Cleanup(
			func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) },
		)

		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		assert.Error(t, err)
	})

	t.Run("returns error when nested directory cannot be created", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		require.NoError(
			t,
			os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o500),
		)
		t.Cleanup(
			func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) },
		)

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

func TestCopyObject(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("src-bucket", ""))
		require.NoError(t, s.CreateBucket("dst-bucket", ""))
		_, err := s.PutObject("src-bucket", "orig.txt", strings.NewReader("hello"), "text/plain")
		require.NoError(t, err)
		return s, rootPath
	}

	t.Run("copies object to different key in same bucket", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CopyObject("src-bucket", "orig.txt", "src-bucket", "copy.txt")
		require.NoError(t, err)
		f, _, err := s.GetObject("src-bucket", "copy.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(data))
	})

	t.Run("copies object to different bucket", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CopyObject("src-bucket", "orig.txt", "dst-bucket", "copy.txt")
		require.NoError(t, err)
		f, _, err := s.GetObject("dst-bucket", "copy.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(data))
	})

	t.Run("copy to same key preserves content and ETag", func(t *testing.T) {
		s, _ := setup(t)
		origMeta, err := s.HeadObject("src-bucket", "orig.txt")
		require.NoError(t, err)
		meta, err := s.CopyObject("src-bucket", "orig.txt", "src-bucket", "orig.txt")
		require.NoError(t, err)
		assert.Equal(t, origMeta.ETag, meta.ETag)
		f, _, err := s.GetObject("src-bucket", "orig.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(data))
	})

	t.Run("returns error when same-key copy meta write fails", func(t *testing.T) {
		s, rootPath := setup(t)
		metaPath := filepath.Join(rootPath, "src-bucket", "orig.txt.meta.json")
		// 0o444: readable (so readMeta succeeds) but not writable (so writeMeta fails).
		require.NoError(
			t,
			os.Chmod(metaPath, 0o444),
		)
		t.Cleanup(func() { _ = os.Chmod(metaPath, 0o600) })

		_, err := s.CopyObject("src-bucket", "orig.txt", "src-bucket", "orig.txt")
		assert.Error(t, err)
	})

	t.Run("copied object gets new LastModified", func(t *testing.T) {
		s, _ := setup(t)
		srcMeta, err := s.HeadObject("src-bucket", "orig.txt")
		require.NoError(t, err)
		dstMeta, err := s.CopyObject("src-bucket", "orig.txt", "dst-bucket", "copy.txt")
		require.NoError(t, err)
		assert.True(t, !dstMeta.LastModified.Before(srcMeta.LastModified))
	})

	t.Run("copies object with nested destination key", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CopyObject("src-bucket", "orig.txt", "dst-bucket", "path/to/copy.txt")
		require.NoError(t, err)
		_, _, err = s.GetObject("dst-bucket", "path/to/copy.txt")
		assert.NoError(t, err)
	})

	t.Run("returns error when source metadata is corrupt", func(t *testing.T) {
		s, rootPath := setup(t)
		require.NoError(t, os.WriteFile(
			filepath.Join(rootPath, "src-bucket", "orig.txt.meta.json"),
			[]byte("not-json"),
			0o600,
		))

		_, err := s.CopyObject("src-bucket", "orig.txt", "dst-bucket", "copy.txt")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns ErrBucketNotFound when source bucket does not exist", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CopyObject("no-bucket", "orig.txt", "dst-bucket", "copy.txt")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("returns ErrObjectNotFound when source key does not exist", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CopyObject("src-bucket", "missing.txt", "dst-bucket", "copy.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns ErrBucketNotFound when destination bucket does not exist", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CopyObject("src-bucket", "orig.txt", "no-bucket", "copy.txt")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("returns error when destination directory cannot be created", func(t *testing.T) {
		s, rootPath := setup(t)
		require.NoError(
			t,
			os.Chmod(filepath.Join(rootPath, "dst-bucket"), 0o500),
		)
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(rootPath, "dst-bucket"), 0o750) })

		_, err := s.CopyObject("src-bucket", "orig.txt", "dst-bucket", "nested/copy.txt")
		assert.Error(t, err)
	})

	t.Run("returns ErrObjectNotFound when source data file is missing", func(t *testing.T) {
		s, rootPath := setup(t)
		require.NoError(t, os.Remove(filepath.Join(rootPath, "src-bucket", "orig.txt")))

		_, err := s.CopyObject("src-bucket", "orig.txt", "dst-bucket", "copy.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns error when source data file is unreadable", func(t *testing.T) {
		s, rootPath := setup(t)
		dataPath := filepath.Join(rootPath, "src-bucket", "orig.txt")
		require.NoError(t, os.Chmod(dataPath, 0o000))
		t.Cleanup(func() { _ = os.Chmod(dataPath, 0o600) })

		_, err := s.CopyObject("src-bucket", "orig.txt", "dst-bucket", "copy.txt")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrObjectNotFound)
	})
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

	t.Run("deletes tags file when object is deleted", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		require.NoError(t, err)
		require.NoError(
			t,
			s.PutObjectTagging("my-bucket", "obj.txt", []Tag{{Key: "k", Value: "v"}}),
		)

		require.NoError(t, s.DeleteObject("my-bucket", "obj.txt"))

		_, statErr := os.Stat(filepath.Join(rootPath, "my-bucket", "obj.txt.tags.json"))
		assert.True(t, os.IsNotExist(statErr), "tags file should be removed with the object")
	})

	t.Run("DeleteBucket succeeds after deleting tagged object", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		require.NoError(t, err)
		require.NoError(
			t,
			s.PutObjectTagging("my-bucket", "obj.txt", []Tag{{Key: "k", Value: "v"}}),
		)
		require.NoError(t, s.DeleteObject("my-bucket", "obj.txt"))

		assert.NoError(t, s.DeleteBucket("my-bucket"))
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

	t.Run("does not list tags sidecar files as objects", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		_, err := s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")
		require.NoError(t, err)
		require.NoError(
			t,
			s.PutObjectTagging("my-bucket", "obj.txt", []Tag{{Key: "k", Value: "v"}}),
		)

		objects, err := s.ListObjects("my-bucket")
		require.NoError(t, err)
		require.Len(t, objects, 1)
		assert.Equal(t, "obj.txt", objects[0].Key)
	})
}

func TestMultipartUpload(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", ""))
		return s, rootPath
	}

	t.Run("full lifecycle: create, upload parts, complete", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "big.txt", "text/plain")
		require.NoError(t, err)
		assert.NotEmpty(t, uploadID)

		etag1, err := s.UploadPart(uploadID, 1, strings.NewReader("hello "))
		require.NoError(t, err)
		assert.NotEmpty(t, etag1)

		etag2, err := s.UploadPart(uploadID, 2, strings.NewReader("world"))
		require.NoError(t, err)
		assert.NotEmpty(t, etag2)

		meta, err := s.CompleteMultipartUpload(uploadID, []CompletePart{
			{PartNumber: 1, ETag: etag1},
			{PartNumber: 2, ETag: etag2},
		})
		require.NoError(t, err)
		assert.Contains(t, meta.ETag, "-2")

		f, _, err := s.GetObject("my-bucket", "big.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(data))
	})

	t.Run("abort cleans up temp files", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "big.txt", "text/plain")
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)

		require.NoError(t, s.AbortMultipartUpload(uploadID))

		_, err = os.Stat(filepath.Join(rootPath, mpuDir, uploadID))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("complete removes temp files", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "big.txt", "text/plain")
		require.NoError(t, err)
		etag, err := s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: etag}})
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(rootPath, mpuDir, uploadID))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("create returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CreateMultipartUpload("no-bucket", "key", "text/plain")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("upload part returns ErrUploadNotFound for unknown uploadId", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.UploadPart("nonexistent-id", 1, strings.NewReader("data"))
		assert.ErrorIs(t, err, ErrUploadNotFound)
	})

	t.Run("complete returns ErrUploadNotFound for unknown uploadId", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CompleteMultipartUpload(
			"nonexistent-id",
			[]CompletePart{{PartNumber: 1, ETag: `"abc"`}},
		)
		assert.ErrorIs(t, err, ErrUploadNotFound)
	})

	t.Run("complete returns ErrInvalidPart for empty parts list", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "big.txt", "text/plain")
		require.NoError(t, err)
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{})
		assert.ErrorIs(t, err, ErrInvalidPart)
	})

	t.Run("complete returns ErrInvalidPartOrder for non-ascending parts", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "big.txt", "text/plain")
		require.NoError(t, err)
		etag1, err := s.UploadPart(uploadID, 1, strings.NewReader("a"))
		require.NoError(t, err)
		etag2, err := s.UploadPart(uploadID, 2, strings.NewReader("b"))
		require.NoError(t, err)
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{
			{PartNumber: 2, ETag: etag2},
			{PartNumber: 1, ETag: etag1},
		})
		assert.ErrorIs(t, err, ErrInvalidPartOrder)
	})

	t.Run("complete returns ErrInvalidPart for wrong ETag", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "big.txt", "text/plain")
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{
			{PartNumber: 1, ETag: `"wrongetag"`},
		})
		assert.ErrorIs(t, err, ErrInvalidPart)
	})

	t.Run("complete returns ErrInvalidPart for missing part on disk", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "big.txt", "text/plain")
		require.NoError(t, err)
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{
			{PartNumber: 1, ETag: `"abc"`},
		})
		assert.ErrorIs(t, err, ErrInvalidPart)
	})

	t.Run("abort returns ErrUploadNotFound for unknown uploadId", func(t *testing.T) {
		s, _ := setup(t)
		err := s.AbortMultipartUpload("nonexistent-id")
		assert.ErrorIs(t, err, ErrUploadNotFound)
	})

	t.Run("ListBuckets does not expose .mpu directory", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)

		buckets, err := s.ListBuckets()
		require.NoError(t, err)
		for _, b := range buckets {
			assert.NotEqual(t, ".mpu", b.Name)
		}
	})

	t.Run("ListObjects does not expose part files", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)

		objects, err := s.ListObjects("my-bucket")
		require.NoError(t, err)
		assert.Empty(t, objects)
	})

	t.Run(
		"complete returns ErrBucketNotFound when bucket deleted before completion",
		func(t *testing.T) {
			s, _ := setup(t)
			uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
			require.NoError(t, err)
			etag, err := s.UploadPart(uploadID, 1, strings.NewReader("data"))
			require.NoError(t, err)

			// Delete bucket between upload and completion.
			require.NoError(t, s.DeleteBucket("my-bucket"))

			_, err = s.CompleteMultipartUpload(
				uploadID,
				[]CompletePart{{PartNumber: 1, ETag: etag}},
			)
			assert.ErrorIs(t, err, ErrBucketNotFound)
		},
	)

	t.Run("complete with nested key creates intermediate directories", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "path/to/big.txt", "text/plain")
		require.NoError(t, err)
		etag, err := s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: etag}})
		require.NoError(t, err)
		_, _, err = s.GetObject("my-bucket", "path/to/big.txt")
		assert.NoError(t, err)
	})

	t.Run("upload part returns error when part file cannot be written", func(t *testing.T) {
		s, _ := setup(t)
		origOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".part") && !strings.HasSuffix(name, ".meta.json") {
				return nil, errors.New("disk full")
			}
			return origOpenFile(name, flag, perm)
		}
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		assert.Error(t, err)
	})

	t.Run("upload part returns error when meta file cannot be written", func(t *testing.T) {
		s, _ := setup(t)
		origOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".part.meta.json") {
				return nil, errors.New("disk full")
			}
			return origOpenFile(name, flag, perm)
		}
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		assert.Error(t, err)
	})

	t.Run("create returns error when mpu directory cannot be created", func(t *testing.T) {
		s, rootPath := setup(t)
		// Place a regular file at .mpu to block MkdirAll.
		require.NoError(t, os.WriteFile(filepath.Join(rootPath, mpuDir), []byte{}, 0o600))
		_, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		assert.Error(t, err)
	})

	t.Run("create returns error when upload.json cannot be opened", func(t *testing.T) {
		s, rootPath := setup(t)
		var capturedUploadDir string
		origOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, "upload.json") {
				capturedUploadDir = filepath.Join(rootPath, filepath.Dir(name))
				return nil, errors.New("disk full")
			}
			return origOpenFile(name, flag, perm)
		}
		_, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		assert.Error(t, err)
		assert.NoDirExists(t, capturedUploadDir)
	})

	t.Run("create returns error when upload.json write fails", func(t *testing.T) {
		s, rootPath := setup(t)
		var capturedUploadDir string
		origOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, "upload.json") {
				capturedUploadDir = filepath.Join(rootPath, filepath.Dir(name))
				wc, err := origOpenFile(name, flag, perm)
				if err != nil {
					return nil, err
				}
				return badWriteWriter{wc}, nil
			}
			return origOpenFile(name, flag, perm)
		}
		_, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		assert.Error(t, err)
		assert.NoDirExists(t, capturedUploadDir)
	})

	t.Run("create returns error when upload.json close fails", func(t *testing.T) {
		s, rootPath := setup(t)
		var capturedUploadDir string
		origOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, "upload.json") {
				capturedUploadDir = filepath.Join(rootPath, filepath.Dir(name))
				wc, err := origOpenFile(name, flag, perm)
				if err != nil {
					return nil, err
				}
				return badCloseWriter{wc}, nil
			}
			return origOpenFile(name, flag, perm)
		}
		_, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		assert.Error(t, err)
		assert.NoDirExists(t, capturedUploadDir)
	})

	t.Run("upload part returns error when io.Copy fails", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, errReader{})
		assert.Error(t, err)
	})

	t.Run("upload part logs warning when part file close fails", func(t *testing.T) {
		s, _ := setup(t)
		origOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			wc, err := origOpenFile(name, flag, perm)
			if err != nil {
				return nil, err
			}
			if strings.HasSuffix(name, ".part") && !strings.HasSuffix(name, ".meta.json") {
				return badCloseWriter{wc}, nil
			}
			return wc, nil
		}
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		// The close failure is logged as a warning; UploadPart still returns the ETag.
		etag, err := s.UploadPart(uploadID, 1, strings.NewReader("data"))
		assert.NoError(t, err)
		assert.NotEmpty(t, etag)
	})

	t.Run("upload part returns error when meta write fails", func(t *testing.T) {
		s, _ := setup(t)
		origOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			wc, err := origOpenFile(name, flag, perm)
			if err != nil {
				return nil, err
			}
			if strings.HasSuffix(name, ".part.meta.json") {
				return badWriteWriter{wc}, nil
			}
			return wc, nil
		}
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		assert.Error(t, err)
	})

	t.Run("complete returns error when readUploadMeta readAll fails", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		s.readAll = func(_ io.Reader) ([]byte, error) { return nil, errors.New("read error") }
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: `"abc"`}})
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrUploadNotFound)
	})

	t.Run("complete returns error when part meta is corrupt JSON", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			filepath.Join(rootPath, mpuDir, uploadID, "1.part.meta.json"),
			[]byte("not-json"),
			0o600,
		))
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: `"abc"`}})
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrInvalidPart)
	})

	t.Run("complete returns error when part file is unreadable", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		etag, err := s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)
		partPath := filepath.Join(rootPath, mpuDir, uploadID, "1.part")
		require.NoError(t, os.Chmod(partPath, 0o000))
		t.Cleanup(func() { _ = os.Chmod(partPath, 0o600) })
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: etag}})
		assert.Error(t, err)
	})

	t.Run("complete returns error when meta write fails", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		etag, err := s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)
		origOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".meta.json") && !strings.Contains(name, mpuDir) {
				return nil, errors.New("disk full")
			}
			return origOpenFile(name, flag, perm)
		}
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: etag}})
		assert.Error(t, err)
	})

	t.Run("abort returns error for non-ErrNotExist stat failure", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		// Replace upload dir with a file to make Stat of upload.json fail with "not a directory".
		uploadDir := filepath.Join(rootPath, mpuDir, uploadID)
		require.NoError(t, os.RemoveAll(uploadDir))
		require.NoError(t, os.WriteFile(uploadDir, []byte{}, 0o600))
		err = s.AbortMultipartUpload(uploadID)
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrUploadNotFound)
	})

	t.Run("removeUploadDir returns error when entry removal fails", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		// Create a subdirectory inside the upload dir; Remove on a non-empty dir fails.
		subDir := filepath.Join(rootPath, mpuDir, uploadID, "subdir")
		require.NoError(t, os.Mkdir(subDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(subDir, "file"), []byte{}, 0o600))
		err = s.AbortMultipartUpload(uploadID)
		assert.Error(t, err)
	})

	t.Run(
		"readUploadMeta returns error when upload.json contains invalid JSON",
		func(t *testing.T) {
			s, rootPath := setup(t)
			uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(
				filepath.Join(rootPath, mpuDir, uploadID, "upload.json"),
				[]byte("not-json"),
				0o600,
			))
			_, err = s.CompleteMultipartUpload(
				uploadID,
				[]CompletePart{{PartNumber: 1, ETag: `"abc"`}},
			)
			assert.Error(t, err)
			assert.NotErrorIs(t, err, ErrUploadNotFound)
		},
	)

	t.Run("readPartMeta returns error when readAll fails", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)
		// Fail on the 2nd readAll call: 1st is upload.json, 2nd is the part meta.
		call := 0
		s.readAll = func(r io.Reader) ([]byte, error) {
			call++
			if call == 2 {
				return nil, errors.New("read error")
			}
			return io.ReadAll(r)
		}
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: `"abc"`}})
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrInvalidPart)
	})

	t.Run("removeUploadDir returns error when readDir fails", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		origListDir := s.listDirFn
		s.listDirFn = func(name string) ([]os.DirEntry, error) {
			if strings.Contains(name, uploadID) {
				return nil, errors.New("simulated readDir failure")
			}
			return origListDir(name)
		}
		err = s.AbortMultipartUpload(uploadID)
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrUploadNotFound)
	})

	t.Run("ListObjects skips .mpu directory inside bucket", func(t *testing.T) {
		s, rootPath := setup(t)
		// Create a .mpu directory inside the bucket to simulate a key collision.
		mpuInBucket := filepath.Join(rootPath, "my-bucket", ".mpu")
		require.NoError(t, os.Mkdir(mpuInBucket, 0o750))
		require.NoError(
			t,
			os.WriteFile(filepath.Join(mpuInBucket, "fakefile"), []byte("data"), 0o600),
		)
		objects, err := s.ListObjects("my-bucket")
		require.NoError(t, err)
		assert.Empty(t, objects)
	})

	t.Run("create returns error when randRead fails", func(t *testing.T) {
		s, _ := setup(t)
		s.randRead = func(_ []byte) (int, error) {
			return 0, errors.New("entropy exhausted")
		}
		_, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		assert.Error(t, err)
	})

	t.Run("upload part returns error on non-ErrNotExist stat failure", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		// Replace upload dir with a file so Stat of upload.json fails with ENOTDIR.
		uploadDir := filepath.Join(rootPath, mpuDir, uploadID)
		require.NoError(t, os.RemoveAll(uploadDir))
		require.NoError(t, os.WriteFile(uploadDir, []byte{}, 0o600))
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrUploadNotFound)
	})

	t.Run("complete returns error when MkdirAll fails for nested key", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "a/b/big.txt", "text/plain")
		require.NoError(t, err)
		etag, err := s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)
		// Block MkdirAll("my-bucket/a/b") by placing a file at "my-bucket/a".
		require.NoError(t, os.WriteFile(filepath.Join(rootPath, "my-bucket", "a"), []byte{}, 0o600))
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: etag}})
		assert.Error(t, err)
	})

	t.Run("complete returns error when writeObject fails", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key.txt", "text/plain")
		require.NoError(t, err)
		etag, err := s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)
		origOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			// Fail for the final assembled object file: not .json, not inside mpuDir.
			if !strings.HasSuffix(name, ".json") && !strings.Contains(name, mpuDir) {
				return nil, errors.New("disk full")
			}
			return origOpenFile(name, flag, perm)
		}
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: etag}})
		assert.Error(t, err)
	})

	t.Run("complete logs warning when cleanup fails", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		etag, err := s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)
		s.listDirFn = func(_ string) ([]os.DirEntry, error) {
			return nil, errors.New("simulated cleanup failure")
		}
		// CompleteMultipartUpload must still succeed despite cleanup failure.
		meta, err := s.CompleteMultipartUpload(
			uploadID,
			[]CompletePart{{PartNumber: 1, ETag: etag}},
		)
		require.NoError(t, err)
		assert.NotEmpty(t, meta.ETag)
	})

	t.Run(
		"complete closes already-opened files when a later part file cannot be opened",
		func(t *testing.T) {
			s, rootPath := setup(t)
			uploadID, err := s.CreateMultipartUpload("my-bucket", "big.txt", "text/plain")
			require.NoError(t, err)
			etag1, err := s.UploadPart(uploadID, 1, strings.NewReader("hello"))
			require.NoError(t, err)
			etag2, err := s.UploadPart(uploadID, 2, strings.NewReader("world"))
			require.NoError(t, err)

			// Make part 2's file unreadable; part 1 will be opened successfully first.
			part2Path := filepath.Join(rootPath, mpuDir, uploadID, "2.part")
			require.NoError(t, os.Chmod(part2Path, 0o000))
			t.Cleanup(func() { _ = os.Chmod(part2Path, 0o600) })

			_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{
				{PartNumber: 1, ETag: etag1},
				{PartNumber: 2, ETag: etag2},
			})
			assert.Error(t, err)
		},
	)

	t.Run(
		"create logs warning when cleanup removeUploadDir fails after write failure",
		func(t *testing.T) {
			s, _ := setup(t)
			origOpenFile := s.openFile
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				if strings.HasSuffix(name, "upload.json") {
					wc, err := origOpenFile(name, flag, perm)
					if err != nil {
						return nil, err
					}
					// Inject a listDirFn failure so removeUploadDir fails during cleanup.
					s.listDirFn = func(_ string) ([]os.DirEntry, error) {
						return nil, errors.New("simulated readDir failure")
					}
					return badWriteWriter{wc}, nil
				}
				return origOpenFile(name, flag, perm)
			}
			_, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
			assert.Error(t, err)
		},
	)

	t.Run("ListMultipartUploads returns nil when mpu dir does not exist", func(t *testing.T) {
		s, _ := setup(t)
		uploads, err := s.ListMultipartUploads("my-bucket")
		require.NoError(t, err)
		assert.Nil(t, uploads)
	})

	t.Run("ListMultipartUploads returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.ListMultipartUploads("no-bucket")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run(
		"ListMultipartUploads returns error when readDir fails with non-ErrNotExist",
		func(t *testing.T) {
			s, _ := setup(t)
			origListDir := s.listDirFn
			s.listDirFn = func(name string) ([]os.DirEntry, error) {
				if name == mpuDir {
					return nil, errors.New("simulated readDir failure")
				}
				return origListDir(name)
			}
			_, err := s.ListMultipartUploads("my-bucket")
			assert.Error(t, err)
		},
	)

	t.Run("ListMultipartUploads skips non-directory entries in mpu dir", func(t *testing.T) {
		s, rootPath := setup(t)
		// Create mpu dir with a regular file inside.
		mpuPath := filepath.Join(rootPath, mpuDir)
		require.NoError(t, os.MkdirAll(mpuPath, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(mpuPath, "not-a-dir.txt"), []byte{}, 0o600))
		uploads, err := s.ListMultipartUploads("my-bucket")
		require.NoError(t, err)
		assert.Empty(t, uploads)
	})

	t.Run("ListMultipartUploads skips uploads with unreadable metadata", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		uploads, err := s.ListMultipartUploads("my-bucket")
		require.NoError(t, err)
		assert.Empty(t, uploads)
	})

	t.Run("ListMultipartUploads filters uploads by bucket", func(t *testing.T) {
		s, _ := setup(t)
		require.NoError(t, s.CreateBucket("other-bucket", ""))
		uploadID1, err := s.CreateMultipartUpload("my-bucket", "key1", "text/plain")
		require.NoError(t, err)
		_, err = s.CreateMultipartUpload("other-bucket", "key2", "text/plain")
		require.NoError(t, err)
		uploads, err := s.ListMultipartUploads("my-bucket")
		require.NoError(t, err)
		require.Len(t, uploads, 1)
		assert.Equal(t, uploadID1, uploads[0].UploadID)
	})

	t.Run("ListParts returns ErrUploadNotFound for missing upload", func(t *testing.T) {
		s, _ := setup(t)
		_, _, err := s.ListParts("nonexistent-id")
		assert.ErrorIs(t, err, ErrUploadNotFound)
	})

	t.Run(
		"ListParts returns error when readUploadMeta fails with non-ErrNotExist",
		func(t *testing.T) {
			s, _ := setup(t)
			uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
			require.NoError(t, err)
			s.readAll = func(_ io.Reader) ([]byte, error) {
				return nil, errors.New("read error")
			}
			_, _, err = s.ListParts(uploadID)
			assert.Error(t, err)
			assert.NotErrorIs(t, err, ErrUploadNotFound)
		},
	)

	t.Run("ListParts returns error when readDir fails", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		origListDir := s.listDirFn
		s.listDirFn = func(name string) ([]os.DirEntry, error) {
			if strings.Contains(name, uploadID) {
				return nil, errors.New("simulated readDir failure")
			}
			return origListDir(name)
		}
		_, _, err = s.ListParts(uploadID)
		assert.Error(t, err)
	})

	t.Run("ListParts skips directory and non-meta entries", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		uploadDir := filepath.Join(rootPath, mpuDir, uploadID)
		// Add a regular file that doesn't match the part meta pattern.
		require.NoError(t, os.WriteFile(filepath.Join(uploadDir, "other.txt"), []byte{}, 0o600))
		// Add a subdirectory.
		require.NoError(t, os.Mkdir(filepath.Join(uploadDir, "subdir"), 0o750))
		// Add a file matching the suffix but with an unparseable part number prefix.
		require.NoError(
			t,
			os.WriteFile(filepath.Join(uploadDir, "invalid.part.meta.json"), []byte{}, 0o600),
		)
		_, parts, err := s.ListParts(uploadID)
		require.NoError(t, err)
		assert.Empty(t, parts)
	})

	t.Run("ListParts skips parts with unreadable metadata", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload("my-bucket", "key", "text/plain")
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)
		origReadAll := s.readAll
		call := 0
		s.readAll = func(r io.Reader) ([]byte, error) {
			call++
			if call == 2 { // 1st = upload.json, 2nd = part meta
				return nil, errors.New("read error")
			}
			return origReadAll(r)
		}
		_, parts, err := s.ListParts(uploadID)
		require.NoError(t, err)
		assert.Empty(t, parts)
	})
}

func TestObjectTagging(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("bucket", "us-east-1"))
		_, err := s.PutObject("bucket", "key.txt", strings.NewReader("hello"), "text/plain")
		require.NoError(t, err)
		return s, "bucket"
	}

	t.Run("PutObjectTagging and GetObjectTagging roundtrip", func(t *testing.T) {
		s, bucket := setup(t)
		tags := []Tag{{Key: "env", Value: "prod"}, {Key: "team", Value: "backend"}}
		require.NoError(t, s.PutObjectTagging(bucket, "key.txt", tags))
		got, err := s.GetObjectTagging(bucket, "key.txt")
		require.NoError(t, err)
		assert.Equal(t, tags, got)
	})

	t.Run("GetObjectTagging returns empty slice when no tags set", func(t *testing.T) {
		s, bucket := setup(t)
		got, err := s.GetObjectTagging(bucket, "key.txt")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("DeleteObjectTagging removes tags", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutObjectTagging(bucket, "key.txt", []Tag{{Key: "k", Value: "v"}}))
		require.NoError(t, s.DeleteObjectTagging(bucket, "key.txt"))
		got, err := s.GetObjectTagging(bucket, "key.txt")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("DeleteObjectTagging on object with no tags is a no-op", func(t *testing.T) {
		s, bucket := setup(t)
		assert.NoError(t, s.DeleteObjectTagging(bucket, "key.txt"))
	})

	t.Run("PutObjectTagging returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.PutObjectTagging("no-bucket", "key.txt", []Tag{})
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("PutObjectTagging returns ErrObjectNotFound for missing object", func(t *testing.T) {
		s, bucket := setup(t)
		err := s.PutObjectTagging(bucket, "no-such-key.txt", []Tag{})
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("GetObjectTagging returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.GetObjectTagging("no-bucket", "key.txt")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("GetObjectTagging returns ErrObjectNotFound for missing object", func(t *testing.T) {
		s, bucket := setup(t)
		_, err := s.GetObjectTagging(bucket, "no-such-key.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("DeleteObjectTagging returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.DeleteObjectTagging("no-bucket", "key.txt")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("DeleteObjectTagging returns ErrObjectNotFound for missing object", func(t *testing.T) {
		s, bucket := setup(t)
		err := s.DeleteObjectTagging(bucket, "no-such-key.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("PutObjectTagging returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		err := s.PutObjectTagging(bucket, "key.txt", []Tag{{Key: "k", Value: "v"}})
		assert.Error(t, err)
	})

	t.Run("PutObjectTagging returns error when write fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".tags.json") {
				return nil, errors.New("disk full")
			}
			return s.root.OpenFile(name, flag, perm)
		}
		err := s.PutObjectTagging(bucket, "key.txt", []Tag{{Key: "k", Value: "v"}})
		assert.Error(t, err)
	})

	t.Run("GetObjectTagging returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		_, err := s.GetObjectTagging(bucket, "key.txt")
		assert.Error(t, err)
	})

	t.Run("GetObjectTagging returns error when tags file is corrupt", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutObjectTagging(bucket, "key.txt", []Tag{{Key: "k", Value: "v"}}))
		call := 0
		origReadAll := s.readAll
		s.readAll = func(r io.Reader) ([]byte, error) {
			call++
			if call == 2 { // 1st = meta, 2nd = tags
				return []byte("not-json"), nil
			}
			return origReadAll(r)
		}
		_, err := s.GetObjectTagging(bucket, "key.txt")
		assert.Error(t, err)
	})

	t.Run("DeleteObjectTagging returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		err := s.DeleteObjectTagging(bucket, "key.txt")
		assert.Error(t, err)
	})

	t.Run("DeleteObjectTagging returns error when remove fails", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutObjectTagging(bucket, "key.txt", []Tag{{Key: "k", Value: "v"}}))
		s.removeFile = func(name string) error {
			if strings.HasSuffix(name, ".tags.json") {
				return errors.New("permission denied")
			}
			return s.root.Remove(name)
		}
		err := s.DeleteObjectTagging(bucket, "key.txt")
		assert.Error(t, err)
	})
}
