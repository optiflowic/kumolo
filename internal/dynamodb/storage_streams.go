package dynamodb

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- MD5 used only for deterministic shard ID generation, not security
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrStreamNotFound is returned when a stream ARN does not match any known stream.
var ErrStreamNotFound = errors.New("stream not found")

const (
	// streamRetentionPeriod is the AWS DynamoDB Streams retention window.
	streamRetentionPeriod = 24 * time.Hour
	// streamTrimInterval is how often the background goroutine evicts expired records.
	streamTrimInterval = time.Hour
)

func deepCloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		cp := make(map[string]any, len(x))
		for k, vv := range x {
			cp[k] = deepCloneAny(vv)
		}
		return cp
	case []any:
		cp := make([]any, len(x))
		for i, vv := range x {
			cp[i] = deepCloneAny(vv)
		}
		return cp
	default:
		return v
	}
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	return deepCloneAny(m).(map[string]any)
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
	deleted bool // set under mu by deleteStreamBuffer; prevents stale file writes
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
// using the label stored in the table metadata. On first creation the buffer is
// populated with any records persisted to disk, minus those older than 24 hours.
func (s *Storage) ensureStreamBuffer(tableName, label string) *streamBuffer {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if buf, ok := s.streams[tableName]; ok {
		return buf
	}
	buf := &streamBuffer{
		label:   label,
		shardID: shardID(tableName, label),
		records: s.loadStreamRecordsFromDisk(tableName),
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

// deleteStreamBuffer removes the buffer and the on-disk JSONL file for a table.
// Called on DeleteTable or when streaming is disabled.
func (s *Storage) deleteStreamBuffer(tableName string) {
	s.streamsMu.Lock()
	buf := s.streams[tableName]
	delete(s.streams, tableName)
	s.streamsMu.Unlock()

	// Mark the buffer deleted before removing the file so that any goroutine
	// which already holds a pointer to buf (captured before the map removal)
	// sees deleted=true when it next acquires buf.mu and skips the file write.
	if buf != nil {
		buf.mu.Lock()
		buf.deleted = true
		buf.mu.Unlock()
	}

	if err := s.removeFile(
		streamFilePath(tableName),
	); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		slog.Error("failed to remove stream file", "table", tableName, "err", err)
	}
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

	seqNum := s.seqNum.Add(1)
	rec := streamRecord{
		EventID:   fmt.Sprintf("%032x", seqNum),
		EventName: eventName,
		SeqNum:    seqNum,
		CreatedAt: time.Now().UTC(),
		ViewType:  viewType,
	}

	// Keys are always included; snapshot to avoid mutation after emit.
	rec.Keys = cloneMap(keys)

	switch viewType {
	case "KEYS_ONLY":
		// only keys; NewImage/OldImage stay nil
	case "NEW_IMAGE":
		rec.NewImage = cloneMap(newImage)
	case "OLD_IMAGE":
		rec.OldImage = cloneMap(oldImage)
	case "NEW_AND_OLD_IMAGES":
		rec.NewImage = cloneMap(newImage)
		rec.OldImage = cloneMap(oldImage)
	default:
		rec.NewImage = cloneMap(newImage)
		rec.OldImage = cloneMap(oldImage)
	}

	buf.mu.Lock()
	if !buf.deleted {
		buf.records = append(buf.records, rec)
		s.appendToStreamFile(tableName, rec)
	}
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
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read table metadata for %s: %w", name, err)
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
	if start < 0 {
		start = 0
	}
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
	return strconv.ParseUint(s, 10, 64)
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

// streamFilePath returns the JSONL file path for a table's stream records.
func streamFilePath(tableName string) string {
	return tableName + ".stream.jsonl"
}

// loadStreamRecordsFromDisk reads the JSONL file for tableName and returns all
// records whose CreatedAt is within the last 24 hours. Corrupt lines are skipped.
func (s *Storage) loadStreamRecordsFromDisk(tableName string) []streamRecord {
	path := streamFilePath(tableName)
	f, err := s.root.Open(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			// untestable: triggering a non-ErrNotExist Open failure requires OS-level
			// permission manipulation (e.g. chmod 000) which is fragile in CI.
			slog.Error("failed to open stream file", "table", tableName, "err", err)
		}
		return nil
	}
	defer func() { _ = f.Close() }()
	data, err := s.readAll(f)
	if err != nil {
		slog.Error("failed to read stream file", "table", tableName, "err", err)
		return nil
	}
	cutoff := time.Now().UTC().Add(-streamRetentionPeriod)
	var records []streamRecord
	filtered := false
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var rec streamRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			slog.Warn("skipping corrupt stream record", "table", tableName, "err", err)
			filtered = true
			continue
		}
		if rec.CreatedAt.After(cutoff) {
			records = append(records, rec)
		} else {
			filtered = true
		}
	}
	if filtered {
		// No buf.mu needed here — the buffer does not exist yet at load time.
		s.rewriteStreamFile(tableName, records)
	}
	return records
}

// appendToStreamFile appends rec as a single JSON line to the table's JSONL file.
// Must be called with buf.mu held to preserve write ordering.
// A new file descriptor is opened per call; acceptable at emulator scale.
func (s *Storage) appendToStreamFile(tableName string, rec streamRecord) {
	data, err := json.Marshal(rec)
	if err != nil {
		slog.Error("failed to marshal stream record", "table", tableName, "err", err)
		return
	}
	path := streamFilePath(tableName)
	line := append(data, '\n')
	f, err := s.openFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		slog.Error("failed to open stream file for append", "table", tableName, "err", err)
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(line); err != nil {
		slog.Error("failed to append stream record", "table", tableName, "err", err)
	}
}

// rewriteStreamFile atomically replaces the table's JSONL file. Records are marshaled
// into an in-memory buffer first so a marshal failure never touches the existing file.
// The payload is then written to a sibling ".tmp" file; only after a successful write
// and close is the tmp file renamed over the destination. Any failure cleans up the
// tmp file, leaving the original JSONL intact.
// If records is empty the file is removed. Must be called with buf.mu held.
func (s *Storage) rewriteStreamFile(tableName string, records []streamRecord) {
	path := streamFilePath(tableName)
	if len(records) == 0 {
		if err := s.removeFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Error("failed to remove empty stream file", "table", tableName, "err", err)
		}
		return
	}
	var payload bytes.Buffer
	for _, rec := range records {
		recData, merr := json.Marshal(rec)
		if merr != nil {
			slog.Error(
				"failed to marshal stream record during rewrite",
				"table",
				tableName,
				"err",
				merr,
			)
			return
		}
		payload.Write(recData)
		payload.WriteByte('\n')
	}
	tmpPath := path + ".tmp"
	f, err := s.openFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		slog.Error("failed to create temp stream file", "table", tableName, "err", err)
		return
	}
	_, writeErr := f.Write(payload.Bytes())
	closeErr := f.Close()
	if writeErr != nil {
		slog.Error("failed to write temp stream file", "table", tableName, "err", writeErr)
		_ = s.removeFile(tmpPath)
		return
	}
	if closeErr != nil {
		slog.Error("failed to close temp stream file", "table", tableName, "err", closeErr)
		_ = s.removeFile(tmpPath)
		return
	}
	if err := s.renameFn(tmpPath, path); err != nil {
		slog.Error("failed to rename temp stream file", "table", tableName, "err", err)
		_ = s.removeFile(tmpPath)
	}
}

// trimStreamForTable removes records older than cutoff from the buffer and rewrites
// the JSONL file. No-op if the buffer has no expired records.
func (s *Storage) trimStreamForTable(tableName string, cutoff time.Time) {
	buf := s.getStreamBuffer(tableName)
	if buf == nil {
		return
	}
	buf.mu.Lock()
	defer buf.mu.Unlock()
	if buf.deleted {
		return
	}
	trimmed := buf.records[:0:0]
	for _, r := range buf.records {
		if r.CreatedAt.After(cutoff) {
			trimmed = append(trimmed, r)
		}
	}
	if len(trimmed) == len(buf.records) {
		return
	}
	buf.records = trimmed
	s.rewriteStreamFile(tableName, trimmed)
}

// trimAllStreams trims expired records for every active stream buffer.
func (s *Storage) trimAllStreams() {
	s.streamsMu.RLock()
	tables := make([]string, 0, len(s.streams))
	for t := range s.streams {
		tables = append(tables, t)
	}
	s.streamsMu.RUnlock()

	cutoff := time.Now().UTC().Add(-streamRetentionPeriod)
	for _, t := range tables {
		s.trimStreamForTable(t, cutoff)
	}
}

// loadAllStreamBuffers is called at startup to restore in-memory buffers from disk.
func (s *Storage) loadAllStreamBuffers() {
	entries, err := s.readDir(".")
	if err != nil {
		slog.Warn("failed to read storage root during startup", "err", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".table.json") {
			continue
		}
		tableName := strings.TrimSuffix(e.Name(), ".table.json")
		meta, err := s.readTableMeta(tableName)
		if err != nil {
			slog.Warn("failed to read table meta during startup", "table", tableName, "err", err)
			continue
		}
		if meta.StreamSpec != nil && meta.StreamSpec.StreamEnabled && meta.StreamLabel != "" {
			s.ensureStreamBuffer(tableName, meta.StreamLabel)
		}
	}
}

// startTrimLoop starts a background goroutine that trims expired stream records
// every interval. It runs only when started via NewStorage (not in tests).
func (s *Storage) startTrimLoop(interval time.Duration) {
	s.trimWg.Add(1)
	go func() {
		defer s.trimWg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.trimAllStreams()
			case <-s.stopCh:
				return
			}
		}
	}()
}
