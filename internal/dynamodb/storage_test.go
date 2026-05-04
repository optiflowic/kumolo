package dynamodb

import (
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStorage(t *testing.T) *Storage {
	t.Helper()
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustPutItem(t *testing.T, s *Storage, tableName string, item map[string]any) {
	t.Helper()
	_, err := s.PutItem(tableName, item, nil)
	require.NoError(t, err)
}

// badCloseWriter wraps an io.WriteCloser and returns an error on Close.
type badCloseWriter struct {
	io.WriteCloser
}

func (b badCloseWriter) Close() error {
	_ = b.WriteCloser.Close()
	return errors.New("simulated close failure")
}

var testKeySchema = []KeySchemaElement{
	{AttributeName: "pk", KeyType: "HASH"},
}

var testMeta = TableMetadata{
	Name:      "test-table",
	KeySchema: testKeySchema,
	AttributeDefinitions: []AttributeDefinition{
		{AttributeName: "pk", AttributeType: "S"},
	},
}

func TestNewStorage(t *testing.T) {
	t.Run("creates storage root", func(t *testing.T) {
		dir := t.TempDir()
		s, err := newStorage(dir, os.OpenRoot)
		require.NoError(t, err)
		require.NoError(t, s.Close())
		_, err = os.Stat(dir + "/dynamodb")
		assert.NoError(t, err)
	})

	t.Run("fails when openRoot errors", func(t *testing.T) {
		failOpen := func(string) (*os.Root, error) { return nil, errors.New("open failed") }
		_, err := newStorage(t.TempDir(), failOpen)
		assert.ErrorContains(t, err, "open storage root")
	})

	t.Run("fails when dynamodb path exists as a file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(dir+"/dynamodb", []byte("x"), 0o600))
		_, err := NewStorage(dir)
		assert.Error(t, err)
	})
}

func TestClose(t *testing.T) {
	t.Run("closes without error", func(t *testing.T) {
		s, err := NewStorage(t.TempDir())
		require.NoError(t, err)
		assert.NoError(t, s.Close())
	})
}

func TestCreateTable(t *testing.T) {
	t.Run("creates table successfully", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		meta, err := s.DescribeTable("test-table")
		require.NoError(t, err)
		assert.Equal(t, "test-table", meta.Name)
		assert.Equal(t, "ACTIVE", meta.Status)
		assert.False(t, meta.CreatedAt.IsZero())
	})

	t.Run("returns ErrTableAlreadyExists for duplicate", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		err := s.CreateTable(testMeta)
		assert.ErrorIs(t, err, ErrTableAlreadyExists)
	})

	t.Run("returns error when mkdir fails", func(t *testing.T) {
		s := newTestStorage(t)
		s.mkdirFn = func(string, os.FileMode) error { return errors.New("mkdir failed") }
		err := s.CreateTable(testMeta)
		assert.Error(t, err)
	})

	t.Run("cleans up dir on meta write failure", func(t *testing.T) {
		s := newTestStorage(t)
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("write failed")
		}
		err := s.CreateTable(testMeta)
		assert.Error(t, err)
		assert.False(t, s.tableExistsLocked("test-table"))
	})

	t.Run("logs warn when cleanup after meta write failure also fails", func(t *testing.T) {
		s := newTestStorage(t)
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("write failed")
		}
		s.removeFile = func(string) error { return errors.New("remove also failed") }
		err := s.CreateTable(testMeta)
		assert.Error(t, err)
	})
}

func TestDeleteTable(t *testing.T) {
	t.Run("deletes table and items", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{
			"pk": map[string]any{"S": "key1"},
		})
		require.NoError(t, s.DeleteTable("test-table"))
		assert.False(t, s.tableExistsLocked("test-table"))
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.DeleteTable("no-such-table")
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readDir fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.listDirFn = func(string) ([]os.DirEntry, error) {
			return nil, errors.New("read dir failed")
		}
		err := s.DeleteTable("test-table")
		assert.Error(t, err)
	})

	t.Run("returns error when removeFile fails for item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "k"}})
		s.removeFile = func(string) error { return errors.New("remove failed") }
		err := s.DeleteTable("test-table")
		assert.Error(t, err)
	})

	t.Run("returns error when removing table dir fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.removeFile = func(string) error { return errors.New("remove dir failed") }
		err := s.DeleteTable("test-table")
		assert.Error(t, err)
	})

	t.Run("returns error when removing table meta file fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		calls := 0
		s.removeFile = func(string) error {
			calls++
			if calls <= 1 {
				return nil // first call (remove dir) succeeds
			}
			return errors.New("remove meta failed")
		}
		err := s.DeleteTable("test-table")
		assert.Error(t, err)
	})
}

func TestDescribeTable(t *testing.T) {
	t.Run("returns table metadata", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		meta, err := s.DescribeTable("test-table")
		require.NoError(t, err)
		assert.Equal(t, "test-table", meta.Name)
		assert.Equal(t, testKeySchema, meta.KeySchema)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.DescribeTable("no-such-table")
		assert.ErrorIs(t, err, ErrTableNotFound)
	})
}

func TestListTables(t *testing.T) {
	t.Run("returns empty list when no tables", func(t *testing.T) {
		s := newTestStorage(t)
		names, err := s.ListTables()
		require.NoError(t, err)
		assert.Empty(t, names)
	})

	t.Run("returns all table names", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(TableMetadata{Name: "alpha", KeySchema: testKeySchema}))
		require.NoError(t, s.CreateTable(TableMetadata{Name: "beta", KeySchema: testKeySchema}))
		names, err := s.ListTables()
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"alpha", "beta"}, names)
	})

	t.Run("returns error when readDir fails", func(t *testing.T) {
		s := newTestStorage(t)
		s.listDirFn = func(string) ([]os.DirEntry, error) {
			return nil, errors.New("read dir failed")
		}
		_, err := s.ListTables()
		assert.Error(t, err)
	})
}

func TestPutItem(t *testing.T) {
	item := map[string]any{"pk": map[string]any{"S": "key1"}, "val": map[string]any{"S": "hello"}}

	t.Run("stores item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item)
	})

	t.Run("overwrites existing item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item)
		updated := map[string]any{
			"pk":  map[string]any{"S": "key1"},
			"val": map[string]any{"S": "updated"},
		}
		mustPutItem(t, s, "test-table", updated)
		got, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "key1"}})
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "updated", got["val"].(map[string]any)["S"])
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.PutItem("no-such-table", item, nil)
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns ErrValidationException for missing key attribute", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.PutItem(
			"test-table",
			map[string]any{"other": map[string]any{"S": "value"}},
			nil,
		)
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("returns error when readTableMeta fails with unexpected error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("unexpected") }
		_, err := s.PutItem("test-table", item, nil)
		assert.Error(t, err)
	})

	t.Run("returns error when reading old item fails with unexpected error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item)
		callCount := 0
		origReadAll := s.readAll
		s.readAll = func(r io.Reader) ([]byte, error) {
			callCount++
			if callCount > 1 {
				return nil, errors.New("unexpected read error")
			}
			return origReadAll(r)
		}
		_, err := s.PutItem("test-table", item, nil)
		assert.Error(t, err)
	})

	t.Run("returns old item when overwriting", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item)
		updated := map[string]any{
			"pk":  map[string]any{"S": "key1"},
			"val": map[string]any{"S": "new"},
		}
		old, err := s.PutItem("test-table", updated, nil)
		require.NoError(t, err)
		require.NotNil(t, old)
		assert.Equal(t, map[string]any{"S": "hello"}, old["val"])
	})

	t.Run("returns nil old item when no previous item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		old, err := s.PutItem("test-table", item, nil)
		require.NoError(t, err)
		assert.Nil(t, old)
	})
}

func TestGetItem(t *testing.T) {
	item := map[string]any{"pk": map[string]any{"S": "key1"}, "val": map[string]any{"S": "hello"}}

	t.Run("returns item by key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item)
		got, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "key1"}})
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "hello", got["val"].(map[string]any)["S"])
	})

	t.Run("returns nil for missing item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		got, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "missing"}})
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.GetItem("no-such-table", map[string]any{"pk": map[string]any{"S": "key1"}})
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns ErrValidationException for missing key attribute", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.GetItem("test-table", map[string]any{})
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("returns error when readTableMeta fails with unexpected error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("unexpected") }
		_, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "k"}})
		assert.Error(t, err)
	})

	t.Run("returns error when item file contains invalid JSON", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item)
		callCount := 0
		origReadAll := s.readAll
		s.readAll = func(r io.Reader) ([]byte, error) {
			callCount++
			if callCount > 1 {
				return []byte("not-json"), nil
			}
			return origReadAll(r)
		}
		_, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "key1"}})
		assert.Error(t, err)
	})
}

func TestDeleteItem(t *testing.T) {
	item := map[string]any{"pk": map[string]any{"S": "key1"}}

	t.Run("deletes existing item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item)
		_, err := s.DeleteItem("test-table", map[string]any{"pk": map[string]any{"S": "key1"}}, nil)
		require.NoError(t, err)
		got, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "key1"}})
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("no error when item does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.DeleteItem(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "missing"}},
			nil,
		)
		assert.NoError(t, err)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.DeleteItem(
			"no-such-table",
			map[string]any{"pk": map[string]any{"S": "key1"}},
			nil,
		)
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns ErrValidationException for missing key attribute", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.DeleteItem("test-table", map[string]any{}, nil)
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("returns error when readTableMeta fails with unexpected error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("unexpected") }
		_, err := s.DeleteItem("test-table", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
		assert.Error(t, err)
	})

	t.Run("returns error when removeFile fails with non-ErrNotExist error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "k"}})
		s.removeFile = func(string) error { return errors.New("remove failed") }
		_, err := s.DeleteItem("test-table", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
		assert.Error(t, err)
	})

	t.Run("returns deleted item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(
			t,
			s,
			"test-table",
			map[string]any{"pk": map[string]any{"S": "k"}, "v": map[string]any{"S": "hi"}},
		)
		old, err := s.DeleteItem("test-table", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
		require.NoError(t, err)
		require.NotNil(t, old)
		assert.Equal(t, map[string]any{"S": "hi"}, old["v"])
	})

	t.Run("returns nil when item not found", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		old, err := s.DeleteItem(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "missing"}},
			nil,
		)
		require.NoError(t, err)
		assert.Nil(t, old)
	})

	t.Run("returns error when reading old item fails with unexpected error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "k"}})
		callCount := 0
		origReadAll := s.readAll
		s.readAll = func(r io.Reader) ([]byte, error) {
			callCount++
			if callCount > 1 {
				return nil, errors.New("unexpected read error")
			}
			return origReadAll(r)
		}
		_, err := s.DeleteItem("test-table", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
		assert.Error(t, err)
	})
}

func TestScan(t *testing.T) {
	t.Run("returns all items", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "a"}})
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "b"}})
		items, err := s.Scan("test-table")
		require.NoError(t, err)
		assert.Len(t, items, 2)
	})

	t.Run("returns empty slice for empty table", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		items, err := s.Scan("test-table")
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.Scan("no-such-table")
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readDir fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.listDirFn = func(string) ([]os.DirEntry, error) {
			return nil, errors.New("read dir failed")
		}
		_, err := s.Scan("test-table")
		assert.Error(t, err)
	})

	t.Run("skips non-json files", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		f, err := s.root.OpenFile("test-table/other.txt", os.O_CREATE|os.O_WRONLY, 0o600)
		require.NoError(t, err)
		require.NoError(t, f.Close())
		items, err := s.Scan("test-table")
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("skips item when json is invalid", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "a"}})
		s.readAll = func(io.Reader) ([]byte, error) {
			return []byte("not-json"), nil
		}
		items, err := s.Scan("test-table")
		require.NoError(t, err)
		assert.Empty(t, items)
	})
}

func TestItemKey(t *testing.T) {
	schema := []KeySchemaElement{
		{AttributeName: "pk", KeyType: "HASH"},
		{AttributeName: "sk", KeyType: "RANGE"},
	}

	t.Run("produces deterministic key", func(t *testing.T) {
		item := map[string]any{"pk": map[string]any{"S": "p1"}, "sk": map[string]any{"S": "s1"}}
		k1, err := itemKey(item, schema)
		require.NoError(t, err)
		k2, err := itemKey(item, schema)
		require.NoError(t, err)
		assert.Equal(t, k1, k2)
	})

	t.Run("different keys for different values", func(t *testing.T) {
		item1 := map[string]any{"pk": map[string]any{"S": "p1"}, "sk": map[string]any{"S": "s1"}}
		item2 := map[string]any{"pk": map[string]any{"S": "p2"}, "sk": map[string]any{"S": "s1"}}
		k1, err := itemKey(item1, schema)
		require.NoError(t, err)
		k2, err := itemKey(item2, schema)
		require.NoError(t, err)
		assert.NotEqual(t, k1, k2)
	})

	t.Run("returns error for missing attribute", func(t *testing.T) {
		item := map[string]any{"pk": map[string]any{"S": "p1"}}
		_, err := itemKey(item, schema)
		assert.ErrorIs(t, err, ErrValidationException)
	})
}

func TestReadDir(t *testing.T) {
	t.Run("returns error when path does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.readDir("nonexistent")
		assert.Error(t, err)
	})
}

func TestWriteJSON_ReadJSON(t *testing.T) {
	t.Run("openFile error propagates", func(t *testing.T) {
		s := newTestStorage(t)
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("disk full")
		}
		err := s.writeJSON("some.json", map[string]any{"k": "v"})
		assert.Error(t, err)
	})

	t.Run("close error propagates", func(t *testing.T) {
		s := newTestStorage(t)
		origOpenFile := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			f, err := origOpenFile(name, flag, perm)
			if err != nil {
				return nil, err
			}
			return badCloseWriter{f}, nil
		}
		err := s.writeJSON("some.json", map[string]any{"k": "v"})
		assert.Error(t, err)
	})

	t.Run("readAll error propagates", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) {
			return nil, errors.New("read failed")
		}
		_, err := s.DescribeTable("test-table")
		assert.Error(t, err)
	})

	t.Run("unmarshal error propagates", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) {
			return []byte("not valid json"), nil
		}
		_, err := s.DescribeTable("test-table")
		assert.Error(t, err)
	})
}

func TestUpdateItem(t *testing.T) {
	item1 := map[string]any{"pk": map[string]any{"S": "k1"}, "val": map[string]any{"S": "old"}}

	t.Run("updates attribute on existing item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item1)
		_, got, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}},
			map[string]any{"val": map[string]any{"S": "new"}}, nil)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"S": "new"}, got["val"])
	})

	t.Run("returns before state", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item1)
		before, _, err := s.UpdateItem(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "k1"}},
			map[string]any{"val": map[string]any{"S": "new"}},
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"S": "old"}, before["val"])
	})

	t.Run("removes attribute when value is nil", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item1)
		_, got, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}},
			map[string]any{"val": nil}, nil)
		require.NoError(t, err)
		_, present := got["val"]
		assert.False(t, present)
	})

	t.Run("creates item if not exists", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, got, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "new"}},
			map[string]any{"x": map[string]any{"N": "1"}}, nil)
		require.NoError(t, err)
		assert.NotNil(t, got["pk"])
		assert.NotNil(t, got["x"])
	})

	t.Run("returns nil before when item did not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		before, _, err := s.UpdateItem(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "new"}},
			map[string]any{"x": map[string]any{"N": "1"}},
			nil,
		)
		require.NoError(t, err)
		assert.Nil(t, before)
	})

	t.Run("error when table not found", func(t *testing.T) {
		s := newTestStorage(t)
		_, _, err := s.UpdateItem(
			"no-table",
			map[string]any{"pk": map[string]any{"S": "k"}},
			nil,
			nil,
		)
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("error when key attribute missing", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, _, err := s.UpdateItem("test-table", map[string]any{}, nil, nil)
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("error when writeJSON fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item1)
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("open failed")
		}
		_, _, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}},
			map[string]any{"val": map[string]any{"S": "x"}}, nil)
		assert.Error(t, err)
	})

	t.Run("error when readAll fails for existing item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item1)
		callCount := 0
		s.readAll = func(r io.Reader) ([]byte, error) {
			callCount++
			if callCount > 1 { // first call reads table meta, second reads the item
				return nil, errors.New("read failed")
			}
			return io.ReadAll(r)
		}
		_, _, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}},
			map[string]any{"val": map[string]any{"S": "x"}}, nil)
		assert.Error(t, err)
	})

	t.Run("error when readTableMeta fails with non-ErrNotExist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) {
			return nil, errors.New("io failed")
		}
		_, _, err := s.UpdateItem(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "k1"}},
			nil,
			nil,
		)
		assert.Error(t, err)
	})
}

func TestConditionCheck(t *testing.T) {
	item := map[string]any{"pk": map[string]any{"S": "k1"}, "v": map[string]any{"N": "1"}}

	t.Run("PutItem: condition passes when item absent", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		cond := &ConditionCheck{Expr: "attribute_not_exists(pk)"}
		_, err := s.PutItem("test-table", item, cond)
		assert.NoError(t, err)
	})

	t.Run("PutItem: condition fails when item exists", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item)
		cond := &ConditionCheck{Expr: "attribute_not_exists(pk)"}
		_, err := s.PutItem("test-table", item, cond)
		assert.ErrorIs(t, err, ErrConditionalCheckFailed)
	})

	t.Run("PutItem: condition parse error returns ValidationException", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		cond := &ConditionCheck{
			Expr:  "attribute_not_exists(#missing)",
			Names: map[string]string{},
		}
		_, err := s.PutItem("test-table", item, cond)
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("DeleteItem: condition passes when item exists", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item)
		cond := &ConditionCheck{Expr: "attribute_exists(pk)"}
		_, err := s.DeleteItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}}, cond)
		assert.NoError(t, err)
	})

	t.Run("DeleteItem: condition fails when item absent", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		cond := &ConditionCheck{Expr: "attribute_exists(pk)"}
		_, err := s.DeleteItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}}, cond)
		assert.ErrorIs(t, err, ErrConditionalCheckFailed)
	})

	t.Run("DeleteItem: condition parse error returns ValidationException", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		cond := &ConditionCheck{
			Expr:  "attribute_exists(#missing)",
			Names: map[string]string{},
		}
		_, err := s.DeleteItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}}, cond)
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("UpdateItem: condition passes when version matches", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item)
		cond := &ConditionCheck{
			Expr:   "v = :cur",
			Values: map[string]any{":cur": map[string]any{"N": "1"}},
		}
		_, _, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}},
			map[string]any{"v": map[string]any{"N": "2"}}, cond)
		assert.NoError(t, err)
	})

	t.Run("UpdateItem: condition fails when version mismatches", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item)
		cond := &ConditionCheck{
			Expr:   "v = :cur",
			Values: map[string]any{":cur": map[string]any{"N": "99"}},
		}
		_, _, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}},
			map[string]any{"v": map[string]any{"N": "2"}}, cond)
		assert.ErrorIs(t, err, ErrConditionalCheckFailed)
	})

	t.Run("UpdateItem: condition against non-existent item uses empty map", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		cond := &ConditionCheck{Expr: "attribute_not_exists(pk)"}
		_, _, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "new"}},
			map[string]any{"v": map[string]any{"N": "1"}}, cond)
		assert.NoError(t, err)
	})

	t.Run("UpdateItem: condition parse error returns ValidationException", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item)
		cond := &ConditionCheck{
			Expr:  "attribute_exists(#missing)",
			Names: map[string]string{},
		}
		_, _, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}},
			map[string]any{"v": map[string]any{"N": "2"}}, cond)
		assert.ErrorIs(t, err, ErrValidationException)
	})
}

func TestQuery(t *testing.T) {
	t.Run("returns matching items by hash key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table",
			map[string]any{"pk": map[string]any{"S": "a"}, "v": map[string]any{"N": "1"}})
		mustPutItem(t, s, "test-table",
			map[string]any{"pk": map[string]any{"S": "b"}, "v": map[string]any{"N": "2"}})
		items, _, err := s.Query(
			"test-table",
			"pk",
			map[string]any{"S": "a"},
			nil,
			QueryOptions{ScanIndexForward: true},
		)
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, map[string]any{"S": "a"}, items[0]["pk"])
	})

	t.Run("returns empty slice when no match", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table",
			map[string]any{"pk": map[string]any{"S": "x"}})
		items, _, err := s.Query(
			"test-table",
			"pk",
			map[string]any{"S": "notfound"},
			nil,
			QueryOptions{ScanIndexForward: true},
		)
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("returns nil when hash key attribute absent in item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table",
			map[string]any{"pk": map[string]any{"S": "k1"}})
		items, _, err := s.Query(
			"test-table",
			"other",
			map[string]any{"S": "k1"},
			nil,
			QueryOptions{ScanIndexForward: true},
		)
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("error when table not found", func(t *testing.T) {
		s := newTestStorage(t)
		_, _, err := s.Query(
			"no-table",
			"pk",
			map[string]any{"S": "x"},
			nil,
			QueryOptions{ScanIndexForward: true},
		)
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		calls := 0
		s.readAll = func(r io.Reader) ([]byte, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("metadata read failed")
			}
			return io.ReadAll(r)
		}
		_, _, err := s.Query(
			"test-table",
			"pk",
			map[string]any{"S": "x"},
			nil,
			QueryOptions{ScanIndexForward: true},
		)
		assert.Error(t, err)
	})

	t.Run("error when readDir fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.listDirFn = func(string) ([]os.DirEntry, error) {
			return nil, errors.New("list failed")
		}
		_, _, err := s.Query(
			"test-table",
			"pk",
			map[string]any{"S": "x"},
			nil,
			QueryOptions{ScanIndexForward: true},
		)
		assert.Error(t, err)
	})
}

var skTestMeta = TableMetadata{
	Name: "sk-table",
	KeySchema: []KeySchemaElement{
		{AttributeName: "pk", KeyType: "HASH"},
		{AttributeName: "sk", KeyType: "RANGE"},
	},
	AttributeDefinitions: []AttributeDefinition{
		{AttributeName: "pk", AttributeType: "S"},
		{AttributeName: "sk", AttributeType: "S"},
	},
}

func TestQuerySortKeyCondition(t *testing.T) {
	mkItem := func(pk, sk string) map[string]any {
		return map[string]any{
			"pk": map[string]any{"S": pk},
			"sk": map[string]any{"S": sk},
		}
	}
	mkNumItem := func(pk string, sk int) map[string]any {
		return map[string]any{
			"pk": map[string]any{"S": pk},
			"sk": map[string]any{"N": fmt.Sprintf("%d", sk)},
		}
	}

	t.Run("equality", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		mustPutItem(t, s, "sk-table", mkItem("p", "a"))
		mustPutItem(t, s, "sk-table", mkItem("p", "b"))
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpEQ, Value: map[string]any{"S": "a"}},
			QueryOptions{ScanIndexForward: true})
		require.NoError(t, err)
		assert.Len(t, items, 1)
	})

	t.Run("less-than", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c"} {
			mustPutItem(t, s, "sk-table", mkItem("p", v))
		}
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpLT, Value: map[string]any{"S": "b"}},
			QueryOptions{ScanIndexForward: true})
		require.NoError(t, err)
		assert.Len(t, items, 1)
	})

	t.Run("less-than-or-equal", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c"} {
			mustPutItem(t, s, "sk-table", mkItem("p", v))
		}
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpLTE, Value: map[string]any{"S": "b"}},
			QueryOptions{ScanIndexForward: true})
		require.NoError(t, err)
		assert.Len(t, items, 2)
	})

	t.Run("greater-than", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c"} {
			mustPutItem(t, s, "sk-table", mkItem("p", v))
		}
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpGT, Value: map[string]any{"S": "b"}},
			QueryOptions{ScanIndexForward: true})
		require.NoError(t, err)
		assert.Len(t, items, 1)
	})

	t.Run("greater-than-or-equal", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c"} {
			mustPutItem(t, s, "sk-table", mkItem("p", v))
		}
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpGTE, Value: map[string]any{"S": "b"}},
			QueryOptions{ScanIndexForward: true})
		require.NoError(t, err)
		assert.Len(t, items, 2)
	})

	t.Run("BETWEEN", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c", "d"} {
			mustPutItem(t, s, "sk-table", mkItem("p", v))
		}
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{
				Name:     "sk",
				Operator: OpBETWEEN,
				Value:    map[string]any{"S": "b"},
				Value2:   map[string]any{"S": "c"},
			},
			QueryOptions{ScanIndexForward: true})
		require.NoError(t, err)
		assert.Len(t, items, 2)
	})

	t.Run("begins_with", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"foo1", "foo2", "bar"} {
			mustPutItem(t, s, "sk-table", mkItem("p", v))
		}
		items, _, err := s.Query(
			"sk-table",
			"pk",
			map[string]any{"S": "p"},
			&SortKeyCondition{
				Name:     "sk",
				Operator: OpBeginsWith,
				Value:    map[string]any{"S": "foo"},
			},
			QueryOptions{ScanIndexForward: true},
		)
		require.NoError(t, err)
		assert.Len(t, items, 2)
	})

	t.Run("begins_with with non-S type returns no match", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		mustPutItem(t, s, "sk-table",
			map[string]any{"pk": map[string]any{"S": "p"}, "sk": map[string]any{"N": "1"}})
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpBeginsWith, Value: map[string]any{"N": "1"}},
			QueryOptions{ScanIndexForward: true})
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("numeric comparison", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []int{1, 2, 3, 4, 5} {
			mustPutItem(t, s, "sk-table", mkNumItem("p", v))
		}
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpGTE, Value: map[string]any{"N": "3"}},
			QueryOptions{ScanIndexForward: true})
		require.NoError(t, err)
		assert.Len(t, items, 3)
	})

	t.Run("item without sort key attribute is excluded", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		mustPutItem(t, s, "sk-table",
			map[string]any{"pk": map[string]any{"S": "p"}, "sk": map[string]any{"S": "x"}})
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "other", Operator: OpEQ, Value: map[string]any{"S": "x"}},
			QueryOptions{ScanIndexForward: true})
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("unknown operator returns no match", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		mustPutItem(t, s, "sk-table", mkItem("p", "x"))
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: "contains", Value: map[string]any{"S": "x"}},
			QueryOptions{ScanIndexForward: true})
		require.NoError(t, err)
		assert.Empty(t, items)
	})
}

func TestQueryScanIndexForward(t *testing.T) {
	mkItem := func(pk, sk string) map[string]any {
		return map[string]any{
			"pk": map[string]any{"S": pk},
			"sk": map[string]any{"S": sk},
		}
	}

	setup := func(t *testing.T) *Storage {
		t.Helper()
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c", "d"} {
			mustPutItem(t, s, "sk-table", mkItem("p", v))
		}
		return s
	}

	t.Run("ascending order (ScanIndexForward=true)", func(t *testing.T) {
		s := setup(t)
		items, _, err := s.Query(
			"sk-table",
			"pk",
			map[string]any{"S": "p"},
			nil,
			QueryOptions{ScanIndexForward: true},
		)
		require.NoError(t, err)
		require.Len(t, items, 4)
		assert.Equal(t, "a", items[0]["sk"].(map[string]any)["S"])
		assert.Equal(t, "d", items[3]["sk"].(map[string]any)["S"])
	})

	t.Run("descending order (ScanIndexForward=false)", func(t *testing.T) {
		s := setup(t)
		items, _, err := s.Query(
			"sk-table",
			"pk",
			map[string]any{"S": "p"},
			nil,
			QueryOptions{ScanIndexForward: false},
		)
		require.NoError(t, err)
		require.Len(t, items, 4)
		assert.Equal(t, "d", items[0]["sk"].(map[string]any)["S"])
		assert.Equal(t, "a", items[3]["sk"].(map[string]any)["S"])
	})

	t.Run("no sort key schema: order is stable without panic", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta)) // hash-only table
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "x"}})
		items, _, err := s.Query(
			"test-table",
			"pk",
			map[string]any{"S": "x"},
			nil,
			QueryOptions{ScanIndexForward: false},
		)
		require.NoError(t, err)
		assert.Len(t, items, 1)
	})
}

func TestQueryLimit(t *testing.T) {
	mkItem := func(pk, sk string) map[string]any {
		return map[string]any{
			"pk": map[string]any{"S": pk},
			"sk": map[string]any{"S": sk},
		}
	}

	setup := func(t *testing.T) *Storage {
		t.Helper()
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c", "d", "e"} {
			mustPutItem(t, s, "sk-table", mkItem("p", v))
		}
		return s
	}

	t.Run("Limit=2 returns first 2 items and LastEvaluatedKey", func(t *testing.T) {
		s := setup(t)
		items, lek, err := s.Query(
			"sk-table",
			"pk",
			map[string]any{"S": "p"},
			nil,
			QueryOptions{ScanIndexForward: true, Limit: 2},
		)
		require.NoError(t, err)
		require.Len(t, items, 2)
		assert.Equal(t, "a", items[0]["sk"].(map[string]any)["S"])
		assert.Equal(t, "b", items[1]["sk"].(map[string]any)["S"])
		require.NotNil(t, lek)
		assert.Equal(t, map[string]any{"S": "b"}, lek["sk"])
		assert.Equal(t, map[string]any{"S": "p"}, lek["pk"])
	})

	t.Run("Limit >= total items returns no LastEvaluatedKey", func(t *testing.T) {
		s := setup(t)
		items, lek, err := s.Query(
			"sk-table",
			"pk",
			map[string]any{"S": "p"},
			nil,
			QueryOptions{ScanIndexForward: true, Limit: 10},
		)
		require.NoError(t, err)
		assert.Len(t, items, 5)
		assert.Nil(t, lek)
	})

	t.Run("Limit=0 means no limit", func(t *testing.T) {
		s := setup(t)
		items, lek, err := s.Query(
			"sk-table",
			"pk",
			map[string]any{"S": "p"},
			nil,
			QueryOptions{ScanIndexForward: true, Limit: 0},
		)
		require.NoError(t, err)
		assert.Len(t, items, 5)
		assert.Nil(t, lek)
	})

	t.Run("Limit with ScanIndexForward=false returns last items", func(t *testing.T) {
		s := setup(t)
		items, lek, err := s.Query(
			"sk-table",
			"pk",
			map[string]any{"S": "p"},
			nil,
			QueryOptions{ScanIndexForward: false, Limit: 2},
		)
		require.NoError(t, err)
		require.Len(t, items, 2)
		assert.Equal(t, "e", items[0]["sk"].(map[string]any)["S"])
		assert.Equal(t, "d", items[1]["sk"].(map[string]any)["S"])
		require.NotNil(t, lek)
		assert.Equal(t, map[string]any{"S": "d"}, lek["sk"])
	})
}

func TestQueryExclusiveStartKey(t *testing.T) {
	mkItem := func(pk, sk string) map[string]any {
		return map[string]any{
			"pk": map[string]any{"S": pk},
			"sk": map[string]any{"S": sk},
		}
	}

	setup := func(t *testing.T) *Storage {
		t.Helper()
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c", "d", "e"} {
			mustPutItem(t, s, "sk-table", mkItem("p", v))
		}
		return s
	}

	t.Run("resumes after cursor", func(t *testing.T) {
		s := setup(t)
		cursor := map[string]any{
			"pk": map[string]any{"S": "p"},
			"sk": map[string]any{"S": "b"},
		}
		items, lek, err := s.Query("sk-table", "pk", map[string]any{"S": "p"}, nil,
			QueryOptions{ScanIndexForward: true, Limit: 2, ExclusiveStartKey: cursor})
		require.NoError(t, err)
		require.Len(t, items, 2)
		assert.Equal(t, "c", items[0]["sk"].(map[string]any)["S"])
		assert.Equal(t, "d", items[1]["sk"].(map[string]any)["S"])
		require.NotNil(t, lek)
		assert.Equal(t, map[string]any{"S": "d"}, lek["sk"])
	})

	t.Run("cursor at last page returns no LastEvaluatedKey", func(t *testing.T) {
		s := setup(t)
		cursor := map[string]any{
			"pk": map[string]any{"S": "p"},
			"sk": map[string]any{"S": "c"},
		}
		items, lek, err := s.Query("sk-table", "pk", map[string]any{"S": "p"}, nil,
			QueryOptions{ScanIndexForward: true, ExclusiveStartKey: cursor})
		require.NoError(t, err)
		assert.Len(t, items, 2) // "d" and "e"
		assert.Nil(t, lek)
	})

	t.Run("unknown cursor key returns empty result", func(t *testing.T) {
		s := setup(t)
		cursor := map[string]any{
			"pk": map[string]any{"S": "p"},
			"sk": map[string]any{"S": "z"},
		}
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"}, nil,
			QueryOptions{ScanIndexForward: true, ExclusiveStartKey: cursor})
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("paginate all items with Limit=2", func(t *testing.T) {
		s := setup(t)
		var allItems []map[string]any
		var cursor map[string]any
		for {
			items, lek, err := s.Query("sk-table", "pk", map[string]any{"S": "p"}, nil,
				QueryOptions{ScanIndexForward: true, Limit: 2, ExclusiveStartKey: cursor})
			require.NoError(t, err)
			allItems = append(allItems, items...)
			if lek == nil {
				break
			}
			cursor = lek
		}
		require.Len(t, allItems, 5)
		for i, expected := range []string{"a", "b", "c", "d", "e"} {
			assert.Equal(t, expected, allItems[i]["sk"].(map[string]any)["S"])
		}
	})
}

func TestMatchesSortKey(t *testing.T) {
	t.Run("begins_with returns false when itemVal is not a map", func(t *testing.T) {
		cond := SortKeyCondition{
			Name:     "sk",
			Operator: OpBeginsWith,
			Value:    map[string]any{"S": "foo"},
		}
		assert.False(t, matchesSortKey("not-a-map", cond))
	})
}

func TestDynamoValueCmp(t *testing.T) {
	tests := []struct {
		name    string
		a, b    any
		wantCmp int
		wantErr bool
	}{
		{"string less than", map[string]any{"S": "a"}, map[string]any{"S": "b"}, -1, false},
		{"string equal", map[string]any{"S": "a"}, map[string]any{"S": "a"}, 0, false},
		{"string greater than", map[string]any{"S": "b"}, map[string]any{"S": "a"}, 1, false},
		{"number less than", map[string]any{"N": "1"}, map[string]any{"N": "2"}, -1, false},
		{"number equal", map[string]any{"N": "3"}, map[string]any{"N": "3"}, 0, false},
		{"number greater than", map[string]any{"N": "5"}, map[string]any{"N": "2"}, 1, false},
		{"non-map a returns error", "not a map", map[string]any{"S": "x"}, 0, true},
		{"non-map b returns error", map[string]any{"S": "x"}, "not a map", 0, true},
		{"string type mismatch", map[string]any{"S": "x"}, map[string]any{"N": "1"}, 0, true},
		{"number type mismatch", map[string]any{"N": "1"}, map[string]any{"S": "x"}, 0, true},
		{
			"bool fallback equal",
			map[string]any{"BOOL": true},
			map[string]any{"BOOL": true},
			0,
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dynamoValueCmp(tc.a, tc.b)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			switch {
			case tc.wantCmp < 0:
				assert.Less(t, got, 0)
			case tc.wantCmp > 0:
				assert.Greater(t, got, 0)
			default:
				assert.Equal(t, 0, got)
			}
		})
	}
}

func TestBatchGetItems(t *testing.T) {
	item1 := map[string]any{"pk": map[string]any{"S": "k1"}, "val": map[string]any{"S": "v1"}}
	item2 := map[string]any{"pk": map[string]any{"S": "k2"}, "val": map[string]any{"S": "v2"}}

	t.Run("returns all requested items when all exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item1)
		mustPutItem(t, s, "test-table", item2)
		keys := []map[string]any{
			{"pk": map[string]any{"S": "k1"}},
			{"pk": map[string]any{"S": "k2"}},
		}
		items, err := s.BatchGetItems("test-table", keys)
		require.NoError(t, err)
		assert.Len(t, items, 2)
	})

	t.Run("omits items that do not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item1)
		keys := []map[string]any{
			{"pk": map[string]any{"S": "k1"}},
			{"pk": map[string]any{"S": "missing"}},
		}
		items, err := s.BatchGetItems("test-table", keys)
		require.NoError(t, err)
		assert.Len(t, items, 1)
	})

	t.Run("returns empty when no keys match", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		keys := []map[string]any{{"pk": map[string]any{"S": "missing"}}}
		items, err := s.BatchGetItems("test-table", keys)
		require.NoError(t, err)
		assert.Nil(t, items)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.BatchGetItems("no-such-table", nil)
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns ErrValidationException for missing key attribute", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		keys := []map[string]any{{}}
		_, err := s.BatchGetItems("test-table", keys)
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("returns error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("unexpected") }
		_, err := s.BatchGetItems("test-table", nil)
		assert.Error(t, err)
	})

	t.Run("returns error when item read fails with unexpected error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", item1)
		callCount := 0
		origReadAll := s.readAll
		s.readAll = func(r io.Reader) ([]byte, error) {
			callCount++
			if callCount > 1 {
				return nil, errors.New("read failed")
			}
			return origReadAll(r)
		}
		keys := []map[string]any{{"pk": map[string]any{"S": "k1"}}}
		_, err := s.BatchGetItems("test-table", keys)
		assert.Error(t, err)
	})
}

func TestBatchWriteItems(t *testing.T) {
	t.Run("puts multiple items", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		puts := []map[string]any{
			{"pk": map[string]any{"S": "k1"}},
			{"pk": map[string]any{"S": "k2"}},
		}
		require.NoError(t, s.BatchWriteItems("test-table", puts, nil))
		items, err := s.Scan("test-table")
		require.NoError(t, err)
		assert.Len(t, items, 2)
	})

	t.Run("deletes multiple items", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "k1"}})
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "k2"}})
		deletes := []map[string]any{
			{"pk": map[string]any{"S": "k1"}},
			{"pk": map[string]any{"S": "k2"}},
		}
		require.NoError(t, s.BatchWriteItems("test-table", nil, deletes))
		items, err := s.Scan("test-table")
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("no error when deleting non-existent item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		deletes := []map[string]any{{"pk": map[string]any{"S": "missing"}}}
		assert.NoError(t, s.BatchWriteItems("test-table", nil, deletes))
	})

	t.Run("applies mixed puts and deletes", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "old"}})
		puts := []map[string]any{{"pk": map[string]any{"S": "new"}}}
		deletes := []map[string]any{{"pk": map[string]any{"S": "old"}}}
		require.NoError(t, s.BatchWriteItems("test-table", puts, deletes))
		items, err := s.Scan("test-table")
		require.NoError(t, err)
		assert.Len(t, items, 1)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.BatchWriteItems("no-such-table", nil, nil)
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns ErrValidationException for put with missing key attribute", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		puts := []map[string]any{{}}
		err := s.BatchWriteItems("test-table", puts, nil)
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run(
		"returns ErrValidationException for delete with missing key attribute",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(testMeta))
			deletes := []map[string]any{{}}
			err := s.BatchWriteItems("test-table", nil, deletes)
			assert.ErrorIs(t, err, ErrValidationException)
		},
	)

	t.Run("returns error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("unexpected") }
		err := s.BatchWriteItems("test-table", nil, nil)
		assert.Error(t, err)
	})

	t.Run("returns error when put writeJSON fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("open failed")
		}
		puts := []map[string]any{{"pk": map[string]any{"S": "k1"}}}
		err := s.BatchWriteItems("test-table", puts, nil)
		assert.Error(t, err)
	})

	t.Run("returns error when delete removeFile fails with non-ErrNotExist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "k1"}})
		s.removeFile = func(string) error { return errors.New("remove failed") }
		deletes := []map[string]any{{"pk": map[string]any{"S": "k1"}}}
		err := s.BatchWriteItems("test-table", nil, deletes)
		assert.Error(t, err)
	})
}

func TestUpdateTable(t *testing.T) {
	t.Run("updates billing mode and records timestamp", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		meta, err := s.UpdateTable("test-table", UpdateTableInput{BillingMode: "PROVISIONED"})
		require.NoError(t, err)
		assert.Equal(t, "PROVISIONED", meta.BillingMode)
		require.NotNil(t, meta.BillingModeUpdatedAt)
		assert.False(t, meta.BillingModeUpdatedAt.IsZero())
	})

	t.Run("does not update timestamp when billing mode unchanged", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.UpdateTable("test-table", UpdateTableInput{BillingMode: "PROVISIONED"})
		require.NoError(t, err)
		meta1, err := s.UpdateTable("test-table", UpdateTableInput{BillingMode: "PROVISIONED"})
		require.NoError(t, err)
		meta2, err := s.UpdateTable("test-table", UpdateTableInput{BillingMode: "PROVISIONED"})
		require.NoError(t, err)
		assert.Equal(t, meta1.BillingModeUpdatedAt, meta2.BillingModeUpdatedAt)
	})

	t.Run("updates provisioned throughput", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		pt := &ProvisionedThroughput{ReadCapacityUnits: 10, WriteCapacityUnits: 10}
		meta, err := s.UpdateTable("test-table", UpdateTableInput{ProvisionedThroughput: pt})
		require.NoError(t, err)
		require.NotNil(t, meta.ProvisionedThroughput)
		assert.Equal(t, int64(10), meta.ProvisionedThroughput.ReadCapacityUnits)
	})

	t.Run("creates GSI", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		gsi := GlobalSecondaryIndex{
			IndexName: "gsi1",
			KeySchema: []KeySchemaElement{{AttributeName: "sk", KeyType: "HASH"}},
		}
		meta, err := s.UpdateTable(
			"test-table",
			UpdateTableInput{GSICreates: []GlobalSecondaryIndex{gsi}},
		)
		require.NoError(t, err)
		require.Len(t, meta.GlobalSecondaryIndexes, 1)
		assert.Equal(t, "gsi1", meta.GlobalSecondaryIndexes[0].IndexName)
	})

	t.Run("merges AttributeDefinitions without duplicates", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		newAttrs := []AttributeDefinition{
			{AttributeName: "pk", AttributeType: "S"}, // duplicate, should be skipped
			{AttributeName: "sk", AttributeType: "S"}, // new
		}
		meta, err := s.UpdateTable("test-table", UpdateTableInput{AttributeDefinitions: newAttrs})
		require.NoError(t, err)
		var names []string
		for _, a := range meta.AttributeDefinitions {
			names = append(names, a.AttributeName)
		}
		assert.Contains(t, names, "pk")
		assert.Contains(t, names, "sk")
		assert.Len(t, meta.AttributeDefinitions, 2)
	})

	t.Run("updates GSI throughput", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		gsi := GlobalSecondaryIndex{
			IndexName: "gsi1",
			KeySchema: []KeySchemaElement{{AttributeName: "sk", KeyType: "HASH"}},
			ProvisionedThroughput: &ProvisionedThroughput{
				ReadCapacityUnits:  5,
				WriteCapacityUnits: 5,
			},
		}
		_, err := s.UpdateTable(
			"test-table",
			UpdateTableInput{GSICreates: []GlobalSecondaryIndex{gsi}},
		)
		require.NoError(t, err)
		newPT := &ProvisionedThroughput{ReadCapacityUnits: 20, WriteCapacityUnits: 20}
		meta, err := s.UpdateTable("test-table", UpdateTableInput{
			GSIUpdates: map[string]*ProvisionedThroughput{"gsi1": newPT},
		})
		require.NoError(t, err)
		require.NotNil(t, meta.GlobalSecondaryIndexes[0].ProvisionedThroughput)
		assert.Equal(
			t,
			int64(20),
			meta.GlobalSecondaryIndexes[0].ProvisionedThroughput.ReadCapacityUnits,
		)
	})

	t.Run("deletes GSI", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		gsi := GlobalSecondaryIndex{
			IndexName: "gsi1",
			KeySchema: []KeySchemaElement{{AttributeName: "sk", KeyType: "HASH"}},
		}
		_, err := s.UpdateTable(
			"test-table",
			UpdateTableInput{GSICreates: []GlobalSecondaryIndex{gsi}},
		)
		require.NoError(t, err)
		meta, err := s.UpdateTable("test-table", UpdateTableInput{GSIDeletes: []string{"gsi1"}})
		require.NoError(t, err)
		assert.Empty(t, meta.GlobalSecondaryIndexes)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.UpdateTable("no-such", UpdateTableInput{BillingMode: "PROVISIONED"})
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read error") }
		_, err := s.UpdateTable("test-table", UpdateTableInput{BillingMode: "PROVISIONED"})
		assert.Error(t, err)
	})

	t.Run("returns error when writeTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("write error")
		}
		_, err := s.UpdateTable("test-table", UpdateTableInput{BillingMode: "PROVISIONED"})
		assert.Error(t, err)
	})
}

const testTableARN = "arn:aws:dynamodb:us-east-1:000000000000:table/test-table"

func TestTagResource(t *testing.T) {
	t.Run("adds and merges tags", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		require.NoError(t, s.TagResource(testTableARN, map[string]string{"env": "dev"}))
		require.NoError(t, s.TagResource(testTableARN, map[string]string{"app": "kumolo"}))
		tags, err := s.ListTagsOfResource(testTableARN)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"env": "dev", "app": "kumolo"}, tags)
	})

	t.Run("returns ErrTableNotFound for invalid ARN", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.TagResource("invalid-arn", map[string]string{"k": "v"})
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.TagResource(testTableARN, map[string]string{"k": "v"})
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read error") }
		err := s.TagResource(testTableARN, map[string]string{"k": "v"})
		assert.Error(t, err)
	})

	t.Run("returns error when writeTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("write error")
		}
		err := s.TagResource(testTableARN, map[string]string{"k": "v"})
		assert.Error(t, err)
	})
}

func TestUntagResource(t *testing.T) {
	t.Run("removes specified tags", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		require.NoError(
			t,
			s.TagResource(testTableARN, map[string]string{"env": "dev", "app": "kumolo"}),
		)
		require.NoError(t, s.UntagResource(testTableARN, []string{"env"}))
		tags, err := s.ListTagsOfResource(testTableARN)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"app": "kumolo"}, tags)
	})

	t.Run("returns ErrTableNotFound for invalid ARN", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.UntagResource("invalid-arn", []string{"k"})
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.UntagResource(testTableARN, []string{"k"})
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read error") }
		err := s.UntagResource(testTableARN, []string{"k"})
		assert.Error(t, err)
	})

	t.Run("returns error when writeTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("write error")
		}
		err := s.UntagResource(testTableARN, []string{"k"})
		assert.Error(t, err)
	})
}

func TestListTagsOfResource(t *testing.T) {
	t.Run("returns empty map for untagged table", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		tags, err := s.ListTagsOfResource(testTableARN)
		require.NoError(t, err)
		assert.Empty(t, tags)
	})

	t.Run("returns ErrTableNotFound for invalid ARN", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.ListTagsOfResource("invalid-arn")
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.ListTagsOfResource(testTableARN)
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read error") }
		_, err := s.ListTagsOfResource(testTableARN)
		assert.Error(t, err)
	})
}

func TestUpdateTimeToLive(t *testing.T) {
	t.Run("enables and persists TTL", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		spec, err := s.UpdateTimeToLive(
			"test-table",
			TTLSpec{AttributeName: "expires", Enabled: true},
		)
		require.NoError(t, err)
		assert.Equal(t, "expires", spec.AttributeName)
		assert.True(t, spec.Enabled)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.UpdateTimeToLive("no-such", TTLSpec{AttributeName: "exp", Enabled: true})
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read error") }
		_, err := s.UpdateTimeToLive("test-table", TTLSpec{AttributeName: "exp", Enabled: true})
		assert.Error(t, err)
	})

	t.Run("returns error when writeTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("write error")
		}
		_, err := s.UpdateTimeToLive("test-table", TTLSpec{AttributeName: "exp", Enabled: true})
		assert.Error(t, err)
	})
}

func TestDescribeTimeToLive(t *testing.T) {
	t.Run("returns DISABLED with no TTL attribute when not set", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		status, spec, err := s.DescribeTimeToLive("test-table")
		require.NoError(t, err)
		assert.Equal(t, "DISABLED", status)
		assert.Nil(t, spec)
	})

	t.Run("returns ENABLED after UpdateTimeToLive enables it", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.UpdateTimeToLive("test-table", TTLSpec{AttributeName: "expires", Enabled: true})
		require.NoError(t, err)
		status, spec, err := s.DescribeTimeToLive("test-table")
		require.NoError(t, err)
		assert.Equal(t, "ENABLED", status)
		require.NotNil(t, spec)
		assert.Equal(t, "expires", spec.AttributeName)
	})

	t.Run("returns DISABLED after TTL is disabled", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.UpdateTimeToLive("test-table", TTLSpec{AttributeName: "expires", Enabled: true})
		require.NoError(t, err)
		_, err = s.UpdateTimeToLive("test-table", TTLSpec{AttributeName: "expires", Enabled: false})
		require.NoError(t, err)
		status, spec, err := s.DescribeTimeToLive("test-table")
		require.NoError(t, err)
		assert.Equal(t, "DISABLED", status)
		require.NotNil(t, spec)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, _, err := s.DescribeTimeToLive("no-such")
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readTableMeta fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read error") }
		_, _, err := s.DescribeTimeToLive("test-table")
		assert.Error(t, err)
	})
}
