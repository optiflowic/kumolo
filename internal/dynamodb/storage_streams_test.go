package dynamodb

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	t.Run("unreadable table meta is skipped", func(t *testing.T) {
		s2 := newTestStorage(t)
		mustCreateStreamTable(t, s2, "good", "KEYS_ONLY")
		mustCreateStreamTable(t, s2, "bad", "NEW_IMAGE")
		f, err := s2.root.OpenFile("bad.table.json", os.O_WRONLY|os.O_TRUNC, 0o600)
		require.NoError(t, err)
		_, err = f.Write([]byte("invalid json"))
		require.NoError(t, err)
		require.NoError(t, f.Close())
		entries, err := s2.ListStreamARNs("")
		require.NoError(t, err)
		assert.Len(t, entries, 1)
		assert.Equal(t, "good", entries[0].TableName)
	})
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
