package dynamodb

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errWriter is an io.WriteCloser whose Write always returns an error.
type errWriter struct{ writeErr error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.writeErr }
func (errWriter) Close() error                { return nil }

func TestStreamARNParsing(t *testing.T) {
	tests := []struct {
		name      string
		arn       string
		wantTable string
		wantLabel string
		wantErr   bool
	}{
		{
			name:      "valid ARN",
			arn:       "arn:aws:dynamodb:us-east-1:000000000000:table/MyTable/stream/2024-01-02T03:04:05.678",
			wantTable: "MyTable",
			wantLabel: "2024-01-02T03:04:05.678",
		},
		{
			name:    "missing stream part",
			arn:     "arn:aws:dynamodb:us-east-1:000000000000:table/MyTable",
			wantErr: true,
		},
		{
			name:    "empty ARN",
			arn:     "",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			table, label, err := parseStreamARN(tc.arn)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantTable, table)
			assert.Equal(t, tc.wantLabel, label)
		})
	}
}

func TestIteratorEncoding(t *testing.T) {
	iter, err := encodeIterator("my-table", 42)
	require.NoError(t, err)
	state, err := decodeIterator(iter)
	require.NoError(t, err)
	assert.Equal(t, "my-table", state.Table)
	assert.Equal(t, 42, state.Position)
}

func mustCreateStreamTable(t *testing.T, s *Storage, tableName, viewType string) TableMetadata {
	t.Helper()
	meta := TableMetadata{
		Name:      tableName,
		KeySchema: []KeySchemaElement{{AttributeName: "pk", KeyType: "HASH"}},
		AttributeDefinitions: []AttributeDefinition{
			{AttributeName: "pk", AttributeType: "S"},
		},
		StreamSpec: &StreamSpecification{
			StreamEnabled:  true,
			StreamViewType: viewType,
		},
	}
	require.NoError(t, s.CreateTable(meta))
	m, err := s.DescribeTable(tableName)
	require.NoError(t, err)
	return m
}

func TestStreamRecordEmission(t *testing.T) {
	s := newTestStorage(t)
	meta := mustCreateStreamTable(t, s, "stream-test", "NEW_AND_OLD_IMAGES")
	require.NotEmpty(t, meta.StreamLabel)
	arn := streamARN("stream-test", meta.StreamLabel)

	item1 := map[string]any{"pk": map[string]any{"S": "key1"}, "val": map[string]any{"S": "hello"}}
	item2 := map[string]any{"pk": map[string]any{"S": "key1"}, "val": map[string]any{"S": "world"}}

	// INSERT
	_, err := s.PutItem("stream-test", item1, nil)
	require.NoError(t, err)

	// MODIFY
	_, err = s.PutItem("stream-test", item2, nil)
	require.NoError(t, err)

	// REMOVE
	_, err = s.DeleteItem("stream-test", map[string]any{"pk": map[string]any{"S": "key1"}}, nil)
	require.NoError(t, err)

	// GetShardIterator TRIM_HORIZON
	shardIDStr := shardID("stream-test", meta.StreamLabel)
	iter, err := s.GetShardIterator(arn, shardIDStr, "TRIM_HORIZON", "")
	require.NoError(t, err)
	require.NotEmpty(t, iter)

	// GetStreamRecords — should return 3 records
	records, nextIter, err := s.GetStreamRecords(iter, 1000)
	require.NoError(t, err)
	require.NotEmpty(t, nextIter)
	require.Len(t, records, 3)

	assert.Equal(t, "INSERT", records[0].EventName)
	assert.NotNil(t, records[0].NewImage)
	assert.Nil(t, records[0].OldImage)

	assert.Equal(t, "MODIFY", records[1].EventName)
	assert.NotNil(t, records[1].NewImage)
	assert.NotNil(t, records[1].OldImage)

	assert.Equal(t, "REMOVE", records[2].EventName)
	assert.Nil(t, records[2].NewImage)
	assert.NotNil(t, records[2].OldImage)
}

func TestStreamIteratorTypes(t *testing.T) {
	s := newTestStorage(t)
	meta := mustCreateStreamTable(t, s, "iter-test", "NEW_IMAGE")
	arn := streamARN("iter-test", meta.StreamLabel)
	shardIDStr := shardID("iter-test", meta.StreamLabel)

	for i := 0; i < 5; i++ {
		item := map[string]any{"pk": map[string]any{"S": "k"}, "n": map[string]any{"N": "0"}}
		_, err := s.PutItem("iter-test", item, nil)
		require.NoError(t, err)
	}

	buf := s.getStreamBuffer("iter-test")
	require.NotNil(t, buf)
	buf.mu.RLock()
	seqOf2 := buf.records[2].SeqNum
	buf.mu.RUnlock()

	t.Run("TRIM_HORIZON reads all", func(t *testing.T) {
		iter, err := s.GetShardIterator(arn, shardIDStr, "TRIM_HORIZON", "")
		require.NoError(t, err)
		recs, _, err := s.GetStreamRecords(iter, 1000)
		require.NoError(t, err)
		assert.Len(t, recs, 5)
	})

	t.Run("LATEST reads none", func(t *testing.T) {
		iter, err := s.GetShardIterator(arn, shardIDStr, "LATEST", "")
		require.NoError(t, err)
		recs, _, err := s.GetStreamRecords(iter, 1000)
		require.NoError(t, err)
		assert.Len(t, recs, 0)
	})

	t.Run("AT_SEQUENCE_NUMBER reads from index 2", func(t *testing.T) {
		iter, err := s.GetShardIterator(arn, shardIDStr, "AT_SEQUENCE_NUMBER", seqNumStr(seqOf2))
		require.NoError(t, err)
		recs, _, err := s.GetStreamRecords(iter, 1000)
		require.NoError(t, err)
		assert.Len(t, recs, 3) // index 2, 3, 4
	})

	t.Run("AFTER_SEQUENCE_NUMBER reads from index 3", func(t *testing.T) {
		iter, err := s.GetShardIterator(arn, shardIDStr, "AFTER_SEQUENCE_NUMBER", seqNumStr(seqOf2))
		require.NoError(t, err)
		recs, _, err := s.GetStreamRecords(iter, 1000)
		require.NoError(t, err)
		assert.Len(t, recs, 2) // index 3, 4
	})
}

func TestStreamViewTypes(t *testing.T) {
	tests := []struct {
		viewType     string
		wantNewImage bool
		wantOldImage bool
	}{
		{"KEYS_ONLY", false, false},
		{"NEW_IMAGE", true, false},
		{"OLD_IMAGE", false, true},
		{"NEW_AND_OLD_IMAGES", true, true},
	}
	for _, tc := range tests {
		t.Run(tc.viewType, func(t *testing.T) {
			s := newTestStorage(t)
			tableName := "vt-test-" + tc.viewType
			mustCreateStreamTable(t, s, tableName, tc.viewType)
			meta, err := s.DescribeTable(tableName)
			require.NoError(t, err)
			arn := streamARN(tableName, meta.StreamLabel)
			shardIDStr := shardID(tableName, meta.StreamLabel)

			old := map[string]any{"pk": map[string]any{"S": "k"}, "v": map[string]any{"S": "old"}}
			_, err = s.PutItem(tableName, old, nil)
			require.NoError(t, err)
			newItem := map[string]any{
				"pk": map[string]any{"S": "k"},
				"v":  map[string]any{"S": "new"},
			}
			_, err = s.PutItem(tableName, newItem, nil)
			require.NoError(t, err)

			iter, err := s.GetShardIterator(arn, shardIDStr, "TRIM_HORIZON", "")
			require.NoError(t, err)
			recs, _, err := s.GetStreamRecords(iter, 1000)
			require.NoError(t, err)
			require.Len(t, recs, 2)

			modifyRec := recs[1]
			if tc.wantNewImage {
				assert.NotNil(t, modifyRec.NewImage)
			} else {
				assert.Nil(t, modifyRec.NewImage)
			}
			if tc.wantOldImage {
				assert.NotNil(t, modifyRec.OldImage)
			} else {
				assert.Nil(t, modifyRec.OldImage)
			}
		})
	}
}

func TestStreamNotEnabledNoRecords(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateTable(testMeta))

	_, err := s.PutItem("test-table", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
	require.NoError(t, err)

	buf := s.getStreamBuffer("test-table")
	assert.Nil(t, buf, "no buffer should exist for a table without streaming")
}

func TestListStreamARNs(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "t1", "KEYS_ONLY")
	mustCreateStreamTable(t, s, "t2", "NEW_IMAGE")
	require.NoError(t, s.CreateTable(TableMetadata{
		Name:                 "t3-no-stream",
		KeySchema:            []KeySchemaElement{{AttributeName: "pk", KeyType: "HASH"}},
		AttributeDefinitions: []AttributeDefinition{{AttributeName: "pk", AttributeType: "S"}},
	}))

	t.Run("all streams", func(t *testing.T) {
		entries, err := s.ListStreamARNs("")
		require.NoError(t, err)
		assert.Len(t, entries, 2)
	})

	t.Run("filter by table", func(t *testing.T) {
		entries, err := s.ListStreamARNs("t1")
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "t1", entries[0].TableName)
	})

	t.Run("table without stream returns empty", func(t *testing.T) {
		entries, err := s.ListStreamARNs("t3-no-stream")
		require.NoError(t, err)
		assert.Len(t, entries, 0)
	})

	t.Run("readDir error propagates", func(t *testing.T) {
		s2 := newTestStorage(t)
		s2.listDirFn = func(string) ([]os.DirEntry, error) { return nil, errors.New("boom") }
		_, err := s2.ListStreamARNs("")
		require.Error(t, err)
	})

	t.Run(
		"non-ErrNotExist readTableMeta error propagates when tableName is set",
		func(t *testing.T) {
			s2 := newTestStorage(t)
			mustCreateStreamTable(t, s2, "my-table", "NEW_IMAGE")
			orig := s2.readAll
			called := false
			s2.readAll = func(r io.Reader) ([]byte, error) {
				if !called {
					called = true
					return nil, errors.New("disk error")
				}
				return orig(r)
			}
			_, err := s2.ListStreamARNs("my-table")
			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrTableNotFound)
		},
	)

	t.Run("corrupt table meta returns error", func(t *testing.T) {
		s2 := newTestStorage(t)
		mustCreateStreamTable(t, s2, "good", "KEYS_ONLY")
		mustCreateStreamTable(t, s2, "bad", "NEW_IMAGE")
		f, err := s2.root.OpenFile("bad.table.json", os.O_WRONLY|os.O_TRUNC, 0o600)
		require.NoError(t, err)
		_, err = f.Write([]byte("invalid json"))
		require.NoError(t, err)
		require.NoError(t, f.Close())
		_, err = s2.ListStreamARNs("")
		require.Error(t, err)
	})
}

func TestDeepCloneAny(t *testing.T) {
	t.Run("clones slice branch", func(t *testing.T) {
		orig := map[string]any{
			"L": []any{map[string]any{"S": "a"}, map[string]any{"S": "b"}},
		}
		got := cloneMap(orig)
		// Mutating the original slice must not affect the clone.
		orig["L"].([]any)[0].(map[string]any)["S"] = "mutated"
		assert.Equal(t, "a", got["L"].([]any)[0].(map[string]any)["S"])
	})

	t.Run("clones default (scalar) branch", func(t *testing.T) {
		orig := map[string]any{"S": "hello", "N": "42"}
		got := cloneMap(orig)
		orig["S"] = "changed"
		assert.Equal(t, "hello", got["S"])
	})
}

func TestListStreamARNs_ErrNotExistSkipped(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "ghost", "KEYS_ONLY")
	// Remove only the metadata file; the table directory remains.
	require.NoError(t, s.root.Remove("ghost.table.json"))
	entries, err := s.ListStreamARNs("")
	require.NoError(t, err)
	assert.Len(t, entries, 0)
}

func TestDescribeStream(t *testing.T) {
	s := newTestStorage(t)
	meta := mustCreateStreamTable(t, s, "ds-test", "NEW_AND_OLD_IMAGES")
	arn := streamARN("ds-test", meta.StreamLabel)

	desc, err := s.DescribeStream(arn)
	require.NoError(t, err)
	assert.Equal(t, arn, desc.StreamARN)
	assert.Equal(t, "ENABLED", desc.StreamStatus)
	assert.Equal(t, "NEW_AND_OLD_IMAGES", desc.StreamViewType)
	assert.Equal(t, "ds-test", desc.TableName)
	assert.NotEmpty(t, desc.ShardID)

	_, err = s.DescribeStream(
		"arn:aws:dynamodb:us-east-1:000000000000:table/ds-test/stream/bad-label",
	)
	assert.ErrorIs(t, err, ErrStreamNotFound)
}

func TestUpdateTableEnablesStream(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.CreateTable(testMeta))

	meta, err := s.UpdateTable("test-table", UpdateTableInput{
		StreamSpec: &StreamSpecification{StreamEnabled: true, StreamViewType: "KEYS_ONLY"},
	})
	require.NoError(t, err)
	require.NotNil(t, meta.StreamSpec)
	assert.True(t, meta.StreamSpec.StreamEnabled)
	assert.NotEmpty(t, meta.StreamLabel)

	label := meta.StreamLabel
	// Put an item — should emit a stream record.
	_, err = s.PutItem("test-table", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
	require.NoError(t, err)

	arn := streamARN("test-table", label)
	shardIDStr := shardID("test-table", label)
	iter, err := s.GetShardIterator(arn, shardIDStr, "TRIM_HORIZON", "")
	require.NoError(t, err)
	recs, _, err := s.GetStreamRecords(iter, 1000)
	require.NoError(t, err)
	assert.Len(t, recs, 1)
}

func TestUpdateTableDisablesStream(t *testing.T) {
	s := newTestStorage(t)
	meta := mustCreateStreamTable(t, s, "disable-test", "NEW_IMAGE")

	_, err := s.UpdateTable("disable-test", UpdateTableInput{
		StreamSpec: &StreamSpecification{StreamEnabled: false},
	})
	require.NoError(t, err)

	// Buffer should be removed.
	buf := s.getStreamBuffer("disable-test")
	assert.Nil(t, buf)

	// PutItem should not emit records.
	_, err = s.PutItem("disable-test", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
	require.NoError(t, err)
	buf = s.getStreamBuffer("disable-test")
	assert.Nil(t, buf)

	// Re-enable should get a new label.
	meta2, err := s.UpdateTable("disable-test", UpdateTableInput{
		StreamSpec: &StreamSpecification{StreamEnabled: true, StreamViewType: "NEW_IMAGE"},
	})
	require.NoError(t, err)
	assert.NotEqual(
		t,
		meta.StreamLabel,
		meta2.StreamLabel,
		"re-enabling should produce a new stream label",
	)
}

func TestDeleteTableCleansUpStream(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "del-test", "NEW_IMAGE")
	require.NoError(t, s.DeleteTable("del-test"))
	buf := s.getStreamBuffer("del-test")
	assert.Nil(t, buf)
}

func TestGetStreamRecordsLimit(t *testing.T) {
	s := newTestStorage(t)
	meta := mustCreateStreamTable(t, s, "limit-test", "KEYS_ONLY")
	arn := streamARN("limit-test", meta.StreamLabel)
	shardIDStr := shardID("limit-test", meta.StreamLabel)

	for i := 0; i < 10; i++ {
		item := map[string]any{"pk": map[string]any{"S": "k"}}
		_, err := s.PutItem("limit-test", item, nil)
		require.NoError(t, err)
	}

	iter, err := s.GetShardIterator(arn, shardIDStr, "TRIM_HORIZON", "")
	require.NoError(t, err)

	recs, nextIter, err := s.GetStreamRecords(iter, 3)
	require.NoError(t, err)
	assert.Len(t, recs, 3)
	assert.NotEmpty(t, nextIter)

	recs2, _, err := s.GetStreamRecords(nextIter, 1000)
	require.NoError(t, err)
	assert.Len(t, recs2, 7)
}

func TestUpdateItemEmitsStream(t *testing.T) {
	s := newTestStorage(t)
	meta := mustCreateStreamTable(t, s, "update-stream", "NEW_AND_OLD_IMAGES")
	arn := streamARN("update-stream", meta.StreamLabel)
	shardIDStr := shardID("update-stream", meta.StreamLabel)

	// UpdateItem on non-existent item → INSERT
	_, _, err := s.UpdateItem(
		"update-stream",
		map[string]any{"pk": map[string]any{"S": "k1"}},
		map[string]any{"val": map[string]any{"S": "v1"}},
		nil,
	)
	require.NoError(t, err)

	// UpdateItem on existing item → MODIFY
	_, _, err = s.UpdateItem(
		"update-stream",
		map[string]any{"pk": map[string]any{"S": "k1"}},
		map[string]any{"val": map[string]any{"S": "v2"}},
		nil,
	)
	require.NoError(t, err)

	iter, err := s.GetShardIterator(arn, shardIDStr, "TRIM_HORIZON", "")
	require.NoError(t, err)
	recs, _, err := s.GetStreamRecords(iter, 1000)
	require.NoError(t, err)
	require.Len(t, recs, 2)
	assert.Equal(t, "INSERT", recs[0].EventName)
	assert.Equal(t, "MODIFY", recs[1].EventName)

	// Check sequence numbers are monotonically increasing.
	assert.Less(t, recs[0].SeqNum, recs[1].SeqNum)
	// Check sequence number string format (21 digits).
	assert.Len(t, seqNumStr(recs[0].SeqNum), 21)
}

func TestStreamLabelFormat(t *testing.T) {
	label := newStreamLabel()
	_, err := time.Parse("2006-01-02T15:04:05.000000000", label)
	require.NoError(t, err, "stream label should be valid ISO8601 with nanoseconds: %s", label)
}

func TestGetStreamRecordsNegativePosition(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "neg-pos", "NEW_IMAGE")
	item := map[string]any{"pk": map[string]any{"S": "k1"}}
	_, err := s.PutItem("neg-pos", item, nil)
	require.NoError(t, err)

	// Craft an iterator with a negative position to exercise the start < 0 guard.
	iter, err := encodeIterator("neg-pos", -5)
	require.NoError(t, err)

	recs, _, err := s.GetStreamRecords(iter, 1000)
	require.NoError(t, err)
	// Should return all records starting from position 0, not panic.
	assert.Len(t, recs, 1)
}

func TestBatchWriteItemsPutPreimageReadError(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "batch-put-err", "NEW_AND_OLD_IMAGES")

	item := map[string]any{"pk": map[string]any{"S": "k1"}}
	_, err := s.PutItem("batch-put-err", item, nil)
	require.NoError(t, err)

	// Inject a readAll that fails on the 2nd call: 1st is readTableMeta inside
	// BatchWriteItems, 2nd is the pre-image read for the existing item.
	callCount := 0
	s.readAll = func(r io.Reader) ([]byte, error) {
		callCount++
		if callCount >= 2 {
			return nil, errors.New("simulated I/O error")
		}
		return io.ReadAll(r)
	}

	err = s.BatchWriteItems("batch-put-err", []map[string]any{item}, nil)
	require.Error(t, err)
}

func TestBatchWriteItemsDeletePreimageReadError(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "batch-del-err", "NEW_AND_OLD_IMAGES")

	item := map[string]any{"pk": map[string]any{"S": "k1"}}
	_, err := s.PutItem("batch-del-err", item, nil)
	require.NoError(t, err)

	// Inject a readAll that fails on the 2nd call: 1st is readTableMeta inside
	// BatchWriteItems, 2nd is the pre-image read for the existing item.
	callCount := 0
	s.readAll = func(r io.Reader) ([]byte, error) {
		callCount++
		if callCount >= 2 {
			return nil, errors.New("simulated I/O error")
		}
		return io.ReadAll(r)
	}

	err = s.BatchWriteItems("batch-del-err", nil, []map[string]any{item})
	require.Error(t, err)
}

// newTestStorageAt creates a storage rooted at dir (shared across calls) so a
// "restart" can be simulated by opening a second storage on the same directory.
func newTestStorageAt(t *testing.T, dir string) *Storage {
	t.Helper()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStreamPersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	// First storage instance: create table, emit records.
	s1 := newTestStorageAt(t, dir)
	meta := mustCreateStreamTable(t, s1, "persist-test", "NEW_AND_OLD_IMAGES")
	item := map[string]any{"pk": map[string]any{"S": "k1"}, "v": map[string]any{"S": "hello"}}
	_, err := s1.PutItem("persist-test", item, nil)
	require.NoError(t, err)
	_, err = s1.PutItem("persist-test", map[string]any{
		"pk": map[string]any{"S": "k1"},
		"v":  map[string]any{"S": "world"},
	}, nil)
	require.NoError(t, err)
	require.NoError(t, s1.Close())

	// Second storage instance: simulate restart against the same directory.
	s2 := newTestStorageAt(t, dir)
	arn := streamARN("persist-test", meta.StreamLabel)
	shardIDStr := shardID("persist-test", meta.StreamLabel)

	iter, err := s2.GetShardIterator(arn, shardIDStr, "TRIM_HORIZON", "")
	require.NoError(t, err)
	recs, _, err := s2.GetStreamRecords(iter, 1000)
	require.NoError(t, err)
	require.Len(t, recs, 2, "records written before restart must survive")
	assert.Equal(t, "INSERT", recs[0].EventName)
	assert.Equal(t, "MODIFY", recs[1].EventName)
}

func TestStreamExpiredRecordsFilteredOnLoad(t *testing.T) {
	dir := t.TempDir()

	s1 := newTestStorageAt(t, dir)
	meta := mustCreateStreamTable(t, s1, "expiry-test", "NEW_IMAGE")
	item := map[string]any{"pk": map[string]any{"S": "k1"}}
	_, err := s1.PutItem("expiry-test", item, nil)
	require.NoError(t, err)

	// Back-date the one record in the JSONL file to 25 hours ago.
	buf := s1.getStreamBuffer("expiry-test")
	require.NotNil(t, buf)
	buf.mu.Lock()
	buf.records[0].CreatedAt = time.Now().UTC().Add(-25 * time.Hour)
	s1.rewriteStreamFile("expiry-test", buf.records)
	buf.mu.Unlock()

	require.NoError(t, s1.Close())

	// Restart: expired record must not be loaded.
	s2 := newTestStorageAt(t, dir)
	arn := streamARN("expiry-test", meta.StreamLabel)
	shardIDStr := shardID("expiry-test", meta.StreamLabel)

	iter, err := s2.GetShardIterator(arn, shardIDStr, "TRIM_HORIZON", "")
	require.NoError(t, err)
	recs, _, err := s2.GetStreamRecords(iter, 1000)
	require.NoError(t, err)
	assert.Empty(t, recs, "records older than 24 hours must not be loaded")
}

func TestTrimStreamForTable(t *testing.T) {
	s := newTestStorage(t)
	meta := mustCreateStreamTable(t, s, "trim-test", "KEYS_ONLY")
	arn := streamARN("trim-test", meta.StreamLabel)
	shardIDStr := shardID("trim-test", meta.StreamLabel)

	for i := range 5 {
		item := map[string]any{"pk": map[string]any{"S": fmt.Sprintf("k%d", i)}}
		_, err := s.PutItem("trim-test", item, nil)
		require.NoError(t, err)
	}

	// Back-date the first 3 records so they appear older than 24 hours.
	buf := s.getStreamBuffer("trim-test")
	require.NotNil(t, buf)
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	buf.mu.Lock()
	for i := range 3 {
		buf.records[i].CreatedAt = cutoff.Add(-time.Minute)
	}
	buf.mu.Unlock()

	s.trimStreamForTable("trim-test", cutoff)

	iter, err := s.GetShardIterator(arn, shardIDStr, "TRIM_HORIZON", "")
	require.NoError(t, err)
	recs, _, err := s.GetStreamRecords(iter, 1000)
	require.NoError(t, err)
	assert.Len(t, recs, 2, "only the 2 non-expired records should remain")
}

func TestStreamFileRemovedOnDeleteTable(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "del-file-test", "NEW_IMAGE")
	_, err := s.PutItem("del-file-test", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
	require.NoError(t, err)

	// Verify the JSONL file exists before deletion.
	_, err = s.root.Stat(streamFilePath("del-file-test"))
	require.NoError(t, err, "stream file should exist after a PutItem")

	require.NoError(t, s.DeleteTable("del-file-test"))

	_, err = s.root.Stat(streamFilePath("del-file-test"))
	assert.True(t, errors.Is(err, os.ErrNotExist), "stream file should be removed on DeleteTable")
}

func TestStreamFileRemovedOnDisable(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "disable-file-test", "NEW_IMAGE")
	_, err := s.PutItem("disable-file-test", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
	require.NoError(t, err)

	_, err = s.root.Stat(streamFilePath("disable-file-test"))
	require.NoError(t, err, "stream file should exist after a PutItem")

	_, err = s.UpdateTable("disable-file-test", UpdateTableInput{
		StreamSpec: &StreamSpecification{StreamEnabled: false},
	})
	require.NoError(t, err)

	_, err = s.root.Stat(streamFilePath("disable-file-test"))
	assert.True(
		t,
		errors.Is(err, os.ErrNotExist),
		"stream file should be removed when streaming is disabled",
	)
}

// TestDeleteStreamBufferRemoveFileError covers lines 145-147: the removeFile warning path
// when the stream JSONL file cannot be removed for a reason other than ErrNotExist.
func TestDeleteStreamBufferRemoveFileError(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "buf-rm-err", "NEW_IMAGE")
	_, err := s.PutItem("buf-rm-err", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
	require.NoError(t, err)

	s.removeFile = func(string) error { return errors.New("remove failed") }
	s.deleteStreamBuffer("buf-rm-err")
	assert.Nil(t, s.getStreamBuffer("buf-rm-err"))
}

// TestLoadStreamRecordsFromDiskReadAllError covers lines 497-500: readAll returns an error
// after the stream file was successfully opened.
func TestLoadStreamRecordsFromDiskReadAllError(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "readall-err", "NEW_IMAGE")
	_, err := s.PutItem("readall-err", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
	require.NoError(t, err)

	s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("disk read error") }
	records := s.loadStreamRecordsFromDisk("readall-err")
	assert.Nil(t, records)
}

// TestLoadStreamRecordsFromDiskCorruptLine covers lines 508-510: a corrupt JSON line in the
// stream file is skipped while valid lines are still returned.
func TestLoadStreamRecordsFromDiskCorruptLine(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "corrupt-stream", "NEW_IMAGE")
	_, err := s.PutItem("corrupt-stream", map[string]any{"pk": map[string]any{"S": "k1"}}, nil)
	require.NoError(t, err)

	f, err := s.root.OpenFile(streamFilePath("corrupt-stream"), os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.Write([]byte("not-valid-json\n"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	s.streamsMu.Lock()
	delete(s.streams, "corrupt-stream")
	s.streamsMu.Unlock()

	records := s.loadStreamRecordsFromDisk("corrupt-stream")
	assert.Len(t, records, 1, "valid record should survive; corrupt line should be skipped")
}

// TestAppendToStreamFileOpenError covers lines 527-530: openFile returns an error when the
// stream JSONL file is being opened for append.
func TestAppendToStreamFileOpenError(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "append-open-err", "NEW_IMAGE")

	origOpenFile := s.openFile
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		if strings.HasSuffix(name, ".stream.jsonl") {
			return nil, errors.New("open failed")
		}
		return origOpenFile(name, flag, perm)
	}
	_, err := s.PutItem(
		"append-open-err",
		map[string]any{"pk": map[string]any{"S": "k"}},
		nil,
	)
	require.NoError(t, err, "PutItem itself must succeed even when stream file cannot be opened")
}

// TestAppendToStreamFileWriteError covers lines 532-534: Write returns an error after the
// stream file was opened successfully.
func TestAppendToStreamFileWriteError(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "append-write-err", "NEW_IMAGE")

	origOpenFile := s.openFile
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		if strings.HasSuffix(name, ".stream.jsonl") {
			return errWriter{writeErr: errors.New("write failed")}, nil
		}
		return origOpenFile(name, flag, perm)
	}
	_, err := s.PutItem(
		"append-write-err",
		map[string]any{"pk": map[string]any{"S": "k"}},
		nil,
	)
	require.NoError(t, err, "PutItem itself must succeed even when stream record write fails")
}

// TestRewriteStreamFileAllExpired covers lines 543 and 547: rewriteStreamFile is called with
// an empty slice when trimStreamForTable removes all records.
func TestRewriteStreamFileAllExpired(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "all-expired", "KEYS_ONLY")
	for i := range 3 {
		item := map[string]any{"pk": map[string]any{"S": fmt.Sprintf("k%d", i)}}
		_, err := s.PutItem("all-expired", item, nil)
		require.NoError(t, err)
	}

	buf := s.getStreamBuffer("all-expired")
	require.NotNil(t, buf)
	cutoff := time.Now().UTC()
	buf.mu.Lock()
	for i := range len(buf.records) {
		buf.records[i].CreatedAt = cutoff.Add(-time.Hour)
	}
	buf.mu.Unlock()

	s.trimStreamForTable("all-expired", cutoff)

	buf.mu.RLock()
	n := len(buf.records)
	buf.mu.RUnlock()
	assert.Equal(t, 0, n, "all records should be trimmed")
}

// TestRewriteStreamFileEmptyRemoveError covers lines 544-546: removeFile returns a
// non-ErrNotExist error when rewriteStreamFile tries to delete the empty stream file.
func TestRewriteStreamFileEmptyRemoveError(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "rw-rm-err", "KEYS_ONLY")
	_, err := s.PutItem("rw-rm-err", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
	require.NoError(t, err)

	origRemoveFile := s.removeFile
	s.removeFile = func(name string) error {
		if strings.HasSuffix(name, ".stream.jsonl") {
			return errors.New("remove failed")
		}
		return origRemoveFile(name)
	}

	buf := s.getStreamBuffer("rw-rm-err")
	require.NotNil(t, buf)
	cutoff := time.Now().UTC()
	buf.mu.Lock()
	for i := range len(buf.records) {
		buf.records[i].CreatedAt = cutoff.Add(-time.Hour)
	}
	buf.mu.Unlock()

	s.trimStreamForTable("rw-rm-err", cutoff)
}

// TestRewriteStreamFileOpenError covers lines 550-553: openFile returns an error when
// rewriteStreamFile tries to open the stream file for truncation write.
func TestRewriteStreamFileOpenError(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "rw-open-err", "NEW_IMAGE")

	s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
		return nil, errors.New("open failed")
	}
	recs := []streamRecord{
		{EventID: "1", EventName: "INSERT", SeqNum: 1, CreatedAt: time.Now()},
	}
	s.rewriteStreamFile("rw-open-err", recs)
}

// TestRewriteStreamFileWriteError covers lines 557-566: Write returns an error while
// rewriteStreamFile is iterating over records.
func TestRewriteStreamFileWriteError(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "rw-write-err", "NEW_IMAGE")

	s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
		return errWriter{writeErr: errors.New("write failed")}, nil
	}
	recs := []streamRecord{
		{EventID: "1", EventName: "INSERT", SeqNum: 1, CreatedAt: time.Now()},
	}
	s.rewriteStreamFile("rw-write-err", recs)
}

// TestTrimStreamForTableNilBuf covers lines 574-576: trimStreamForTable returns immediately
// when there is no stream buffer for the given table.
func TestTrimStreamForTableNilBuf(t *testing.T) {
	s := newTestStorage(t)
	s.trimStreamForTable("nonexistent-table", time.Now())
}

// TestTrimStreamForTableNoOp covers lines 585-587: trimStreamForTable returns early when no
// records fall before the cutoff (nothing to trim).
func TestTrimStreamForTableNoOp(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "trim-noop", "KEYS_ONLY")
	_, err := s.PutItem("trim-noop", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
	require.NoError(t, err)

	// Zero cutoff: all records are after it, so nothing gets trimmed.
	s.trimStreamForTable("trim-noop", time.Time{})

	buf := s.getStreamBuffer("trim-noop")
	require.NotNil(t, buf)
	buf.mu.RLock()
	n := len(buf.records)
	buf.mu.RUnlock()
	assert.Equal(t, 1, n)
}

// TestTrimAllStreams covers lines 593-604: trimAllStreams iterates over all active stream
// buffers and trims records older than 24 hours.
func TestTrimAllStreams(t *testing.T) {
	s := newTestStorage(t)
	for _, tn := range []string{"trim-all-a", "trim-all-b"} {
		mustCreateStreamTable(t, s, tn, "KEYS_ONLY")
		_, err := s.PutItem(tn, map[string]any{"pk": map[string]any{"S": "k"}}, nil)
		require.NoError(t, err)
	}

	for _, tn := range []string{"trim-all-a", "trim-all-b"} {
		buf := s.getStreamBuffer(tn)
		require.NotNil(t, buf)
		buf.mu.Lock()
		for i := range len(buf.records) {
			buf.records[i].CreatedAt = time.Now().UTC().Add(-25 * time.Hour)
		}
		buf.mu.Unlock()
	}

	s.trimAllStreams()

	for _, tn := range []string{"trim-all-a", "trim-all-b"} {
		buf := s.getStreamBuffer(tn)
		require.NotNil(t, buf)
		buf.mu.RLock()
		n := len(buf.records)
		buf.mu.RUnlock()
		assert.Equal(t, 0, n, "table %s: all expired records should be trimmed", tn)
	}
}

// TestLoadAllStreamBuffersReadDirError covers lines 610-613: loadAllStreamBuffers logs a
// warning and returns when readDir fails.
func TestLoadAllStreamBuffersReadDirError(t *testing.T) {
	s := newTestStorage(t)
	s.listDirFn = func(string) ([]os.DirEntry, error) { return nil, errors.New("readdir failed") }
	s.loadAllStreamBuffers()
}

// TestLoadAllStreamBuffersReadTableMetaError covers lines 620-622: loadAllStreamBuffers logs
// a warning and continues when a table metadata file cannot be parsed.
func TestLoadAllStreamBuffersReadTableMetaError(t *testing.T) {
	s := newTestStorage(t)

	f, err := s.root.OpenFile("corrupt.table.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	require.NoError(t, err)
	_, err = f.Write([]byte("not-valid-json"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	s.loadAllStreamBuffers()
}

// TestStartTrimLoopTickerFires covers lines 640-641: the ticker case in startTrimLoop fires
// and calls trimAllStreams, which removes expired records.
func TestStartTrimLoopTickerFires(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "trim-loop", "KEYS_ONLY")
	_, err := s.PutItem("trim-loop", map[string]any{"pk": map[string]any{"S": "k"}}, nil)
	require.NoError(t, err)

	buf := s.getStreamBuffer("trim-loop")
	require.NotNil(t, buf)
	buf.mu.Lock()
	buf.records[0].CreatedAt = time.Now().UTC().Add(-25 * time.Hour)
	s.rewriteStreamFile("trim-loop", buf.records)
	buf.mu.Unlock()

	s.startTrimLoop(1 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	buf.mu.RLock()
	n := len(buf.records)
	buf.mu.RUnlock()
	assert.Equal(t, 0, n, "trim loop should have removed the expired record")
}
