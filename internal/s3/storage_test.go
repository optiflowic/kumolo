package s3

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// errWriteCloser is an io.WriteCloser that always returns an error on Write.
type errWriteCloser struct{}

func (errWriteCloser) Write(_ []byte) (int, error) { return 0, errors.New("write failure") }
func (errWriteCloser) Close() error                { return nil }

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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		assert.True(t, s.BucketExists("my-bucket"))
	})

	t.Run("persists region", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "ap-northeast-1", false))
		region, err := s.GetBucketRegion("my-bucket")
		require.NoError(t, err)
		assert.Equal(t, "ap-northeast-1", region)
	})

	t.Run("returns error and does not create bucket when meta write fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		// Place a directory where the metadata file should be written to force a failure.
		require.NoError(t, os.MkdirAll(filepath.Join(rootPath, "my-bucket.bucket.json"), 0o750))

		err := s.CreateBucket("my-bucket", "us-west-2", false)
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

			err := s.CreateBucket("my-bucket", "us-west-2", false)
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

		err := s.CreateBucket("my-bucket", "ap-northeast-1", false)
		assert.Error(t, err)
	})

	t.Run("objectLockEnabled enables versioning and stores ObjectLock XML", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", true))

		status, err := s.GetBucketVersioning("my-bucket")
		require.NoError(t, err)
		assert.Equal(t, "Enabled", status)

		lockXML, err := s.GetBucketObjectLock("my-bucket")
		require.NoError(t, err)
		assert.Contains(t, lockXML, "ObjectLockEnabled")
		assert.Contains(t, lockXML, "Enabled")
	})
}

func TestGetBucketRegion(t *testing.T) {
	t.Run("returns region the bucket was created with", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "eu-west-1", false))
		region, err := s.GetBucketRegion("my-bucket")
		require.NoError(t, err)
		assert.Equal(t, "eu-west-1", region)
	})

	t.Run("returns empty string for bucket created without region", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, os.Remove(filepath.Join(rootPath, "my-bucket.bucket.json")))

		region, err := s.GetBucketRegion("my-bucket")
		require.NoError(t, err)
		assert.Equal(t, "", region)
	})

	t.Run("returns error when metadata is corrupt", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
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
		require.NoError(t, s.CreateBucket("my-bucket", "us-east-2", false))

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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.DeleteBucket("my-bucket"))
		assert.False(t, s.BucketExists("my-bucket"))
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		assert.ErrorIs(t, s.DeleteBucket("no-such-bucket"), ErrBucketNotFound)
	})

	t.Run("returns ErrBucketNotEmpty when bucket has objects", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("hello"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		assert.ErrorIs(t, s.DeleteBucket("my-bucket"), ErrBucketNotEmpty)
	})

	t.Run("removes bucket metadata on delete", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "ap-northeast-1", false))
		require.NoError(t, s.DeleteBucket("my-bucket"))
		assert.NoFileExists(t, filepath.Join(rootPath, "my-bucket.bucket.json"))
	})

	t.Run("logs warning when metadata removal fails but still deletes bucket", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		// Replace the .bucket.json file with a non-empty directory so Remove fails.
		metaPath := filepath.Join(rootPath, "my-bucket.bucket.json")
		require.NoError(t, os.Remove(metaPath))
		require.NoError(t, os.MkdirAll(filepath.Join(metaPath, "child"), 0o750))

		require.NoError(t, s.DeleteBucket("my-bucket"))
		assert.False(t, s.BucketExists("my-bucket"))
	})

	t.Run("returns error when directory listing fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o000))
		t.Cleanup(
			func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) },
		)

		err := s.DeleteBucket("my-bucket")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("succeeds after all object versions are deleted", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))

		meta, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		_, err = s.DeleteObjectVersion("my-bucket", "obj.txt", meta.VersionID, false)
		require.NoError(t, err)

		assert.NoError(t, s.DeleteBucket("my-bucket"))
		assert.False(t, s.BucketExists("my-bucket"))
	})

	t.Run("returns ErrBucketNotEmpty when versioned objects remain", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("hello"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		assert.ErrorIs(t, s.DeleteBucket("my-bucket"), ErrBucketNotEmpty)
	})

	t.Run(
		"succeeds after versioning enabled then all objects deleted via delete markers cleared",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))

			// Put and delete-marker an object, then delete the marker itself.
			meta, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			markerVersionID, _, err := s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
			require.NoError(t, err)
			_, err = s.DeleteObjectVersion("my-bucket", "obj.txt", markerVersionID, false)
			require.NoError(t, err)
			_, err = s.DeleteObjectVersion("my-bucket", "obj.txt", meta.VersionID, false)
			require.NoError(t, err)

			assert.NoError(t, s.DeleteBucket("my-bucket"))
		},
	)

	t.Run(
		"returns ErrBucketNotEmpty when original version remains in .ver after marker deleted",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket", "config/app.json",
				strings.NewReader("v1"), "text/plain", nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)

			// Create delete marker — archives original to .ver/config/app.json/<v_orig>.
			markerID, _, err := s.DeleteObjectVersioned("my-bucket", "config/app.json", false)
			require.NoError(t, err)

			// Delete only the marker; original version in .ver/ remains.
			_, err = s.DeleteObjectVersion("my-bucket", "config/app.json", markerID, false)
			require.NoError(t, err)

			// verDirIsEmpty recursively finds the versioned object → ErrBucketNotEmpty.
			assert.ErrorIs(t, s.DeleteBucket("my-bucket"), ErrBucketNotEmpty)
		},
	)

	t.Run("succeeds when .ver contains only empty subdirectories", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))

		// Simulate leftover empty subdirs in .ver (e.g. from a failed write).
		emptyVerDir := filepath.Join(rootPath, "my-bucket", ".ver", "leftover", "subdir")
		require.NoError(t, os.MkdirAll(emptyVerDir, 0o750))

		// verDirIsEmpty returns true → removeAllDir recursively cleans up.
		assert.NoError(t, s.DeleteBucket("my-bucket"))
	})

	t.Run("succeeds after deleting nested-key objects with versioning", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))

		putObj := func(key string) ObjectMetadata {
			meta, err := s.PutObject(
				"my-bucket", key, strings.NewReader("data"),
				"text/plain", nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			return meta
		}

		meta1 := putObj("config/app.json")
		meta2 := putObj("docs/README.txt")

		// Delete via marker then remove marker + version for each object.
		for _, tc := range []struct {
			key  string
			meta ObjectMetadata
		}{{key: "config/app.json", meta: meta1}, {key: "docs/README.txt", meta: meta2}} {
			markerID, _, err := s.DeleteObjectVersioned("my-bucket", tc.key, false)
			require.NoError(t, err)
			_, err = s.DeleteObjectVersion("my-bucket", tc.key, markerID, false)
			require.NoError(t, err)
			_, err = s.DeleteObjectVersion("my-bucket", tc.key, tc.meta.VersionID, false)
			require.NoError(t, err)
		}

		assert.NoError(t, s.DeleteBucket("my-bucket"))
	})
}

func TestListBuckets(t *testing.T) {
	t.Run("lists all buckets in lexicographic order", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("bucket-c", "", false))
		require.NoError(t, s.CreateBucket("bucket-a", "", false))
		require.NoError(t, s.CreateBucket("bucket-b", "", false))

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
		require.NoError(t, s.CreateBucket("bucket-a", "", false))
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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))

		meta, err := s.PutObject(
			"my-bucket",
			"hello.txt",
			strings.NewReader("hello world"),
			"text/plain",
			nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)
		assert.Equal(t, int64(11), meta.Size)
		assert.Equal(t, "text/plain", meta.ContentType)
		assert.NotEmpty(t, meta.ETag)
	})

	t.Run("stores object with nested key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))

		_, err := s.PutObject(
			"my-bucket",
			"dir/sub/obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)

		objects, err := s.ListObjects("my-bucket")
		require.NoError(t, err)
		require.Len(t, objects, 1)
		assert.Equal(t, "dir/sub/obj.txt", objects[0].Key)
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.PutObject(
			"no-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("returns error when file cannot be opened", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o000))
		t.Cleanup(
			func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) },
		)

		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		assert.Error(t, err)
	})

	t.Run("returns error when nested directory cannot be created", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
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
			nil, "", "", false, "", nil, nil, "",
		)
		assert.Error(t, err)
	})

	t.Run("returns error when reader fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))

		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			errReader{},
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		assert.Error(t, err)
	})

	t.Run("cleans up object file when meta write fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(
			t,
			os.MkdirAll(filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"), 0o750),
		)

		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		assert.Error(t, err)
		assert.NoFileExists(t, filepath.Join(rootPath, "my-bucket", "obj.txt"))
	})

	t.Run("logs warning when cleanup remove also fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(
			t,
			os.MkdirAll(filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json"), 0o750),
		)

		s.removeFile = func(_ string) error {
			return errors.New("simulated remove failure")
		}

		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		assert.Error(t, err)
	})

	t.Run(
		"returns close error when meta file close fails after successful write",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))

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

			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("data"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			assert.Error(t, err)
		},
	)

	t.Run(
		"returns close error when object file close fails after successful write",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))

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

			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("data"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			assert.Error(t, err)
		},
	)

	t.Run("stores and returns user metadata", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))

		userMeta := map[string]string{"original-filename": "photo.jpg", "uploader": "user1"}
		meta, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			userMeta,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		assert.Equal(t, userMeta, meta.UserMetadata)

		got, err := s.HeadObject("my-bucket", "obj.txt")
		require.NoError(t, err)
		assert.Equal(t, userMeta, got.UserMetadata)
	})

	t.Run("SSE fields round-trip through PutObject", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		meta, err := s.PutObject("b", "k", strings.NewReader("x"), "text/plain", nil,
			"aws:kms", "my-key-id", true, "", nil, nil, "")
		require.NoError(t, err)
		assert.Equal(t, "aws:kms", meta.SSEAlgorithm)
		assert.Equal(t, "my-key-id", meta.SSEKMSKeyID)
		assert.True(t, meta.SSEBucketKeyEnabled)

		got, err := s.HeadObject("b", "k")
		require.NoError(t, err)
		assert.Equal(t, "aws:kms", got.SSEAlgorithm)
		assert.Equal(t, "my-key-id", got.SSEKMSKeyID)
		assert.True(t, got.SSEBucketKeyEnabled)
	})

	t.Run("SSECKeyMD5 round-trips through PutObject", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		meta, err := s.PutObject("b", "k", strings.NewReader("x"), "text/plain", nil,
			"", "", false, ssecMD5(), nil, nil, "")
		require.NoError(t, err)
		assert.Equal(t, ssecMD5(), meta.SSECKeyMD5)
		assert.Empty(t, meta.SSEAlgorithm)

		got, err := s.HeadObject("b", "k")
		require.NoError(t, err)
		assert.Equal(t, ssecMD5(), got.SSECKeyMD5)
	})

	t.Run("SSEBucketKeyEnabled round-trips through CopyObject", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.PutObject("b", "src", strings.NewReader("x"), "text/plain", nil,
			"aws:kms", "key-1", true, "", nil, nil, "")
		require.NoError(t, err)
		meta, err := s.CopyObject(
			"b",
			"src",
			"",
			"b",
			"dst",
			"",
			nil,
			"aws:kms",
			"key-1",
			true,
			"",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		assert.True(t, meta.SSEBucketKeyEnabled)
		got, err := s.HeadObject("b", "dst")
		require.NoError(t, err)
		assert.True(t, got.SSEBucketKeyEnabled)
	})

	t.Run("SSECKeyMD5 round-trips through CopyObject", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.PutObject("b", "src", strings.NewReader("x"), "text/plain", nil,
			"", "", false, ssecMD5(), nil, nil, "")
		require.NoError(t, err)
		meta, err := s.CopyObject("b", "src", "", "b", "dst", "",
			nil, "", "", false, ssecMD5(), nil, nil, "", nil)
		require.NoError(t, err)
		assert.Equal(t, ssecMD5(), meta.SSECKeyMD5)
		assert.Empty(t, meta.SSEAlgorithm)

		got, err := s.HeadObject("b", "dst")
		require.NoError(t, err)
		assert.Equal(t, ssecMD5(), got.SSECKeyMD5)
	})

	t.Run("SSECKeyMD5 round-trips through multipart upload", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		uploadID, err := s.CreateMultipartUpload(
			"b", "k", "text/plain", "", "", false, ssecMD5(), nil, nil, "", nil,
		)
		require.NoError(t, err)

		uploadMetaResult, err := s.GetUploadMeta(uploadID)
		require.NoError(t, err)
		assert.Equal(t, ssecMD5(), uploadMetaResult.SSECKeyMD5)
		assert.Empty(t, uploadMetaResult.SSEAlgorithm)

		etag, err := s.UploadPart(
			uploadID, 1, strings.NewReader(strings.Repeat("a", 5*1024*1024+1)),
		)
		require.NoError(t, err)
		meta, err := s.CompleteMultipartUpload(
			uploadID, []CompletePart{{PartNumber: 1, ETag: etag}},
		)
		require.NoError(t, err)
		assert.Equal(t, ssecMD5(), meta.SSECKeyMD5)
		assert.Empty(t, meta.SSEAlgorithm)

		got, err := s.HeadObject("b", "k")
		require.NoError(t, err)
		assert.Equal(t, ssecMD5(), got.SSECKeyMD5)
	})

	t.Run("SSEBucketKeyEnabled round-trips through multipart upload", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		uploadID, err := s.CreateMultipartUpload(
			"b",
			"k",
			"text/plain",
			"aws:kms",
			"key-1",
			true,
			"",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		etag, err := s.UploadPart(
			uploadID,
			1,
			strings.NewReader(strings.Repeat("a", 5*1024*1024+1)),
		)
		require.NoError(t, err)
		meta, err := s.CompleteMultipartUpload(
			uploadID,
			[]CompletePart{{PartNumber: 1, ETag: etag}},
		)
		require.NoError(t, err)
		assert.True(t, meta.SSEBucketKeyEnabled)
		got, err := s.HeadObject("b", "k")
		require.NoError(t, err)
		assert.True(t, got.SSEBucketKeyEnabled)
	})

	t.Run("retention and legal hold headers are stored in metadata", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(
			t,
			s.CreateBucket("my-bucket", "", true),
		) // objectLockEnabled → versioning on
		retention := &ObjectRetention{
			Mode:            "GOVERNANCE",
			RetainUntilDate: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		legalHold := &ObjectLegalHold{Status: "ON"}
		meta, err := s.PutObject(
			"my-bucket", "obj.txt", strings.NewReader("data"), "text/plain", nil, "", "", false, "",
			retention, legalHold, "",
		)
		require.NoError(t, err)
		require.NotNil(t, meta.Retention)
		assert.Equal(t, "GOVERNANCE", meta.Retention.Mode)
		require.NotNil(t, meta.LegalHold)
		assert.Equal(t, "ON", meta.LegalHold.Status)

		got, err := s.HeadObject("my-bucket", "obj.txt")
		require.NoError(t, err)
		require.NotNil(t, got.Retention)
		assert.Equal(t, "GOVERNANCE", got.Retention.Mode)
		require.NotNil(t, got.LegalHold)
		assert.Equal(t, "ON", got.LegalHold.Status)
	})
}

func TestPutObjectIfNotExists(t *testing.T) {
	t.Run("succeeds when object does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))

		meta, err := s.PutObjectIfNotExists(
			"my-bucket", "obj.txt",
			strings.NewReader("hello"),
			"text/plain", nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)
		assert.Equal(t, int64(5), meta.Size)
	})

	t.Run("returns ErrObjectAlreadyExists when object exists", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("first"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		_, err = s.PutObjectIfNotExists(
			"my-bucket", "obj.txt",
			strings.NewReader("second"),
			"text/plain", nil, "", "", false, "", nil, nil, "",
		)
		require.ErrorIs(t, err, ErrObjectAlreadyExists)
		var oae *ObjectAlreadyExistsError
		require.ErrorAs(t, err, &oae)
		assert.NotEmpty(t, oae.ETag)
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)

		_, err := s.PutObjectIfNotExists(
			"no-bucket", "obj.txt",
			strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "",
		)
		require.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("succeeds when current version is a delete marker", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))

		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		_, _, err = s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
		require.NoError(t, err)

		meta, err := s.PutObjectIfNotExists(
			"my-bucket", "obj.txt",
			strings.NewReader("v2"),
			"text/plain", nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)
		assert.NotEmpty(t, meta.VersionID)
	})

	t.Run("assigns versionID when versioning is enabled", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))

		meta, err := s.PutObjectIfNotExists(
			"my-bucket", "obj.txt",
			strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)
		assert.NotEmpty(t, meta.VersionID)
	})

	t.Run("stores object with nested key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))

		_, err := s.PutObjectIfNotExists(
			"my-bucket", "dir/sub/obj.txt",
			strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)

		objects, err := s.ListObjects("my-bucket")
		require.NoError(t, err)
		require.Len(t, objects, 1)
		assert.Equal(t, "dir/sub/obj.txt", objects[0].Key)
	})

	t.Run("returns error when meta file is corrupt", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))

		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		// Corrupt the meta file so readMeta returns a non-ErrNotExist error.
		f, err := s.root.OpenFile("my-bucket/obj.txt.meta.json", os.O_WRONLY|os.O_TRUNC, 0o600)
		require.NoError(t, err)
		_, err = f.Write([]byte("not-json"))
		require.NoError(t, err)
		require.NoError(t, f.Close())

		_, err = s.PutObjectIfNotExists(
			"my-bucket", "obj.txt",
			strings.NewReader("second"),
			"text/plain", nil, "", "", false, "", nil, nil, "",
		)
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrObjectAlreadyExists)
		assert.NotErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns error when MkdirAll fails for nested key", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))

		// Remove write permission so MkdirAll cannot create subdirectories.
		require.NoError(t, os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o500))
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(rootPath, "my-bucket"), 0o750) })

		_, err := s.PutObjectIfNotExists(
			"my-bucket", "nested/obj.txt",
			strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "",
		)
		require.Error(t, err)
	})

	t.Run("returns error when isVersioningEnabledLocked fails", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))

		// Corrupt bucket meta so JSON unmarshal fails inside isVersioningEnabledLocked.
		f, err := s.root.OpenFile("my-bucket.bucket.json", os.O_WRONLY|os.O_TRUNC, 0o600)
		require.NoError(t, err)
		_, err = f.Write([]byte("not-json"))
		require.NoError(t, err)
		require.NoError(t, f.Close())

		_, err = s.PutObjectIfNotExists(
			"my-bucket", "obj.txt",
			strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "",
		)
		require.Error(t, err)
	})

	t.Run("returns error when newVersionID fails", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
		s.randRead = func(b []byte) (int, error) { return 0, errors.New("rand failure") }

		_, err := s.PutObjectIfNotExists(
			"my-bucket", "obj.txt",
			strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "",
		)
		require.Error(t, err)
	})
}

func TestCopyObject(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("src-bucket", "", false))
		require.NoError(t, s.CreateBucket("dst-bucket", "", false))
		_, err := s.PutObject(
			"src-bucket",
			"orig.txt",
			strings.NewReader("hello"),
			"text/plain",
			nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)
		return s, rootPath
	}

	t.Run("copies object to different key in same bucket", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"src-bucket",
			"copy.txt",
			"",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		_, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"dst-bucket",
			"copy.txt",
			"",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		meta, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"src-bucket",
			"orig.txt",
			"",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, origMeta.ETag, meta.ETag)
		f, _, err := s.GetObject("src-bucket", "orig.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(data))
	})

	t.Run("same-key copy applies retention and legal-hold from headers", func(t *testing.T) {
		s, _ := setup(t)
		retention := &ObjectRetention{
			Mode:            "GOVERNANCE",
			RetainUntilDate: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		legalHold := &ObjectLegalHold{Status: "ON"}
		meta, err := s.CopyObject(
			"src-bucket", "orig.txt", "",
			"src-bucket", "orig.txt", "",
			nil, "", "", false, "", retention, legalHold, "", nil,
		)
		require.NoError(t, err)
		require.NotNil(t, meta.Retention)
		assert.Equal(t, "GOVERNANCE", meta.Retention.Mode)
		require.NotNil(t, meta.LegalHold)
		assert.Equal(t, "ON", meta.LegalHold.Status)
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

		_, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"src-bucket",
			"orig.txt",
			"",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		assert.Error(t, err)
	})

	t.Run("copied object gets new LastModified", func(t *testing.T) {
		s, _ := setup(t)
		srcMeta, err := s.HeadObject("src-bucket", "orig.txt")
		require.NoError(t, err)
		dstMeta, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"dst-bucket",
			"copy.txt",
			"",
			nil, "", "", false, "", nil, nil, "",
			nil,
		)
		require.NoError(t, err)
		assert.True(t, !dstMeta.LastModified.Before(srcMeta.LastModified))
	})

	t.Run("copies object with nested destination key", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"dst-bucket",
			"path/to/copy.txt",
			"",
			nil, "", "", false, "", nil, nil, "",
			nil,
		)
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

		_, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"dst-bucket",
			"copy.txt",
			"",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns ErrBucketNotFound when source bucket does not exist", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CopyObject(
			"no-bucket",
			"orig.txt",
			"",
			"dst-bucket",
			"copy.txt",
			"",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("returns ErrObjectNotFound when source key does not exist", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CopyObject(
			"src-bucket",
			"missing.txt",
			"",
			"dst-bucket",
			"copy.txt",
			"",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns ErrBucketNotFound when destination bucket does not exist", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"no-bucket",
			"copy.txt",
			"",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("returns error when destination directory cannot be created", func(t *testing.T) {
		s, rootPath := setup(t)
		require.NoError(
			t,
			os.Chmod(filepath.Join(rootPath, "dst-bucket"), 0o500),
		)
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(rootPath, "dst-bucket"), 0o750) })

		_, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"dst-bucket",
			"nested/copy.txt",
			"",
			nil, "", "", false, "", nil, nil, "",
			nil,
		)
		assert.Error(t, err)
	})

	t.Run("returns ErrObjectNotFound when source data file is missing", func(t *testing.T) {
		s, rootPath := setup(t)
		require.NoError(t, os.Remove(filepath.Join(rootPath, "src-bucket", "orig.txt")))

		_, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"dst-bucket",
			"copy.txt",
			"",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns error when source data file is unreadable", func(t *testing.T) {
		s, rootPath := setup(t)
		dataPath := filepath.Join(rootPath, "src-bucket", "orig.txt")
		require.NoError(t, os.Chmod(dataPath, 0o000))
		t.Cleanup(func() { _ = os.Chmod(dataPath, 0o600) })

		_, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"dst-bucket",
			"copy.txt",
			"",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("COPY directive inherits source user metadata", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("src-bucket", "", false))
		require.NoError(t, s.CreateBucket("dst-bucket", "", false))
		srcMeta := map[string]string{"x": "1"}
		_, err := s.PutObject(
			"src-bucket",
			"orig.txt",
			strings.NewReader("hello"),
			"text/plain",
			srcMeta,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		dstMeta, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"dst-bucket",
			"copy.txt",
			"",
			nil, "", "", false, "", nil, nil, "",
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, srcMeta, dstMeta.UserMetadata)
	})

	t.Run("REPLACE directive uses new user metadata", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("src-bucket", "", false))
		require.NoError(t, s.CreateBucket("dst-bucket", "", false))
		_, err := s.PutObject("src-bucket", "orig.txt", strings.NewReader("hello"), "text/plain",
			map[string]string{"x": "1"}, "", "", false, "", nil, nil, "")
		require.NoError(t, err)

		newMeta := map[string]string{"y": "2"}
		dstMeta, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"dst-bucket",
			"copy.txt",
			"",
			newMeta,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, newMeta, dstMeta.UserMetadata)
	})

	t.Run("same-key COPY directive inherits user metadata", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("src-bucket", "", false))
		srcMeta := map[string]string{"x": "1"}
		_, err := s.PutObject(
			"src-bucket",
			"orig.txt",
			strings.NewReader("hello"),
			"text/plain",
			srcMeta,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		dstMeta, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"src-bucket",
			"orig.txt",
			"",
			nil, "", "", false, "", nil, nil, "",
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, srcMeta, dstMeta.UserMetadata)
	})

	t.Run("same-key REPLACE directive uses new user metadata", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("src-bucket", "", false))
		_, err := s.PutObject("src-bucket", "orig.txt", strings.NewReader("hello"), "text/plain",
			map[string]string{"x": "1"}, "", "", false, "", nil, nil, "")
		require.NoError(t, err)

		newMeta := map[string]string{"y": "2"}
		dstMeta, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"src-bucket",
			"orig.txt",
			"",
			newMeta,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, newMeta, dstMeta.UserMetadata)
	})

	t.Run("REPLACE directive replaces content type", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("src-bucket", "", false))
		require.NoError(t, s.CreateBucket("dst-bucket", "", false))
		_, err := s.PutObject(
			"src-bucket",
			"orig.txt",
			strings.NewReader("hello"),
			"text/plain",
			nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)

		dstMeta, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"dst-bucket",
			"copy.txt",
			"application/json",
			nil, "", "", false, "", nil, nil, "",
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, "application/json", dstMeta.ContentType)
	})

	t.Run("COPY directive inherits content type", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("src-bucket", "", false))
		require.NoError(t, s.CreateBucket("dst-bucket", "", false))
		_, err := s.PutObject(
			"src-bucket",
			"orig.txt",
			strings.NewReader("hello"),
			"text/plain",
			nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)

		dstMeta, err := s.CopyObject(
			"src-bucket",
			"orig.txt",
			"",
			"dst-bucket",
			"copy.txt",
			"",
			nil, "", "", false, "", nil, nil, "",
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, "text/plain", dstMeta.ContentType)
	})

	t.Run("retention and legal hold are applied to the destination object", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("src-bucket", "", false))
		require.NoError(
			t,
			s.CreateBucket("dst-bucket", "", true),
		) // objectLockEnabled → versioning on
		_, err := s.PutObject("src-bucket", "orig.txt", strings.NewReader("hello"), "text/plain",
			nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)

		retention := &ObjectRetention{
			Mode:            "COMPLIANCE",
			RetainUntilDate: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		legalHold := &ObjectLegalHold{Status: "ON"}
		dstMeta, err := s.CopyObject(
			"src-bucket", "orig.txt", "", "dst-bucket", "copy.txt", "", nil, "", "", false, "",
			retention, legalHold, "", nil,
		)
		require.NoError(t, err)
		require.NotNil(t, dstMeta.Retention)
		assert.Equal(t, "COMPLIANCE", dstMeta.Retention.Mode)
		require.NotNil(t, dstMeta.LegalHold)
		assert.Equal(t, "ON", dstMeta.LegalHold.Status)
	})

	t.Run("default_retention", func(t *testing.T) {
		const defaultRetentionXML = `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>COMPLIANCE</Mode><Days>7</Days></DefaultRetention></Rule></ObjectLockConfiguration>`
		now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

		t.Run("applies default retention when retention is nil", func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("src-bucket", "", false))
			require.NoError(t, s.CreateBucket("dst-bucket", "", true))
			s.now = func() time.Time { return now }
			require.NoError(t, s.PutBucketObjectLock("dst-bucket", defaultRetentionXML))
			_, err := s.PutObject(
				"src-bucket",
				"orig.txt",
				strings.NewReader("hello"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			_, err = s.CopyObject(
				"src-bucket",
				"orig.txt",
				"",
				"dst-bucket",
				"copy.txt",
				"",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)

			ret, err := s.GetObjectRetention("dst-bucket", "copy.txt", "")
			require.NoError(t, err)
			assert.Equal(t, "COMPLIANCE", ret.Mode)
			assert.True(t, now.AddDate(0, 0, 7).Equal(ret.RetainUntilDate))
		})

		t.Run("explicit retention takes precedence over bucket default", func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("src-bucket", "", false))
			require.NoError(t, s.CreateBucket("dst-bucket", "", true))
			s.now = func() time.Time { return now }
			require.NoError(t, s.PutBucketObjectLock("dst-bucket", defaultRetentionXML))
			_, err := s.PutObject(
				"src-bucket",
				"orig.txt",
				strings.NewReader("hello"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			explicit := &ObjectRetention{
				Mode:            "GOVERNANCE",
				RetainUntilDate: now.AddDate(0, 0, 30),
			}
			_, err = s.CopyObject(
				"src-bucket",
				"orig.txt",
				"",
				"dst-bucket",
				"copy.txt",
				"",
				nil,
				"",
				"", false, "",
				explicit,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)

			ret, err := s.GetObjectRetention("dst-bucket", "copy.txt", "")
			require.NoError(t, err)
			assert.Equal(t, "GOVERNANCE", ret.Mode)
			assert.True(t, now.AddDate(0, 0, 30).Equal(ret.RetainUntilDate))
		})

		t.Run("no default retention rule leaves object without retention", func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("src-bucket", "", false))
			require.NoError(t, s.CreateBucket("dst-bucket", "", true))
			_, err := s.PutObject(
				"src-bucket",
				"orig.txt",
				strings.NewReader("hello"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			_, err = s.CopyObject(
				"src-bucket",
				"orig.txt",
				"",
				"dst-bucket",
				"copy.txt",
				"",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)

			_, err = s.GetObjectRetention("dst-bucket", "copy.txt", "")
			assert.ErrorIs(t, err, ErrNoObjectRetention)
		})

		t.Run("object lock disabled leaves object without retention", func(t *testing.T) {
			s, _ := setup(t)
			_, err := s.CopyObject(
				"src-bucket",
				"orig.txt",
				"",
				"dst-bucket",
				"copy.txt",
				"",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)

			_, err = s.GetObjectRetention("dst-bucket", "copy.txt", "")
			assert.ErrorIs(t, err, ErrNoObjectRetention)
		})
	})
}

func TestCopyObjectTaggingDirective(t *testing.T) {
	setup := func(t *testing.T) *Storage {
		t.Helper()
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("src", "", false))
		require.NoError(t, s.CreateBucket("dst", "", false))
		_, err := s.PutObject("src", "obj.txt", strings.NewReader("data"), "text/plain",
			nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		require.NoError(t, s.PutObjectTagging("src", "obj.txt", []Tag{
			{Key: "env", Value: "prod"},
			{Key: "team", Value: "core"},
		}))
		return s
	}

	t.Run("nil tags (COPY) copies source tags to destination", func(t *testing.T) {
		s := setup(t)
		_, err := s.CopyObject("src", "obj.txt", "", "dst", "copy.txt",
			"", nil, "", "", false, "", nil, nil, "", nil)
		require.NoError(t, err)
		tags, err := s.GetObjectTagging("dst", "copy.txt")
		require.NoError(t, err)
		require.Len(t, tags, 2)
		tagMap := make(map[string]string, len(tags))
		for _, tg := range tags {
			tagMap[tg.Key] = tg.Value
		}
		assert.Equal(t, "prod", tagMap["env"])
		assert.Equal(t, "core", tagMap["team"])
	})

	t.Run("non-nil empty tags (REPLACE with empty) clears tags on destination", func(t *testing.T) {
		s := setup(t)
		// First copy with COPY so dst has tags.
		_, err := s.CopyObject("src", "obj.txt", "", "dst", "copy.txt",
			"", nil, "", "", false, "", nil, nil, "", nil)
		require.NoError(t, err)

		// Overwrite with REPLACE and empty tag set.
		_, err = s.CopyObject("src", "obj.txt", "", "dst", "copy.txt",
			"", nil, "", "", false, "", nil, nil, "", []Tag{})
		require.NoError(t, err)
		tags, err := s.GetObjectTagging("dst", "copy.txt")
		require.NoError(t, err)
		assert.Empty(t, tags)
	})

	t.Run("non-nil tags (REPLACE) applies provided tags ignoring source", func(t *testing.T) {
		s := setup(t)
		replaceTags := []Tag{{Key: "new-key", Value: "new-val"}}
		_, err := s.CopyObject("src", "obj.txt", "", "dst", "copy.txt",
			"", nil, "", "", false, "", nil, nil, "", replaceTags)
		require.NoError(t, err)
		tags, err := s.GetObjectTagging("dst", "copy.txt")
		require.NoError(t, err)
		require.Len(t, tags, 1)
		assert.Equal(t, "new-key", tags[0].Key)
		assert.Equal(t, "new-val", tags[0].Value)
	})

	t.Run(
		"nil tags (COPY) on source with no tags results in no destination tags",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("src", "", false))
			require.NoError(t, s.CreateBucket("dst", "", false))
			_, err := s.PutObject("src", "obj.txt", strings.NewReader("data"), "text/plain",
				nil, "", "", false, "", nil, nil, "")
			require.NoError(t, err)
			_, err = s.CopyObject("src", "obj.txt", "", "dst", "copy.txt",
				"", nil, "", "", false, "", nil, nil, "", nil)
			require.NoError(t, err)
			tags, err := s.GetObjectTagging("dst", "copy.txt")
			require.NoError(t, err)
			assert.Empty(t, tags)
		},
	)

	t.Run("same-key REPLACE updates tags in-place", func(t *testing.T) {
		s := setup(t)
		newTags := []Tag{{Key: "stage", Value: "dev"}}
		_, err := s.CopyObject("src", "obj.txt", "", "src", "obj.txt",
			"", nil, "", "", false, "", nil, nil, "", newTags)
		require.NoError(t, err)
		tags, err := s.GetObjectTagging("src", "obj.txt")
		require.NoError(t, err)
		require.Len(t, tags, 1)
		assert.Equal(t, "stage", tags[0].Key)
	})

	t.Run("same-key COPY leaves existing tags unchanged", func(t *testing.T) {
		s := setup(t)
		_, err := s.CopyObject("src", "obj.txt", "", "src", "obj.txt",
			"", nil, "", "", false, "", nil, nil, "", nil)
		require.NoError(t, err)
		tags, err := s.GetObjectTagging("src", "obj.txt")
		require.NoError(t, err)
		require.Len(t, tags, 2)
	})

	t.Run("versioned source COPY copies archived tags from specific version", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
		// v1: put with tags.
		meta1, err := s.PutObject("b", "obj.txt", strings.NewReader("v1"), "text/plain",
			nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		require.NoError(t, s.PutObjectTagging("b", "obj.txt", []Tag{{Key: "ver", Value: "one"}}))
		// Overwrite → v1 is archived (along with its .tags.json) by our fix.
		_, err = s.PutObject("b", "obj.txt", strings.NewReader("v2"), "text/plain",
			nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		// Copy specifically from v1 (archived).
		require.NoError(t, s.CreateBucket("dst", "", false))
		_, err = s.CopyObject("b", "obj.txt", meta1.VersionID, "dst", "copy.txt",
			"", nil, "", "", false, "", nil, nil, "", nil)
		require.NoError(t, err)
		tags, err := s.GetObjectTagging("dst", "copy.txt")
		require.NoError(t, err)
		require.Len(t, tags, 1)
		assert.Equal(t, "ver", tags[0].Key)
		assert.Equal(t, "one", tags[0].Value)
	})
}

func TestApplyTagsLocked(t *testing.T) {
	t.Run("removeFile error is returned", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.PutObject("b", "obj.txt", strings.NewReader("data"), "text/plain",
			nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		removeErr := errors.New("remove failed")
		s.removeFile = func(_ string) error { return removeErr }
		// applyTagsLocked with empty tags triggers removeFile.
		_, err = s.CopyObject("b", "obj.txt", "", "b", "dst.txt",
			"", nil, "", "", false, "", nil, nil, "", []Tag{})
		assert.ErrorIs(t, err, removeErr)
	})

	t.Run("same-path REPLACE applyTagsLocked error is returned", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.PutObject("b", "obj.txt", strings.NewReader("data"), "text/plain",
			nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		tagErr := errors.New("tag write failure")
		realOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".tags.json") {
				return nil, tagErr
			}
			return realOpenFile(name, flag, perm)
		}
		_, err = s.CopyObject("b", "obj.txt", "", "b", "obj.txt",
			"", nil, "", "", false, "", nil, nil, "", []Tag{{Key: "k", Value: "v"}})
		assert.ErrorIs(t, err, tagErr)
	})

	t.Run("writeObject failure in CopyObject is returned", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("src", "", false))
		require.NoError(t, s.CreateBucket("dst", "", false))
		_, err := s.PutObject("src", "obj.txt", strings.NewReader("data"), "text/plain",
			nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		writeErr := errors.New("write failure")
		realOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasPrefix(name, "dst/") && !strings.HasSuffix(name, ".meta.json") &&
				!strings.HasSuffix(name, ".tags.json") {
				return nil, writeErr
			}
			return realOpenFile(name, flag, perm)
		}
		_, err = s.CopyObject("src", "obj.txt", "", "dst", "copy.txt",
			"", nil, "", "", false, "", nil, nil, "", nil)
		assert.ErrorIs(t, err, writeErr)
	})
}

func TestStorageClass(t *testing.T) {
	t.Run("PutObject stores storage class in object metadata", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.PutObject("b", "obj.txt", strings.NewReader("data"), "text/plain",
			nil, "", "", false, "", nil, nil, "GLACIER",
		)
		require.NoError(t, err)
		meta, err := s.HeadObject("b", "obj.txt")
		require.NoError(t, err)
		assert.Equal(t, "GLACIER", meta.StorageClass)
	})

	t.Run("PutObject with empty storage class stores empty string", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.PutObject("b", "obj.txt", strings.NewReader("data"), "text/plain",
			nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)
		meta, err := s.HeadObject("b", "obj.txt")
		require.NoError(t, err)
		assert.Equal(t, "", meta.StorageClass)
	})

	t.Run("CopyObject inherits storage class from source when not specified", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.PutObject("b", "src.txt", strings.NewReader("data"), "text/plain",
			nil, "", "", false, "", nil, nil, "GLACIER",
		)
		require.NoError(t, err)
		_, err = s.CopyObject(
			"b",
			"src.txt",
			"",
			"b",
			"dst.txt",
			"",
			nil,
			"",
			"",
			false,
			"",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		meta, err := s.HeadObject("b", "dst.txt")
		require.NoError(t, err)
		assert.Equal(t, "GLACIER", meta.StorageClass)
	})

	t.Run("CopyObject overrides storage class when specified", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.PutObject("b", "src.txt", strings.NewReader("data"), "text/plain",
			nil, "", "", false, "", nil, nil, "GLACIER",
		)
		require.NoError(t, err)
		_, err = s.CopyObject(
			"b",
			"src.txt",
			"",
			"b",
			"dst.txt",
			"",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"STANDARD",
			nil,
		)
		require.NoError(t, err)
		meta, err := s.HeadObject("b", "dst.txt")
		require.NoError(t, err)
		assert.Equal(t, "STANDARD", meta.StorageClass)
	})

	t.Run("ListObjectVersions returns StorageClass for versioned objects", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", true))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
		_, err := s.PutObject("b", "obj.txt", strings.NewReader("data"), "text/plain",
			nil, "", "", false, "", nil, nil, "GLACIER",
		)
		require.NoError(t, err)

		versions, _, err := s.ListObjectVersions("b")
		require.NoError(t, err)
		require.Len(t, versions, 1)
		assert.Equal(t, "GLACIER", versions[0].StorageClass)
	})

	t.Run("ListMultipartUploads returns StorageClass from upload metadata", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.CreateMultipartUpload(
			"b",
			"key",
			"text/plain",
			"",
			"",
			false,
			"",
			nil,
			nil,
			"GLACIER",
			nil,
		)
		require.NoError(t, err)

		uploads, err := s.ListMultipartUploads("b")
		require.NoError(t, err)
		require.Len(t, uploads, 1)
		assert.Equal(t, "GLACIER", uploads[0].StorageClass)
	})
}

func TestGetObject(t *testing.T) {
	t.Run("returns file and metadata for existing object", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		meta, err := s.PutObject(
			"my-bucket",
			"hello.txt",
			strings.NewReader("hello world"),
			"text/plain",
			nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)

		f, gotMeta, err := s.GetObject("my-bucket", "hello.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		assert.Equal(t, meta.Size, gotMeta.Size)
	})

	t.Run("returns ErrObjectNotFound when object does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))

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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))

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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		require.NoError(t, s.DeleteObject("my-bucket", "obj.txt", false))

		_, _, err = s.GetObject("my-bucket", "obj.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns ErrObjectNotFound when object does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		assert.ErrorIs(t, s.DeleteObject("my-bucket", "missing.txt", false), ErrObjectNotFound)
	})

	t.Run("returns ErrBucketNotFound when bucket does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		assert.ErrorIs(t, s.DeleteObject("no-bucket", "obj.txt", false), ErrBucketNotFound)
	})

	t.Run("returns error when object removal fails", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(
			t,
			os.MkdirAll(filepath.Join(rootPath, "my-bucket", "dir-obj", "child"), 0o750),
		)

		err := s.DeleteObject("my-bucket", "dir-obj", false)
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("logs warning when metadata removal fails but still succeeds", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		require.NoError(t, os.Remove(filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json")))
		require.NoError(t, os.MkdirAll(
			filepath.Join(rootPath, "my-bucket", "obj.txt.meta.json", "child"),
			0o750,
		))

		assert.NoError(t, s.DeleteObject("my-bucket", "obj.txt", false))
	})

	t.Run("logs warning when tags removal fails but still succeeds", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		require.NoError(
			t,
			s.PutObjectTagging("my-bucket", "obj.txt", []Tag{{Key: "k", Value: "v"}}),
		)

		require.NoError(t, os.Remove(filepath.Join(rootPath, "my-bucket", "obj.txt.tags.json")))
		require.NoError(t, os.MkdirAll(
			filepath.Join(rootPath, "my-bucket", "obj.txt.tags.json", "child"),
			0o750,
		))

		assert.NoError(t, s.DeleteObject("my-bucket", "obj.txt", false))
	})

	t.Run("deletes tags file when object is deleted", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		require.NoError(
			t,
			s.PutObjectTagging("my-bucket", "obj.txt", []Tag{{Key: "k", Value: "v"}}),
		)

		require.NoError(t, s.DeleteObject("my-bucket", "obj.txt", false))

		_, statErr := os.Stat(filepath.Join(rootPath, "my-bucket", "obj.txt.tags.json"))
		assert.True(t, os.IsNotExist(statErr), "tags file should be removed with the object")
	})

	t.Run("DeleteBucket succeeds after deleting tagged object", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		require.NoError(
			t,
			s.PutObjectTagging("my-bucket", "obj.txt", []Tag{{Key: "k", Value: "v"}}),
		)
		require.NoError(t, s.DeleteObject("my-bucket", "obj.txt", false))

		assert.NoError(t, s.DeleteBucket("my-bucket"))
	})

	t.Run("prunes empty parent directories after deleting nested-key object", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"a/b/obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		require.NoError(t, s.DeleteObject("my-bucket", "a/b/obj.txt", false))

		assert.NoDirExists(t, filepath.Join(rootPath, "my-bucket", "a"))
		assert.NoError(t, s.DeleteBucket("my-bucket"))
	})

	t.Run("does not prune parent directory when sibling object exists", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		for _, key := range []string{"a/obj1.txt", "a/obj2.txt"} {
			_, err := s.PutObject(
				"my-bucket",
				key,
				strings.NewReader("data"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
		}

		require.NoError(t, s.DeleteObject("my-bucket", "a/obj1.txt", false))

		assert.DirExists(t, filepath.Join(rootPath, "my-bucket", "a"))
	})

	t.Run(
		"returns ErrObjectLocked when object has active GOVERNANCE retention",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", true))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("data"),
				"text/plain",
				nil,
				"",
				"", false, "",
				&ObjectRetention{Mode: "GOVERNANCE", RetainUntilDate: time.Now().Add(time.Hour)},
				nil,
				"",
			)
			require.NoError(t, err)

			assert.ErrorIs(t, s.DeleteObject("my-bucket", "obj.txt", false), ErrObjectLocked)
		},
	)
}

func TestHeadObject(t *testing.T) {
	t.Run("returns metadata for existing object", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		meta, err := s.HeadObject("my-bucket", "obj.txt")
		require.NoError(t, err)
		assert.Equal(t, int64(4), meta.Size)
	})

	t.Run("returns ErrObjectNotFound when object does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"c.txt",
			strings.NewReader("c"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		_, err = s.PutObject(
			"my-bucket",
			"a.txt",
			strings.NewReader("a"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		_, err = s.PutObject(
			"my-bucket",
			"b.txt",
			strings.NewReader("b"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"real.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"subdir/obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil, "", "", false, "", nil, nil, "",
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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"data.json",
			strings.NewReader("{}"),
			"application/json",
			nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)

		objects, err := s.ListObjects("my-bucket")
		require.NoError(t, err)
		require.Len(t, objects, 1)
		assert.Equal(t, "data.json", objects[0].Key)
	})

	t.Run("does not list tags sidecar files as objects", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
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
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		return s, rootPath
	}

	t.Run("full lifecycle: create, upload parts, complete", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"big.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		assert.NotEmpty(t, uploadID)

		// Part 1 must be >= 5 MiB (AWS minimum for non-final parts).
		part1Data := strings.Repeat("x", minPartSize) + "hello "
		etag1, err := s.UploadPart(uploadID, 1, strings.NewReader(part1Data))
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
		assert.Equal(t, part1Data+"world", string(data))
	})

	t.Run("abort cleans up temp files", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"big.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)

		require.NoError(t, s.AbortMultipartUpload(uploadID))

		_, err = os.Stat(filepath.Join(rootPath, mpuDir, uploadID))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("complete removes temp files", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"big.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		_, err := s.CreateMultipartUpload(
			"no-bucket",
			"key",
			"text/plain",
			"",
			"",
			false,
			"",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"big.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{})
		assert.ErrorIs(t, err, ErrInvalidPart)
	})

	t.Run("complete returns ErrInvalidPartOrder for non-ascending parts", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"big.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		etag1, err := s.UploadPart(uploadID, 1, strings.NewReader("a"))
		require.NoError(t, err)
		// Part 2 must be >= 5 MiB so the size check on the non-final submitted part
		// does not fire before the order check validates {2, 1} as descending.
		etag2, err := s.UploadPart(uploadID, 2, strings.NewReader(strings.Repeat("b", minPartSize)))
		require.NoError(t, err)
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{
			{PartNumber: 2, ETag: etag2},
			{PartNumber: 1, ETag: etag1},
		})
		assert.ErrorIs(t, err, ErrInvalidPartOrder)
	})

	t.Run("complete returns ErrInvalidPart for wrong ETag", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"big.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"big.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{
			{PartNumber: 1, ETag: `"abc"`},
		})
		assert.ErrorIs(t, err, ErrInvalidPart)
	})

	t.Run("complete returns ErrEntityTooSmall for non-final part below 5MiB", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"big.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		etag1, err := s.UploadPart(uploadID, 1, strings.NewReader("small")) // < 5 MiB
		require.NoError(t, err)
		etag2, err := s.UploadPart(uploadID, 2, strings.NewReader("last"))
		require.NoError(t, err)
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{
			{PartNumber: 1, ETag: etag1},
			{PartNumber: 2, ETag: etag2},
		})
		assert.ErrorIs(t, err, ErrEntityTooSmall)
	})

	t.Run("complete allows single part below 5MiB", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"small.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		etag, err := s.UploadPart(uploadID, 1, strings.NewReader("tiny"))
		require.NoError(t, err)
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{
			{PartNumber: 1, ETag: etag},
		})
		assert.NoError(t, err)
	})

	t.Run(
		"complete allows final part below 5MiB when preceding parts are large enough",
		func(t *testing.T) {
			s, _ := setup(t)
			uploadID, err := s.CreateMultipartUpload(
				"my-bucket",
				"mixed.txt",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)
			bigPart := strings.NewReader(strings.Repeat("x", minPartSize))
			etag1, err := s.UploadPart(uploadID, 1, bigPart)
			require.NoError(t, err)
			etag2, err := s.UploadPart(uploadID, 2, strings.NewReader("last-small"))
			require.NoError(t, err)
			_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{
				{PartNumber: 1, ETag: etag1},
				{PartNumber: 2, ETag: etag2},
			})
			assert.NoError(t, err)
		},
	)

	t.Run("abort returns ErrUploadNotFound for unknown uploadId", func(t *testing.T) {
		s, _ := setup(t)
		err := s.AbortMultipartUpload("nonexistent-id")
		assert.ErrorIs(t, err, ErrUploadNotFound)
	})

	t.Run("ListBuckets does not expose .mpu directory", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
			uploadID, err := s.CreateMultipartUpload(
				"my-bucket",
				"key",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"path/to/big.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		assert.Error(t, err)
	})

	t.Run("create returns error when mpu directory cannot be created", func(t *testing.T) {
		s, rootPath := setup(t)
		// Place a regular file at .mpu to block MkdirAll.
		require.NoError(t, os.WriteFile(filepath.Join(rootPath, mpuDir), []byte{}, 0o600))
		_, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"",
			false,
			"",
			nil,
			nil,
			"",
			nil,
		)
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
		_, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"",
			false,
			"",
			nil,
			nil,
			"",
			nil,
		)
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
		_, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"",
			false,
			"",
			nil,
			nil,
			"",
			nil,
		)
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
		_, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"",
			false,
			"",
			nil,
			nil,
			"",
			nil,
		)
		assert.Error(t, err)
		assert.NoDirExists(t, capturedUploadDir)
	})

	t.Run("upload part returns error when io.Copy fails", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		assert.Error(t, err)
	})

	t.Run("complete returns error when readUploadMeta readAll fails", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		s.readAll = func(_ io.Reader) ([]byte, error) { return nil, errors.New("read error") }
		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: `"abc"`}})
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrUploadNotFound)
	})

	t.Run("complete returns error when part meta is corrupt JSON", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
			uploadID, err := s.CreateMultipartUpload(
				"my-bucket",
				"key",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		_, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"",
			false,
			"",
			nil,
			nil,
			"",
			nil,
		)
		assert.Error(t, err)
	})

	t.Run("upload part returns error on non-ErrNotExist stat failure", func(t *testing.T) {
		s, rootPath := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"a/b/big.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
			uploadID, err := s.CreateMultipartUpload(
				"my-bucket",
				"big.txt",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
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
			_, err := s.CreateMultipartUpload(
				"my-bucket",
				"key",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
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
		_, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"",
			false,
			"",
			nil,
			nil,
			"",
			nil,
		)
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
		require.NoError(t, s.CreateBucket("other-bucket", "", false))
		uploadID1, err := s.CreateMultipartUpload(
			"my-bucket",
			"key1",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		_, err = s.CreateMultipartUpload(
			"other-bucket",
			"key2",
			"text/plain",
			"",
			"",
			false,
			"",
			nil,
			nil,
			"",
			nil,
		)
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
			uploadID, err := s.CreateMultipartUpload(
				"my-bucket",
				"key",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
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

	t.Run(
		"CompleteMultipartUpload on versioning-enabled bucket assigns versionId",
		func(t *testing.T) {
			s, _ := setup(t)
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))

			uploadID, err := s.CreateMultipartUpload(
				"my-bucket",
				"big.txt",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)
			etag1, err := s.UploadPart(
				uploadID,
				1,
				strings.NewReader(strings.Repeat("x", minPartSize)),
			)
			require.NoError(t, err)
			etag2, err := s.UploadPart(uploadID, 2, strings.NewReader("world"))
			require.NoError(t, err)

			meta, err := s.CompleteMultipartUpload(uploadID, []CompletePart{
				{PartNumber: 1, ETag: etag1},
				{PartNumber: 2, ETag: etag2},
			})
			require.NoError(t, err)
			assert.NotEmpty(t, meta.VersionID)
			assert.Contains(t, meta.ETag, "-2")
		},
	)

	t.Run("DeletePart removes part and meta files", func(t *testing.T) {
		s, _ := setup(t)
		uploadID, err := s.CreateMultipartUpload(
			"my-bucket",
			"key",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
		require.NoError(t, err)
		require.NoError(t, s.DeletePart(uploadID, 1))

		_, parts, err := s.ListParts(uploadID)
		require.NoError(t, err)
		assert.Empty(t, parts)
	})

	t.Run(
		"DeletePart logs warning when meta file removal fails with non-NotExist error",
		func(t *testing.T) {
			s, _ := setup(t)
			uploadID, err := s.CreateMultipartUpload(
				"my-bucket",
				"key",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)
			_, err = s.UploadPart(uploadID, 1, strings.NewReader("data"))
			require.NoError(t, err)
			origRemoveFile := s.removeFile
			s.removeFile = func(name string) error {
				if strings.HasSuffix(name, ".meta.json") {
					return errors.New("permission denied")
				}
				return origRemoveFile(name)
			}
			require.NoError(t, s.DeletePart(uploadID, 1))
		},
	)
}

func TestObjectTagging(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("bucket", "us-east-1", false))
		_, err := s.PutObject(
			"bucket",
			"key.txt",
			strings.NewReader("hello"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
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

func TestBucketTagging(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("bucket", "us-east-1", false))
		return s, "bucket"
	}

	t.Run("PutBucketTagging and GetBucketTagging roundtrip", func(t *testing.T) {
		s, bucket := setup(t)
		tags := []Tag{{Key: "env", Value: "prod"}, {Key: "team", Value: "backend"}}
		require.NoError(t, s.PutBucketTagging(bucket, tags))
		got, err := s.GetBucketTagging(bucket)
		require.NoError(t, err)
		assert.Equal(t, tags, got)
	})

	t.Run("GetBucketTagging returns empty slice when no tags set", func(t *testing.T) {
		s, bucket := setup(t)
		got, err := s.GetBucketTagging(bucket)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("PutBucketTagging preserves bucket region", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketTagging(bucket, []Tag{{Key: "k", Value: "v"}}))
		region, err := s.GetBucketRegion(bucket)
		require.NoError(t, err)
		assert.Equal(t, "us-east-1", region)
	})

	t.Run("DeleteBucketTagging removes tags", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketTagging(bucket, []Tag{{Key: "k", Value: "v"}}))
		require.NoError(t, s.DeleteBucketTagging(bucket))
		got, err := s.GetBucketTagging(bucket)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("DeleteBucketTagging on bucket with no tags is a no-op", func(t *testing.T) {
		s, bucket := setup(t)
		assert.NoError(t, s.DeleteBucketTagging(bucket))
	})

	t.Run("PutBucketTagging creates fresh meta when bucket.json is missing", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.root.Remove(bucket+".bucket.json"))
		require.NoError(t, s.PutBucketTagging(bucket, []Tag{{Key: "k", Value: "v"}}))
		got, err := s.GetBucketTagging(bucket)
		require.NoError(t, err)
		assert.Equal(t, []Tag{{Key: "k", Value: "v"}}, got)
	})

	t.Run("GetBucketTagging returns empty slice when bucket.json is missing", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.root.Remove(bucket+".bucket.json"))
		got, err := s.GetBucketTagging(bucket)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("DeleteBucketTagging is no-op when bucket.json is missing", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.root.Remove(bucket+".bucket.json"))
		assert.NoError(t, s.DeleteBucketTagging(bucket))
	})

	t.Run("PutBucketTagging returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.PutBucketTagging("no-bucket", []Tag{})
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("GetBucketTagging returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.GetBucketTagging("no-bucket")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("DeleteBucketTagging returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.DeleteBucketTagging("no-bucket")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("PutBucketTagging returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		err := s.PutBucketTagging(bucket, []Tag{{Key: "k", Value: "v"}})
		assert.Error(t, err)
	})

	t.Run("PutBucketTagging returns error when write fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".bucket.json") {
				return nil, errors.New("write error")
			}
			return s.root.OpenFile(name, flag, perm)
		}
		err := s.PutBucketTagging(bucket, []Tag{{Key: "k", Value: "v"}})
		assert.Error(t, err)
	})

	t.Run("GetBucketTagging returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		_, err := s.GetBucketTagging(bucket)
		assert.Error(t, err)
	})

	t.Run("DeleteBucketTagging returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		err := s.DeleteBucketTagging(bucket)
		assert.Error(t, err)
	})

	t.Run("DeleteBucketTagging returns error when write fails", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketTagging(bucket, []Tag{{Key: "k", Value: "v"}}))
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".bucket.json") {
				return nil, errors.New("write error")
			}
			return s.root.OpenFile(name, flag, perm)
		}
		err := s.DeleteBucketTagging(bucket)
		assert.Error(t, err)
	})
}

func TestBucketCORS(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s := newTestStorage(t)
		bucket := "test-bucket"
		require.NoError(t, s.CreateBucket(bucket, "us-east-1", false))
		return s, bucket
	}

	rules := []CORSRule{
		{
			ID:             "rule1",
			AllowedOrigins: []string{"http://example.com"},
			AllowedMethods: []string{"GET", "PUT"},
			AllowedHeaders: []string{"*"},
			ExposeHeaders:  []string{"x-amz-meta-custom"},
			MaxAgeSeconds:  3000,
		},
	}

	t.Run("PutBucketCors and GetBucketCors roundtrip", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketCors(bucket, rules))
		got, err := s.GetBucketCors(bucket)
		require.NoError(t, err)
		assert.Equal(t, rules, got)
	})

	t.Run("GetBucketCors returns ErrNoCORSConfiguration when not set", func(t *testing.T) {
		s, bucket := setup(t)
		_, err := s.GetBucketCors(bucket)
		assert.ErrorIs(t, err, ErrNoCORSConfiguration)
	})

	t.Run(
		"GetBucketCors returns ErrNoCORSConfiguration when bucket.json is missing",
		func(t *testing.T) {
			s, bucket := setup(t)
			require.NoError(t, s.root.Remove(bucket+".bucket.json"))
			_, err := s.GetBucketCors(bucket)
			assert.ErrorIs(t, err, ErrNoCORSConfiguration)
		},
	)

	t.Run("PutBucketCors preserves existing bucket tags", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketTagging(bucket, []Tag{{Key: "env", Value: "prod"}}))
		require.NoError(t, s.PutBucketCors(bucket, rules))
		tags, err := s.GetBucketTagging(bucket)
		require.NoError(t, err)
		assert.Equal(t, []Tag{{Key: "env", Value: "prod"}}, tags)
	})

	t.Run("PutBucketCors creates fresh meta when bucket.json is missing", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.root.Remove(bucket+".bucket.json"))
		require.NoError(t, s.PutBucketCors(bucket, rules))
		got, err := s.GetBucketCors(bucket)
		require.NoError(t, err)
		assert.Equal(t, rules, got)
	})

	t.Run("DeleteBucketCors removes rules", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketCors(bucket, rules))
		require.NoError(t, s.DeleteBucketCors(bucket))
		_, err := s.GetBucketCors(bucket)
		assert.ErrorIs(t, err, ErrNoCORSConfiguration)
	})

	t.Run("DeleteBucketCors is idempotent when not set", func(t *testing.T) {
		s, bucket := setup(t)
		assert.NoError(t, s.DeleteBucketCors(bucket))
	})

	t.Run("DeleteBucketCors is idempotent when bucket.json is missing", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.root.Remove(bucket+".bucket.json"))
		assert.NoError(t, s.DeleteBucketCors(bucket))
	})

	t.Run("PutBucketCors returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.PutBucketCors("no-bucket", rules)
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("GetBucketCors returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.GetBucketCors("no-bucket")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("DeleteBucketCors returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.DeleteBucketCors("no-bucket")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("PutBucketCors returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		err := s.PutBucketCors(bucket, rules)
		assert.Error(t, err)
	})

	t.Run("PutBucketCors returns error when write fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".bucket.json") {
				return nil, errors.New("write error")
			}
			return s.root.OpenFile(name, flag, perm)
		}
		err := s.PutBucketCors(bucket, rules)
		assert.Error(t, err)
	})

	t.Run("GetBucketCors returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketCors(bucket, rules))
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		_, err := s.GetBucketCors(bucket)
		assert.Error(t, err)
	})

	t.Run("DeleteBucketCors returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketCors(bucket, rules))
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		err := s.DeleteBucketCors(bucket)
		assert.Error(t, err)
	})

	t.Run("DeleteBucketCors returns error when write fails", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketCors(bucket, rules))
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".bucket.json") {
				return nil, errors.New("write error")
			}
			return s.root.OpenFile(name, flag, perm)
		}
		err := s.DeleteBucketCors(bucket)
		assert.Error(t, err)
	})
}

func TestBucketVersioning(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s := newTestStorage(t)
		bucket := "test-bucket"
		require.NoError(t, s.CreateBucket(bucket, "us-east-1", false))
		return s, bucket
	}

	t.Run("PutBucketVersioning and GetBucketVersioning roundtrip", func(t *testing.T) {
		tests := []struct {
			name   string
			status string
		}{
			{name: "Enabled", status: "Enabled"},
			{name: "Suspended", status: "Suspended"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s, bucket := setup(t)
				require.NoError(t, s.PutBucketVersioning(bucket, tt.status))
				got, err := s.GetBucketVersioning(bucket)
				require.NoError(t, err)
				assert.Equal(t, tt.status, got)
			})
		}
	})

	t.Run("GetBucketVersioning returns empty string when not set", func(t *testing.T) {
		s, bucket := setup(t)
		status, err := s.GetBucketVersioning(bucket)
		require.NoError(t, err)
		assert.Equal(t, "", status)
	})

	t.Run("PutBucketVersioning preserves existing bucket tags", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketTagging(bucket, []Tag{{Key: "env", Value: "prod"}}))
		require.NoError(t, s.PutBucketVersioning(bucket, "Enabled"))
		tags, err := s.GetBucketTagging(bucket)
		require.NoError(t, err)
		assert.Equal(t, []Tag{{Key: "env", Value: "prod"}}, tags)
	})

	t.Run(
		"GetBucketVersioning returns empty string when bucket.json is missing",
		func(t *testing.T) {
			s, bucket := setup(t)
			require.NoError(t, s.root.Remove(bucket+".bucket.json"))
			status, err := s.GetBucketVersioning(bucket)
			require.NoError(t, err)
			assert.Equal(t, "", status)
		},
	)

	t.Run("PutBucketVersioning creates fresh meta when bucket.json is missing", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.root.Remove(bucket+".bucket.json"))
		require.NoError(t, s.PutBucketVersioning(bucket, "Enabled"))
		status, err := s.GetBucketVersioning(bucket)
		require.NoError(t, err)
		assert.Equal(t, "Enabled", status)
	})

	t.Run("PutBucketVersioning returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.PutBucketVersioning("no-bucket", "Enabled")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("GetBucketVersioning returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.GetBucketVersioning("no-bucket")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("PutBucketVersioning returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		err := s.PutBucketVersioning(bucket, "Enabled")
		assert.Error(t, err)
	})

	t.Run("PutBucketVersioning returns error when write fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".bucket.json") {
				return nil, errors.New("write error")
			}
			return s.root.OpenFile(name, flag, perm)
		}
		err := s.PutBucketVersioning(bucket, "Enabled")
		assert.Error(t, err)
	})

	t.Run("GetBucketVersioning returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		_, err := s.GetBucketVersioning(bucket)
		assert.Error(t, err)
	})
}

func TestBucketPolicy(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s, err := NewStorage(t.TempDir())
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		bucket := "test-bucket"
		require.NoError(t, s.CreateBucket(bucket, "us-east-1", false))
		return s, bucket
	}

	policy := `{"Version":"2012-10-17","Statement":[]}`

	t.Run("PutBucketPolicy and GetBucketPolicy roundtrip", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketPolicy(bucket, policy))
		got, err := s.GetBucketPolicy(bucket)
		require.NoError(t, err)
		assert.Equal(t, policy, got)
	})

	t.Run("GetBucketPolicy returns ErrNoBucketPolicy when not set", func(t *testing.T) {
		s, bucket := setup(t)
		_, err := s.GetBucketPolicy(bucket)
		assert.ErrorIs(t, err, ErrNoBucketPolicy)
	})

	t.Run(
		"GetBucketPolicy returns ErrNoBucketPolicy when bucket.json is missing",
		func(t *testing.T) {
			s, bucket := setup(t)
			require.NoError(t, s.root.Remove(bucket+".bucket.json"))
			_, err := s.GetBucketPolicy(bucket)
			assert.ErrorIs(t, err, ErrNoBucketPolicy)
		},
	)

	t.Run("PutBucketPolicy preserves existing bucket tags", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketTagging(bucket, []Tag{{Key: "env", Value: "prod"}}))
		require.NoError(t, s.PutBucketPolicy(bucket, policy))
		tags, err := s.GetBucketTagging(bucket)
		require.NoError(t, err)
		assert.Equal(t, []Tag{{Key: "env", Value: "prod"}}, tags)
	})

	t.Run("PutBucketPolicy creates fresh meta when bucket.json is missing", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.root.Remove(bucket+".bucket.json"))
		require.NoError(t, s.PutBucketPolicy(bucket, policy))
		got, err := s.GetBucketPolicy(bucket)
		require.NoError(t, err)
		assert.Equal(t, policy, got)
	})

	t.Run("DeleteBucketPolicy removes policy", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketPolicy(bucket, policy))
		require.NoError(t, s.DeleteBucketPolicy(bucket))
		_, err := s.GetBucketPolicy(bucket)
		assert.ErrorIs(t, err, ErrNoBucketPolicy)
	})

	t.Run("DeleteBucketPolicy is idempotent when not set", func(t *testing.T) {
		s, bucket := setup(t)
		assert.NoError(t, s.DeleteBucketPolicy(bucket))
	})

	t.Run("DeleteBucketPolicy is idempotent when bucket.json is missing", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.root.Remove(bucket+".bucket.json"))
		assert.NoError(t, s.DeleteBucketPolicy(bucket))
	})

	t.Run("PutBucketPolicy returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s, _ := setup(t)
		err := s.PutBucketPolicy("no-bucket", policy)
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("GetBucketPolicy returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s, _ := setup(t)
		_, err := s.GetBucketPolicy("no-bucket")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("DeleteBucketPolicy returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s, _ := setup(t)
		err := s.DeleteBucketPolicy("no-bucket")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("PutBucketPolicy returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		err := s.PutBucketPolicy(bucket, policy)
		assert.Error(t, err)
	})

	t.Run("PutBucketPolicy returns error when write fails", func(t *testing.T) {
		s, bucket := setup(t)
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".bucket.json") {
				return nil, errors.New("write error")
			}
			return s.root.OpenFile(name, flag, perm)
		}
		err := s.PutBucketPolicy(bucket, policy)
		assert.Error(t, err)
	})

	t.Run("GetBucketPolicy returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketPolicy(bucket, policy))
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		_, err := s.GetBucketPolicy(bucket)
		assert.Error(t, err)
	})

	t.Run("DeleteBucketPolicy returns error when meta read fails", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketPolicy(bucket, policy))
		s.readAll = func(r io.Reader) ([]byte, error) {
			return nil, errors.New("read error")
		}
		err := s.DeleteBucketPolicy(bucket)
		assert.Error(t, err)
	})

	t.Run("DeleteBucketPolicy returns error when write fails", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.PutBucketPolicy(bucket, policy))
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".bucket.json") {
				return nil, errors.New("write error")
			}
			return s.root.OpenFile(name, flag, perm)
		}
		err := s.DeleteBucketPolicy(bucket)
		assert.Error(t, err)
	})
}

func TestBucketConfigStorage(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s, err := NewStorage(t.TempDir())
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		require.NoError(t, s.CreateBucket("b", "", false))
		return s, "b"
	}

	const xmlBody = `<Cfg><X>1</X></Cfg>`

	// configs with Put / Get / Delete
	type configWithDelete struct {
		name   string
		put    func(s *Storage, bucket, xml string) error
		get    func(s *Storage, bucket string) (string, error)
		delete func(s *Storage, bucket string) error
	}
	deleteConfigs := []configWithDelete{
		{
			"PublicAccessBlock",
			func(s *Storage, b, x string) error { return s.PutPublicAccessBlock(b, x) },
			func(s *Storage, b string) (string, error) { return s.GetPublicAccessBlock(b) },
			func(s *Storage, b string) error { return s.DeletePublicAccessBlock(b) },
		},
		{
			"Encryption",
			func(s *Storage, b, x string) error { return s.PutBucketEncryption(b, x) },
			func(s *Storage, b string) (string, error) { return s.GetBucketEncryption(b) },
			func(s *Storage, b string) error { return s.DeleteBucketEncryption(b) },
		},
		{
			"OwnershipControls",
			func(s *Storage, b, x string) error { return s.PutBucketOwnershipControls(b, x) },
			func(s *Storage, b string) (string, error) { return s.GetBucketOwnershipControls(b) },
			func(s *Storage, b string) error { return s.DeleteBucketOwnershipControls(b) },
		},
		{
			"Lifecycle",
			func(s *Storage, b, x string) error { return s.PutBucketLifecycle(b, x) },
			func(s *Storage, b string) (string, error) { return s.GetBucketLifecycle(b) },
			func(s *Storage, b string) error { return s.DeleteBucketLifecycle(b) },
		},
		{
			"Website",
			func(s *Storage, b, x string) error { return s.PutBucketWebsite(b, x) },
			func(s *Storage, b string) (string, error) { return s.GetBucketWebsite(b) },
			func(s *Storage, b string) error { return s.DeleteBucketWebsite(b) },
		},
		{
			"Replication",
			func(s *Storage, b, x string) error { return s.PutBucketReplication(b, x) },
			func(s *Storage, b string) (string, error) { return s.GetBucketReplication(b) },
			func(s *Storage, b string) error { return s.DeleteBucketReplication(b) },
		},
	}

	for _, c := range deleteConfigs {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Run("Put/Get roundtrip", func(t *testing.T) {
				s, bucket := setup(t)
				require.NoError(t, c.put(s, bucket, xmlBody))
				got, err := c.get(s, bucket)
				require.NoError(t, err)
				assert.Equal(t, xmlBody, got)
			})

			t.Run("Get returns empty string when not set", func(t *testing.T) {
				s, bucket := setup(t)
				got, err := c.get(s, bucket)
				require.NoError(t, err)
				assert.Empty(t, got)
			})

			t.Run("Delete clears value", func(t *testing.T) {
				s, bucket := setup(t)
				require.NoError(t, c.put(s, bucket, xmlBody))
				require.NoError(t, c.delete(s, bucket))
				got, err := c.get(s, bucket)
				require.NoError(t, err)
				assert.Empty(t, got)
			})

			t.Run("Put returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
				s, _ := setup(t)
				assert.ErrorIs(t, c.put(s, "no-bucket", xmlBody), ErrBucketNotFound)
			})

			t.Run("Get returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
				s, _ := setup(t)
				_, err := c.get(s, "no-bucket")
				assert.ErrorIs(t, err, ErrBucketNotFound)
			})

			t.Run("Delete returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
				s, _ := setup(t)
				assert.ErrorIs(t, c.delete(s, "no-bucket"), ErrBucketNotFound)
			})
		})
	}

	// configs with Put / Get only (no Delete)
	type configGetOnly struct {
		name string
		put  func(s *Storage, bucket, xml string) error
		get  func(s *Storage, bucket string) (string, error)
	}
	getOnlyConfigs := []configGetOnly{
		{
			"Notification",
			func(s *Storage, b, x string) error { return s.PutBucketNotification(b, x) },
			func(s *Storage, b string) (string, error) { return s.GetBucketNotification(b) },
		},
		{
			"Logging",
			func(s *Storage, b, x string) error { return s.PutBucketLogging(b, x) },
			func(s *Storage, b string) (string, error) { return s.GetBucketLogging(b) },
		},
		{
			"Accelerate",
			func(s *Storage, b, x string) error { return s.PutBucketAccelerate(b, x) },
			func(s *Storage, b string) (string, error) { return s.GetBucketAccelerate(b) },
		},
		{
			"RequestPayment",
			func(s *Storage, b, x string) error { return s.PutBucketRequestPayment(b, x) },
			func(s *Storage, b string) (string, error) { return s.GetBucketRequestPayment(b) },
		},
	}

	for _, c := range getOnlyConfigs {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Run("Put/Get roundtrip", func(t *testing.T) {
				s, bucket := setup(t)
				require.NoError(t, c.put(s, bucket, xmlBody))
				got, err := c.get(s, bucket)
				require.NoError(t, err)
				assert.Equal(t, xmlBody, got)
			})

			t.Run("Get returns empty string when not set", func(t *testing.T) {
				s, bucket := setup(t)
				got, err := c.get(s, bucket)
				require.NoError(t, err)
				assert.Empty(t, got)
			})

			t.Run("Put returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
				s, _ := setup(t)
				assert.ErrorIs(t, c.put(s, "no-bucket", xmlBody), ErrBucketNotFound)
			})

			t.Run("Get returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
				s, _ := setup(t)
				_, err := c.get(s, "no-bucket")
				assert.ErrorIs(t, err, ErrBucketNotFound)
			})
		})
	}

	// ObjectLock requires versioning to be enabled — tested separately.
	t.Run("ObjectLock", func(t *testing.T) {
		const lockXML = `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`

		t.Run("Put/Get roundtrip with versioning enabled", func(t *testing.T) {
			s, bucket := setup(t)
			require.NoError(t, s.PutBucketVersioning(bucket, "Enabled"))
			require.NoError(t, s.PutBucketObjectLock(bucket, lockXML))
			got, err := s.GetBucketObjectLock(bucket)
			require.NoError(t, err)
			assert.Equal(t, lockXML, got)
		})

		t.Run("Put returns ErrInvalidBucketState when versioning not enabled", func(t *testing.T) {
			s, bucket := setup(t)
			assert.ErrorIs(t, s.PutBucketObjectLock(bucket, lockXML), ErrInvalidBucketState)
		})

		t.Run("Put returns ErrInvalidBucketState when versioning suspended", func(t *testing.T) {
			s, bucket := setup(t)
			require.NoError(t, s.PutBucketVersioning(bucket, "Suspended"))
			assert.ErrorIs(t, s.PutBucketObjectLock(bucket, lockXML), ErrInvalidBucketState)
		})

		t.Run("Get returns empty string when not set", func(t *testing.T) {
			s, bucket := setup(t)
			got, err := s.GetBucketObjectLock(bucket)
			require.NoError(t, err)
			assert.Empty(t, got)
		})

		t.Run("Put returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
			s, _ := setup(t)
			assert.ErrorIs(t, s.PutBucketObjectLock("no-bucket", lockXML), ErrBucketNotFound)
		})

		t.Run("Get returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
			s, _ := setup(t)
			_, err := s.GetBucketObjectLock("no-bucket")
			assert.ErrorIs(t, err, ErrBucketNotFound)
		})

		t.Run("Put returns ErrInvalidBucketState when bucket.json is missing", func(t *testing.T) {
			// os.ErrNotExist from readBucketMeta → falls back to bucketMeta{} →
			// VersioningStatus == "" → ErrInvalidBucketState.
			s, bucket := setup(t)
			require.NoError(t, s.root.Remove(bucket+".bucket.json"))
			assert.ErrorIs(t, s.PutBucketObjectLock(bucket, lockXML), ErrInvalidBucketState)
		})

		t.Run(
			"Put returns error when meta read fails with non-ErrNotExist error",
			func(t *testing.T) {
				s, bucket := setup(t)
				s.readAll = func(r io.Reader) ([]byte, error) {
					return nil, errors.New("read error")
				}
				assert.Error(t, s.PutBucketObjectLock(bucket, lockXML))
			},
		)
	})

	t.Run("parseBucketDefaultRetention", func(t *testing.T) {
		now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

		cases := []struct {
			name      string
			xml       string
			wantNil   bool
			wantMode  string
			wantUntil time.Time
		}{
			{
				name:    "empty string",
				xml:     "",
				wantNil: true,
			},
			{
				name:    "no Rule element",
				xml:     `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`,
				wantNil: true,
			},
			{
				name:    "invalid XML",
				xml:     `not-xml`,
				wantNil: true,
			},
			{
				name:    "Days=0 and Years=0",
				xml:     `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>COMPLIANCE</Mode></DefaultRetention></Rule></ObjectLockConfiguration>`,
				wantNil: true,
			},
			{
				name:    "invalid mode",
				xml:     `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>INVALID</Mode><Days>1</Days></DefaultRetention></Rule></ObjectLockConfiguration>`,
				wantNil: true,
			},
			{
				name:      "COMPLIANCE mode with Days",
				xml:       `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>COMPLIANCE</Mode><Days>10</Days></DefaultRetention></Rule></ObjectLockConfiguration>`,
				wantNil:   false,
				wantMode:  "COMPLIANCE",
				wantUntil: now.AddDate(0, 0, 10),
			},
			{
				name:      "GOVERNANCE mode with Years",
				xml:       `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>GOVERNANCE</Mode><Years>2</Years></DefaultRetention></Rule></ObjectLockConfiguration>`,
				wantNil:   false,
				wantMode:  "GOVERNANCE",
				wantUntil: now.AddDate(2, 0, 0),
			},
			{
				name:      "Days and Years both set — Days wins",
				xml:       `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>COMPLIANCE</Mode><Days>7</Days><Years>1</Years></DefaultRetention></Rule></ObjectLockConfiguration>`,
				wantNil:   false,
				wantMode:  "COMPLIANCE",
				wantUntil: now.AddDate(0, 0, 7),
			},
			{
				name:    "Days exceeds maximum (36500) — treated as invalid",
				xml:     `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>COMPLIANCE</Mode><Days>36501</Days></DefaultRetention></Rule></ObjectLockConfiguration>`,
				wantNil: true,
			},
			{
				name:    "Years exceeds maximum (100) — treated as invalid",
				xml:     `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>GOVERNANCE</Mode><Years>101</Years></DefaultRetention></Rule></ObjectLockConfiguration>`,
				wantNil: true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := parseBucketDefaultRetention(tc.xml, now)
				if tc.wantNil {
					assert.Nil(t, got)
					return
				}
				require.NotNil(t, got)
				assert.Equal(t, tc.wantMode, got.Mode)
				assert.True(
					t,
					tc.wantUntil.Equal(got.RetainUntilDate),
					"retain until: got %v want %v",
					got.RetainUntilDate,
					tc.wantUntil,
				)
			})
		}
	})

	t.Run(
		"PutObject applies bucket default retention when no explicit retention",
		func(t *testing.T) {
			const defaultRetentionXML = `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>COMPLIANCE</Mode><Days>5</Days></DefaultRetention></Rule></ObjectLockConfiguration>`
			now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

			t.Run("applies default retention when retention is nil", func(t *testing.T) {
				s, bucket := setup(t)
				s.now = func() time.Time { return now }
				require.NoError(t, s.PutBucketVersioning(bucket, "Enabled"))
				require.NoError(t, s.PutBucketObjectLock(bucket, defaultRetentionXML))

				_, err := s.PutObject(
					bucket,
					"key",
					strings.NewReader("data"),
					"text/plain",
					nil,
					"",
					"", false, "",
					nil,
					nil,
					"",
				)
				require.NoError(t, err)

				ret, err := s.GetObjectRetention(bucket, "key", "")
				require.NoError(t, err)
				assert.Equal(t, "COMPLIANCE", ret.Mode)
				assert.True(t, now.AddDate(0, 0, 5).Equal(ret.RetainUntilDate))
			})

			t.Run(
				"explicit retention header takes precedence over bucket default",
				func(t *testing.T) {
					s, bucket := setup(t)
					s.now = func() time.Time { return now }
					require.NoError(t, s.PutBucketVersioning(bucket, "Enabled"))
					require.NoError(t, s.PutBucketObjectLock(bucket, defaultRetentionXML))

					explicit := &ObjectRetention{
						Mode:            "GOVERNANCE",
						RetainUntilDate: now.AddDate(0, 0, 30),
					}
					_, err := s.PutObject(
						bucket,
						"key",
						strings.NewReader("data"),
						"text/plain",
						nil,
						"",
						"", false, "",
						explicit,
						nil,
						"",
					)
					require.NoError(t, err)

					ret, err := s.GetObjectRetention(bucket, "key", "")
					require.NoError(t, err)
					assert.Equal(t, "GOVERNANCE", ret.Mode)
					assert.True(t, now.AddDate(0, 0, 30).Equal(ret.RetainUntilDate))
				},
			)

			t.Run(
				"no default retention configured leaves object without retention",
				func(t *testing.T) {
					s, bucket := setup(t)
					_, err := s.PutObject(
						bucket,
						"key",
						strings.NewReader("data"),
						"text/plain",
						nil,
						"",
						"", false, "",
						nil,
						nil,
						"",
					)
					require.NoError(t, err)
					_, err = s.GetObjectRetention(bucket, "key", "")
					assert.ErrorIs(t, err, ErrNoObjectRetention)
				},
			)
		},
	)

	t.Run(
		"PutObjectIfNotExists applies bucket default retention when no explicit retention",
		func(t *testing.T) {
			const defaultRetentionXML = `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>GOVERNANCE</Mode><Years>1</Years></DefaultRetention></Rule></ObjectLockConfiguration>`
			now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

			s, bucket := setup(t)
			s.now = func() time.Time { return now }
			require.NoError(t, s.PutBucketVersioning(bucket, "Enabled"))
			require.NoError(t, s.PutBucketObjectLock(bucket, defaultRetentionXML))

			_, err := s.PutObjectIfNotExists(
				bucket,
				"key",
				strings.NewReader("data"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			ret, err := s.GetObjectRetention(bucket, "key", "")
			require.NoError(t, err)
			assert.Equal(t, "GOVERNANCE", ret.Mode)
			assert.True(t, now.AddDate(1, 0, 0).Equal(ret.RetainUntilDate))
		},
	)

	t.Run(
		"CreateMultipartUpload applies bucket default retention when no explicit retention",
		func(t *testing.T) {
			const defaultRetentionXML = `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled><Rule><DefaultRetention><Mode>COMPLIANCE</Mode><Days>3</Days></DefaultRetention></Rule></ObjectLockConfiguration>`
			now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

			s, bucket := setup(t)
			s.now = func() time.Time { return now }
			require.NoError(t, s.PutBucketVersioning(bucket, "Enabled"))
			require.NoError(t, s.PutBucketObjectLock(bucket, defaultRetentionXML))

			uploadID, err := s.CreateMultipartUpload(
				bucket,
				"key",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)

			// Read the stored upload meta to verify retention was applied.
			umeta, err := s.readUploadMeta(uploadID)
			require.NoError(t, err)
			require.NotNil(t, umeta.Retention)
			assert.Equal(t, "COMPLIANCE", umeta.Retention.Mode)
			assert.True(t, now.AddDate(0, 0, 3).Equal(umeta.Retention.RetainUntilDate))
		},
	)

	// Error path tests via injectable helpers (use PublicAccessBlock as representative).
	t.Run("put error paths", func(t *testing.T) {
		t.Run("Put returns error when meta read fails", func(t *testing.T) {
			s, bucket := setup(t)
			s.readAll = func(r io.Reader) ([]byte, error) {
				return nil, errors.New("read error")
			}
			assert.Error(t, s.PutPublicAccessBlock(bucket, xmlBody))
		})

		t.Run("Put returns error when write fails", func(t *testing.T) {
			s, bucket := setup(t)
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				if strings.HasSuffix(name, ".bucket.json") {
					return nil, errors.New("write error")
				}
				return s.root.OpenFile(name, flag, perm)
			}
			assert.Error(t, s.PutPublicAccessBlock(bucket, xmlBody))
		})

		t.Run("Put creates fresh meta when bucket.json is missing", func(t *testing.T) {
			s, bucket := setup(t)
			require.NoError(t, s.root.Remove(bucket+".bucket.json"))
			require.NoError(t, s.PutPublicAccessBlock(bucket, xmlBody))
			got, err := s.GetPublicAccessBlock(bucket)
			require.NoError(t, err)
			assert.Equal(t, xmlBody, got)
		})
	})

	t.Run("get error paths", func(t *testing.T) {
		t.Run("Get returns empty when bucket.json is missing", func(t *testing.T) {
			s, bucket := setup(t)
			require.NoError(t, s.root.Remove(bucket+".bucket.json"))
			got, err := s.GetPublicAccessBlock(bucket)
			require.NoError(t, err)
			assert.Empty(t, got)
		})

		t.Run("Get returns error when meta read fails", func(t *testing.T) {
			s, bucket := setup(t)
			require.NoError(t, s.PutPublicAccessBlock(bucket, xmlBody))
			s.readAll = func(r io.Reader) ([]byte, error) {
				return nil, errors.New("read error")
			}
			_, err := s.GetPublicAccessBlock(bucket)
			assert.Error(t, err)
		})
	})
}

func TestVersioning(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("versioned-bucket", "us-east-1", false))
		require.NoError(t, s.PutBucketVersioning("versioned-bucket", "Enabled"))
		return s, "versioned-bucket"
	}

	t.Run("PutObject on versioned bucket sets VersionID in metadata", func(t *testing.T) {
		s, bucket := setup(t)
		meta, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		assert.NotEmpty(t, meta.VersionID)
	})

	t.Run("PutObject twice creates two versions", func(t *testing.T) {
		s, bucket := setup(t)
		m1, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		m2, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		assert.NotEqual(t, m1.VersionID, m2.VersionID)
	})

	t.Run("GetObject without versionId returns latest version", func(t *testing.T) {
		s, bucket := setup(t)
		_, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		_, err = s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		f, _, err := s.GetObject(bucket, "obj.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		body, _ := io.ReadAll(f)
		assert.Equal(t, "v2", string(body))
	})

	t.Run("GetObjectVersion retrieves a specific archived version", func(t *testing.T) {
		s, bucket := setup(t)
		m1, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		_, err = s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		f, meta, err := s.GetObjectVersion(bucket, "obj.txt", m1.VersionID)
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		body, _ := io.ReadAll(f)
		assert.Equal(t, "v1", string(body))
		assert.Equal(t, m1.VersionID, meta.VersionID)
	})

	t.Run("GetObjectVersion with current versionId returns current object", func(t *testing.T) {
		s, bucket := setup(t)
		_, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		m2, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		f, meta, err := s.GetObjectVersion(bucket, "obj.txt", m2.VersionID)
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		body, _ := io.ReadAll(f)
		assert.Equal(t, "v2", string(body))
		assert.Equal(t, m2.VersionID, meta.VersionID)
	})

	t.Run("HeadObjectVersion returns metadata for a specific version", func(t *testing.T) {
		s, bucket := setup(t)
		m1, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		_, err = s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		meta, err := s.HeadObjectVersion(bucket, "obj.txt", m1.VersionID)
		require.NoError(t, err)
		assert.Equal(t, m1.VersionID, meta.VersionID)
		assert.Equal(t, int64(2), meta.Size)
	})

	t.Run(
		"DeleteObjectVersioned creates delete marker when versioning is enabled",
		func(t *testing.T) {
			s, bucket := setup(t)
			_, err := s.PutObject(
				bucket,
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			vid, isMarker, err := s.DeleteObjectVersioned(bucket, "obj.txt", false)
			require.NoError(t, err)
			assert.True(t, isMarker)
			assert.NotEmpty(t, vid)

			// GetObject should now return DeleteMarkerError.
			_, _, err = s.GetObject(bucket, "obj.txt")
			var dme *DeleteMarkerError
			assert.ErrorAs(t, err, &dme)
		},
	)

	t.Run(
		"DeleteObjectVersioned on non-versioned bucket removes object normally",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("plain-bucket", "", false))
			_, err := s.PutObject(
				"plain-bucket",
				"obj.txt",
				strings.NewReader("data"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)

			vid, isMarker, err := s.DeleteObjectVersioned("plain-bucket", "obj.txt", false)
			require.NoError(t, err)
			assert.Empty(t, vid)
			assert.False(t, isMarker)

			_, _, err = s.GetObject("plain-bucket", "obj.txt")
			assert.ErrorIs(t, err, ErrObjectNotFound)
		},
	)

	t.Run("DeleteObjectVersion removes a specific archived version", func(t *testing.T) {
		s, bucket := setup(t)
		m1, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		_, err = s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		isMarker, err := s.DeleteObjectVersion(bucket, "obj.txt", m1.VersionID, false)
		require.NoError(t, err)
		assert.False(t, isMarker)

		_, _, err = s.GetObjectVersion(bucket, "obj.txt", m1.VersionID)
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run(
		"DeleteObjectVersion returns ErrObjectLocked for archived version under COMPLIANCE retention",
		func(t *testing.T) {
			s, bucket := setup(t)
			retention := &ObjectRetention{
				Mode:            "COMPLIANCE",
				RetainUntilDate: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
			}
			m1, err := s.PutObject(
				bucket, "obj.txt", strings.NewReader("v1"), "text/plain",
				nil, "", "", false, "", retention, nil, "",
			)
			require.NoError(t, err)
			// Put v2 so v1 becomes an archived version.
			_, err = s.PutObject(
				bucket, "obj.txt", strings.NewReader("v2"), "text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)

			_, err = s.DeleteObjectVersion(bucket, "obj.txt", m1.VersionID, false)
			assert.ErrorIs(t, err, ErrObjectLocked)
		},
	)

	t.Run("DeleteObjectVersion on current version removes it", func(t *testing.T) {
		s, bucket := setup(t)
		_, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		m2, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		isMarker, err := s.DeleteObjectVersion(bucket, "obj.txt", m2.VersionID, false)
		require.NoError(t, err)
		assert.False(t, isMarker)
	})

	t.Run(
		"DeleteObjectVersion returns ErrObjectNotFound for unknown versionId",
		func(t *testing.T) {
			s, bucket := setup(t)
			_, err := s.PutObject(
				bucket,
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			_, err = s.DeleteObjectVersion(bucket, "obj.txt", "deadbeefdeadbeef", false)
			assert.ErrorIs(t, err, ErrObjectNotFound)
		},
	)

	t.Run("DeleteObjectVersion removes a delete marker", func(t *testing.T) {
		s, bucket := setup(t)
		_, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		markerVID, _, err := s.DeleteObjectVersioned(bucket, "obj.txt", false)
		require.NoError(t, err)

		isMarker, err := s.DeleteObjectVersion(bucket, "obj.txt", markerVID, false)
		require.NoError(t, err)
		assert.True(t, isMarker)
	})

	t.Run("DeleteObjectVersion removes archived version tag sidecar", func(t *testing.T) {
		s, bucket := setup(t)
		m1, err := s.PutObject(
			bucket, "obj.txt", strings.NewReader("v1"), "text/plain",
			nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)
		require.NoError(t, s.PutObjectTagging(bucket, "obj.txt", []Tag{{Key: "k", Value: "v"}}))
		// Overwrite → m1 becomes an archived version with a .tags.json sidecar.
		_, err = s.PutObject(
			bucket, "obj.txt", strings.NewReader("v2"), "text/plain",
			nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)

		_, err = s.DeleteObjectVersion(bucket, "obj.txt", m1.VersionID, false)
		require.NoError(t, err)

		vp := filepath.Join(bucket, ".ver", "obj.txt", m1.VersionID)
		_, statErr := s.root.Stat(vp + ".tags.json")
		assert.True(t, errors.Is(statErr, os.ErrNotExist), "archived tag sidecar should be removed")
	})

	t.Run(
		"DeleteObjectVersion logs warning and succeeds when archived tag sidecar removal fails",
		func(t *testing.T) {
			s, bucket := setup(t)
			m1, err := s.PutObject(
				bucket, "obj.txt", strings.NewReader("v1"), "text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			require.NoError(t, s.PutObjectTagging(bucket, "obj.txt", []Tag{{Key: "k", Value: "v"}}))
			_, err = s.PutObject(
				bucket, "obj.txt", strings.NewReader("v2"), "text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)

			tagsErr := errors.New("tags remove failed")
			realRemove := s.removeFile
			hit := false
			s.removeFile = func(name string) error {
				if strings.HasSuffix(name, ".tags.json") {
					hit = true
					return tagsErr
				}
				return realRemove(name)
			}

			isMarker, err := s.DeleteObjectVersion(bucket, "obj.txt", m1.VersionID, false)
			require.NoError(t, err)
			assert.False(t, isMarker)
			require.True(t, hit, "removeFile for .tags.json should have been called")
		},
	)

	t.Run("ListObjectVersions returns all versions and delete markers", func(t *testing.T) {
		s, bucket := setup(t)
		m1, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		m2, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		markerVID, _, err := s.DeleteObjectVersioned(bucket, "obj.txt", false)
		require.NoError(t, err)

		versions, deleteMarkers, err := s.ListObjectVersions(bucket)
		require.NoError(t, err)
		assert.Len(t, deleteMarkers, 1)
		assert.Equal(t, markerVID, deleteMarkers[0].VersionID)
		assert.True(t, deleteMarkers[0].IsLatest)

		var vids []string
		for _, v := range versions {
			vids = append(vids, v.VersionID)
		}
		assert.Contains(t, vids, m1.VersionID)
		assert.Contains(t, vids, m2.VersionID)
	})

	t.Run("ListObjectVersions sets NoncurrentSince on noncurrent versions", func(t *testing.T) {
		s, bucket := setup(t)

		_, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		// Inject a fixed time so we can assert on NoncurrentSince precisely.
		noncurrentAt := time.Date(2030, 6, 15, 12, 0, 0, 0, time.UTC)
		s.now = func() time.Time { return noncurrentAt }
		_, err = s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		versions, _, err := s.ListObjectVersions(bucket)
		require.NoError(t, err)

		var noncurrent *VersionInfo
		for i := range versions {
			if !versions[i].IsLatest {
				noncurrent = &versions[i]
			}
		}
		require.NotNil(t, noncurrent)
		assert.Equal(
			t,
			noncurrentAt.UTC(),
			noncurrent.NoncurrentSince.UTC(),
			"NoncurrentSince should be set to when v2 was written",
		)
		assert.False(t, noncurrent.NoncurrentSince.IsZero())
	})

	t.Run(
		"ListObjectVersions sets NoncurrentSince on noncurrent delete markers",
		func(t *testing.T) {
			s, bucket := setup(t)

			_, err := s.PutObject(
				bucket,
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			_, _, err = s.DeleteObjectVersioned(bucket, "obj.txt", false)
			require.NoError(t, err)

			supersededAt := time.Date(2030, 6, 15, 12, 0, 0, 0, time.UTC)
			s.now = func() time.Time { return supersededAt }
			_, err = s.PutObject(
				bucket,
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			_, markers, err := s.ListObjectVersions(bucket)
			require.NoError(t, err)

			var noncurrentDM *DeleteMarkerInfo
			for i := range markers {
				if !markers[i].IsLatest {
					noncurrentDM = &markers[i]
				}
			}
			require.NotNil(t, noncurrentDM)
			assert.Equal(t, supersededAt.UTC(), noncurrentDM.NoncurrentSince.UTC())
		},
	)

	t.Run(
		"ListObjectVersions on non-versioned bucket returns objects with null versionId",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("plain-bucket", "", false))
			_, err := s.PutObject(
				"plain-bucket",
				"obj.txt",
				strings.NewReader("data"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)

			versions, deleteMarkers, err := s.ListObjectVersions("plain-bucket")
			require.NoError(t, err)
			require.Len(t, versions, 1)
			assert.Equal(t, "null", versions[0].VersionID)
			assert.Empty(t, deleteMarkers)
		},
	)

	t.Run("ListObjectVersions returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		_, _, err := s.ListObjectVersions("no-bucket")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run(
		"GetObject returns DeleteMarkerError when current version is delete marker",
		func(t *testing.T) {
			s, bucket := setup(t)
			_, err := s.PutObject(
				bucket,
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			_, _, err = s.DeleteObjectVersioned(bucket, "obj.txt", false)
			require.NoError(t, err)

			_, _, err = s.GetObject(bucket, "obj.txt")
			var dme *DeleteMarkerError
			assert.ErrorAs(t, err, &dme)
		},
	)

	t.Run(
		"HeadObject returns DeleteMarkerError when current version is delete marker",
		func(t *testing.T) {
			s, bucket := setup(t)
			_, err := s.PutObject(
				bucket,
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			_, _, err = s.DeleteObjectVersioned(bucket, "obj.txt", false)
			require.NoError(t, err)

			_, err = s.HeadObject(bucket, "obj.txt")
			var dme *DeleteMarkerError
			assert.ErrorAs(t, err, &dme)
		},
	)

	t.Run("ListObjects does not show delete-marker objects", func(t *testing.T) {
		s, bucket := setup(t)
		_, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		_, _, err = s.DeleteObjectVersioned(bucket, "obj.txt", false)
		require.NoError(t, err)

		objects, err := s.ListObjects(bucket)
		require.NoError(t, err)
		assert.Empty(t, objects)
	})

	t.Run("CopyObject on versioned destination creates new version", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.CreateBucket("src-bucket", "", false))
		_, err := s.PutObject(
			"src-bucket",
			"src.txt",
			strings.NewReader("hello"),
			"text/plain",
			nil, "", "", false, "", nil, nil, "",
		)
		require.NoError(t, err)
		m1, err := s.PutObject(
			bucket,
			"dst.txt",
			strings.NewReader("original"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		m2, err := s.CopyObject(
			"src-bucket",
			"src.txt",
			"",
			bucket,
			"dst.txt",
			"",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		assert.NotEqual(t, m1.VersionID, m2.VersionID)
		assert.NotEmpty(t, m2.VersionID)
	})

	t.Run("CopyObject with srcVersionId copies a specific version", func(t *testing.T) {
		s, bucket := setup(t)
		m1, err := s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		_, err = s.PutObject(
			bucket,
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		require.NoError(t, s.CreateBucket("dst-bucket", "", false))
		dstMeta, err := s.CopyObject(
			bucket,
			"obj.txt",
			m1.VersionID,
			"dst-bucket",
			"copy.txt",
			"",
			nil, "", "", false, "", nil, nil, "",
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, m1.ETag, dstMeta.ETag)
	})

	t.Run(
		"CopyObject with srcVersionId matching current version copies from current version",
		func(t *testing.T) {
			s, bucket := setup(t)
			_, err := s.PutObject(
				bucket,
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			m2, err := s.PutObject(
				bucket,
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			require.NoError(t, s.CreateBucket("dst-bucket", "", false))
			// Pass m2.VersionID as srcVersionID; m2 is the current version.
			dstMeta, err := s.CopyObject(
				bucket, "obj.txt", m2.VersionID,
				"dst-bucket", "copy.txt", "", nil, "", "", false, "", nil, nil, "",
				nil,
			)
			require.NoError(t, err)
			assert.Equal(t, m2.ETag, dstMeta.ETag)
		},
	)

	t.Run(
		"ListObjectVersions with multiple distinct keys sorts entries by key",
		func(t *testing.T) {
			s, bucket := setup(t)
			_, err := s.PutObject(
				bucket,
				"b.txt",
				strings.NewReader("b1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			_, err = s.PutObject(
				bucket,
				"a.txt",
				strings.NewReader("a1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			_, err = s.PutObject(
				bucket,
				"b.txt",
				strings.NewReader("b2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			versions, _, err := s.ListObjectVersions(bucket)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(versions), 2)
			// Verify a.txt appears before b.txt.
			var keys []string
			for _, v := range versions {
				if len(keys) == 0 || keys[len(keys)-1] != v.Key {
					keys = append(keys, v.Key)
				}
			}
			assert.Equal(t, "a.txt", keys[0])
			assert.Equal(t, "b.txt", keys[1])
		},
	)

	t.Run(
		"archiveCurrentVersionLocked assigns versionId to pre-versioning object on re-put",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			// Put WITHOUT versioning — object has no VersionID.
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Enable versioning after the fact.
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			// Second put archives the pre-versioning object (assigns it a versionId).
			m2, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			assert.NotEmpty(t, m2.VersionID)

			versions, _, err := s.ListObjectVersions("my-bucket")
			require.NoError(t, err)
			assert.Len(t, versions, 2)
		},
	)
}

func TestDeleteMarkerError(t *testing.T) {
	dme := &DeleteMarkerError{VersionID: "abc123"}
	assert.Equal(t, "object is a delete marker", dme.Error())
}

func TestVersioningErrorPaths(t *testing.T) {
	t.Run("GetObjectVersion returns ErrBucketNotFound", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		_, _, err := s.GetObjectVersion("no-bucket", "obj.txt", "abc123")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("GetObjectVersion returns ErrObjectNotFound for unknown versionId", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		_, _, err = s.GetObjectVersion("my-bucket", "obj.txt", "deadbeefdeadbeef")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run(
		"GetObjectVersion returns DeleteMarkerError for delete marker versionId",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			markerVID, _, err := s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
			require.NoError(t, err)

			// Accessing a delete marker directly should return DeleteMarkerError.
			_, _, err = s.GetObjectVersion("my-bucket", "obj.txt", markerVID)
			var dme *DeleteMarkerError
			assert.ErrorAs(t, err, &dme)
			assert.Equal(t, markerVID, dme.VersionID)
		},
	)

	t.Run("HeadObjectVersion returns ErrBucketNotFound", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		_, err := s.HeadObjectVersion("no-bucket", "obj.txt", "abc123")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("HeadObjectVersion returns ErrObjectNotFound for unknown versionId", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		_, err = s.HeadObjectVersion("my-bucket", "obj.txt", "deadbeefdeadbeef")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("HeadObjectVersion returns DeleteMarkerError for delete marker", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		markerVID, _, err := s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
		require.NoError(t, err)

		_, err = s.HeadObjectVersion("my-bucket", "obj.txt", markerVID)
		var dme *DeleteMarkerError
		assert.ErrorAs(t, err, &dme)
		assert.Equal(t, markerVID, dme.VersionID)
	})

	t.Run("DeleteObjectVersioned returns ErrBucketNotFound", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		_, _, err := s.DeleteObjectVersioned("no-bucket", "obj.txt", false)
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("DeleteObjectVersion returns ErrBucketNotFound", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		_, err := s.DeleteObjectVersion("no-bucket", "obj.txt", "abc123", false)
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("PutObject returns error when newVersionID fails", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
		s.randRead = func(b []byte) (int, error) { return 0, errors.New("rand failure") }
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		assert.Error(t, err)
	})

	t.Run(
		"archiveCurrentVersionLocked handles randRead failure for unversioned object",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			// Put object WITHOUT versioning first.
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			// Enable versioning.
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			// Make randRead fail — triggered during archiveCurrentVersionLocked for pre-versioning object.
			s.randRead = func(b []byte) (int, error) { return 0, errors.New("rand failure") }
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			assert.Error(t, err)
		},
	)

	t.Run("DeleteObjectVersioned returns error when newVersionID fails", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		s.randRead = func(b []byte) (int, error) { return 0, errors.New("rand failure") }
		_, _, err = s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
		assert.Error(t, err)
	})

	t.Run("archiveCurrentVersionLocked returns error when openFile fails", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		// Fail on .ver writes.
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.Contains(name, ".ver") {
				return nil, errors.New("write error")
			}
			return s.root.OpenFile(name, flag, perm)
		}
		_, err = s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		assert.Error(t, err)
	})

	t.Run(
		"GetObjectVersion archived version ErrObjectNotFound when body missing",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			m1, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			// Remove the archived body to simulate corruption.
			vp := verPath("my-bucket", "obj.txt", m1.VersionID)
			require.NoError(t, s.root.Remove(vp))

			_, _, err = s.GetObjectVersion("my-bucket", "obj.txt", m1.VersionID)
			assert.ErrorIs(t, err, ErrObjectNotFound)
		},
	)

	t.Run("PutObject returns error when isVersioningEnabledLocked fails", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		// Corrupt bucket meta so JSON unmarshal fails inside isVersioningEnabledLocked.
		f, err := s.root.OpenFile("my-bucket.bucket.json", os.O_WRONLY|os.O_TRUNC, 0o600)
		require.NoError(t, err)
		_, err = f.Write([]byte("not-json"))
		require.NoError(t, err)
		require.NoError(t, f.Close())
		_, err = s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		assert.Error(t, err)
	})

	t.Run(
		"isVersioningEnabledLocked returns false when bucket meta file is missing",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			// Remove bucket meta; versioning should be treated as disabled.
			require.NoError(t, s.root.Remove("my-bucket.bucket.json"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err) // proceeds without versioning
		},
	)

	t.Run(
		"archiveCurrentVersionLocked returns error when readMeta fails with non-ErrNotExist",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			// Corrupt current version meta so readMeta in archiveCurrentVersionLocked fails.
			f, err := s.root.OpenFile(
				"my-bucket/obj.txt.meta.json",
				os.O_WRONLY|os.O_TRUNC,
				0o600,
			)
			require.NoError(t, err)
			_, err = f.Write([]byte("not-json"))
			require.NoError(t, err)
			require.NoError(t, f.Close())
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			assert.Error(t, err)
		},
	)

	t.Run("archiveCurrentVersionLocked returns error when io.Copy fails", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
		_, err := s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		// Return a writer that fails on Write for the archived version body.
		orig := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.Contains(name, ".ver") && !strings.HasSuffix(name, ".meta.json") {
				return errWriteCloser{}, nil
			}
			return orig(name, flag, perm)
		}
		_, err = s.PutObject(
			"my-bucket",
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		assert.Error(t, err)
	})

	t.Run(
		"archiveCurrentVersionLocked returns error when writeJSON fails for archived meta",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			// Fail openFile only for .ver meta files; body copy must succeed first.
			orig := s.openFile
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				if strings.Contains(name, ".ver") && strings.HasSuffix(name, ".meta.json") {
					return nil, errors.New("meta write error")
				}
				return orig(name, flag, perm)
			}
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			assert.Error(t, err)
		},
	)

	t.Run(
		"DeleteObjectVersioned returns error when openFile fails for delete marker body",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			// Archiving must succeed (.ver path); fail only for the marker body file.
			orig := s.openFile
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				if !strings.Contains(name, ".ver") && !strings.HasSuffix(name, ".meta.json") {
					return nil, errors.New("disk full")
				}
				return orig(name, flag, perm)
			}
			_, _, err = s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
			assert.Error(t, err)
		},
	)

	t.Run(
		"DeleteObjectVersioned returns error when writeMeta fails for delete marker",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			// Fail openFile only for the marker's .meta.json (not inside .ver).
			orig := s.openFile
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				if !strings.Contains(name, ".ver") && strings.HasSuffix(name, ".meta.json") {
					return nil, errors.New("meta write error")
				}
				return orig(name, flag, perm)
			}
			_, _, err = s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
			assert.Error(t, err)
		},
	)

	t.Run(
		"HeadObjectVersion returns error for corrupt archived version metadata",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			m1, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Overwrite archived v1 meta with invalid JSON.
			vp := verPath("my-bucket", "obj.txt", m1.VersionID)
			f, err := s.root.OpenFile(vp+".meta.json", os.O_WRONLY|os.O_TRUNC, 0o600)
			require.NoError(t, err)
			_, err = f.Write([]byte("not-json"))
			require.NoError(t, err)
			require.NoError(t, f.Close())
			_, err = s.HeadObjectVersion("my-bucket", "obj.txt", m1.VersionID)
			assert.Error(t, err)
		},
	)

	t.Run("ListObjectVersions returns error when readDir fails for bucket", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("my-bucket", "", false))
		s.listDirFn = func(_ string) ([]os.DirEntry, error) {
			return nil, errors.New("read error")
		}
		_, _, err := s.ListObjectVersions("my-bucket")
		assert.Error(t, err)
	})

	t.Run(
		"ListObjectVersions returns error when readDir fails for versioned subdir",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			// Use a nested key so collectVersionEntries recurses into a subdirectory.
			_, err := s.PutObject(
				"my-bucket",
				"prefix/obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			orig := s.listDirFn
			s.listDirFn = func(name string) ([]os.DirEntry, error) {
				if name != "my-bucket" {
					return nil, errors.New("read error")
				}
				return orig(name)
			}
			_, _, err = s.ListObjectVersions("my-bucket")
			assert.Error(t, err)
		},
	)

	t.Run(
		"ListObjectVersions returns error when readDir fails for .ver directory",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			// Put v2 to create an archived entry under .ver.
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			orig := s.listDirFn
			s.listDirFn = func(name string) ([]os.DirEntry, error) {
				if strings.Contains(name, ".ver") {
					return nil, errors.New("read error")
				}
				return orig(name)
			}
			_, _, err = s.ListObjectVersions("my-bucket")
			assert.Error(t, err)
		},
	)

	t.Run(
		"collectArchivedEntries returns error when recursive readDir fails",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			// Nested key creates a nested .ver tree: .ver/prefix/obj.txt/<versionId>
			_, err := s.PutObject(
				"my-bucket",
				"prefix/obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			_, err = s.PutObject(
				"my-bucket",
				"prefix/obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			orig := s.listDirFn
			verCalls := 0
			s.listDirFn = func(name string) ([]os.DirEntry, error) {
				if strings.Contains(name, ".ver") {
					verCalls++
					if verCalls > 1 {
						return nil, errors.New("read error")
					}
				}
				return orig(name)
			}
			_, _, err = s.ListObjectVersions("my-bucket")
			assert.Error(t, err)
		},
	)

	t.Run(
		"DeleteObjectVersion returns error when archived version meta is corrupt",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			m1, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Corrupt archived v1 meta.
			vp := verPath("my-bucket", "obj.txt", m1.VersionID)
			f, err := s.root.OpenFile(vp+".meta.json", os.O_WRONLY|os.O_TRUNC, 0o600)
			require.NoError(t, err)
			_, err = f.Write([]byte("not-json"))
			require.NoError(t, err)
			require.NoError(t, f.Close())
			_, err = s.DeleteObjectVersion("my-bucket", "obj.txt", m1.VersionID, false)
			assert.Error(t, err)
		},
	)

	t.Run(
		"DeleteObjectVersion returns error when removeFile fails for archived version",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			m1, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Make removeFile fail for the archived version body.
			orig := s.removeFile
			s.removeFile = func(name string) error {
				if strings.Contains(name, ".ver") && !strings.HasSuffix(name, ".meta.json") {
					return errors.New("remove failure")
				}
				return orig(name)
			}
			_, err = s.DeleteObjectVersion("my-bucket", "obj.txt", m1.VersionID, false)
			assert.Error(t, err)
		},
	)

	t.Run(
		"DeleteObjectVersion warns and continues when archived version meta removal fails",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			m1, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil, "", "", false, "", nil, nil, "",
			)
			require.NoError(t, err)
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Body removal succeeds but meta removal fails → slog.Warn, still returns nil.
			orig := s.removeFile
			s.removeFile = func(name string) error {
				if strings.Contains(name, ".ver") && strings.HasSuffix(name, ".meta.json") {
					return errors.New("meta remove failure")
				}
				return orig(name)
			}
			isMarker, err := s.DeleteObjectVersion("my-bucket", "obj.txt", m1.VersionID, false)
			require.NoError(t, err)
			assert.False(t, isMarker)
		},
	)

	t.Run(
		"GetObjectVersion returns ErrObjectNotFound when current version body is missing",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			m2, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Remove the current version body but keep the meta file.
			require.NoError(t, s.root.Remove("my-bucket/obj.txt"))

			_, _, err = s.GetObjectVersion("my-bucket", "obj.txt", m2.VersionID)
			assert.ErrorIs(t, err, ErrObjectNotFound)
		},
	)

	t.Run(
		"GetObjectVersion returns DeleteMarkerError for archived delete marker",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Delete creates a delete marker as current.
			markerVID, _, err := s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
			require.NoError(t, err)
			// Put again — delete marker gets archived.
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			// The archived delete marker should return DeleteMarkerError.
			_, _, err = s.GetObjectVersion("my-bucket", "obj.txt", markerVID)
			var dme *DeleteMarkerError
			assert.ErrorAs(t, err, &dme)
			assert.Equal(t, markerVID, dme.VersionID)
		},
	)

	t.Run(
		"GetObjectVersion returns error for archived version with corrupt metadata",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			m1, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Corrupt the archived v1 meta so readMeta returns a non-ErrNotExist error.
			vp := verPath("my-bucket", "obj.txt", m1.VersionID)
			f, err := s.root.OpenFile(vp+".meta.json", os.O_WRONLY|os.O_TRUNC, 0o600)
			require.NoError(t, err)
			_, err = f.Write([]byte("not-json"))
			require.NoError(t, err)
			require.NoError(t, f.Close())

			_, _, err = s.GetObjectVersion("my-bucket", "obj.txt", m1.VersionID)
			assert.Error(t, err)
			assert.NotErrorIs(t, err, ErrObjectNotFound)
		},
	)

	t.Run(
		"HeadObjectVersion returns metadata for current version when queried by its versionId",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			m2, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			// m2 is the current version; HeadObjectVersion with m2.VersionID hits the
			// "check current version" branch.
			meta, err := s.HeadObjectVersion("my-bucket", "obj.txt", m2.VersionID)
			require.NoError(t, err)
			assert.Equal(t, m2.VersionID, meta.VersionID)
		},
	)

	t.Run(
		"HeadObjectVersion returns DeleteMarkerError for archived delete marker",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			markerVID, _, err := s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
			require.NoError(t, err)
			// Put v2 — archives the delete marker.
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			_, err = s.HeadObjectVersion("my-bucket", "obj.txt", markerVID)
			var dme *DeleteMarkerError
			assert.ErrorAs(t, err, &dme)
			assert.Equal(t, markerVID, dme.VersionID)
		},
	)

	t.Run(
		"CopyObject with srcVersionId not found in archived versions returns ErrObjectNotFound",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			require.NoError(t, s.CreateBucket("dst-bucket", "", false))

			_, err = s.CopyObject(
				"my-bucket", "obj.txt", "deadbeefdeadbeef",
				"dst-bucket", "copy.txt", "", nil, "", "", false, "", nil, nil, "",
				nil,
			)
			assert.ErrorIs(t, err, ErrObjectNotFound)
		},
	)

	t.Run(
		"CopyObject with srcVersionId pointing to delete marker returns ErrObjectNotFound",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			markerVID, _, err := s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
			require.NoError(t, err)
			require.NoError(t, s.CreateBucket("dst-bucket", "", false))

			// Copying from the current delete marker should return ErrObjectNotFound.
			_, err = s.CopyObject(
				"my-bucket", "obj.txt", markerVID,
				"dst-bucket", "copy.txt", "", nil, "", "", false, "", nil, nil, "",
				nil,
			)
			assert.ErrorIs(t, err, ErrObjectNotFound)
		},
	)

	t.Run(
		"DeleteObjectVersioned returns error when isVersioningEnabledLocked fails",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			// Corrupt bucket meta so isVersioningEnabledLocked fails.
			f, err := s.root.OpenFile("my-bucket.bucket.json", os.O_WRONLY|os.O_TRUNC, 0o600)
			require.NoError(t, err)
			_, err = f.Write([]byte("not-json"))
			require.NoError(t, err)
			require.NoError(t, f.Close())

			_, _, err = s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
			assert.Error(t, err)
		},
	)

	t.Run(
		"DeleteObjectVersioned returns error when archiveCurrentVersionLocked fails",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Fail openFile for .ver paths so archiving fails.
			orig := s.openFile
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				if strings.Contains(name, ".ver") {
					return nil, errors.New("archive write error")
				}
				return orig(name, flag, perm)
			}
			_, _, err = s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
			assert.Error(t, err)
		},
	)

	t.Run(
		"DeleteObjectVersioned returns error when delete marker body Close fails",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Archive must succeed (.ver); fail Close() only for the marker body.
			orig := s.openFile
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				wc, err := orig(name, flag, perm)
				if err != nil {
					return nil, err
				}
				if !strings.Contains(name, ".ver") && !strings.HasSuffix(name, ".meta.json") {
					return badCloseWriter{wc}, nil
				}
				return wc, nil
			}
			_, _, err = s.DeleteObjectVersioned("my-bucket", "obj.txt", false)
			assert.Error(t, err)
		},
	)

	t.Run(
		"collectVersionEntries skips objects with corrupt metadata",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			_, err := s.PutObject(
				"my-bucket",
				"good.txt",
				strings.NewReader("data"),
				"text/plain",
				nil,
				"",
				"",
				false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			_, err = s.PutObject(
				"my-bucket", "bad.txt", strings.NewReader("data"), "text/plain", nil, "", "", false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Corrupt bad.txt's metadata — ListObjectVersions should skip it.
			f, err := s.root.OpenFile(
				"my-bucket/bad.txt.meta.json", os.O_WRONLY|os.O_TRUNC, 0o600,
			)
			require.NoError(t, err)
			_, err = f.Write([]byte("not-json"))
			require.NoError(t, err)
			require.NoError(t, f.Close())

			versions, _, err := s.ListObjectVersions("my-bucket")
			require.NoError(t, err)
			require.Len(t, versions, 1)
			assert.Equal(t, "good.txt", versions[0].Key)
		},
	)

	t.Run(
		"collectArchivedEntries skips archived versions with corrupt metadata",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			m1, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Corrupt the archived v1 meta — ListObjectVersions should skip it.
			vp := verPath("my-bucket", "obj.txt", m1.VersionID)
			f, err := s.root.OpenFile(vp+".meta.json", os.O_WRONLY|os.O_TRUNC, 0o600)
			require.NoError(t, err)
			_, err = f.Write([]byte("not-json"))
			require.NoError(t, err)
			require.NoError(t, f.Close())

			versions, _, err := s.ListObjectVersions("my-bucket")
			require.NoError(t, err)
			// Only v2 (current) should be returned.
			assert.Len(t, versions, 1)
		},
	)

	t.Run(
		"archiveCurrentVersionLocked is a no-op when body file is missing",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Remove the body file, keeping the meta — simulates partial corruption.
			require.NoError(t, s.root.Remove("my-bucket/obj.txt"))
			// Second put triggers archiveCurrentVersionLocked; body is missing → no-op.
			m2, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			assert.NotEmpty(t, m2.VersionID)
		},
	)

	t.Run(
		"archiveCurrentVersionLocked returns error when dst.Close fails",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Return a writer that succeeds on Write but fails on Close for archive body.
			orig := s.openFile
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				wc, err := orig(name, flag, perm)
				if err != nil {
					return nil, err
				}
				if strings.Contains(name, ".ver") && !strings.HasSuffix(name, ".meta.json") {
					return badCloseWriter{wc}, nil
				}
				return wc, nil
			}
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			assert.Error(t, err)
		},
	)

	t.Run(
		"CompleteMultipartUpload returns error when isVersioningEnabledLocked fails",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			uploadID, err := s.CreateMultipartUpload(
				"my-bucket",
				"big.txt",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)
			etag, err := s.UploadPart(uploadID, 1, strings.NewReader("data"))
			require.NoError(t, err)
			// Corrupt bucket meta so isVersioningEnabledLocked fails.
			f, err := s.root.OpenFile("my-bucket.bucket.json", os.O_WRONLY|os.O_TRUNC, 0o600)
			require.NoError(t, err)
			_, err = f.Write([]byte("not-json"))
			require.NoError(t, err)
			require.NoError(t, f.Close())

			_, err = s.CompleteMultipartUpload(
				uploadID,
				[]CompletePart{{PartNumber: 1, ETag: etag}},
			)
			assert.Error(t, err)
		},
	)

	t.Run(
		"CompleteMultipartUpload returns error when archiveCurrentVersionLocked fails",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			_, err := s.PutObject(
				"my-bucket",
				"big.txt",
				strings.NewReader("existing"),
				"text/plain",
				nil,
				"",
				"",
				false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			uploadID, err := s.CreateMultipartUpload(
				"my-bucket",
				"big.txt",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)
			etag, err := s.UploadPart(uploadID, 1, strings.NewReader("data"))
			require.NoError(t, err)
			// Fail openFile for .ver paths so archiving fails.
			orig := s.openFile
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				if strings.Contains(name, ".ver") {
					return nil, errors.New("archive write error")
				}
				return orig(name, flag, perm)
			}
			_, err = s.CompleteMultipartUpload(
				uploadID,
				[]CompletePart{{PartNumber: 1, ETag: etag}},
			)
			assert.Error(t, err)
		},
	)

	t.Run(
		"CompleteMultipartUpload returns error when newVersionID fails on versioned bucket",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			// Put v1 WITH versioning so it already has a VersionID; archive won't need randRead.
			_, err := s.PutObject(
				"my-bucket",
				"big.txt",
				strings.NewReader("existing"),
				"text/plain",
				nil,
				"",
				"",
				false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)

			uploadID, err := s.CreateMultipartUpload(
				"my-bucket",
				"big.txt",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)
			etag, err := s.UploadPart(uploadID, 1, strings.NewReader("data"))
			require.NoError(t, err)
			// Fail randRead after archiving (which already has a VersionID and won't call randRead).
			s.randRead = func(b []byte) (int, error) { return 0, errors.New("rand failure") }
			_, err = s.CompleteMultipartUpload(
				uploadID,
				[]CompletePart{{PartNumber: 1, ETag: etag}},
			)
			assert.Error(t, err)
		},
	)

	t.Run(
		"CopyObject without srcVersionId returns ErrObjectNotFound when source is delete marker",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("src-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("src-bucket", "Enabled"))
			_, err := s.PutObject(
				"src-bucket", "obj.txt", strings.NewReader("v1"), "text/plain", nil, "", "", false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Delete the object — current version becomes a delete marker.
			_, _, err = s.DeleteObjectVersioned("src-bucket", "obj.txt", false)
			require.NoError(t, err)
			require.NoError(t, s.CreateBucket("dst-bucket", "", false))

			// Copying without specifying a versionId should return ErrObjectNotFound
			// because the current version is a delete marker.
			_, err = s.CopyObject(
				"src-bucket",
				"obj.txt",
				"",
				"dst-bucket",
				"copy.txt",
				"",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			assert.ErrorIs(t, err, ErrObjectNotFound)
		},
	)

	t.Run(
		"CopyObject with srcVersionId and corrupt archived version meta returns error",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("my-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("my-bucket", "Enabled"))
			m1, err := s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			_, err = s.PutObject(
				"my-bucket",
				"obj.txt",
				strings.NewReader("v2"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Corrupt the archived v1 meta.
			vp := verPath("my-bucket", "obj.txt", m1.VersionID)
			f, err := s.root.OpenFile(vp+".meta.json", os.O_WRONLY|os.O_TRUNC, 0o600)
			require.NoError(t, err)
			_, err = f.Write([]byte("not-json"))
			require.NoError(t, err)
			require.NoError(t, f.Close())
			require.NoError(t, s.CreateBucket("dst-bucket", "", false))

			_, err = s.CopyObject(
				"my-bucket", "obj.txt", m1.VersionID,
				"dst-bucket", "copy.txt", "", nil, "", "", false, "", nil, nil, "",
				nil,
			)
			assert.Error(t, err)
			assert.NotErrorIs(t, err, ErrObjectNotFound)
		},
	)

	t.Run(
		"CopyObject returns error when isVersioningEnabledLocked fails for dst bucket",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("src-bucket", "", false))
			_, err := s.PutObject(
				"src-bucket",
				"obj.txt",
				strings.NewReader("hello"),
				"text/plain",
				nil,
				"",
				"",
				false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			require.NoError(t, s.CreateBucket("dst-bucket", "", false))
			// Corrupt dst bucket meta so isVersioningEnabledLocked fails.
			f, err := s.root.OpenFile("dst-bucket.bucket.json", os.O_WRONLY|os.O_TRUNC, 0o600)
			require.NoError(t, err)
			_, err = f.Write([]byte("not-json"))
			require.NoError(t, err)
			require.NoError(t, f.Close())

			_, err = s.CopyObject(
				"src-bucket",
				"obj.txt",
				"",
				"dst-bucket",
				"copy.txt",
				"",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			assert.Error(t, err)
		},
	)

	t.Run(
		"CopyObject returns error when archiveCurrentVersionLocked fails for dst bucket",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("src-bucket", "", false))
			_, err := s.PutObject(
				"src-bucket",
				"obj.txt",
				strings.NewReader("hello"),
				"text/plain",
				nil,
				"",
				"",
				false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			require.NoError(t, s.CreateBucket("dst-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("dst-bucket", "Enabled"))
			_, err = s.PutObject(
				"dst-bucket",
				"copy.txt",
				strings.NewReader("existing"),
				"text/plain",
				nil,
				"",
				"",
				false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Fail openFile for .ver paths (dst archive).
			orig := s.openFile
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				if strings.Contains(name, ".ver") {
					return nil, errors.New("archive write error")
				}
				return orig(name, flag, perm)
			}
			_, err = s.CopyObject(
				"src-bucket",
				"obj.txt",
				"",
				"dst-bucket",
				"copy.txt",
				"",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			assert.Error(t, err)
		},
	)

	t.Run(
		"CopyObject returns error when newVersionID fails for versioned dst bucket",
		func(t *testing.T) {
			s, _ := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("src-bucket", "", false))
			_, err := s.PutObject(
				"src-bucket",
				"obj.txt",
				strings.NewReader("hello"),
				"text/plain",
				nil,
				"",
				"",
				false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			require.NoError(t, s.CreateBucket("dst-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("dst-bucket", "Enabled"))
			// Put existing dst object WITH versioning so it has a VersionID;
			// archiveCurrentVersionLocked won't call randRead.
			_, err = s.PutObject(
				"dst-bucket",
				"copy.txt",
				strings.NewReader("existing"),
				"text/plain",
				nil,
				"",
				"",
				false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Fail randRead only for the new version ID assignment.
			s.randRead = func(b []byte) (int, error) { return 0, errors.New("rand failure") }
			_, err = s.CopyObject(
				"src-bucket",
				"obj.txt",
				"",
				"dst-bucket",
				"copy.txt",
				"",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			assert.Error(t, err)
		},
	)
}

func TestSetObjectRestoreInitiated(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("bucket", "us-east-1", false))
		_, err := s.PutObject(
			"bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		return s, "bucket"
	}

	t.Run("marks restore initiated and is visible via HeadObject", func(t *testing.T) {
		s, bucket := setup(t)
		require.NoError(t, s.SetObjectRestoreInitiated(bucket, "obj.txt"))
		meta, err := s.HeadObject(bucket, "obj.txt")
		require.NoError(t, err)
		assert.True(t, meta.RestoreInitiated)
	})

	t.Run("returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.SetObjectRestoreInitiated("no-bucket", "obj.txt")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("returns ErrObjectNotFound for missing object", func(t *testing.T) {
		s, bucket := setup(t)
		err := s.SetObjectRestoreInitiated(bucket, "missing.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns error when metadata is corrupt", func(t *testing.T) {
		s, rootPath := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("bucket", "", false))
		require.NoError(t, os.WriteFile(
			filepath.Join(rootPath, "bucket", "obj.txt.meta.json"),
			[]byte("not valid json"),
			0o600,
		))
		err := s.SetObjectRestoreInitiated("bucket", "obj.txt")
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrObjectNotFound)
	})
}

func TestUploadPartCopy(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("src-bucket", "", false))
		require.NoError(t, s.CreateBucket("dst-bucket", "", false))
		_, err := s.PutObject(
			"src-bucket",
			"source.txt",
			strings.NewReader("hello world"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		uploadID, err := s.CreateMultipartUpload(
			"dst-bucket",
			"dest.txt",
			"text/plain",
			"",
			"", false, "",
			nil,
			nil,
			"",
			nil,
		)
		require.NoError(t, err)
		return s, uploadID
	}

	t.Run("copies full source object as part", func(t *testing.T) {
		s, uploadID := setup(t)
		etag, lastModified, _, err := s.UploadPartCopy(
			uploadID,
			1,
			"src-bucket",
			"source.txt",
			"",
			nil,
		)
		require.NoError(t, err)
		assert.NotEmpty(t, etag)
		assert.False(t, lastModified.IsZero())

		meta, err := s.CompleteMultipartUpload(
			uploadID,
			[]CompletePart{{PartNumber: 1, ETag: etag}},
		)
		require.NoError(t, err)

		f, _, err := s.GetObject("dst-bucket", "dest.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(data))
		assert.Contains(t, meta.ETag, "-1")
	})

	t.Run("copies byte range of source as part", func(t *testing.T) {
		s, uploadID := setup(t)
		etag, _, _, err := s.UploadPartCopy(
			uploadID,
			1,
			"src-bucket",
			"source.txt",
			"",
			&byteRange{Start: 0, End: 4},
		)
		require.NoError(t, err)
		assert.NotEmpty(t, etag)

		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: etag}})
		require.NoError(t, err)

		f, _, err := s.GetObject("dst-bucket", "dest.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(data))
	})

	t.Run("copies middle byte range of source as part", func(t *testing.T) {
		s, uploadID := setup(t)
		etag, _, _, err := s.UploadPartCopy(
			uploadID,
			1,
			"src-bucket",
			"source.txt",
			"",
			&byteRange{Start: 6, End: 10},
		)
		require.NoError(t, err)

		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: etag}})
		require.NoError(t, err)

		f, _, err := s.GetObject("dst-bucket", "dest.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, "world", string(data))
	})

	t.Run("returns ErrUploadNotFound for nonexistent upload", func(t *testing.T) {
		s, _ := newTestStorageWithRoot(t)
		require.NoError(t, s.CreateBucket("src-bucket", "", false))
		_, err := s.PutObject(
			"src-bucket",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		_, _, _, err = s.UploadPartCopy("nonexistent-upload", 1, "src-bucket", "obj.txt", "", nil)
		assert.ErrorIs(t, err, ErrUploadNotFound)
	})

	t.Run("returns ErrObjectNotFound for nonexistent source object", func(t *testing.T) {
		s, uploadID := setup(t)
		_, _, _, err := s.UploadPartCopy(uploadID, 1, "src-bucket", "missing.txt", "", nil)
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns ErrBucketNotFound for nonexistent source bucket", func(t *testing.T) {
		s, uploadID := setup(t)
		_, _, _, err := s.UploadPartCopy(uploadID, 1, "no-such-bucket", "obj.txt", "", nil)
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("copies versioned source object", func(t *testing.T) {
		s, uploadID := setup(t)
		require.NoError(t, s.PutBucketVersioning("src-bucket", "Enabled"))

		meta1, err := s.PutObject(
			"src-bucket",
			"ver.txt",
			strings.NewReader("version-one"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		_, err = s.PutObject(
			"src-bucket",
			"ver.txt",
			strings.NewReader("version-two"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		etag, _, _, err := s.UploadPartCopy(
			uploadID,
			1,
			"src-bucket",
			"ver.txt",
			meta1.VersionID,
			nil,
		)
		require.NoError(t, err)

		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: etag}})
		require.NoError(t, err)

		f, _, err := s.GetObject("dst-bucket", "dest.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, "version-one", string(data))
	})

	t.Run("copies current version when versionId matches current object", func(t *testing.T) {
		s, uploadID := setup(t)
		require.NoError(t, s.PutBucketVersioning("src-bucket", "Enabled"))

		meta, err := s.PutObject(
			"src-bucket", "cur.txt", strings.NewReader("current"), "text/plain", nil, "", "", false,
			"",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		require.NotEmpty(t, meta.VersionID)

		// meta.VersionID is the current version — exercises the cm.VersionID == srcVersionID branch.
		etag, _, _, err := s.UploadPartCopy(
			uploadID,
			1,
			"src-bucket",
			"cur.txt",
			meta.VersionID,
			nil,
		)
		require.NoError(t, err)

		_, err = s.CompleteMultipartUpload(uploadID, []CompletePart{{PartNumber: 1, ETag: etag}})
		require.NoError(t, err)

		f, _, err := s.GetObject("dst-bucket", "dest.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		d, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, "current", string(d))
	})

	t.Run("returns ErrObjectNotFound for versioned delete marker", func(t *testing.T) {
		s, uploadID := setup(t)
		require.NoError(t, s.PutBucketVersioning("src-bucket", "Enabled"))

		_, err := s.PutObject(
			"src-bucket", "del.txt", strings.NewReader("data"), "text/plain", nil, "", "", false,
			"",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		// DeleteObjectVersioned creates a delete marker and returns its versionID.
		dmVersionID, _, err := s.DeleteObjectVersioned("src-bucket", "del.txt", false)
		require.NoError(t, err)
		require.NotEmpty(t, dmVersionID)

		_, _, _, err = s.UploadPartCopy(uploadID, 1, "src-bucket", "del.txt", dmVersionID, nil)
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("returns copySourceVersionID for versioned source", func(t *testing.T) {
		s, uploadID := setup(t)
		require.NoError(t, s.PutBucketVersioning("src-bucket", "Enabled"))
		meta, err := s.PutObject(
			"src-bucket", "ver.txt", strings.NewReader("v1"), "text/plain", nil, "", "", false,
			"",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		require.NotEmpty(t, meta.VersionID)

		_, _, copySourceVersionID, err := s.UploadPartCopy(
			uploadID,
			1,
			"src-bucket",
			"ver.txt",
			"",
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, meta.VersionID, copySourceVersionID)
	})

	t.Run("returns empty copySourceVersionID for unversioned source", func(t *testing.T) {
		s, uploadID := setup(t)
		_, _, copySourceVersionID, err := s.UploadPartCopy(
			uploadID,
			1,
			"src-bucket",
			"source.txt",
			"",
			nil,
		)
		require.NoError(t, err)
		assert.Empty(t, copySourceVersionID)
	})

	t.Run("returns ErrObjectNotFound when versionId does not exist in archive", func(t *testing.T) {
		s, uploadID := setup(t)
		require.NoError(t, s.PutBucketVersioning("src-bucket", "Enabled"))
		_, err := s.PutObject(
			"src-bucket", "ver.txt", strings.NewReader("v1"), "text/plain", nil, "", "", false,
			"",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		_, _, _, err = s.UploadPartCopy(
			uploadID,
			1,
			"src-bucket",
			"ver.txt",
			"nonexistent-version-id",
			nil,
		)
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run(
		"returns error when current object meta is corrupt and versionId is set",
		func(t *testing.T) {
			s, rootPath := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("src-bucket", "", false))
			require.NoError(t, s.CreateBucket("dst-bucket", "", false))
			require.NoError(t, s.PutBucketVersioning("src-bucket", "Enabled"))
			meta1, err := s.PutObject(
				"src-bucket", "obj.txt", strings.NewReader("v1"), "text/plain", nil, "", "", false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			// Put a second version so v1 is archived and a current object file exists.
			_, err = s.PutObject(
				"src-bucket", "obj.txt", strings.NewReader("v2"), "text/plain", nil, "", "", false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			uploadID, err := s.CreateMultipartUpload(
				"dst-bucket",
				"obj.txt",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)

			// Corrupt the current object's meta so readMeta returns a non-ErrNotExist error.
			require.NoError(t, os.WriteFile(
				filepath.Join(rootPath, "src-bucket", "obj.txt.meta.json"),
				[]byte("not valid json"),
				0o600,
			))

			// The handler tries to read the current meta, gets a json error (not ErrNotExist),
			// and returns it immediately without falling through to the archive lookup.
			_, _, _, err = s.UploadPartCopy(
				uploadID,
				1,
				"src-bucket",
				"obj.txt",
				meta1.VersionID,
				nil,
			)
			assert.Error(t, err)
			assert.NotErrorIs(t, err, ErrObjectNotFound)
		},
	)

	t.Run(
		"returns error when upload.json stat fails with non-ErrNotExist error",
		func(t *testing.T) {
			if os.Getuid() == 0 {
				t.Skip("skipping: cannot test permission errors as root")
			}
			s, rootPath := newTestStorageWithRoot(t)
			require.NoError(t, s.CreateBucket("src-bucket", "", false))
			require.NoError(t, s.CreateBucket("dst-bucket", "", false))
			_, err := s.PutObject(
				"src-bucket",
				"obj.txt",
				strings.NewReader("data"),
				"text/plain",
				nil,
				"",
				"",
				false,
				"",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			uploadID, err := s.CreateMultipartUpload(
				"dst-bucket",
				"obj.txt",
				"text/plain",
				"",
				"", false, "",
				nil,
				nil,
				"",
				nil,
			)
			require.NoError(t, err)

			uploadDir := filepath.Join(rootPath, ".mpu", uploadID)
			require.NoError(t, os.Chmod(uploadDir, 0))
			t.Cleanup(func() { _ = os.Chmod(uploadDir, 0o750) })

			_, _, _, err = s.UploadPartCopy(uploadID, 1, "src-bucket", "obj.txt", "", nil)
			assert.Error(t, err)
			assert.NotErrorIs(t, err, ErrUploadNotFound)
		},
	)
}

func TestObjectRetention(t *testing.T) {
	retention := ObjectRetention{
		Mode:            "GOVERNANCE",
		RetainUntilDate: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	setup := func(t *testing.T) (*Storage, string, string) {
		t.Helper()
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		return s, "b", "obj.txt"
	}

	t.Run("Put/Get roundtrip", func(t *testing.T) {
		s, bucket, key := setup(t)
		require.NoError(t, s.PutObjectRetention(bucket, key, "", retention))
		got, err := s.GetObjectRetention(bucket, key, "")
		require.NoError(t, err)
		assert.Equal(t, retention.Mode, got.Mode)
		assert.True(t, retention.RetainUntilDate.Equal(got.RetainUntilDate))
	})

	t.Run("Get returns ErrNoObjectRetention when not set", func(t *testing.T) {
		s, bucket, key := setup(t)
		_, err := s.GetObjectRetention(bucket, key, "")
		assert.ErrorIs(t, err, ErrNoObjectRetention)
	})

	t.Run("Put overwrites existing retention", func(t *testing.T) {
		s, bucket, key := setup(t)
		require.NoError(t, s.PutObjectRetention(bucket, key, "", retention))
		updated := ObjectRetention{
			Mode:            "COMPLIANCE",
			RetainUntilDate: time.Date(2035, 6, 1, 0, 0, 0, 0, time.UTC),
		}
		require.NoError(t, s.PutObjectRetention(bucket, key, "", updated))
		got, err := s.GetObjectRetention(bucket, key, "")
		require.NoError(t, err)
		assert.Equal(t, "COMPLIANCE", got.Mode)
	})

	t.Run("Put returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		assert.ErrorIs(
			t,
			s.PutObjectRetention("no-bucket", "obj.txt", "", retention),
			ErrBucketNotFound,
		)
	})

	t.Run("Get returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.GetObjectRetention("no-bucket", "obj.txt", "")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("Put returns ErrObjectNotFound for missing object", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		assert.ErrorIs(
			t,
			s.PutObjectRetention("b", "missing.txt", "", retention),
			ErrObjectNotFound,
		)
	})

	t.Run("Get returns ErrObjectNotFound for missing object", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.GetObjectRetention("b", "missing.txt", "")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("Put/Get by versionId on versioned bucket", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
		m1, err := s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		m2, err := s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		// Set retention only on v1 (archived).
		require.NoError(t, s.PutObjectRetention("b", "obj.txt", m1.VersionID, retention))

		got1, err := s.GetObjectRetention("b", "obj.txt", m1.VersionID)
		require.NoError(t, err)
		assert.Equal(t, retention.Mode, got1.Mode)

		// v2 (current) should have no retention.
		_, err = s.GetObjectRetention("b", "obj.txt", m2.VersionID)
		assert.ErrorIs(t, err, ErrNoObjectRetention)
	})

	t.Run("Put returns DeleteMarkerError for delete marker", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
		_, err := s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		markerVID, _, err := s.DeleteObjectVersioned("b", "obj.txt", false)
		require.NoError(t, err)

		err = s.PutObjectRetention("b", "obj.txt", markerVID, retention)
		var dme *DeleteMarkerError
		assert.ErrorAs(t, err, &dme)
	})

	t.Run("Get returns DeleteMarkerError for delete marker", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
		_, err := s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		markerVID, _, err := s.DeleteObjectVersioned("b", "obj.txt", false)
		require.NoError(t, err)

		_, err = s.GetObjectRetention("b", "obj.txt", markerVID)
		var dme *DeleteMarkerError
		assert.ErrorAs(t, err, &dme)
	})

	t.Run(
		"Put returns DeleteMarkerError when current is delete marker and no versionId",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("b", "", false))
			require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
			_, err := s.PutObject(
				"b",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			_, _, err = s.DeleteObjectVersioned("b", "obj.txt", false)
			require.NoError(t, err)

			err = s.PutObjectRetention("b", "obj.txt", "", retention)
			var dme *DeleteMarkerError
			assert.ErrorAs(t, err, &dme)
		},
	)

	t.Run(
		"Get returns DeleteMarkerError when current is delete marker and no versionId",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateBucket("b", "", false))
			require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
			_, err := s.PutObject(
				"b",
				"obj.txt",
				strings.NewReader("v1"),
				"text/plain",
				nil,
				"",
				"", false, "",
				nil,
				nil,
				"",
			)
			require.NoError(t, err)
			_, _, err = s.DeleteObjectVersioned("b", "obj.txt", false)
			require.NoError(t, err)

			_, err = s.GetObjectRetention("b", "obj.txt", "")
			var dme *DeleteMarkerError
			assert.ErrorAs(t, err, &dme)
		},
	)

	t.Run("Put returns DeleteMarkerError for archived delete marker", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
		_, err := s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		markerVID, _, err := s.DeleteObjectVersioned("b", "obj.txt", false)
		require.NoError(t, err)
		// Put v2 — archives the delete marker.
		_, err = s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		err = s.PutObjectRetention("b", "obj.txt", markerVID, retention)
		var dme *DeleteMarkerError
		assert.ErrorAs(t, err, &dme)
	})

	t.Run("Get returns DeleteMarkerError for archived delete marker", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
		_, err := s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		markerVID, _, err := s.DeleteObjectVersioned("b", "obj.txt", false)
		require.NoError(t, err)
		// Put v2 — archives the delete marker.
		_, err = s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		_, err = s.GetObjectRetention("b", "obj.txt", markerVID)
		var dme *DeleteMarkerError
		assert.ErrorAs(t, err, &dme)
	})
}

func TestObjectLegalHold(t *testing.T) {
	setup := func(t *testing.T) (*Storage, string, string) {
		t.Helper()
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("data"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		return s, "b", "obj.txt"
	}

	t.Run("Put/Get roundtrip ON", func(t *testing.T) {
		s, bucket, key := setup(t)
		require.NoError(t, s.PutObjectLegalHold(bucket, key, "", "ON"))
		got, err := s.GetObjectLegalHold(bucket, key, "")
		require.NoError(t, err)
		assert.Equal(t, "ON", got)
	})

	t.Run("Put/Get roundtrip OFF", func(t *testing.T) {
		s, bucket, key := setup(t)
		require.NoError(t, s.PutObjectLegalHold(bucket, key, "", "ON"))
		require.NoError(t, s.PutObjectLegalHold(bucket, key, "", "OFF"))
		got, err := s.GetObjectLegalHold(bucket, key, "")
		require.NoError(t, err)
		assert.Equal(t, "OFF", got)
	})

	t.Run("Get returns ErrNoObjectLegalHold when not set", func(t *testing.T) {
		s, bucket, key := setup(t)
		_, err := s.GetObjectLegalHold(bucket, key, "")
		assert.ErrorIs(t, err, ErrNoObjectLegalHold)
	})

	t.Run("Put returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		assert.ErrorIs(t, s.PutObjectLegalHold("no-bucket", "obj.txt", "", "ON"), ErrBucketNotFound)
	})

	t.Run("Get returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.GetObjectLegalHold("no-bucket", "obj.txt", "")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("Put returns ErrObjectNotFound for missing object", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		assert.ErrorIs(t, s.PutObjectLegalHold("b", "missing.txt", "", "ON"), ErrObjectNotFound)
	})

	t.Run("Get returns ErrObjectNotFound for missing object", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		_, err := s.GetObjectLegalHold("b", "missing.txt", "")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("Put/Get by versionId on versioned bucket", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
		m1, err := s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		m2, err := s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("v2"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)

		// Set legal hold only on v1 (archived).
		require.NoError(t, s.PutObjectLegalHold("b", "obj.txt", m1.VersionID, "ON"))

		got1, err := s.GetObjectLegalHold("b", "obj.txt", m1.VersionID)
		require.NoError(t, err)
		assert.Equal(t, "ON", got1)

		// v2 (current) should have no legal hold.
		_, err = s.GetObjectLegalHold("b", "obj.txt", m2.VersionID)
		assert.ErrorIs(t, err, ErrNoObjectLegalHold)
	})

	t.Run("Put returns DeleteMarkerError for delete marker", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
		_, err := s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		markerVID, _, err := s.DeleteObjectVersioned("b", "obj.txt", false)
		require.NoError(t, err)

		err = s.PutObjectLegalHold("b", "obj.txt", markerVID, "ON")
		var dme *DeleteMarkerError
		assert.ErrorAs(t, err, &dme)
	})

	t.Run("Get returns DeleteMarkerError for delete marker", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
		_, err := s.PutObject(
			"b",
			"obj.txt",
			strings.NewReader("v1"),
			"text/plain",
			nil,
			"",
			"", false, "",
			nil,
			nil,
			"",
		)
		require.NoError(t, err)
		markerVID, _, err := s.DeleteObjectVersioned("b", "obj.txt", false)
		require.NoError(t, err)

		_, err = s.GetObjectLegalHold("b", "obj.txt", markerVID)
		var dme *DeleteMarkerError
		assert.ErrorAs(t, err, &dme)
	})
}

func TestSetObjectVersionStorageClass(t *testing.T) {
	putObject := func(t *testing.T, s *Storage, bucket, key, content string) ObjectMetadata {
		t.Helper()
		meta, err := s.PutObject(
			bucket, key,
			strings.NewReader(content),
			"text/plain", nil,
			"", "", false, "",
			nil, nil, "",
		)
		require.NoError(t, err)
		return meta
	}

	t.Run("returns ErrBucketNotFound for missing bucket", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.SetObjectVersionStorageClass("no-bucket", "obj.txt", "v1", "GLACIER")
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("updates StorageClass on current version", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))

		meta := putObject(t, s, "b", "obj.txt", "data")
		require.NotEmpty(t, meta.VersionID)

		require.NoError(
			t,
			s.SetObjectVersionStorageClass("b", "obj.txt", meta.VersionID, "GLACIER"),
		)

		head, err := s.HeadObject("b", "obj.txt")
		require.NoError(t, err)
		assert.Equal(t, "GLACIER", head.StorageClass)
	})

	t.Run("returns ErrObjectNotFound for unknown versionID", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))
		putObject(t, s, "b", "obj.txt", "data")

		err := s.SetObjectVersionStorageClass("b", "obj.txt", "does-not-exist", "GLACIER")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("updates StorageClass on archived (noncurrent) version", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("b", "", false))
		require.NoError(t, s.PutBucketVersioning("b", "Enabled"))

		v1 := putObject(t, s, "b", "obj.txt", "v1")
		require.NotEmpty(t, v1.VersionID)
		// Second put archives v1.
		putObject(t, s, "b", "obj.txt", "v2")

		require.NoError(t, s.SetObjectVersionStorageClass("b", "obj.txt", v1.VersionID, "GLACIER"))

		_, archivedMeta, err := s.GetObjectVersion("b", "obj.txt", v1.VersionID)
		require.NoError(t, err)
		assert.Equal(t, "GLACIER", archivedMeta.StorageClass)

		// Verify ListObjectVersions also reflects the updated StorageClass,
		// since the lifecycle enforcer reads it from listings to skip redundant transitions.
		versions, _, err := s.ListObjectVersions("b")
		require.NoError(t, err)
		var found bool
		for _, v := range versions {
			if v.VersionID == v1.VersionID {
				assert.Equal(t, "GLACIER", v.StorageClass)
				found = true
				break
			}
		}
		assert.True(t, found, "v1 not found in ListObjectVersions result")
	})
}
