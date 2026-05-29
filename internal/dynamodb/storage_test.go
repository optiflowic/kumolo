package dynamodb

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int { return &v }

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

	t.Run(
		"succeeds on retry after orphan directory left from previous failure",
		func(t *testing.T) {
			s := newTestStorage(t)
			s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
				return nil, errors.New("write failed")
			}
			s.removeFile = func(string) error { return errors.New("remove also failed") }
			require.Error(t, s.CreateTable(testMeta))

			// Restore normal behaviour and retry — must not return ErrTableAlreadyExists.
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				return s.root.OpenFile(name, flag, perm)
			}
			s.removeFile = s.root.Remove
			require.NoError(t, s.CreateTable(testMeta))
			meta, err := s.DescribeTable("test-table")
			require.NoError(t, err)
			assert.Equal(t, "test-table", meta.Name)
		},
	)
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

	t.Run(
		"returns error when stat of metadata file fails with non-ErrNotExist",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(TableMetadata{Name: "t1", KeySchema: testKeySchema}))
			s.statFn = func(string) (os.FileInfo, error) {
				return nil, errors.New("injected stat error")
			}
			_, err := s.ListTables()
			assert.Error(t, err)
		},
	)

	t.Run("excludes orphan directory that has no metadata file", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(
			t,
			s.CreateTable(TableMetadata{Name: "real-table", KeySchema: testKeySchema}),
		)

		// Create an orphan directory (failed CreateTable + failed cleanup scenario).
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("write failed")
		}
		s.removeFile = func(string) error { return errors.New("remove also failed") }
		require.Error(t, s.CreateTable(TableMetadata{Name: "orphan", KeySchema: testKeySchema}))
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			return s.root.OpenFile(name, flag, perm)
		}
		s.removeFile = s.root.Remove

		names, err := s.ListTables()
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"real-table"}, names)
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

	t.Run("returns error when writeJSON fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
			return nil, errors.New("write error")
		}
		_, err := s.PutItem("test-table", item, nil)
		assert.Error(t, err)
	})
}

func TestIsTTLExpired(t *testing.T) {
	past := strconv.FormatInt(time.Now().Unix()-10, 10)
	future := strconv.FormatInt(time.Now().Unix()+3600, 10)
	pastFloat := fmt.Sprintf("%.3f", float64(time.Now().Unix()-10)+0.5)
	futureFloat := fmt.Sprintf("%.3f", float64(time.Now().Unix()+3600)+0.5)

	tests := []struct {
		name string
		ttl  *TTLSpec
		item map[string]any
		want bool
	}{
		{
			name: "nil TTL spec",
			ttl:  nil,
			item: map[string]any{"exp": map[string]any{"N": past}},
			want: false,
		},
		{
			name: "disabled TTL",
			ttl:  &TTLSpec{AttributeName: "exp", Enabled: false},
			item: map[string]any{"exp": map[string]any{"N": past}},
			want: false,
		},
		{
			name: "empty AttributeName",
			ttl:  &TTLSpec{AttributeName: "", Enabled: true},
			item: map[string]any{"exp": map[string]any{"N": past}},
			want: false,
		},
		{
			name: "attribute missing from item",
			ttl:  &TTLSpec{AttributeName: "exp", Enabled: true},
			item: map[string]any{},
			want: false,
		},
		{
			name: "attribute value is not a map",
			ttl:  &TTLSpec{AttributeName: "exp", Enabled: true},
			item: map[string]any{"exp": "not-a-map"},
			want: false,
		},
		{
			name: "attribute map has no N field",
			ttl:  &TTLSpec{AttributeName: "exp", Enabled: true},
			item: map[string]any{"exp": map[string]any{"S": "not-a-number"}},
			want: false,
		},
		{
			name: "N value is not a valid number",
			ttl:  &TTLSpec{AttributeName: "exp", Enabled: true},
			item: map[string]any{"exp": map[string]any{"N": "not-a-number"}},
			want: false,
		},
		{
			name: "expired item",
			ttl:  &TTLSpec{AttributeName: "exp", Enabled: true},
			item: map[string]any{"exp": map[string]any{"N": past}},
			want: true,
		},
		{
			name: "non-expired item",
			ttl:  &TTLSpec{AttributeName: "exp", Enabled: true},
			item: map[string]any{"exp": map[string]any{"N": future}},
			want: false,
		},
		{
			name: "expired item with float N value",
			ttl:  &TTLSpec{AttributeName: "exp", Enabled: true},
			item: map[string]any{"exp": map[string]any{"N": pastFloat}},
			want: true,
		},
		{
			name: "non-expired item with float N value",
			ttl:  &TTLSpec{AttributeName: "exp", Enabled: true},
			item: map[string]any{"exp": map[string]any{"N": futureFloat}},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTTLExpired(tc.item, tc.ttl))
		})
	}
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

	t.Run("returns nil for TTL-expired item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.UpdateTimeToLive("test-table", TTLSpec{AttributeName: "exp", Enabled: true})
		require.NoError(t, err)
		pastTS := strconv.FormatInt(time.Now().Unix()-10, 10)
		mustPutItem(t, s, "test-table", map[string]any{
			"pk":  map[string]any{"S": "key1"},
			"exp": map[string]any{"N": pastTS},
		})
		got, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "key1"}})
		require.NoError(t, err)
		assert.Nil(t, got)
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
		items, _, err := s.Scan("test-table", ScanOptions{})
		require.NoError(t, err)
		assert.Len(t, items, 2)
	})

	t.Run("returns empty slice for empty table", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		items, _, err := s.Scan("test-table", ScanOptions{})
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("returns ErrTableNotFound for missing table", func(t *testing.T) {
		s := newTestStorage(t)
		_, _, err := s.Scan("no-such-table", ScanOptions{})
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("returns error when readDir fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.listDirFn = func(string) ([]os.DirEntry, error) {
			return nil, errors.New("read dir failed")
		}
		_, _, err := s.Scan("test-table", ScanOptions{})
		assert.Error(t, err)
	})

	t.Run("skips non-json files", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		f, err := s.root.OpenFile("test-table/other.txt", os.O_CREATE|os.O_WRONLY, 0o600)
		require.NoError(t, err)
		require.NoError(t, f.Close())
		items, _, err := s.Scan("test-table", ScanOptions{})
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("skips item when json is invalid", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "a"}})
		// Scan calls readTableMeta first (call 1), then reads each item file.
		// Allow the metadata read to succeed; corrupt only item file reads.
		metaRead := false
		origReadAll := s.readAll
		s.readAll = func(r io.Reader) ([]byte, error) {
			if !metaRead {
				metaRead = true
				return origReadAll(r)
			}
			return []byte("not-json"), nil
		}
		items, _, err := s.Scan("test-table", ScanOptions{})
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("returns error when readTableMeta fails with Limit set", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) {
			return nil, errors.New("meta read failed")
		}
		limit := 1
		_, _, err := s.Scan("test-table", ScanOptions{Limit: &limit})
		assert.Error(t, err)
	})

	t.Run("returns error when readTableMeta fails with ESK set", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) {
			return nil, errors.New("meta read failed")
		}
		esk := map[string]any{"pk": map[string]any{"S": "a"}}
		_, _, err := s.Scan("test-table", ScanOptions{ExclusiveStartKey: esk})
		assert.Error(t, err)
	})

	t.Run("Limit truncates and returns LastEvaluatedKey", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "a"}})
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "b"}})
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "c"}})
		limit := 2
		items, lek, err := s.Scan("test-table", ScanOptions{Limit: &limit})
		require.NoError(t, err)
		assert.Len(t, items, 2)
		assert.NotNil(t, lek)
	})

	t.Run("Limit equal to count returns no LastEvaluatedKey", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "a"}})
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "b"}})
		limit := 2
		items, lek, err := s.Scan("test-table", ScanOptions{Limit: &limit})
		require.NoError(t, err)
		assert.Len(t, items, 2)
		assert.Nil(t, lek)
	})

	t.Run("ExclusiveStartKey resumes after the given key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "a"}})
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "b"}})
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "c"}})
		limit := 1
		// first page
		page1, lek, err := s.Scan("test-table", ScanOptions{Limit: &limit})
		require.NoError(t, err)
		require.Len(t, page1, 1)
		require.NotNil(t, lek)
		// second page
		page2, lek2, err := s.Scan("test-table", ScanOptions{Limit: &limit, ExclusiveStartKey: lek})
		require.NoError(t, err)
		require.Len(t, page2, 1)
		require.NotNil(t, lek2)
		// third page
		page3, lek3, err := s.Scan(
			"test-table",
			ScanOptions{Limit: &limit, ExclusiveStartKey: lek2},
		)
		require.NoError(t, err)
		require.Len(t, page3, 1)
		assert.Nil(t, lek3)
		// all pages together cover all items without duplicates
		combined := make([]map[string]any, 0, 3)
		combined = append(combined, page1...)
		combined = append(combined, page2...)
		combined = append(combined, page3...)
		assert.Len(t, combined, 3)
	})

	t.Run("ExclusiveStartKey resumes from hash position when item was deleted", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "a"}})
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "b"}})
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "c"}})
		// Establish scan order and get LEK for the first item.
		limit := 1
		page1, lek, err := s.Scan("test-table", ScanOptions{Limit: &limit})
		require.NoError(t, err)
		require.Len(t, page1, 1)
		require.NotNil(t, lek)
		// Delete the first-scanned item to simulate a stale ESK.
		_, err = s.DeleteItem("test-table", page1[0], nil)
		require.NoError(t, err)
		// Scanning with the stale ESK must resume from the correct hash position.
		remaining, _, err := s.Scan("test-table", ScanOptions{ExclusiveStartKey: lek})
		require.NoError(t, err)
		assert.Len(t, remaining, 2)
	})

	t.Run(
		"ExclusiveStartKey with missing key attribute returns ErrValidationException",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(testMeta))
			mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "a"}})
			esk := map[string]any{"wrong-attr": map[string]any{"S": "a"}}
			_, _, err := s.Scan("test-table", ScanOptions{ExclusiveStartKey: esk})
			assert.ErrorIs(t, err, ErrValidationException)
		},
	)

	t.Run("parallel scan distributes items across segments", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		for i := range 6 {
			mustPutItem(t, s, "test-table", map[string]any{
				"pk": map[string]any{"S": fmt.Sprintf("item%d", i)},
			})
		}
		total := 3
		var allItems []map[string]any
		for seg := range total {
			items, _, err := s.Scan("test-table", ScanOptions{Segment: &seg, TotalSegments: &total})
			require.NoError(t, err)
			allItems = append(allItems, items...)
		}
		assert.Len(t, allItems, 6)
	})

	t.Run("parallel scan single segment returns subset", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		for i := range 4 {
			mustPutItem(t, s, "test-table", map[string]any{
				"pk": map[string]any{"S": fmt.Sprintf("k%d", i)},
			})
		}
		total := 2
		seg0 := 0
		seg1 := 1
		items0, _, err := s.Scan("test-table", ScanOptions{Segment: &seg0, TotalSegments: &total})
		require.NoError(t, err)
		items1, _, err := s.Scan("test-table", ScanOptions{Segment: &seg1, TotalSegments: &total})
		require.NoError(t, err)
		assert.Len(t, append(items0, items1...), 4)
	})

	t.Run("parallel scan ESK resumes within segment not global list", func(t *testing.T) {
		// Regression: segment filter must precede ESK so that ESK applies within
		// the segment. With the old ordering (ESK first), page 2 would return
		// items belonging to the wrong segment.
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		for i := range 4 {
			mustPutItem(t, s, "test-table", map[string]any{
				"pk": map[string]any{"S": fmt.Sprintf("k%d", i)},
			})
		}
		total := 2
		seg := 0
		limit := 1
		// First page: segment 0, limit 1.
		page1, lek, err := s.Scan("test-table", ScanOptions{
			Segment: &seg, TotalSegments: &total, Limit: &limit,
		})
		require.NoError(t, err)
		require.Len(t, page1, 1)
		require.NotNil(t, lek, "expected more items in segment")
		// Second page: resume with ESK within the same segment.
		page2, lek2, err := s.Scan("test-table", ScanOptions{
			Segment: &seg, TotalSegments: &total, Limit: &limit, ExclusiveStartKey: lek,
		})
		require.NoError(t, err)
		require.Len(t, page2, 1)
		assert.Nil(t, lek2, "segment 0 should be exhausted after two pages")
		// Full segment 0 (no ESK, no limit) must equal the two pages combined.
		full, _, err := s.Scan("test-table", ScanOptions{Segment: &seg, TotalSegments: &total})
		require.NoError(t, err)
		assert.Equal(t, full, append(page1, page2...))
	})

	t.Run("excludes TTL-expired items", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.UpdateTimeToLive("test-table", TTLSpec{AttributeName: "exp", Enabled: true})
		require.NoError(t, err)
		pastTS := strconv.FormatInt(time.Now().Unix()-10, 10)
		futureTS := strconv.FormatInt(time.Now().Unix()+3600, 10)
		mustPutItem(t, s, "test-table", map[string]any{
			"pk":  map[string]any{"S": "expired"},
			"exp": map[string]any{"N": pastTS},
		})
		mustPutItem(t, s, "test-table", map[string]any{
			"pk":  map[string]any{"S": "live"},
			"exp": map[string]any{"N": futureTS},
		})
		items, _, err := s.Scan("test-table", ScanOptions{})
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, map[string]any{"S": "live"}, items[0]["pk"])
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

	t.Run("excludes TTL-expired items", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.UpdateTimeToLive("test-table", TTLSpec{AttributeName: "exp", Enabled: true})
		require.NoError(t, err)
		pastTS := strconv.FormatInt(time.Now().Unix()-10, 10)
		mustPutItem(t, s, "test-table", map[string]any{
			"pk":  map[string]any{"S": "a"},
			"exp": map[string]any{"N": pastTS},
		})
		items, _, err := s.Query(
			"test-table",
			"pk",
			map[string]any{"S": "a"},
			nil,
			QueryOptions{ScanIndexForward: true},
		)
		require.NoError(t, err)
		assert.Empty(t, items)
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

	t.Run("corrupt sort key value does not panic (slog.Warn path)", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		mustPutItem(t, s, "sk-table", mkItem("p", "a"))
		mustPutItem(t, s, "sk-table", mkItem("p", "b"))
		// Write an item whose sort key is a raw string, not a DynamoDB typed value.
		// This bypasses PutItem validation to exercise the sort callback error path.
		corrupt := map[string]any{
			"pk": map[string]any{"S": "p"},
			"sk": "not-a-typed-value",
		}
		require.NoError(t, s.writeJSON("sk-table/ffffffffffffffffffffffffffffffff.json", corrupt))
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"}, nil,
			QueryOptions{ScanIndexForward: true})
		require.NoError(t, err)
		assert.Len(t, items, 3) // all items returned; sort order undefined for corrupt item
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
			QueryOptions{ScanIndexForward: true, Limit: intPtr(2)},
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
			QueryOptions{ScanIndexForward: true, Limit: intPtr(10)},
		)
		require.NoError(t, err)
		assert.Len(t, items, 5)
		assert.Nil(t, lek)
	})

	t.Run("nil Limit means no limit", func(t *testing.T) {
		s := setup(t)
		items, lek, err := s.Query(
			"sk-table",
			"pk",
			map[string]any{"S": "p"},
			nil,
			QueryOptions{ScanIndexForward: true},
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
			QueryOptions{ScanIndexForward: false, Limit: intPtr(2)},
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
			QueryOptions{ScanIndexForward: true, Limit: intPtr(2), ExclusiveStartKey: cursor})
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

	t.Run("cursor past last item returns empty", func(t *testing.T) {
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

	t.Run("cursor item deleted: resumes from sort key position", func(t *testing.T) {
		s := setup(t)
		// Delete the item at sk="b" so it no longer exists.
		_, err := s.DeleteItem("sk-table",
			map[string]any{"pk": map[string]any{"S": "p"}, "sk": map[string]any{"S": "b"}},
			nil)
		require.NoError(t, err)
		cursor := map[string]any{
			"pk": map[string]any{"S": "p"},
			"sk": map[string]any{"S": "b"},
		}
		// Even though sk="b" is gone, the cursor should resume from after "b".
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"}, nil,
			QueryOptions{ScanIndexForward: true, ExclusiveStartKey: cursor})
		require.NoError(t, err)
		require.Len(t, items, 3) // "c", "d", "e"
		assert.Equal(t, "c", items[0]["sk"].(map[string]any)["S"])
	})

	t.Run("descending: cursor item deleted resumes correctly", func(t *testing.T) {
		s := setup(t)
		_, err := s.DeleteItem("sk-table",
			map[string]any{"pk": map[string]any{"S": "p"}, "sk": map[string]any{"S": "d"}},
			nil)
		require.NoError(t, err)
		cursor := map[string]any{
			"pk": map[string]any{"S": "p"},
			"sk": map[string]any{"S": "d"},
		}
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"}, nil,
			QueryOptions{ScanIndexForward: false, ExclusiveStartKey: cursor})
		require.NoError(t, err)
		require.Len(t, items, 3) // "c", "b", "a"
		assert.Equal(t, "c", items[0]["sk"].(map[string]any)["S"])
	})

	t.Run("paginate all items with Limit=2", func(t *testing.T) {
		s := setup(t)
		var allItems []map[string]any
		var cursor map[string]any
		for {
			items, lek, err := s.Query("sk-table", "pk", map[string]any{"S": "p"}, nil,
				QueryOptions{ScanIndexForward: true, Limit: intPtr(2), ExclusiveStartKey: cursor})
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

	t.Run("hash-only table: ExclusiveStartKey returns empty", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "x"}})
		cursor := map[string]any{"pk": map[string]any{"S": "x"}}
		items, _, err := s.Query("test-table", "pk", map[string]any{"S": "x"}, nil,
			QueryOptions{ScanIndexForward: true, ExclusiveStartKey: cursor})
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("ExclusiveStartKey missing sort key attribute returns empty", func(t *testing.T) {
		s := setup(t)
		// ESK has only the hash key; no sort key attribute.
		cursor := map[string]any{"pk": map[string]any{"S": "p"}}
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"}, nil,
			QueryOptions{ScanIndexForward: true, ExclusiveStartKey: cursor})
		require.NoError(t, err)
		assert.Empty(t, items)
	})

	t.Run("corrupt sort key in matched item skipped during ESK search", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(skTestMeta))
		// Write only a corrupt item (sort key is a raw string, not a DynamoDB typed value).
		// With a single item in matched, sort.SliceStable makes no comparisons, so the
		// corrupt value is only encountered in the ESK position search loop.
		corrupt := map[string]any{
			"pk": map[string]any{"S": "p"},
			"sk": "not-a-typed-value",
		}
		require.NoError(t, s.writeJSON("sk-table/ffffffffffffffffffffffffffffffff.json", corrupt))
		cursor := map[string]any{
			"pk": map[string]any{"S": "p"},
			"sk": map[string]any{"S": "a"},
		}
		// dynamoValueCmp("not-a-typed-value", {"S":"a"}) fails → continue.
		// startIdx stays at len(matched); result is empty.
		items, _, err := s.Query("sk-table", "pk", map[string]any{"S": "p"}, nil,
			QueryOptions{ScanIndexForward: true, ExclusiveStartKey: cursor})
		require.NoError(t, err)
		assert.Empty(t, items)
	})
}

var gsiTestMeta = TableMetadata{
	Name: "gsi-table",
	KeySchema: []KeySchemaElement{
		{AttributeName: "pk", KeyType: "HASH"},
		{AttributeName: "sk", KeyType: "RANGE"},
	},
	AttributeDefinitions: []AttributeDefinition{
		{AttributeName: "pk", AttributeType: "S"},
		{AttributeName: "sk", AttributeType: "S"},
		{AttributeName: "gsi_pk", AttributeType: "S"},
		{AttributeName: "gsi_sk", AttributeType: "S"},
	},
	GlobalSecondaryIndexes: []GlobalSecondaryIndex{
		{
			IndexName: "gsi-index",
			KeySchema: []KeySchemaElement{
				{AttributeName: "gsi_pk", KeyType: "HASH"},
				{AttributeName: "gsi_sk", KeyType: "RANGE"},
			},
		},
		{
			IndexName: "gsi-hash-only",
			KeySchema: []KeySchemaElement{
				{AttributeName: "gsi_pk", KeyType: "HASH"},
			},
		},
	},
	LocalSecondaryIndexes: []LocalSecondaryIndex{
		{
			IndexName: "lsi-index",
			KeySchema: []KeySchemaElement{
				{AttributeName: "pk", KeyType: "HASH"},
				{AttributeName: "gsi_sk", KeyType: "RANGE"},
			},
		},
	},
}

func TestQueryGSI(t *testing.T) {
	mkItem := func(pk, sk, gsiPK, gsiSK string) map[string]any {
		return map[string]any{
			"pk":     map[string]any{"S": pk},
			"sk":     map[string]any{"S": sk},
			"gsi_pk": map[string]any{"S": gsiPK},
			"gsi_sk": map[string]any{"S": gsiSK},
		}
	}

	t.Run("query by GSI hash key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(gsiTestMeta))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "g1", "a"))
		mustPutItem(t, s, "gsi-table", mkItem("p2", "s2", "g1", "b"))
		mustPutItem(t, s, "gsi-table", mkItem("p3", "s3", "g2", "c"))

		items, _, err := s.Query("gsi-table", "gsi_pk", map[string]any{"S": "g1"}, nil,
			QueryOptions{ScanIndexForward: true, IndexName: "gsi-index"})
		require.NoError(t, err)
		require.Len(t, items, 2)
		pks := []string{
			items[0]["pk"].(map[string]any)["S"].(string),
			items[1]["pk"].(map[string]any)["S"].(string),
		}
		assert.ElementsMatch(t, []string{"p1", "p2"}, pks)
	})

	t.Run("GSI results sorted by GSI sort key ascending", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(gsiTestMeta))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "g1", "z"))
		mustPutItem(t, s, "gsi-table", mkItem("p2", "s2", "g1", "a"))
		mustPutItem(t, s, "gsi-table", mkItem("p3", "s3", "g1", "m"))

		items, _, err := s.Query("gsi-table", "gsi_pk", map[string]any{"S": "g1"}, nil,
			QueryOptions{ScanIndexForward: true, IndexName: "gsi-index"})
		require.NoError(t, err)
		require.Len(t, items, 3)
		assert.Equal(t, "a", items[0]["gsi_sk"].(map[string]any)["S"])
		assert.Equal(t, "m", items[1]["gsi_sk"].(map[string]any)["S"])
		assert.Equal(t, "z", items[2]["gsi_sk"].(map[string]any)["S"])
	})

	t.Run("GSI results sorted by GSI sort key descending", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(gsiTestMeta))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "g1", "a"))
		mustPutItem(t, s, "gsi-table", mkItem("p2", "s2", "g1", "z"))

		items, _, err := s.Query("gsi-table", "gsi_pk", map[string]any{"S": "g1"}, nil,
			QueryOptions{ScanIndexForward: false, IndexName: "gsi-index"})
		require.NoError(t, err)
		require.Len(t, items, 2)
		assert.Equal(t, "z", items[0]["gsi_sk"].(map[string]any)["S"])
		assert.Equal(t, "a", items[1]["gsi_sk"].(map[string]any)["S"])
	})

	t.Run("GSI with sort key condition", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(gsiTestMeta))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "g1", "a"))
		mustPutItem(t, s, "gsi-table", mkItem("p2", "s2", "g1", "b"))
		mustPutItem(t, s, "gsi-table", mkItem("p3", "s3", "g1", "c"))

		skCond := &SortKeyCondition{
			Name:     "gsi_sk",
			Operator: OpGTE,
			Value:    map[string]any{"S": "b"},
		}
		items, _, err := s.Query("gsi-table", "gsi_pk", map[string]any{"S": "g1"}, skCond,
			QueryOptions{ScanIndexForward: true, IndexName: "gsi-index"})
		require.NoError(t, err)
		require.Len(t, items, 2)
		assert.Equal(t, "b", items[0]["gsi_sk"].(map[string]any)["S"])
		assert.Equal(t, "c", items[1]["gsi_sk"].(map[string]any)["S"])
	})

	t.Run("GSI LastEvaluatedKey includes both index and primary keys", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(gsiTestMeta))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "g1", "a"))
		mustPutItem(t, s, "gsi-table", mkItem("p2", "s2", "g1", "b"))
		mustPutItem(t, s, "gsi-table", mkItem("p3", "s3", "g1", "c"))

		items, lek, err := s.Query("gsi-table", "gsi_pk", map[string]any{"S": "g1"}, nil,
			QueryOptions{ScanIndexForward: true, Limit: intPtr(2), IndexName: "gsi-index"})
		require.NoError(t, err)
		require.Len(t, items, 2)
		require.NotNil(t, lek)
		assert.Contains(t, lek, "pk")
		assert.Contains(t, lek, "gsi_pk")
		assert.Contains(t, lek, "gsi_sk")
	})

	t.Run("GSI pagination with ExclusiveStartKey", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(gsiTestMeta))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "g1", "a"))
		mustPutItem(t, s, "gsi-table", mkItem("p2", "s2", "g1", "b"))
		mustPutItem(t, s, "gsi-table", mkItem("p3", "s3", "g1", "c"))

		page1, lek, err := s.Query("gsi-table", "gsi_pk", map[string]any{"S": "g1"}, nil,
			QueryOptions{ScanIndexForward: true, Limit: intPtr(2), IndexName: "gsi-index"})
		require.NoError(t, err)
		require.Len(t, page1, 2)
		require.NotNil(t, lek)

		page2, lek2, err := s.Query(
			"gsi-table",
			"gsi_pk",
			map[string]any{"S": "g1"},
			nil,
			QueryOptions{
				ScanIndexForward:  true,
				Limit:             intPtr(2),
				ExclusiveStartKey: lek,
				IndexName:         "gsi-index",
			},
		)
		require.NoError(t, err)
		require.Len(t, page2, 1)
		assert.Nil(t, lek2)
		assert.Equal(t, "c", page2[0]["gsi_sk"].(map[string]any)["S"])
	})

	t.Run(
		"GSI hash-only index pagination returns empty after ExclusiveStartKey",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(gsiTestMeta))
			mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "g1", "a"))

			cursor := map[string]any{
				"pk":     map[string]any{"S": "p1"},
				"sk":     map[string]any{"S": "s1"},
				"gsi_pk": map[string]any{"S": "g1"},
			}
			items, _, err := s.Query(
				"gsi-table",
				"gsi_pk",
				map[string]any{"S": "g1"},
				nil,
				QueryOptions{
					ScanIndexForward:  true,
					ExclusiveStartKey: cursor,
					IndexName:         "gsi-hash-only",
				},
			)
			require.NoError(t, err)
			assert.Empty(t, items)
		},
	)

	t.Run("error when index not found", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(gsiTestMeta))

		_, _, err := s.Query("gsi-table", "gsi_pk", map[string]any{"S": "g1"}, nil,
			QueryOptions{ScanIndexForward: true, IndexName: "no-such-index"})
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run(
		"sparse GSI: items without GSI sort key are included but sorted stably",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(gsiTestMeta))
			mustPutItem(t, s, "gsi-table", map[string]any{
				"pk":     map[string]any{"S": "p1"},
				"sk":     map[string]any{"S": "s1"},
				"gsi_pk": map[string]any{"S": "g1"},
			})
			mustPutItem(t, s, "gsi-table", mkItem("p2", "s2", "g1", "b"))

			items, _, err := s.Query("gsi-table", "gsi_pk", map[string]any{"S": "g1"}, nil,
				QueryOptions{ScanIndexForward: true, IndexName: "gsi-index"})
			require.NoError(t, err)
			assert.Len(t, items, 2)
		},
	)
}

func TestQueryLSI(t *testing.T) {
	mkItem := func(pk, sk, gsiSK string) map[string]any {
		return map[string]any{
			"pk":     map[string]any{"S": pk},
			"sk":     map[string]any{"S": sk},
			"gsi_sk": map[string]any{"S": gsiSK},
		}
	}

	t.Run("query by LSI hash key with LSI sort key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(gsiTestMeta))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "z"))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s2", "a"))
		mustPutItem(t, s, "gsi-table", mkItem("p2", "s3", "m"))

		items, _, err := s.Query("gsi-table", "pk", map[string]any{"S": "p1"}, nil,
			QueryOptions{ScanIndexForward: true, IndexName: "lsi-index"})
		require.NoError(t, err)
		require.Len(t, items, 2)
		assert.Equal(t, "a", items[0]["gsi_sk"].(map[string]any)["S"])
		assert.Equal(t, "z", items[1]["gsi_sk"].(map[string]any)["S"])
	})

	t.Run("LSI LastEvaluatedKey contains table primary key and LSI sort key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(gsiTestMeta))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "a"))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s2", "b"))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s3", "c"))

		_, lek, err := s.Query("gsi-table", "pk", map[string]any{"S": "p1"}, nil,
			QueryOptions{ScanIndexForward: true, Limit: intPtr(2), IndexName: "lsi-index"})
		require.NoError(t, err)
		require.NotNil(t, lek)
		assert.Contains(t, lek, "pk")
		assert.Contains(t, lek, "sk")
		assert.Contains(t, lek, "gsi_sk")
	})
}

func TestFindIndexDef(t *testing.T) {
	gsiProj := map[string]any{"ProjectionType": "ALL"}
	lsiProj := map[string]any{"ProjectionType": "KEYS_ONLY"}
	meta := TableMetadata{
		GlobalSecondaryIndexes: []GlobalSecondaryIndex{
			{
				IndexName:  "gsi-1",
				KeySchema:  []KeySchemaElement{{AttributeName: "a", KeyType: "HASH"}},
				Projection: gsiProj,
			},
		},
		LocalSecondaryIndexes: []LocalSecondaryIndex{
			{
				IndexName:  "lsi-1",
				KeySchema:  []KeySchemaElement{{AttributeName: "b", KeyType: "RANGE"}},
				Projection: lsiProj,
			},
		},
	}

	t.Run("finds GSI key schema and projection", func(t *testing.T) {
		ks, proj, err := findIndexDef(meta, "gsi-1")
		require.NoError(t, err)
		assert.Equal(t, "a", ks[0].AttributeName)
		assert.Equal(t, gsiProj, proj)
	})

	t.Run("finds LSI key schema and projection", func(t *testing.T) {
		ks, proj, err := findIndexDef(meta, "lsi-1")
		require.NoError(t, err)
		assert.Equal(t, "b", ks[0].AttributeName)
		assert.Equal(t, lsiProj, proj)
	})

	t.Run("returns error for unknown index", func(t *testing.T) {
		_, _, err := findIndexDef(meta, "no-index")
		assert.ErrorIs(t, err, ErrValidationException)
	})
}

func TestMergeKeySchemas(t *testing.T) {
	tableSchema := []KeySchemaElement{
		{AttributeName: "pk", KeyType: "HASH"},
		{AttributeName: "sk", KeyType: "RANGE"},
	}
	indexSchema := []KeySchemaElement{
		{AttributeName: "gsi_pk", KeyType: "HASH"},
		{AttributeName: "gsi_sk", KeyType: "RANGE"},
	}

	t.Run("merges without duplicates", func(t *testing.T) {
		merged := mergeKeySchemas(tableSchema, indexSchema)
		require.Len(t, merged, 4)
		names := make([]string, len(merged))
		for i, k := range merged {
			names[i] = k.AttributeName
		}
		assert.Equal(t, []string{"gsi_pk", "gsi_sk", "pk", "sk"}, names)
	})

	t.Run("LSI shares partition key with table", func(t *testing.T) {
		lsiSchema := []KeySchemaElement{
			{AttributeName: "pk", KeyType: "HASH"},
			{AttributeName: "lsi_sk", KeyType: "RANGE"},
		}
		merged := mergeKeySchemas(tableSchema, lsiSchema)
		require.Len(t, merged, 3)
		names := make([]string, len(merged))
		for i, k := range merged {
			names[i] = k.AttributeName
		}
		assert.Equal(t, []string{"pk", "lsi_sk", "sk"}, names)
	})
}

func TestApplyIndexProjection(t *testing.T) {
	items := []map[string]any{
		{
			"pk":     map[string]any{"S": "p1"},
			"sk":     map[string]any{"S": "s1"},
			"gsi_pk": map[string]any{"S": "g1"},
			"gsi_sk": map[string]any{"S": "a"},
			"extra":  map[string]any{"S": "extra-val"},
		},
	}
	keyAttrs := []string{"pk", "sk", "gsi_pk", "gsi_sk"}

	t.Run("nil projection returns all attributes", func(t *testing.T) {
		got := applyIndexProjection(items, nil, keyAttrs)
		assert.Len(t, got[0], 5)
	})

	t.Run("ALL projection returns all attributes", func(t *testing.T) {
		proj := map[string]any{"ProjectionType": "ALL"}
		got := applyIndexProjection(items, proj, keyAttrs)
		assert.Len(t, got[0], 5)
	})

	t.Run("KEYS_ONLY projection returns only key attributes", func(t *testing.T) {
		proj := map[string]any{"ProjectionType": "KEYS_ONLY"}
		got := applyIndexProjection(items, proj, keyAttrs)
		require.Len(t, got[0], 4)
		assert.Contains(t, got[0], "pk")
		assert.Contains(t, got[0], "sk")
		assert.Contains(t, got[0], "gsi_pk")
		assert.Contains(t, got[0], "gsi_sk")
		assert.NotContains(t, got[0], "extra")
	})

	t.Run("INCLUDE projection returns key attributes plus named extras", func(t *testing.T) {
		proj := map[string]any{
			"ProjectionType":   "INCLUDE",
			"NonKeyAttributes": []any{"extra"},
		}
		got := applyIndexProjection(items, proj, keyAttrs)
		require.Len(t, got[0], 5)
		assert.Contains(t, got[0], "extra")
		assert.NotContains(t, got[0], "nonexistent")
	})

	t.Run("INCLUDE projection omits attributes not in NonKeyAttributes", func(t *testing.T) {
		items2 := []map[string]any{
			{
				"pk":     map[string]any{"S": "p1"},
				"sk":     map[string]any{"S": "s1"},
				"gsi_pk": map[string]any{"S": "g1"},
				"gsi_sk": map[string]any{"S": "a"},
				"extra":  map[string]any{"S": "extra-val"},
				"other":  map[string]any{"S": "other-val"},
			},
		}
		proj := map[string]any{
			"ProjectionType":   "INCLUDE",
			"NonKeyAttributes": []any{"extra"},
		}
		got := applyIndexProjection(items2, proj, keyAttrs)
		assert.Contains(t, got[0], "extra")
		assert.NotContains(t, got[0], "other")
	})
}

func TestQueryGSI_Projection(t *testing.T) {
	mkMeta := func(projType string, nonKeyAttrs []string) TableMetadata {
		proj := map[string]any{"ProjectionType": projType}
		if len(nonKeyAttrs) > 0 {
			attrs := make([]any, len(nonKeyAttrs))
			for i, a := range nonKeyAttrs {
				attrs[i] = a
			}
			proj["NonKeyAttributes"] = attrs
		}
		m := gsiTestMeta
		m.GlobalSecondaryIndexes = []GlobalSecondaryIndex{
			{
				IndexName: "gsi-index",
				KeySchema: []KeySchemaElement{
					{AttributeName: "gsi_pk", KeyType: "HASH"},
					{AttributeName: "gsi_sk", KeyType: "RANGE"},
				},
				Projection: proj,
			},
		}
		m.LocalSecondaryIndexes = nil
		return m
	}

	mkItem := func(pk, sk, gsiPK, gsiSK string) map[string]any {
		return map[string]any{
			"pk":     map[string]any{"S": pk},
			"sk":     map[string]any{"S": sk},
			"gsi_pk": map[string]any{"S": gsiPK},
			"gsi_sk": map[string]any{"S": gsiSK},
			"extra":  map[string]any{"S": "extra-val"},
		}
	}

	t.Run("KEYS_ONLY: only key attributes returned", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(mkMeta("KEYS_ONLY", nil)))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "g1", "a"))

		items, _, err := s.Query("gsi-table", "gsi_pk", map[string]any{"S": "g1"}, nil,
			QueryOptions{ScanIndexForward: true, IndexName: "gsi-index"})
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Contains(t, items[0], "pk")
		assert.Contains(t, items[0], "sk")
		assert.Contains(t, items[0], "gsi_pk")
		assert.Contains(t, items[0], "gsi_sk")
		assert.NotContains(t, items[0], "extra")
	})

	t.Run("INCLUDE: key attrs plus NonKeyAttributes returned", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(mkMeta("INCLUDE", []string{"extra"})))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "g1", "a"))

		items, _, err := s.Query("gsi-table", "gsi_pk", map[string]any{"S": "g1"}, nil,
			QueryOptions{ScanIndexForward: true, IndexName: "gsi-index"})
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Contains(t, items[0], "extra")
	})

	t.Run("ALL: all attributes returned", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(mkMeta("ALL", nil)))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "g1", "a"))

		items, _, err := s.Query("gsi-table", "gsi_pk", map[string]any{"S": "g1"}, nil,
			QueryOptions{ScanIndexForward: true, IndexName: "gsi-index"})
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Contains(t, items[0], "extra")
	})

	t.Run("wrong hash key name returns ValidationException", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(mkMeta("ALL", nil)))
		mustPutItem(t, s, "gsi-table", mkItem("p1", "s1", "g1", "a"))

		_, _, err := s.Query("gsi-table", "pk", map[string]any{"S": "p1"}, nil,
			QueryOptions{ScanIndexForward: true, IndexName: "gsi-index"})
		assert.ErrorIs(t, err, ErrValidationException)
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

	t.Run("omits TTL-expired items", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.UpdateTimeToLive("test-table", TTLSpec{AttributeName: "exp", Enabled: true})
		require.NoError(t, err)
		pastTS := strconv.FormatInt(time.Now().Unix()-10, 10)
		futureTS := strconv.FormatInt(time.Now().Unix()+3600, 10)
		mustPutItem(t, s, "test-table", map[string]any{
			"pk":  map[string]any{"S": "expired"},
			"exp": map[string]any{"N": pastTS},
		})
		mustPutItem(t, s, "test-table", map[string]any{
			"pk":  map[string]any{"S": "live"},
			"exp": map[string]any{"N": futureTS},
		})
		keys := []map[string]any{
			{"pk": map[string]any{"S": "expired"}},
			{"pk": map[string]any{"S": "live"}},
		}
		items, err := s.BatchGetItems("test-table", keys)
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, map[string]any{"S": "live"}, items[0]["pk"])
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
		items, _, err := s.Scan("test-table", ScanOptions{})
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
		items, _, err := s.Scan("test-table", ScanOptions{})
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
		items, _, err := s.Scan("test-table", ScanOptions{})
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

	t.Run("emits INSERT stream event for put on stream-enabled table", func(t *testing.T) {
		s := newTestStorage(t)
		mustCreateStreamTable(t, s, "stream-batch", "NEW_IMAGE")
		puts := []map[string]any{{"pk": map[string]any{"S": "k1"}}}
		require.NoError(t, s.BatchWriteItems("stream-batch", puts, nil))
		buf := s.getStreamBuffer("stream-batch")
		require.NotNil(t, buf)
		buf.mu.RLock()
		defer buf.mu.RUnlock()
		require.Len(t, buf.records, 1)
		assert.Equal(t, "INSERT", buf.records[0].EventName)
	})

	t.Run("emits MODIFY when put overwrites existing item", func(t *testing.T) {
		s := newTestStorage(t)
		mustCreateStreamTable(t, s, "stream-batch2", "NEW_IMAGE")
		item := map[string]any{"pk": map[string]any{"S": "k1"}}
		mustPutItem(t, s, "stream-batch2", item)
		// BatchWrite over the same key
		require.NoError(t, s.BatchWriteItems("stream-batch2", []map[string]any{item}, nil))
		buf := s.getStreamBuffer("stream-batch2")
		require.NotNil(t, buf)
		buf.mu.RLock()
		defer buf.mu.RUnlock()
		require.Len(t, buf.records, 2) // first INSERT from PutItem, then MODIFY from BatchWrite
		assert.Equal(t, "MODIFY", buf.records[1].EventName)
	})

	t.Run("emits REMOVE stream event for delete on stream-enabled table", func(t *testing.T) {
		s := newTestStorage(t)
		mustCreateStreamTable(t, s, "stream-batch3", "OLD_IMAGE")
		item := map[string]any{"pk": map[string]any{"S": "k1"}}
		mustPutItem(t, s, "stream-batch3", item)
		deletes := []map[string]any{{"pk": map[string]any{"S": "k1"}}}
		require.NoError(t, s.BatchWriteItems("stream-batch3", nil, deletes))
		buf := s.getStreamBuffer("stream-batch3")
		require.NotNil(t, buf)
		buf.mu.RLock()
		defer buf.mu.RUnlock()
		require.Len(t, buf.records, 2)
		assert.Equal(t, "REMOVE", buf.records[1].EventName)
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

func TestTransactGetItems(t *testing.T) {
	t.Run("returns items and nil for missing", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(
			t,
			s,
			"test-table",
			map[string]any{"pk": map[string]any{"S": "k1"}, "val": map[string]any{"S": "v1"}},
		)

		results, err := s.TransactGetItems([]TransactGetInput{
			{TableName: "test-table", Key: map[string]any{"pk": map[string]any{"S": "k1"}}},
			{TableName: "test-table", Key: map[string]any{"pk": map[string]any{"S": "missing"}}},
		})
		require.NoError(t, err)
		require.Len(t, results, 2)
		assert.NotNil(t, results[0])
		assert.Nil(t, results[1])
	})

	t.Run("returns ErrTableNotFound for unknown table", func(t *testing.T) {
		s := newTestStorage(t)
		_, err := s.TransactGetItems([]TransactGetInput{
			{TableName: "no-table", Key: map[string]any{"pk": map[string]any{"S": "k"}}},
		})
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("propagates readJSON error that is not ErrNotExist", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "k"}})
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("disk read error") }
		_, err := s.TransactGetItems([]TransactGetInput{
			{TableName: "test-table", Key: map[string]any{"pk": map[string]any{"S": "k"}}},
		})
		assert.Error(t, err)
	})

	t.Run("returns ErrValidationException for malformed key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.TransactGetItems([]TransactGetInput{
			{TableName: "test-table", Key: map[string]any{"not-pk": map[string]any{"S": "x"}}},
		})
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("returns ErrValidationException for duplicate item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		_, err := s.TransactGetItems([]TransactGetInput{
			{TableName: "test-table", Key: map[string]any{"pk": map[string]any{"S": "k"}}},
			{TableName: "test-table", Key: map[string]any{"pk": map[string]any{"S": "k"}}},
		})
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("propagates non-ErrNotExist error on item read", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "k2"}})
		orig := s.readAll
		count := 0
		s.readAll = func(r io.Reader) ([]byte, error) {
			count++
			if count == 2 { // 1st call: meta read; 2nd call: item read
				return nil, errors.New("disk read error")
			}
			return orig(r)
		}
		_, err := s.TransactGetItems([]TransactGetInput{
			{TableName: "test-table", Key: map[string]any{"pk": map[string]any{"S": "k2"}}},
		})
		assert.Error(t, err)
	})
}

func TestTransactWriteItems(t *testing.T) {
	t.Run("applies Put and Delete atomically when all conditions pass", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "del-me"}})

		err := s.TransactWriteItems([]TransactWriteAction{
			{Put: &TransactPut{
				TableName: "test-table",
				Item: map[string]any{
					"pk": map[string]any{"S": "new-item"},
					"v":  map[string]any{"S": "hello"},
				},
			}},
			{Delete: &TransactDelete{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "del-me"}},
			}},
		})
		require.NoError(t, err)

		got, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "new-item"}})
		require.NoError(t, err)
		assert.NotNil(t, got)

		gone, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "del-me"}})
		require.NoError(t, err)
		assert.Nil(t, gone)
	})

	t.Run("cancels all writes when one condition fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "guard"}})

		err := s.TransactWriteItems([]TransactWriteAction{
			{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "guard"}},
				Cond: &ConditionCheck{
					Expr: "attribute_not_exists(pk)",
				},
			}},
			{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "side-effect"}},
			}},
		})
		var txErr *TransactionCanceledError
		require.ErrorAs(t, err, &txErr)
		require.Len(t, txErr.Reasons, 2)
		assert.Equal(t, "ConditionalCheckFailed", txErr.Reasons[0].Code)
		assert.Equal(t, "None", txErr.Reasons[1].Code)

		// side-effect must NOT have been written
		item, err := s.GetItem(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "side-effect"}},
		)
		require.NoError(t, err)
		assert.Nil(t, item)
	})

	t.Run("Update modifies item under transaction", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(
			t,
			s,
			"test-table",
			map[string]any{"pk": map[string]any{"S": "u1"}, "n": map[string]any{"N": "5"}},
		)

		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "u1"}},
				Updates:   map[string]any{"n": map[string]any{"N": "99"}},
			}},
		})
		require.NoError(t, err)
		item, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "u1"}})
		require.NoError(t, err)
		assert.Equal(t, "99", item["n"].(map[string]any)["N"])
	})

	t.Run("ConditionCheck with no write succeeds when condition holds", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "cc"}})

		err := s.TransactWriteItems([]TransactWriteAction{
			{ConditionCheck: &TransactConditionCheck{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "cc"}},
				Cond: &ConditionCheck{
					Expr: "attribute_exists(pk)",
				},
			}},
		})
		require.NoError(t, err)
	})

	t.Run("returns ErrTableNotFound for unknown table", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.TransactWriteItems([]TransactWriteAction{
			{
				Put: &TransactPut{
					TableName: "no-table",
					Item:      map[string]any{"pk": map[string]any{"S": "x"}},
				},
			},
		})
		assert.ErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("propagates Phase 2 write error from openFile failure", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		// inject openFile failure only for item writes (not table meta reads)
		origOpen := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".table.json") {
				return nil, errors.New("simulated disk full")
			}
			return origOpen(name, flag, perm)
		}
		err := s.TransactWriteItems([]TransactWriteAction{
			{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "x"}},
			}},
		})
		assert.Error(t, err)
	})

	t.Run("Delete of non-existent item is a no-op", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		err := s.TransactWriteItems([]TransactWriteAction{
			{Delete: &TransactDelete{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "does-not-exist"}},
			}},
		})
		require.NoError(t, err)
	})

	t.Run("Delete condition failure returns CancellationReason", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "del-guard"}})
		err := s.TransactWriteItems([]TransactWriteAction{
			{Delete: &TransactDelete{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "del-guard"}},
				Cond:      &ConditionCheck{Expr: "attribute_not_exists(pk)"},
			}},
		})
		var txErr *TransactionCanceledError
		require.ErrorAs(t, err, &txErr)
		assert.Equal(t, "ConditionalCheckFailed", txErr.Reasons[0].Code)
		assert.Equal(t, "transaction canceled", txErr.Error())
	})

	t.Run("Delete condition failure with ALL_OLD returns existing item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "del-old"}})
		err := s.TransactWriteItems([]TransactWriteAction{
			{Delete: &TransactDelete{
				TableName: "test-table",
				Key: map[string]any{
					"pk": map[string]any{"S": "del-old"},
				},
				Cond:                           &ConditionCheck{Expr: "attribute_not_exists(pk)"},
				ReturnValuesOnConditionFailure: "ALL_OLD",
			}},
		})
		var txErr *TransactionCanceledError
		require.ErrorAs(t, err, &txErr)
		assert.Equal(t, "ConditionalCheckFailed", txErr.Reasons[0].Code)
		assert.NotNil(t, txErr.Reasons[0].Item)
	})

	t.Run("Update condition failure returns CancellationReason", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "upd-guard"}})
		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "upd-guard"}},
				Updates:   map[string]any{"x": map[string]any{"S": "y"}},
				Cond:      &ConditionCheck{Expr: "attribute_not_exists(pk)"},
			}},
		})
		var txErr *TransactionCanceledError
		require.ErrorAs(t, err, &txErr)
		assert.Equal(t, "ConditionalCheckFailed", txErr.Reasons[0].Code)
	})

	t.Run("Update condition failure with ALL_OLD returns existing item", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "upd-old"}})
		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "test-table",
				Key: map[string]any{
					"pk": map[string]any{"S": "upd-old"},
				},
				Updates:                        map[string]any{"x": map[string]any{"S": "y"}},
				Cond:                           &ConditionCheck{Expr: "attribute_not_exists(pk)"},
				ReturnValuesOnConditionFailure: "ALL_OLD",
			}},
		})
		var txErr *TransactionCanceledError
		require.ErrorAs(t, err, &txErr)
		assert.Equal(t, "ConditionalCheckFailed", txErr.Reasons[0].Code)
		assert.NotNil(t, txErr.Reasons[0].Item)
	})

	t.Run("ConditionCheck condition failure returns CancellationReason", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		// item does not exist, so attribute_exists(pk) fails
		err := s.TransactWriteItems([]TransactWriteAction{
			{ConditionCheck: &TransactConditionCheck{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "cc-fail"}},
				Cond:      &ConditionCheck{Expr: "attribute_exists(pk)"},
			}},
		})
		var txErr *TransactionCanceledError
		require.ErrorAs(t, err, &txErr)
		assert.Equal(t, "ConditionalCheckFailed", txErr.Reasons[0].Code)
	})

	t.Run(
		"ConditionCheck condition failure with ALL_OLD includes existing item",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(testMeta))
			mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "cc-old"}})
			err := s.TransactWriteItems([]TransactWriteAction{
				{ConditionCheck: &TransactConditionCheck{
					TableName: "test-table",
					Key: map[string]any{
						"pk": map[string]any{"S": "cc-old"},
					},
					Cond: &ConditionCheck{
						Expr: "attribute_not_exists(pk)",
					},
					ReturnValuesOnConditionFailure: "ALL_OLD",
				}},
			})
			var txErr *TransactionCanceledError
			require.ErrorAs(t, err, &txErr)
			assert.Equal(t, "ConditionalCheckFailed", txErr.Reasons[0].Code)
			assert.NotNil(t, txErr.Reasons[0].Item)
		},
	)

	t.Run("invalid ConditionExpression returns ErrValidationException", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		err := s.TransactWriteItems([]TransactWriteAction{
			{ConditionCheck: &TransactConditionCheck{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "x"}},
				Cond:      &ConditionCheck{Expr: "%%not-valid%%"},
			}},
		})
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("Update on non-existent item creates it", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "brand-new"}},
				Updates:   map[string]any{"attr": map[string]any{"S": "val"}},
			}},
		})
		require.NoError(t, err)
		item, getErr := s.GetItem(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "brand-new"}},
		)
		require.NoError(t, getErr)
		assert.NotNil(t, item)
	})

	t.Run("Update with REMOVE op deletes attribute", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{
			"pk":    map[string]any{"S": "remove-target"},
			"extra": map[string]any{"S": "bye"},
		})
		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "remove-target"}},
				Updates:   map[string]any{"extra": nil}, // nil means REMOVE
			}},
		})
		require.NoError(t, err)
		item, getErr := s.GetItem(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "remove-target"}},
		)
		require.NoError(t, getErr)
		assert.NotContains(t, item, "extra")
	})

	t.Run("Update with ADD op increments number", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{
			"pk": map[string]any{"S": "add-target"},
			"n":  map[string]any{"N": "10"},
		})
		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "add-target"}},
				Updates:   map[string]any{"n": addOp{val: map[string]any{"N": "5"}}},
			}},
		})
		require.NoError(t, err)
		item, getErr := s.GetItem(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "add-target"}},
		)
		require.NoError(t, getErr)
		assert.Equal(t, "15", item["n"].(map[string]any)["N"])
	})

	t.Run("Update with DELETE op removes element from set", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{
			"pk": map[string]any{"S": "delete-op-target"},
			"ss": map[string]any{"SS": []any{"a", "b", "c"}},
		})
		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "delete-op-target"}},
				Updates:   map[string]any{"ss": deleteOp{val: map[string]any{"SS": []any{"b"}}}},
			}},
		})
		require.NoError(t, err)
		item, getErr := s.GetItem(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "delete-op-target"}},
		)
		require.NoError(t, getErr)
		ss := item["ss"].(map[string]any)["SS"].([]any)
		assert.ElementsMatch(t, []any{"a", "c"}, ss)
	})

	t.Run("Phase 0 rejects item missing partition key", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		err := s.TransactWriteItems([]TransactWriteAction{
			{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"not-pk": map[string]any{"S": "x"}},
			}},
		})
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("Phase 0 propagates non-ErrNotExist readTableMeta error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("disk failure") }
		err := s.TransactWriteItems([]TransactWriteAction{
			{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "x"}},
			}},
		})
		assert.Error(t, err)
		assert.NotErrorIs(t, err, ErrTableNotFound)
	})

	t.Run("empty action in slice is a no-op", func(t *testing.T) {
		// TransactWriteAction{} has all-nil fields: Phase 0 hits default:continue,
		// Phase 1 hits return nil nil, Phase 2 hits the trailing return nil.
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		err := s.TransactWriteItems([]TransactWriteAction{
			{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "real"}},
			}},
			{}, // empty
		})
		require.NoError(t, err)
	})

	// Phase 1 readTableMeta error: fail the 2nd readAll call so Phase 0 succeeds
	// but Phase 1's meta read encounters a non-ErrNotExist disk error.
	for _, tc := range []struct {
		name   string
		action func() TransactWriteAction
	}{
		{"Put arm", func() TransactWriteAction {
			return TransactWriteAction{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "x"}},
			}}
		}},
		{"Delete arm", func() TransactWriteAction {
			return TransactWriteAction{Delete: &TransactDelete{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "x"}},
			}}
		}},
		{"Update arm", func() TransactWriteAction {
			return TransactWriteAction{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "x"}},
				Updates:   map[string]any{"v": map[string]any{"S": "y"}},
			}}
		}},
		{"ConditionCheck arm", func() TransactWriteAction {
			return TransactWriteAction{ConditionCheck: &TransactConditionCheck{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "x"}},
				Cond:      &ConditionCheck{Expr: "attribute_exists(pk)"},
			}}
		}},
	} {
		tc := tc
		t.Run("Phase 1 readTableMeta error for "+tc.name, func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(testMeta))
			orig := s.readAll
			count := 0
			s.readAll = func(r io.Reader) ([]byte, error) {
				count++
				if count == 2 { // Phase 0 meta (1) succeeds; Phase 1 meta (2) fails
					return nil, errors.New("injected disk error")
				}
				return orig(r)
			}
			err := s.TransactWriteItems([]TransactWriteAction{tc.action()})
			assert.Error(t, err)
			assert.NotErrorIs(t, err, ErrTableNotFound)
		})
	}

	// Phase 1 readExistingItemLocked error: fail the 3rd readAll call so Phase 0
	// and Phase 1's meta read both succeed, but the item read hits a disk error.
	for _, tc := range []struct {
		name   string
		action func() TransactWriteAction
	}{
		{"Put arm", func() TransactWriteAction {
			return TransactWriteAction{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "existing"}},
			}}
		}},
		{"Delete arm", func() TransactWriteAction {
			return TransactWriteAction{Delete: &TransactDelete{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "existing"}},
			}}
		}},
		{"Update arm", func() TransactWriteAction {
			return TransactWriteAction{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "existing"}},
				Updates:   map[string]any{"v": map[string]any{"S": "y"}},
			}}
		}},
		{"ConditionCheck arm", func() TransactWriteAction {
			return TransactWriteAction{ConditionCheck: &TransactConditionCheck{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "existing"}},
				Cond:      &ConditionCheck{Expr: "attribute_exists(pk)"},
			}}
		}},
	} {
		tc := tc
		t.Run("Phase 1 readExistingItemLocked error for "+tc.name, func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(testMeta))
			mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "existing"}})
			orig := s.readAll
			count := 0
			s.readAll = func(r io.Reader) ([]byte, error) {
				count++
				if count == 3 { // 1: Phase 0 meta, 2: Phase 1 meta, 3: Phase 1 item
					return nil, errors.New("injected disk error")
				}
				return orig(r)
			}
			err := s.TransactWriteItems([]TransactWriteAction{tc.action()})
			assert.Error(t, err)
		})
	}

	// Phase 2 applyTransactActionLocked readTableMeta error:
	// fail the 4th readAll so Phase 0 (1), Phase 1 meta (2), Phase 2 itemPath meta (3) succeed
	// but Phase 2 applyTransactActionLocked meta read (4) encounters a disk error.
	for _, tc := range []struct {
		name   string
		action func() TransactWriteAction
	}{
		{"Put arm", func() TransactWriteAction {
			return TransactWriteAction{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "x"}},
			}}
		}},
		{"Delete arm", func() TransactWriteAction {
			return TransactWriteAction{Delete: &TransactDelete{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "x"}},
			}}
		}},
		{"Update arm", func() TransactWriteAction {
			return TransactWriteAction{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "x"}},
				Updates:   map[string]any{"v": map[string]any{"S": "y"}},
			}}
		}},
	} {
		tc := tc
		t.Run(
			"Phase 2 applyTransactActionLocked readTableMeta error for "+tc.name,
			func(t *testing.T) {
				s := newTestStorage(t)
				require.NoError(t, s.CreateTable(testMeta))
				orig := s.readAll
				count := 0
				s.readAll = func(r io.Reader) ([]byte, error) {
					count++
					if count == 4 { // 1: Phase 0 meta, 2: Phase 1 meta, 3: Phase 2 itemPath meta, 4: apply meta
						return nil, errors.New("injected disk error")
					}
					return orig(r)
				}
				err := s.TransactWriteItems([]TransactWriteAction{tc.action()})
				assert.Error(t, err)
			},
		)
	}

	t.Run(
		"Phase 2 applyTransactActionLocked readJSON non-ErrNotExist error for Update arm",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(testMeta))
			mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "x"}})
			orig := s.readAll
			count := 0
			s.readAll = func(r io.Reader) ([]byte, error) {
				count++
				// 1: Phase 0 meta, 2: Phase 1 meta, 3: Phase 1 item, 4: Phase 2 itemPath meta,
				// 5: Phase 2 snapshot item, 6: Phase 2 apply meta, 7: Phase 2 apply item read
				if count == 7 {
					return nil, errors.New("injected disk error")
				}
				return orig(r)
			}
			err := s.TransactWriteItems([]TransactWriteAction{
				{Update: &TransactUpdate{
					TableName: "test-table",
					Key:       map[string]any{"pk": map[string]any{"S": "x"}},
					Updates:   map[string]any{"v": map[string]any{"S": "y"}},
				}},
			})
			assert.Error(t, err)
		},
	)

	t.Run("emits INSERT stream event for Put on new item", func(t *testing.T) {
		s := newTestStorage(t)
		mustCreateStreamTable(t, s, "tx-stream", "NEW_IMAGE")
		err := s.TransactWriteItems([]TransactWriteAction{
			{Put: &TransactPut{
				TableName: "tx-stream",
				Item:      map[string]any{"pk": map[string]any{"S": "k1"}},
			}},
		})
		require.NoError(t, err)
		buf := s.getStreamBuffer("tx-stream")
		require.NotNil(t, buf)
		buf.mu.RLock()
		defer buf.mu.RUnlock()
		require.Len(t, buf.records, 1)
		assert.Equal(t, "INSERT", buf.records[0].EventName)
	})

	t.Run("emits MODIFY stream event for Put over existing item", func(t *testing.T) {
		s := newTestStorage(t)
		mustCreateStreamTable(t, s, "tx-stream2", "NEW_AND_OLD_IMAGES")
		item := map[string]any{"pk": map[string]any{"S": "k1"}, "v": map[string]any{"S": "old"}}
		mustPutItem(t, s, "tx-stream2", item)
		err := s.TransactWriteItems([]TransactWriteAction{
			{Put: &TransactPut{
				TableName: "tx-stream2",
				Item: map[string]any{
					"pk": map[string]any{"S": "k1"},
					"v":  map[string]any{"S": "new"},
				},
			}},
		})
		require.NoError(t, err)
		buf := s.getStreamBuffer("tx-stream2")
		require.NotNil(t, buf)
		buf.mu.RLock()
		defer buf.mu.RUnlock()
		require.Len(t, buf.records, 2) // first INSERT from mustPutItem, then MODIFY
		assert.Equal(t, "MODIFY", buf.records[1].EventName)
	})

	t.Run("emits REMOVE stream event for Delete", func(t *testing.T) {
		s := newTestStorage(t)
		mustCreateStreamTable(t, s, "tx-stream3", "OLD_IMAGE")
		item := map[string]any{"pk": map[string]any{"S": "k1"}}
		mustPutItem(t, s, "tx-stream3", item)
		err := s.TransactWriteItems([]TransactWriteAction{
			{Delete: &TransactDelete{
				TableName: "tx-stream3",
				Key:       map[string]any{"pk": map[string]any{"S": "k1"}},
			}},
		})
		require.NoError(t, err)
		buf := s.getStreamBuffer("tx-stream3")
		require.NotNil(t, buf)
		buf.mu.RLock()
		defer buf.mu.RUnlock()
		require.Len(t, buf.records, 2) // INSERT from mustPutItem, then REMOVE
		assert.Equal(t, "REMOVE", buf.records[1].EventName)
	})

	t.Run("emits MODIFY stream event for Update on existing item", func(t *testing.T) {
		s := newTestStorage(t)
		mustCreateStreamTable(t, s, "tx-stream4", "NEW_IMAGE")
		mustPutItem(t, s, "tx-stream4", map[string]any{
			"pk": map[string]any{"S": "k1"},
			"v":  map[string]any{"S": "before"},
		})
		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "tx-stream4",
				Key:       map[string]any{"pk": map[string]any{"S": "k1"}},
				Updates:   map[string]any{"v": map[string]any{"S": "after"}},
			}},
		})
		require.NoError(t, err)
		buf := s.getStreamBuffer("tx-stream4")
		require.NotNil(t, buf)
		buf.mu.RLock()
		defer buf.mu.RUnlock()
		require.Len(t, buf.records, 2) // INSERT from mustPutItem, then MODIFY
		assert.Equal(t, "MODIFY", buf.records[1].EventName)
	})
}

func TestTransactWriteItemsRollback(t *testing.T) {
	t.Run("rolls back first Put when second Put fails with I/O error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))

		writeCount := 0
		origOpen := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			// Count item writes only (paths contain "/" separator).
			if strings.Contains(name, "/") {
				writeCount++
				if writeCount == 2 {
					return nil, errors.New("injected I/O failure")
				}
			}
			return origOpen(name, flag, perm)
		}

		err := s.TransactWriteItems([]TransactWriteAction{
			{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "item-1"}},
			}},
			{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "item-2"}},
			}},
		})
		require.Error(t, err)

		s.openFile = origOpen

		// item-1 was written then rolled back; it must not exist.
		item1, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "item-1"}})
		assert.NoError(t, err)
		assert.Nil(t, item1)
	})

	t.Run("rolls back Put when Delete fails with I/O error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{
			"pk":  map[string]any{"S": "existing"},
			"val": map[string]any{"S": "original"},
		})

		removeCount := 0
		origRemove := s.removeFile
		s.removeFile = func(name string) error {
			removeCount++
			if removeCount == 1 {
				return errors.New("injected remove failure")
			}
			return origRemove(name)
		}

		err := s.TransactWriteItems([]TransactWriteAction{
			{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "new-item"}},
			}},
			{Delete: &TransactDelete{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "existing"}},
			}},
		})
		require.Error(t, err)

		s.removeFile = origRemove

		// new-item was written then rolled back.
		newItem, err := s.GetItem(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "new-item"}},
		)
		assert.NoError(t, err)
		assert.Nil(t, newItem)

		// existing item must be intact.
		item, err := s.GetItem("test-table", map[string]any{"pk": map[string]any{"S": "existing"}})
		require.NoError(t, err)
		assert.Equal(t, "original", item["val"].(map[string]any)["S"])
	})

	t.Run(
		"restores modified item to original value when subsequent action fails",
		func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(testMeta))
			mustPutItem(t, s, "test-table", map[string]any{
				"pk":  map[string]any{"S": "item-a"},
				"val": map[string]any{"S": "original"},
			})

			writeCount := 0
			origOpen := s.openFile
			s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
				if strings.Contains(name, "/") {
					writeCount++
					if writeCount == 2 {
						return nil, errors.New("injected failure on second write")
					}
				}
				return origOpen(name, flag, perm)
			}

			err := s.TransactWriteItems([]TransactWriteAction{
				{Update: &TransactUpdate{
					TableName: "test-table",
					Key:       map[string]any{"pk": map[string]any{"S": "item-a"}},
					Updates:   map[string]any{"val": map[string]any{"S": "modified"}},
				}},
				{Put: &TransactPut{
					TableName: "test-table",
					Item:      map[string]any{"pk": map[string]any{"S": "item-b"}},
				}},
			})
			require.Error(t, err)

			s.openFile = origOpen

			// item-a must be restored to its original value.
			item, err := s.GetItem(
				"test-table",
				map[string]any{"pk": map[string]any{"S": "item-a"}},
			)
			require.NoError(t, err)
			assert.Equal(t, "original", item["val"].(map[string]any)["S"])
		},
	)

	t.Run("logs error and continues when rollback removeFile fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))

		writeCount := 0
		origOpen := s.openFile
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.Contains(name, "/") {
				writeCount++
				if writeCount == 2 {
					return nil, errors.New("injected write failure")
				}
			}
			return origOpen(name, flag, perm)
		}
		s.removeFile = func(string) error { return errors.New("rollback remove also failed") }

		err := s.TransactWriteItems([]TransactWriteAction{
			{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "item-1"}},
			}},
			{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "item-2"}},
			}},
		})
		assert.Error(t, err)
	})

	t.Run("logs error and continues when rollback writeJSON fails", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{
			"pk":  map[string]any{"S": "item-a"},
			"val": map[string]any{"S": "original"},
		})

		writeCount := 0
		origOpen := s.openFile
		// Fail on 2nd write (Phase 2: second action fails, triggering rollback)
		// and on 3rd write (rollback: restore item-a fails → slog.Error path).
		s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
			if strings.Contains(name, "/") {
				writeCount++
				if writeCount >= 2 {
					return nil, errors.New("injected write failure")
				}
			}
			return origOpen(name, flag, perm)
		}

		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "item-a"}},
				Updates:   map[string]any{"val": map[string]any{"S": "modified"}},
			}},
			{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "item-b"}},
			}},
		})
		assert.Error(t, err)
	})
}

func TestReadExistingItemLocked(t *testing.T) {
	t.Run("returns error when key is missing required attribute", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		s.mu.Lock()
		defer s.mu.Unlock()
		_, err := s.readExistingItemLocked(
			"test-table",
			map[string]any{"not-pk": map[string]any{"S": "x"}},
			testMeta,
		)
		assert.Error(t, err)
	})

	t.Run("propagates non-ErrNotExist readJSON error", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "k"}})
		s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("disk error") }
		s.mu.Lock()
		defer s.mu.Unlock()
		_, err := s.readExistingItemLocked(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "k"}},
			testMeta,
		)
		assert.Error(t, err)
	})
}

func TestApplyTransactActionLockedErrors(t *testing.T) {
	// Phase 2 readTableMeta error: fail the 3rd readAll call so both Phase 0
	// and Phase 1 meta reads succeed but Phase 2's meta read hits a disk error.
	// Items do not exist, so Phase 1 skips the item readAll (root.Open → ErrNotExist).
	for _, tc := range []struct {
		name   string
		action func() TransactWriteAction
	}{
		{"Put arm", func() TransactWriteAction {
			return TransactWriteAction{Put: &TransactPut{
				TableName: "test-table",
				Item:      map[string]any{"pk": map[string]any{"S": "nonexistent"}},
			}}
		}},
		{"Delete arm", func() TransactWriteAction {
			return TransactWriteAction{Delete: &TransactDelete{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "nonexistent"}},
			}}
		}},
		{"Update arm", func() TransactWriteAction {
			return TransactWriteAction{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "nonexistent"}},
				Updates:   map[string]any{"v": map[string]any{"S": "y"}},
			}}
		}},
	} {
		tc := tc
		t.Run("Phase 2 readTableMeta error for "+tc.name, func(t *testing.T) {
			s := newTestStorage(t)
			require.NoError(t, s.CreateTable(testMeta))
			orig := s.readAll
			count := 0
			s.readAll = func(r io.Reader) ([]byte, error) {
				count++
				if count == 3 { // Phase 0(1), Phase 1(2), Phase 2(3)←fail
					return nil, errors.New("injected disk error")
				}
				return orig(r)
			}
			err := s.TransactWriteItems([]TransactWriteAction{tc.action()})
			assert.Error(t, err)
		})
	}

	t.Run("Phase 2 Update readJSON non-ErrNotExist error", func(t *testing.T) {
		// Item exists, so Phase 1 reads it (call 3). Phase 2 then reads meta (call 4)
		// and item (call 5); failing on call 5 covers the !errors.Is(err, ErrNotExist) branch.
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{"pk": map[string]any{"S": "x"}})
		orig := s.readAll
		count := 0
		s.readAll = func(r io.Reader) ([]byte, error) {
			count++
			if count == 5 { // Phase 0(1), Phase 1 meta(2), Phase 1 item(3), Phase 2 meta(4), Phase 2 item(5)←fail
				return nil, errors.New("injected disk error")
			}
			return orig(r)
		}
		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "x"}},
				Updates:   map[string]any{"v": map[string]any{"S": "y"}},
			}},
		})
		assert.Error(t, err)
	})

	t.Run("Update with ADD op error returns ErrValidationException", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{
			"pk":  map[string]any{"S": "add-err"},
			"str": map[string]any{"S": "hello"},
		})
		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "add-err"}},
				Updates:   map[string]any{"str": addOp{val: map[string]any{"N": "5"}}},
			}},
		})
		assert.ErrorIs(t, err, ErrValidationException)
	})

	t.Run("Update with DELETE op empties set removes attribute", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{
			"pk": map[string]any{"S": "del-empty"},
			"ss": map[string]any{"SS": []any{"only"}},
		})
		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "del-empty"}},
				Updates:   map[string]any{"ss": deleteOp{val: map[string]any{"SS": []any{"only"}}}},
			}},
		})
		require.NoError(t, err)
		item, getErr := s.GetItem(
			"test-table",
			map[string]any{"pk": map[string]any{"S": "del-empty"}},
		)
		require.NoError(t, getErr)
		assert.NotContains(t, item, "ss")
	})

	t.Run("Update with DELETE op type mismatch returns ErrValidationException", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateTable(testMeta))
		mustPutItem(t, s, "test-table", map[string]any{
			"pk":  map[string]any{"S": "del-err"},
			"str": map[string]any{"S": "hello"},
		})
		err := s.TransactWriteItems([]TransactWriteAction{
			{Update: &TransactUpdate{
				TableName: "test-table",
				Key:       map[string]any{"pk": map[string]any{"S": "del-err"}},
				// str is S type, but deleteOp expects SS/NS/BS → error
				Updates: map[string]any{"str": deleteOp{val: map[string]any{"SS": []any{"hello"}}}},
			}},
		})
		assert.ErrorIs(t, err, ErrValidationException)
	})
}

func TestTransactActionReturnsOldOnFailure(t *testing.T) {
	tests := []struct {
		name   string
		action TransactWriteAction
		want   bool
	}{
		{
			"Put with ALL_OLD",
			TransactWriteAction{Put: &TransactPut{ReturnValuesOnConditionFailure: "ALL_OLD"}},
			true,
		},
		{
			"Put without flag",
			TransactWriteAction{Put: &TransactPut{}},
			false,
		},
		{
			"Delete with ALL_OLD",
			TransactWriteAction{Delete: &TransactDelete{ReturnValuesOnConditionFailure: "ALL_OLD"}},
			true,
		},
		{
			"Update with ALL_OLD",
			TransactWriteAction{Update: &TransactUpdate{ReturnValuesOnConditionFailure: "ALL_OLD"}},
			true,
		},
		{
			"ConditionCheck with ALL_OLD",
			TransactWriteAction{
				ConditionCheck: &TransactConditionCheck{ReturnValuesOnConditionFailure: "ALL_OLD"},
			},
			true,
		},
		{
			"empty action returns false",
			TransactWriteAction{},
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, transactActionReturnsOldOnFailure(tc.action))
		})
	}
}
