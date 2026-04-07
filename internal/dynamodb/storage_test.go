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
		require.NoError(t, s.PutItem("test-table", map[string]any{
			"pk": map[string]any{"S": "key1"},
		}))
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
		require.NoError(t, s.PutItem("test-table", map[string]any{"pk": map[string]any{"S": "k"}}))
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
		require.NoError(t, s.PutItem("test-table", item))
	})

	t.Run("overwrites existing item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		require.NoError(t, s.PutItem("test-table", item))
		updated := map[string]any{
			"pk":  map[string]any{"S": "key1"},
			"val": map[string]any{"S": "updated"},
		}
		require.NoError(t, s.PutItem("test-table", updated))
		got, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "key1"}})
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "updated", got["val"].(map[string]any)["S"])
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.PutItem("no-such-table", item)
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns ErrValidationException for missing key attribute", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		err := s.PutItem("test-table", map[string]any{"other": map[string]any{"S": "value"}})
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("returns error when readTableMeta fails with unexpected error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("unexpected") }
		err := s.PutItem("test-table", item)
		assert.Error(t, err)
	})
}

func TestGetItem(t *testing.T) {
	item := map[string]any{"pk": map[string]any{"S": "key1"}, "val": map[string]any{"S": "hello"}}

	t.Run("returns item by key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		require.NoError(t, s.PutItem("test-table", item))
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
		require.NoError(t, s.PutItem("test-table", item))
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
		require.NoError(t, s.PutItem("test-table", item))
		require.NoError(
			t,
			s.DeleteItem("test-table", map[string]any{"pk": map[string]any{"S": "key1"}}),
		)
		got, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "key1"}})
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("no error when item does not exist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		err := s.DeleteItem("test-table", map[string]any{"pk": map[string]any{"S": "missing"}})
		assert.NoError(t, err)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.DeleteItem("no-such-table", map[string]any{"pk": map[string]any{"S": "key1"}})
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns ErrValidationException for missing key attribute", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		err := s.DeleteItem("test-table", map[string]any{})
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("returns error when readTableMeta fails with unexpected error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("unexpected") }
		err := s.DeleteItem("test-table", map[string]any{"pk": map[string]any{"S": "k"}})
		assert.Error(t, err)
	})

	t.Run("returns error when removeFile fails with non-ErrNotExist error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		require.NoError(t, s.PutItem("test-table", map[string]any{"pk": map[string]any{"S": "k"}}))
		s.removeFile = func(string) error { return errors.New("remove failed") }
		err := s.DeleteItem("test-table", map[string]any{"pk": map[string]any{"S": "k"}})
		assert.Error(t, err)
	})
}

func TestScan(t *testing.T) {
	t.Run("returns all items", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		require.NoError(t, s.PutItem("test-table", map[string]any{"pk": map[string]any{"S": "a"}}))
		require.NoError(t, s.PutItem("test-table", map[string]any{"pk": map[string]any{"S": "b"}}))
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
		require.NoError(t, s.PutItem("test-table", map[string]any{"pk": map[string]any{"S": "a"}}))
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
		require.NoError(t, s.PutItem("test-table", item1))
		got, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}},
			map[string]any{"val": map[string]any{"S": "new"}})
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"S": "new"}, got["val"])
	})

	t.Run("removes attribute when value is nil", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		require.NoError(t, s.PutItem("test-table", item1))
		got, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}},
			map[string]any{"val": nil})
		require.NoError(t, err)
		_, present := got["val"]
		assert.False(t, present)
	})

	t.Run("creates item if not exists", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		got, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "new"}},
			map[string]any{"x": map[string]any{"N": "1"}})
		require.NoError(t, err)
		assert.NotNil(t, got["pk"])
		assert.NotNil(t, got["x"])
	})

	t.Run("error when table not found", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.UpdateItem("no-table", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("error when key attribute missing", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.UpdateItem("test-table", map[string]any{}, nil)
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("error when writeJSON fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		require.NoError(t, s.PutItem("test-table", item1))
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("open failed")
		}
		_, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}},
			map[string]any{"val": map[string]any{"S": "x"}})
		assert.Error(t, err)
	})

	t.Run("error when readAll fails for existing item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		require.NoError(t, s.PutItem("test-table", item1))
		callCount := 0
		s.readAll = func(r io.Reader) ([]byte, error) {
			callCount++
			if callCount > 1 { // first call reads table meta, second reads the item
				return nil, errors.New("read failed")
			}
			return io.ReadAll(r)
		}
		_, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}},
			map[string]any{"val": map[string]any{"S": "x"}})
		assert.Error(t, err)
	})

	t.Run("error when readTableMeta fails with non-ErrNotExist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) {
			return nil, errors.New("io failed")
		}
		_, err := s.UpdateItem("test-table", map[string]any{"pk": map[string]any{"S": "k1"}}, nil)
		assert.Error(t, err)
	})
}

func TestQuery(t *testing.T) {
	t.Run("returns matching items by hash key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		require.NoError(t, s.PutItem("test-table",
			map[string]any{"pk": map[string]any{"S": "a"}, "v": map[string]any{"N": "1"}}))
		require.NoError(t, s.PutItem("test-table",
			map[string]any{"pk": map[string]any{"S": "b"}, "v": map[string]any{"N": "2"}}))
		items, err := s.Query("test-table", "pk", map[string]any{"S": "a"}, nil)
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, map[string]any{"S": "a"}, items[0]["pk"])
	})

	t.Run("returns empty slice when no match", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		require.NoError(t, s.PutItem("test-table",
			map[string]any{"pk": map[string]any{"S": "x"}}))
		items, err := s.Query("test-table", "pk", map[string]any{"S": "notfound"}, nil)
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("returns nil when hash key attribute absent in item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		require.NoError(t, s.PutItem("test-table",
			map[string]any{"pk": map[string]any{"S": "k1"}}))
		items, err := s.Query("test-table", "other", map[string]any{"S": "k1"}, nil)
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("error when table not found", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.Query("no-table", "pk", map[string]any{"S": "x"}, nil)
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("error when readDir fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.listDirFn = func(string) ([]os.DirEntry, error) {
			return nil, errors.New("list failed")
		}
		_, err := s.Query("test-table", "pk", map[string]any{"S": "x"}, nil)
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
		require.NoError(t, s.PutItem("sk-table", mkItem("p", "a")))
		require.NoError(t, s.PutItem("sk-table", mkItem("p", "b")))
		items, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpEQ, Value: map[string]any{"S": "a"}})
		require.NoError(t, err)
		assert.Len(t, items, 1)
	})

	t.Run("less-than", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c"} {
			require.NoError(t, s.PutItem("sk-table", mkItem("p", v)))
		}
		items, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpLT, Value: map[string]any{"S": "b"}})
		require.NoError(t, err)
		assert.Len(t, items, 1)
	})

	t.Run("less-than-or-equal", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c"} {
			require.NoError(t, s.PutItem("sk-table", mkItem("p", v)))
		}
		items, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpLTE, Value: map[string]any{"S": "b"}})
		require.NoError(t, err)
		assert.Len(t, items, 2)
	})

	t.Run("greater-than", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c"} {
			require.NoError(t, s.PutItem("sk-table", mkItem("p", v)))
		}
		items, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpGT, Value: map[string]any{"S": "b"}})
		require.NoError(t, err)
		assert.Len(t, items, 1)
	})

	t.Run("greater-than-or-equal", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c"} {
			require.NoError(t, s.PutItem("sk-table", mkItem("p", v)))
		}
		items, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpGTE, Value: map[string]any{"S": "b"}})
		require.NoError(t, err)
		assert.Len(t, items, 2)
	})

	t.Run("BETWEEN", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"a", "b", "c", "d"} {
			require.NoError(t, s.PutItem("sk-table", mkItem("p", v)))
		}
		items, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{
				Name:     "sk",
				Operator: OpBETWEEN,
				Value:    map[string]any{"S": "b"},
				Value2:   map[string]any{"S": "c"},
			})
		require.NoError(t, err)
		assert.Len(t, items, 2)
	})

	t.Run("begins_with", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []string{"foo1", "foo2", "bar"} {
			require.NoError(t, s.PutItem("sk-table", mkItem("p", v)))
		}
		items, err := s.Query(
			"sk-table",
			"pk",
			map[string]any{"S": "p"},
			&SortKeyCondition{
				Name:     "sk",
				Operator: OpBeginsWith,
				Value:    map[string]any{"S": "foo"},
			},
		)
		require.NoError(t, err)
		assert.Len(t, items, 2)
	})

	t.Run("begins_with with non-S type returns no match", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		require.NoError(t, s.PutItem("sk-table",
			map[string]any{"pk": map[string]any{"S": "p"}, "sk": map[string]any{"N": "1"}}))
		items, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpBeginsWith, Value: map[string]any{"N": "1"}})
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("numeric comparison", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		for _, v := range []int{1, 2, 3, 4, 5} {
			require.NoError(t, s.PutItem("sk-table", mkNumItem("p", v)))
		}
		items, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: OpGTE, Value: map[string]any{"N": "3"}})
		require.NoError(t, err)
		assert.Len(t, items, 3)
	})

	t.Run("item without sort key attribute is excluded", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		require.NoError(t, s.PutItem("sk-table",
			map[string]any{"pk": map[string]any{"S": "p"}, "sk": map[string]any{"S": "x"}}))
		items, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "other", Operator: OpEQ, Value: map[string]any{"S": "x"}})
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("unknown operator returns no match", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		require.NoError(t, s.PutItem("sk-table", mkItem("p", "x")))
		items, err := s.Query("sk-table", "pk", map[string]any{"S": "p"},
			&SortKeyCondition{Name: "sk", Operator: "contains", Value: map[string]any{"S": "x"}})
		require.NoError(t, err)
		assert.Empty(t, items)
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
