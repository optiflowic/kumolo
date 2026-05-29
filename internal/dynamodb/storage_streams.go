package dynamodb

import (
	"crypto/md5" // #nosec G501 -- MD5 used only for deterministic shard ID generation, not security
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// ErrStreamNotFound is returned when a stream ARN does not match any known stream.
var ErrStreamNotFound = errors.New("stream not found")

// globalSeqNum is a monotonically increasing counter for stream record sequence numbers.
var globalSeqNum atomic.Uint64

func init() {
	globalSeqNum.Store(uint64(time.Now().UnixNano() / 1e6))
}

// streamRecord holds one change event in-memory.
type streamRecord struct {
	EventID   string
	EventName string // INSERT | MODIFY | REMOVE
	Keys      map[string]any
	NewImage  map[string]any // nil when not included per StreamViewType
	OldImage  map[string]any // nil when not included per StreamViewType
	SeqNum    uint64
	CreatedAt time.Time
	ViewType  string
}

// streamBuffer is an in-memory ring of stream records for one table.
type streamBuffer struct {
	mu      sync.RWMutex
	label   string // ISO8601 timestamp when streaming was enabled
	shardID string // deterministic shard ID for this stream
	records []streamRecord
}

// shardIterState is the state encoded inside a shard iterator token.
type shardIterState struct {
	Table    string `json:"t"`
	Position int    `json:"p"`
}

// StreamEntry is one item in the ListStreams response.
type StreamEntry struct {
	StreamARN   string
	StreamLabel string
	TableName   string
}

// StreamDesc is the full description returned by DescribeStream.
type StreamDesc struct {
	StreamARN               string
	StreamLabel             string
	StreamStatus            string
	StreamViewType          string
	TableName               string
	KeySchema               []KeySchemaElement
	CreationRequestDateTime float64
	ShardID                 string
	StartingSequenceNumber  string
}

func streamARN(tableName, label string) string {
	return fmt.Sprintf(
		"arn:aws:dynamodb:us-east-1:000000000000:table/%s/stream/%s",
		tableName, label,
	)
}

func shardID(tableName, label string) string {
	// Keep total length ≤ 65 (AWS SDK client-side constraint on GetShardIterator ShardId).
	// Format: "shardId-<20-digit-fixed>-<8-hex>" = 8+20+1+8 = 37 chars.
	// The fixed counter (1) is sufficient for a single-shard model; the hash suffix
	// makes the ID unique per (tableName, label) pair.
	h := md5.Sum( // #nosec G401
		[]byte(tableName + "-" + label),
	)
	return fmt.Sprintf("shardId-%020d-%s", 1, hex.EncodeToString(h[:4]))
}

// ensureStreamBuffer returns the existing buffer for tableName, or creates one
// using the label stored in the table metadata.
func (s *Storage) ensureStreamBuffer(tableName, label string) *streamBuffer {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if buf, ok := s.streams[tableName]; ok {
		return buf
	}
	buf := &streamBuffer{
		label:   label,
		shardID: shardID(tableName, label),
	}
	s.streams[tableName] = buf
	return buf
}

// getStreamBuffer returns the existing buffer, or nil if streaming is not active.
func (s *Storage) getStreamBuffer(tableName string) *streamBuffer {
	s.streamsMu.RLock()
	defer s.streamsMu.RUnlock()
	return s.streams[tableName]
}

// deleteStreamBuffer removes the buffer for a table (called on DeleteTable).
func (s *Storage) deleteStreamBuffer(tableName string) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	delete(s.streams, tableName)
}

// emitStreamRecord records a mutation event if streaming is enabled for tableName.
// Must be called after the item write succeeds, outside the table write lock.
func (s *Storage) emitStreamRecord(
	tableName string,
	eventName string,
	viewType string,
	keys map[string]any,
	oldImage map[string]any,
	newImage map[string]any,
) {
	buf := s.getStreamBuffer(tableName)
	if buf == nil {
		return
	}

	seqNum := globalSeqNum.Add(1)
	rec := streamRecord{
		EventID:   fmt.Sprintf("%032x", seqNum),
		EventName: eventName,
		SeqNum:    seqNum,
		CreatedAt: time.Now().UTC(),
		ViewType:  viewType,
	}

	// Keys are always included.
	rec.Keys = keys

	switch viewType {
	case "KEYS_ONLY":
		// only keys; NewImage/OldImage stay nil
	case "NEW_IMAGE":
		rec.NewImage = newImage
	case "OLD_IMAGE":
		rec.OldImage = oldImage
	case "NEW_AND_OLD_IMAGES":
		rec.NewImage = newImage
		rec.OldImage = oldImage
	default:
		rec.NewImage = newImage
		rec.OldImage = oldImage
	}

	buf.mu.Lock()
	buf.records = append(buf.records, rec)
	buf.mu.Unlock()
}

// extractKeys returns only the primary key attributes from an item.
func extractKeys(item map[string]any, keySchema []KeySchemaElement) map[string]any {
	keys := make(map[string]any, len(keySchema))
	for _, k := range keySchema {
		if v, ok := item[k.AttributeName]; ok {
			keys[k.AttributeName] = v
		}
	}
	return keys
}

// ListStreamARNs returns stream entries for all tables with streaming enabled.
// If tableName is non-empty, only that table's stream is returned.
func (s *Storage) ListStreamARNs(tableName string) ([]StreamEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if tableName != "" {
		if _, err := s.readTableMeta(tableName); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, ErrTableNotFound
			}
			return nil, err
		}
	}

	entries, err := s.readDir(".")
	if err != nil {
		return nil, err
	}

	var result []StreamEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if tableName != "" && name != tableName {
			continue
		}
		meta, err := s.readTableMeta(name)
		if err != nil {
			continue
		}
		if meta.StreamSpec == nil || !meta.StreamSpec.StreamEnabled || meta.StreamLabel == "" {
			continue
		}
		result = append(result, StreamEntry{
			StreamARN:   streamARN(name, meta.StreamLabel),
			StreamLabel: meta.StreamLabel,
			TableName:   name,
		})
	}
	return result, nil
}

// DescribeStream returns stream metadata for the given stream ARN.
func (s *Storage) DescribeStream(arn string) (StreamDesc, error) {
	tableName, label, err := parseStreamARN(arn)
	if err != nil {
		return StreamDesc{}, ErrStreamNotFound
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	meta, err := s.readTableMeta(tableName)
	if err != nil {
		return StreamDesc{}, ErrStreamNotFound
	}
	if meta.StreamSpec == nil || meta.StreamLabel != label {
		return StreamDesc{}, ErrStreamNotFound
	}

	status := "DISABLED"
	if meta.StreamSpec.StreamEnabled {
		status = "ENABLED"
	}

	buf := s.getStreamBuffer(tableName)
	var startSeq string
	if buf != nil {
		buf.mu.RLock()
		if len(buf.records) > 0 {
			startSeq = seqNumStr(buf.records[0].SeqNum)
		}
		buf.mu.RUnlock()
	}

	return StreamDesc{
		StreamARN:               arn,
		StreamLabel:             label,
		StreamStatus:            status,
		StreamViewType:          meta.StreamSpec.StreamViewType,
		TableName:               tableName,
		KeySchema:               meta.KeySchema,
		CreationRequestDateTime: streamLabelToUnix(label),
		ShardID:                 shardID(tableName, label),
		StartingSequenceNumber:  startSeq,
	}, nil
}

// GetShardIterator returns an opaque iterator token for the given shard position.
func (s *Storage) GetShardIterator(
	streamARNStr, shardIDStr, iterType, seqNumInput string,
) (string, error) {
	tableName, label, err := parseStreamARN(streamARNStr)
	if err != nil {
		return "", ErrStreamNotFound
	}

	s.mu.RLock()
	meta, metaErr := s.readTableMeta(tableName)
	s.mu.RUnlock()
	if metaErr != nil || meta.StreamSpec == nil || meta.StreamLabel != label {
		return "", ErrStreamNotFound
	}
	if shardID(tableName, label) != shardIDStr {
		return "", ErrStreamNotFound
	}

	buf := s.getStreamBuffer(tableName)

	var pos int
	switch iterType {
	case "TRIM_HORIZON":
		pos = 0
	case "LATEST":
		if buf != nil {
			buf.mu.RLock()
			pos = len(buf.records)
			buf.mu.RUnlock()
		}
	case "AT_SEQUENCE_NUMBER", "AFTER_SEQUENCE_NUMBER":
		if seqNumInput == "" {
			return "", fmt.Errorf(
				"%w: SequenceNumber required for %s",
				ErrValidationException,
				iterType,
			)
		}
		seq, err := parseSeqNum(seqNumInput)
		if err != nil {
			return "", fmt.Errorf("%w: invalid SequenceNumber", ErrValidationException)
		}
		if buf != nil {
			buf.mu.RLock()
			pos = findSeqPos(buf.records, seq, iterType == "AFTER_SEQUENCE_NUMBER")
			buf.mu.RUnlock()
		}
	default:
		return "", fmt.Errorf("%w: invalid ShardIteratorType %q", ErrValidationException, iterType)
	}

	return encodeIterator(tableName, pos)
}

// GetStreamRecords reads records from the position encoded in iterStr.
// Returns (records, nextIterator, error).
func (s *Storage) GetStreamRecords(iterStr string, limit int) ([]streamRecord, string, error) {
	state, err := decodeIterator(iterStr)
	if err != nil {
		return nil, "", fmt.Errorf("%w: invalid ShardIterator", ErrValidationException)
	}

	buf := s.getStreamBuffer(state.Table)
	if buf == nil {
		// Table had streaming disabled or never had records; return empty + same position.
		next, _ := encodeIterator(state.Table, state.Position)
		return nil, next, nil
	}

	buf.mu.RLock()
	defer buf.mu.RUnlock()

	start := state.Position
	if start > len(buf.records) {
		start = len(buf.records)
	}
	end := len(buf.records)
	if limit > 0 && start+limit < end {
		end = start + limit
	}

	records := buf.records[start:end]
	next, _ := encodeIterator(state.Table, end)
	return records, next, nil
}

func encodeIterator(tableName string, pos int) (string, error) {
	data, err := json.Marshal(shardIterState{Table: tableName, Position: pos})
	if err != nil {
		// unreachable: json.Marshal on a struct with string+int fields always succeeds.
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func decodeIterator(s string) (shardIterState, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return shardIterState{}, err
	}
	var st shardIterState
	if err := json.Unmarshal(data, &st); err != nil {
		return shardIterState{}, err
	}
	return st, nil
}

// parseStreamARN parses "arn:aws:dynamodb:...:table/<name>/stream/<label>".
func parseStreamARN(arn string) (tableName, label string, err error) {
	// Format: arn:aws:dynamodb:<region>:<account>:table/<name>/stream/<label>
	prefix := "arn:aws:dynamodb:us-east-1:000000000000:table/"
	if len(arn) <= len(prefix) {
		return "", "", fmt.Errorf("invalid ARN")
	}
	rest := arn[len(prefix):]
	// rest = "<name>/stream/<label>"
	slashIdx := -1
	const streamSuffix = "/stream/"
	for i := 0; i <= len(rest)-len(streamSuffix); i++ {
		if rest[i:i+len(streamSuffix)] == streamSuffix {
			slashIdx = i
			break
		}
	}
	if slashIdx < 0 {
		return "", "", fmt.Errorf("invalid ARN")
	}
	tableName = rest[:slashIdx]
	label = rest[slashIdx+len(streamSuffix):]
	if tableName == "" || label == "" {
		return "", "", fmt.Errorf("invalid ARN")
	}
	return tableName, label, nil
}

func seqNumStr(n uint64) string {
	return fmt.Sprintf("%021d", n)
}

func parseSeqNum(s string) (uint64, error) {
	var n uint64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, err
	}
	return n, nil
}

// findSeqPos returns the index of the first record at or after seq.
// If after=true, returns the index after the matching record.
func findSeqPos(records []streamRecord, seq uint64, after bool) int {
	for i, r := range records {
		if r.SeqNum == seq {
			if after {
				return i + 1
			}
			return i
		}
		if r.SeqNum > seq {
			return i
		}
	}
	return len(records)
}

func streamLabelToUnix(label string) float64 {
	// Try nanosecond format first, then fall back to millisecond for older labels.
	t, err := time.Parse("2006-01-02T15:04:05.000000000", label)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05.000", label)
		if err != nil {
			return 0
		}
	}
	return float64(t.Unix())
}
