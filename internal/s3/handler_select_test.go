package s3

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseEventStream decodes all event stream messages from a byte slice.
// Returns a slice of (eventType, payload) pairs.
func parseEventStream(t *testing.T, data []byte) []struct{ kind, payload string } {
	t.Helper()
	var events []struct{ kind, payload string }
	for len(data) >= 16 {
		totalLen := binary.BigEndian.Uint32(data[0:4])
		require.Greater(
			t,
			totalLen,
			uint32(0),
			"event stream frame totalLen must be positive to make progress",
		)
		require.GreaterOrEqual(
			t,
			uint32(len(data)),
			totalLen, //nolint:gosec // test payloads are always < 4GB
			"truncated event stream: have %d bytes, need %d",
			len(data),
			totalLen,
		)

		headersLen := binary.BigEndian.Uint32(data[4:8])
		// 12 = 8-byte prelude + 4-byte prelude CRC; 4 = message CRC tail
		require.LessOrEqual(t, uint32(12)+headersLen+uint32(4), totalLen,
			"headersLen %d overflows message of totalLen %d", headersLen, totalLen)

		msg := data[:totalLen]

		// Verify prelude CRC.
		preludeCRC := binary.BigEndian.Uint32(msg[8:12])
		require.Equal(t, crc32.ChecksumIEEE(msg[0:8]), preludeCRC, "prelude CRC mismatch")

		// Verify message CRC.
		msgCRC := binary.BigEndian.Uint32(msg[totalLen-4 : totalLen])
		require.Equal(t, crc32.ChecksumIEEE(msg[:totalLen-4]), msgCRC, "message CRC mismatch")

		// Parse headers.
		hBytes := msg[12 : 12+headersLen]
		headers := parseESHeaders(hBytes)

		payloadStart := uint32(12) + headersLen
		payloadEnd := totalLen - 4
		require.LessOrEqual(
			t,
			payloadStart,
			payloadEnd,
			"payload bounds invalid: start=%d end=%d",
			payloadStart,
			payloadEnd,
		)
		payload := string(msg[payloadStart:payloadEnd])

		events = append(events, struct{ kind, payload string }{
			kind:    headers[":event-type"],
			payload: payload,
		})

		data = data[totalLen:]
	}
	return events
}

func parseESHeaders(hBytes []byte) map[string]string {
	m := make(map[string]string)
	i := 0
	for i < len(hBytes) {
		nameLen := int(hBytes[i])
		i++
		name := string(hBytes[i : i+nameLen])
		i += nameLen
		i++ // skip type byte (always 7)
		valLen := int(binary.BigEndian.Uint16(hBytes[i : i+2]))
		i += 2
		val := string(hBytes[i : i+valLen])
		i += valLen
		m[name] = val
	}
	return m
}

func selectXMLBody(expression, exprType, inputFmt, outputFmt string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<SelectObjectContentRequest>` +
		`<Expression>` + expression + `</Expression>` +
		`<ExpressionType>` + exprType + `</ExpressionType>` +
		inputFmt +
		outputFmt +
		`</SelectObjectContentRequest>`
}

func csvOutputFmt() string {
	return `<OutputSerialization><CSV></CSV></OutputSerialization>`
}

func jsonOutputFmt() string {
	return `<OutputSerialization><JSON><RecordDelimiter>\n</RecordDelimiter></JSON></OutputSerialization>`
}

func putSelectObject(t *testing.T, ro *Router, bucket, key, body string) {
	t.Helper()
	ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/"+bucket, nil))
	req := httptest.NewRequest(http.MethodPut, "/"+bucket+"/"+key, strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	ro.ServeHTTP(httptest.NewRecorder(), req)
}

func doSelect(t *testing.T, ro *Router, bucket, key, xmlBody string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(
		http.MethodPost,
		"/"+bucket+"/"+key+"?select&select-type=2",
		strings.NewReader(xmlBody),
	)
	w := httptest.NewRecorder()
	ro.ServeHTTP(w, req)
	return w
}

func TestSelectObjectContent_CSVSelectStar(t *testing.T) {
	ro := newTestRouter(t)
	csvData := "id,name,score\n1,Alice,90\n2,Bob,75\n3,Carol,85\n"
	putSelectObject(t, ro, "my-bucket", "data.csv", csvData)

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	require.NotEmpty(t, events)

	// Last two events must be Stats and End.
	assert.Equal(t, "Stats", events[len(events)-2].kind)
	assert.Equal(t, "End", events[len(events)-1].kind)

	// Collect all Records payloads.
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	got := buf.String()
	assert.Contains(t, got, "Alice")
	assert.Contains(t, got, "Bob")
	assert.Contains(t, got, "Carol")
}

func TestSelectObjectContent_CSVWhereClause(t *testing.T) {
	ro := newTestRouter(t)
	csvData := "id,name,score\n1,Alice,90\n2,Bob,75\n3,Carol,85\n"
	putSelectObject(t, ro, "my-bucket", "data.csv", csvData)

	xmlBody := selectXMLBody(
		"SELECT s.name FROM S3Object s WHERE CAST(s.score AS FLOAT) >= 85",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	got := buf.String()
	assert.Contains(t, got, "Alice")
	assert.NotContains(t, got, "Bob")
	assert.Contains(t, got, "Carol")
}

func TestSelectObjectContent_CSVPositionalColumns(t *testing.T) {
	ro := newTestRouter(t)
	csvData := "1,Alice,90\n2,Bob,75\n"
	putSelectObject(t, ro, "my-bucket", "data.csv", csvData)

	xmlBody := selectXMLBody(
		"SELECT s._1, s._2 FROM S3Object s WHERE CAST(s._3 AS FLOAT) > 80",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	got := buf.String()
	assert.Contains(t, got, "Alice")
	assert.NotContains(t, got, "Bob")
}

func TestSelectObjectContent_CountStar(t *testing.T) {
	ro := newTestRouter(t)
	csvData := "a,b\n1,x\n2,y\n3,z\n"
	putSelectObject(t, ro, "my-bucket", "data.csv", csvData)

	xmlBody := selectXMLBody(
		"SELECT COUNT(*) FROM S3Object",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	assert.Contains(t, buf.String(), "3")
}

func TestSelectObjectContent_LimitClause(t *testing.T) {
	ro := newTestRouter(t)
	lines := "1\n2\n3\n4\n5\n"
	putSelectObject(t, ro, "my-bucket", "data.csv", lines)

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object LIMIT 3",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	// Should contain 3 lines.
	lines2 := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	assert.Len(t, lines2, 3)
}

func TestSelectObjectContent_JSONInputCSVOutput(t *testing.T) {
	ro := newTestRouter(t)
	jsonData := `{"name":"Alice","score":90}` + "\n" +
		`{"name":"Bob","score":75}` + "\n"
	putSelectObject(t, ro, "my-bucket", "data.json", jsonData)

	xmlBody := selectXMLBody(
		"SELECT s.name FROM S3Object s WHERE CAST(s.score AS FLOAT) > 80",
		"SQL",
		`<InputSerialization><JSON><Type>LINES</Type></JSON></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.json", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	got := buf.String()
	assert.Contains(t, got, "Alice")
	assert.NotContains(t, got, "Bob")
}

func TestSelectObjectContent_StatsEvent(t *testing.T) {
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "data.csv", "a,b\n1,2\n")

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())

	var statsPayload string
	for _, ev := range events {
		if ev.kind == "Stats" {
			statsPayload = ev.payload
			break
		}
	}
	require.NotEmpty(t, statsPayload)

	type statsXML struct {
		XMLName        xml.Name `xml:"Stats"`
		BytesScanned   int64    `xml:"BytesScanned"`
		BytesProcessed int64    `xml:"BytesProcessed"`
		BytesReturned  int64    `xml:"BytesReturned"`
	}
	// Strip XML declaration if present.
	statsPayload = strings.TrimPrefix(statsPayload,
		`<?xml version="1.0" encoding="UTF-8"?>`)
	var stats statsXML
	require.NoError(t, xml.Unmarshal([]byte(statsPayload), &stats))
	assert.Greater(t, stats.BytesScanned, int64(0))
	assert.Greater(t, stats.BytesProcessed, int64(0))
}

func TestSelectObjectContent_EndEvent(t *testing.T) {
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "data.csv", "x\n1\n")

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	last := events[len(events)-1]
	assert.Equal(t, "End", last.kind)
	assert.Empty(t, last.payload)
}

func TestSelectObjectContent_NotFound(t *testing.T) {
	ro := newTestRouter(t)
	ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "missing.csv", xmlBody)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "NoSuchKey")
}

func TestSelectObjectContent_NoBucket(t *testing.T) {
	ro := newTestRouter(t)

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "no-bucket", "data.csv", xmlBody)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "NoSuchBucket")
}

func TestSelectObjectContent_InvalidExpressionType(t *testing.T) {
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "data.csv", "a\n1\n")

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object",
		"XQuery",
		`<InputSerialization><CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "InvalidExpressionType")
}

func TestSelectObjectContent_BadSQL(t *testing.T) {
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "data.csv", "a\n1\n")

	xmlBody := selectXMLBody(
		"NOT VALID SQL",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "ParseUnexpectedToken")
}

func TestSelectObjectContent_ParquetNotSupported(t *testing.T) {
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "data.parquet", "binary")

	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<SelectObjectContentRequest>` +
		`<Expression>SELECT * FROM S3Object</Expression>` +
		`<ExpressionType>SQL</ExpressionType>` +
		`<InputSerialization><Parquet></Parquet></InputSerialization>` +
		`<OutputSerialization><CSV></CSV></OutputSerialization>` +
		`</SelectObjectContentRequest>`
	w := doSelect(t, ro, "my-bucket", "data.parquet", xmlBody)
	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestSelectObjectContent_AndOrWhere(t *testing.T) {
	ro := newTestRouter(t)
	csvData := "name,score,dept\nAlice,90,eng\nBob,75,mkt\nCarol,85,eng\nDave,60,eng\n"
	putSelectObject(t, ro, "my-bucket", "data.csv", csvData)

	xmlBody := selectXMLBody(
		"SELECT s.name FROM S3Object s WHERE s.dept = 'eng' AND CAST(s.score AS FLOAT) >= 85",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	got := buf.String()
	assert.Contains(t, got, "Alice")
	assert.NotContains(t, got, "Bob")
	assert.Contains(t, got, "Carol")
	assert.NotContains(t, got, "Dave")
}

func TestSelectObjectContent_LikePattern(t *testing.T) {
	ro := newTestRouter(t)
	csvData := "name\nAlice\nAlbert\nBob\n"
	putSelectObject(t, ro, "my-bucket", "names.csv", csvData)

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object WHERE name LIKE 'Al%'",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "names.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	got := buf.String()
	assert.Contains(t, got, "Alice")
	assert.Contains(t, got, "Albert")
	assert.NotContains(t, got, "Bob")
}

func TestSelectObjectContent_EmptyResult(t *testing.T) {
	ro := newTestRouter(t)
	csvData := "score\n10\n20\n"
	putSelectObject(t, ro, "my-bucket", "data.csv", csvData)

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object WHERE CAST(score AS FLOAT) > 999",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())

	// No Records events expected; Stats and End must still appear.
	kinds := make(map[string]int)
	for _, ev := range events {
		kinds[ev.kind]++
	}
	assert.Zero(t, kinds["Records"])
	assert.Equal(t, 1, kinds["Stats"])
	assert.Equal(t, 1, kinds["End"])
}

// --- Unit tests for event stream encoding ---

func TestWriteESMessage_CRC(t *testing.T) {
	var buf bytes.Buffer
	err := writeESMessage(&buf, [][2]string{
		{":message-type", "event"},
		{":event-type", "End"},
	}, nil)
	require.NoError(t, err)

	data := buf.Bytes()
	totalLen := binary.BigEndian.Uint32(data[0:4])
	dataLenU32 := uint32(len(data)) //nolint:gosec // test data is always < 4GB
	assert.Equal(t, dataLenU32, totalLen)

	preludeCRC := binary.BigEndian.Uint32(data[8:12])
	assert.Equal(t, crc32.ChecksumIEEE(data[0:8]), preludeCRC)

	msgCRC := binary.BigEndian.Uint32(data[totalLen-4 : totalLen])
	assert.Equal(t, crc32.ChecksumIEEE(data[:totalLen-4]), msgCRC)
}

// --- Unit tests for SQL parser ---

func TestParseSQL_SelectStar(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object")
	require.NoError(t, err)
	assert.Nil(t, q.columns)
	assert.False(t, q.countStar)
	assert.Empty(t, q.tableAlias)
	assert.Nil(t, q.where)
}

func TestParseSQL_WithAlias(t *testing.T) {
	q, err := parseSQL("SELECT s._1, s._2 FROM S3Object s WHERE s._3 > 100")
	require.NoError(t, err)
	assert.Equal(t, "s", q.tableAlias)
	require.Len(t, q.columns, 2)
	assert.Equal(t, "_1", q.columns[0].name)
	assert.Equal(t, "_2", q.columns[1].name)
	assert.NotNil(t, q.where)
}

func TestParseSQL_CountStar(t *testing.T) {
	q, err := parseSQL("SELECT COUNT(*) FROM S3Object")
	require.NoError(t, err)
	assert.True(t, q.countStar)
}

func TestParseSQL_Limit(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object LIMIT 10")
	require.NoError(t, err)
	assert.Equal(t, 10, q.limit)
}

func TestParseSQL_CastExpression(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE CAST(_1 AS FLOAT) > 5.5")
	require.NoError(t, err)
	assert.NotNil(t, q.where)
}

func TestParseSQL_IsNull(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE name IS NULL")
	require.NoError(t, err)
	assert.NotNil(t, q.where)
}

func TestParseSQL_IsNotNull(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE name IS NOT NULL")
	require.NoError(t, err)
	assert.NotNil(t, q.where)
}

func TestParseSQL_AndOr(t *testing.T) {
	_, err := parseSQL("SELECT * FROM S3Object WHERE a = '1' AND b = '2' OR c = '3'")
	require.NoError(t, err)
}

func TestParseSQL_Like(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE name LIKE 'A%'")
	require.NoError(t, err)
	assert.NotNil(t, q.where)
}

func TestParseSQL_NotLike(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE name NOT LIKE '%x%'")
	require.NoError(t, err)
	assert.NotNil(t, q.where)
}

func TestParseSQL_InvalidFrom(t *testing.T) {
	_, err := parseSQL("SELECT * FROM NotS3Object")
	assert.Error(t, err)
}

func TestParseSQL_MissingFrom(t *testing.T) {
	_, err := parseSQL("SELECT * S3Object")
	assert.Error(t, err)
}

// --- Unit tests for SQL execution ---

func makeRow(headers []string, vals map[string]string) selectRow {
	r := newSelectRow(headers)
	for k, v := range vals {
		r.vals[k] = v
	}
	return r
}

func TestExecute_SelectStar(t *testing.T) {
	q, _ := parseSQL("SELECT * FROM S3Object")
	rows := []selectRow{
		makeRow([]string{"a", "b"}, map[string]string{"a": "1", "b": "x"}),
		makeRow([]string{"a", "b"}, map[string]string{"a": "2", "b": "y"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestExecute_WhereNumeric(t *testing.T) {
	q, _ := parseSQL("SELECT * FROM S3Object WHERE _1 > 5")
	rows := []selectRow{
		makeRow([]string{"_1"}, map[string]string{"_1": "3"}),
		makeRow([]string{"_1"}, map[string]string{"_1": "7"}),
		makeRow([]string{"_1"}, map[string]string{"_1": "10"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "7", result[0].vals["_1"])
	assert.Equal(t, "10", result[1].vals["_1"])
}

func TestExecute_CountStar(t *testing.T) {
	q, _ := parseSQL("SELECT COUNT(*) FROM S3Object WHERE _1 > 5")
	rows := []selectRow{
		makeRow([]string{"_1"}, map[string]string{"_1": "3"}),
		makeRow([]string{"_1"}, map[string]string{"_1": "7"}),
		makeRow([]string{"_1"}, map[string]string{"_1": "10"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "2", result[0].vals["_1"])
}

func TestExecute_Limit(t *testing.T) {
	q, _ := parseSQL("SELECT * FROM S3Object LIMIT 2")
	rows := []selectRow{
		makeRow([]string{"_1"}, map[string]string{"_1": "a"}),
		makeRow([]string{"_1"}, map[string]string{"_1": "b"}),
		makeRow([]string{"_1"}, map[string]string{"_1": "c"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

// --- Unit tests for matchLike ---

func TestMatchLike(t *testing.T) {
	tests := []struct {
		s, pattern string
		want       bool
	}{
		{"hello", "hello", true},
		{"hello", "hel%", true},
		{"hello", "%ello", true},
		{"hello", "%ll%", true},
		{"hello", "hell_", true},
		{"hello", "hell__", false},
		{"hello", "H%", false},
		{"", "%", true},
		{"", "_%", false},
		{"abc", "a%c", true},
		{"ac", "a%c", true},
	}
	for _, tt := range tests {
		t.Run(tt.s+"/"+tt.pattern, func(t *testing.T) {
			assert.Equal(t, tt.want, matchLike(tt.s, tt.pattern))
		})
	}
}

// --- Unit tests for CSV parsing ---

func TestReadCSVRows_UseHeaders(t *testing.T) {
	csvData := "name,score\nAlice,90\nBob,75\n"
	rows, _, err := readCSVRows(strings.NewReader(csvData), &xmlCSVInput{FileHeaderInfo: "USE"})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"name", "score"}, rows[0].headers)
	assert.Equal(t, "Alice", rows[0].vals["name"])
	assert.Equal(t, "90", rows[0].vals["score"])
}

func TestReadCSVRows_NoneHeaders(t *testing.T) {
	csvData := "Alice,90\nBob,75\n"
	rows, _, err := readCSVRows(strings.NewReader(csvData), &xmlCSVInput{FileHeaderInfo: "NONE"})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"_1", "_2"}, rows[0].headers)
	assert.Equal(t, "Alice", rows[0].vals["_1"])
}

func TestReadCSVRows_IgnoreHeaders(t *testing.T) {
	csvData := "name,score\nAlice,90\nBob,75\n"
	rows, _, err := readCSVRows(strings.NewReader(csvData), &xmlCSVInput{FileHeaderInfo: "IGNORE"})
	require.NoError(t, err)
	// IGNORE: first row is header (skipped), data starts from row 2.
	require.Len(t, rows, 2)
	assert.Equal(t, "_1", rows[0].headers[0])
	assert.Equal(t, "Alice", rows[0].vals["_1"])
}

func TestReadCSVRows_CustomDelimiter(t *testing.T) {
	csvData := "Alice|90\nBob|75\n"
	rows, _, err := readCSVRows(strings.NewReader(csvData), &xmlCSVInput{
		FileHeaderInfo: "NONE",
		FieldDelimiter: "|",
	})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "Alice", rows[0].vals["_1"])
	assert.Equal(t, "90", rows[0].vals["_2"])
}

// --- Unit tests for JSON parsing ---

func TestReadJSONRows_Lines(t *testing.T) {
	jsonData := `{"name":"Alice","score":90}` + "\n" +
		`{"name":"Bob","score":75}` + "\n"
	rows, _, err := readJSONRows(strings.NewReader(jsonData), &xmlJSONInput{Type: "LINES"})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "Alice", rows[0].vals["name"])
	assert.Equal(t, "90", rows[0].vals["score"])
}

func TestReadJSONRows_Document_Array(t *testing.T) {
	jsonData := `[{"name":"Alice"},{"name":"Bob"}]`
	rows, _, err := readJSONRows(strings.NewReader(jsonData), &xmlJSONInput{Type: "DOCUMENT"})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "Alice", rows[0].vals["name"])
}

func TestSelectObjectContent_JSONOutput(t *testing.T) {
	ro := newTestRouter(t)
	csvData := "name,score\nAlice,90\nBob,75\n"
	putSelectObject(t, ro, "my-bucket", "data.csv", csvData)

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV></InputSerialization>`,
		jsonOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	got := buf.String()
	assert.Contains(t, got, "Alice")
	assert.Contains(t, got, "Bob")
}

func TestSelectObjectContent_MalformedXML(t *testing.T) {
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "data.csv", "a\n1\n")

	req := httptest.NewRequest(
		http.MethodPost,
		"/my-bucket/data.csv?select&select-type=2",
		strings.NewReader("NOT XML"),
	)
	w := httptest.NewRecorder()
	ro.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "MalformedXML")
}

func TestSelectObjectContent_MissingInputSerialization(t *testing.T) {
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "data.csv", "a\n1\n")

	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<SelectObjectContentRequest>` +
		`<Expression>SELECT * FROM S3Object</Expression>` +
		`<ExpressionType>SQL</ExpressionType>` +
		`<OutputSerialization><CSV></CSV></OutputSerialization>` +
		`</SelectObjectContentRequest>`
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "MissingRequiredParameter")
}

func TestSelectObjectContent_OrWhere(t *testing.T) {
	ro := newTestRouter(t)
	csvData := "name\nAlice\nBob\nCarol\n"
	putSelectObject(t, ro, "my-bucket", "data.csv", csvData)

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object WHERE name = 'Alice' OR name = 'Carol'",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	got := buf.String()
	assert.Contains(t, got, "Alice")
	assert.NotContains(t, got, "Bob")
	assert.Contains(t, got, "Carol")
}

func TestSelectObjectContent_NotWhere(t *testing.T) {
	ro := newTestRouter(t)
	csvData := "name\nAlice\nBob\n"
	putSelectObject(t, ro, "my-bucket", "data.csv", csvData)

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object WHERE NOT name = 'Bob'",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	got := buf.String()
	assert.Contains(t, got, "Alice")
	assert.NotContains(t, got, "Bob")
}

func TestSelectObjectContent_IsNull(t *testing.T) {
	ro := newTestRouter(t)
	// Row with fewer columns than the header row: the missing column is NULL.
	// Row "3,w" has only 2 columns so column "c" is absent (NULL).
	csvData := "a,b,c\n1,x,val\n2,y,other\n3,w\n"
	putSelectObject(t, ro, "my-bucket", "data.csv", csvData)

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object WHERE c IS NULL",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	got := buf.String()
	// Only row 3 (missing c column) should match.
	assert.Contains(t, got, "3")
	assert.NotContains(t, got, "1")
	assert.NotContains(t, got, "Alice")
}

func TestFormatCSVOutput_QuoteFieldsAlways(t *testing.T) {
	rows := []selectRow{
		makeRow([]string{"name"}, map[string]string{"name": "Alice"}),
	}
	cfg := &xmlCSVOutput{QuoteFields: "ALWAYS"}
	out, err := formatCSVOutput(rows, cfg)
	require.NoError(t, err)
	assert.Contains(t, string(out), `"Alice"`)
}

func TestFormatJSONOutput_Basic(t *testing.T) {
	rows := []selectRow{
		makeRow([]string{"name", "score"}, map[string]string{"name": "Alice", "score": "90"}),
	}
	out, err := formatJSONOutput(rows, &xmlJSONOutput{RecordDelimiter: "\n"})
	require.NoError(t, err)
	assert.Contains(t, string(out), "Alice")
	assert.Contains(t, string(out), "90")
}

func TestJSONValToString_Types(t *testing.T) {
	assert.Equal(t, "42", jsonValToString(42.0))
	assert.Equal(t, "true", jsonValToString(true))
	assert.Equal(t, "false", jsonValToString(false))
	assert.Equal(t, "", jsonValToString(nil))
	assert.Equal(t, "hello", jsonValToString("hello"))
}

func TestWriteESErrorEvent(t *testing.T) {
	var buf bytes.Buffer
	err := writeESErrorEvent(&buf, "TestCode", "test message")
	require.NoError(t, err)
	assert.NotEmpty(t, buf.Bytes())
	// Parse and verify headers contain error-code.
	data := buf.Bytes()
	headersLen := binary.BigEndian.Uint32(data[4:8])
	hBytes := data[12 : 12+headersLen]
	headers := parseESHeaders(hBytes)
	assert.Equal(t, "error", headers[":message-type"])
	assert.Equal(t, "TestCode", headers[":error-code"])
}

func TestReadJSONRows_Document_SingleObject(t *testing.T) {
	jsonData := `{"name":"Alice","score":90}`
	rows, _, err := readJSONRows(strings.NewReader(jsonData), &xmlJSONInput{Type: "DOCUMENT"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "Alice", rows[0].vals["name"])
}

func TestExecute_CastBranchesAndNull(t *testing.T) {
	tests := []struct {
		sql    string
		input  string
		expect string
	}{
		{
			"SELECT * FROM S3Object WHERE CAST(_1 AS INT) = 7",
			"_1\n7\n8\n",
			"7",
		},
		{
			"SELECT * FROM S3Object WHERE CAST(_1 AS BOOL) = 'true'",
			"_1\n1\n0\n",
			"1",
		},
		{
			"SELECT * FROM S3Object WHERE CAST(_1 AS STRING) = 'hello'",
			"_1\nhello\nworld\n",
			"hello",
		},
	}
	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			q, err := parseSQL(tt.sql)
			require.NoError(t, err)
			rows, _, err := readCSVRows(
				strings.NewReader(tt.input),
				&xmlCSVInput{FileHeaderInfo: "USE"},
			)
			require.NoError(t, err)
			result, err := q.execute(rows)
			require.NoError(t, err)
			require.Len(t, result, 1)
			assert.Equal(t, tt.expect, result[0].vals["_1"])
		})
	}
}

func TestExecute_NullLiteral(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE _1 = NULL")
	require.NoError(t, err)
	rows := []selectRow{makeRow([]string{"_1"}, map[string]string{"_1": "val"})}
	result, err := q.execute(rows)
	require.NoError(t, err)
	// NULL comparison is always false.
	assert.Empty(t, result)
}

// TestCompareValues exercises all operator branches.
func TestCompareValues(t *testing.T) {
	tests := []struct {
		left, op, right string
		want            bool
	}{
		{"5", "=", "5", true},
		{"5", "!=", "6", true},
		{"5", "<>", "6", true},
		{"5", "<", "10", true},
		{"5", "<=", "5", true},
		{"10", ">", "5", true},
		{"5", ">=", "5", true},
		{"abc", "=", "abc", true},
		{"abc", "<", "abd", true},
		{"abc", ">", "abb", true},
	}
	for _, tt := range tests {
		got, err := compareValues(tt.left, tt.op, tt.right)
		require.NoError(t, err)
		assert.Equal(t, tt.want, got, "%s %s %s", tt.left, tt.op, tt.right)
	}
}

// --- Additional handler coverage tests ---

func TestSelectObjectContent_MissingExpression(t *testing.T) {
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "data.csv", "a\n1\n")

	// Missing Expression field.
	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<SelectObjectContentRequest>` +
		`<ExpressionType>SQL</ExpressionType>` +
		`<InputSerialization><CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV></InputSerialization>` +
		`<OutputSerialization><CSV></CSV></OutputSerialization>` +
		`</SelectObjectContentRequest>`
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "MissingRequiredParameter")
}

func gzipBytes(t *testing.T, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, err := gw.Write([]byte(data))
	require.NoError(t, err)
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

func putSelectObjectBytes(t *testing.T, ro *Router, bucket, key string, data []byte) {
	t.Helper()
	ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/"+bucket, nil))
	req := httptest.NewRequest(http.MethodPut, "/"+bucket+"/"+key, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	ro.ServeHTTP(httptest.NewRecorder(), req)
}

func TestSelectObjectContent_GZIPInput(t *testing.T) {
	ro := newTestRouter(t)
	csvData := "name,score\nAlice,90\nBob,75\n"
	putSelectObjectBytes(t, ro, "my-bucket", "data.csv.gz", gzipBytes(t, csvData))

	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<SelectObjectContentRequest>` +
		`<Expression>SELECT * FROM S3Object</Expression>` +
		`<ExpressionType>SQL</ExpressionType>` +
		`<InputSerialization>` +
		`<CompressionType>GZIP</CompressionType>` +
		`<CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV>` +
		`</InputSerialization>` +
		`<OutputSerialization><CSV></CSV></OutputSerialization>` +
		`</SelectObjectContentRequest>`
	w := doSelect(t, ro, "my-bucket", "data.csv.gz", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	assert.Contains(t, buf.String(), "Alice")
	assert.Contains(t, buf.String(), "Bob")
}

func TestSelectObjectContent_GZIPInvalidInput(t *testing.T) {
	ro := newTestRouter(t)
	// Store plain (non-GZIP) bytes but request GZIP decompression.
	putSelectObject(t, ro, "my-bucket", "data.csv", "not gzip data")

	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<SelectObjectContentRequest>` +
		`<Expression>SELECT * FROM S3Object</Expression>` +
		`<ExpressionType>SQL</ExpressionType>` +
		`<InputSerialization>` +
		`<CompressionType>GZIP</CompressionType>` +
		`<CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV>` +
		`</InputSerialization>` +
		`<OutputSerialization><CSV></CSV></OutputSerialization>` +
		`</SelectObjectContentRequest>`
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "InvalidDataSource")
}

func TestSelectObjectContent_EmptyCSV(t *testing.T) {
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "empty.csv", "")

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "empty.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	kinds := make(map[string]int)
	for _, ev := range events {
		kinds[ev.kind]++
	}
	assert.Zero(t, kinds["Records"])
	assert.Equal(t, 1, kinds["End"])
}

func TestSelectObjectContent_CSVWithComments(t *testing.T) {
	ro := newTestRouter(t)
	// Lines starting with '#' should be skipped as comments.
	csvData := "# this is a comment\nname,score\nAlice,90\n# another comment\nBob,75\n"
	putSelectObject(t, ro, "my-bucket", "data.csv", csvData)

	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<SelectObjectContentRequest>` +
		`<Expression>SELECT * FROM S3Object</Expression>` +
		`<ExpressionType>SQL</ExpressionType>` +
		`<InputSerialization>` +
		`<CSV>` +
		`<FileHeaderInfo>USE</FileHeaderInfo>` +
		`<Comments>#</Comments>` +
		`</CSV>` +
		`</InputSerialization>` +
		`<OutputSerialization><CSV></CSV></OutputSerialization>` +
		`</SelectObjectContentRequest>`
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	require.Equal(t, http.StatusOK, w.Code)

	events := parseEventStream(t, w.Body.Bytes())
	var buf strings.Builder
	for _, ev := range events {
		if ev.kind == "Records" {
			buf.WriteString(ev.payload)
		}
	}
	got := buf.String()
	assert.Contains(t, got, "Alice")
	assert.Contains(t, got, "Bob")
}

// --- Additional SQL lexer/parser coverage ---

func TestParseSQL_NegativeNumber(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE _1 > -5")
	require.NoError(t, err)
	assert.NotNil(t, q.where)
}

func TestParseSQL_FloatLiteral(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE _1 >= 3.14")
	require.NoError(t, err)
	assert.NotNil(t, q.where)
}

func TestParseSQL_StringEscapedQuote(t *testing.T) {
	// SQL string with doubled single-quote escape: 'it''s'
	q, err := parseSQL("SELECT * FROM S3Object WHERE name = 'it''s'")
	require.NoError(t, err)
	rows := []selectRow{makeRow([]string{"name"}, map[string]string{"name": "it's"})}
	result, err := q.execute(rows)
	require.NoError(t, err)
	require.Len(t, result, 1)
}

func TestParseSQL_NEOperators(t *testing.T) {
	for _, op := range []string{"!=", "<>"} {
		t.Run(op, func(t *testing.T) {
			q, err := parseSQL("SELECT * FROM S3Object WHERE _1 " + op + " 'x'")
			require.NoError(t, err)
			rows := []selectRow{
				makeRow([]string{"_1"}, map[string]string{"_1": "x"}),
				makeRow([]string{"_1"}, map[string]string{"_1": "y"}),
			}
			result, err := q.execute(rows)
			require.NoError(t, err)
			assert.Len(t, result, 1)
			assert.Equal(t, "y", result[0].vals["_1"])
		})
	}
}

func TestParseSQL_LEGEOperators(t *testing.T) {
	q, err := parseSQL(
		"SELECT * FROM S3Object WHERE CAST(_1 AS FLOAT) <= 5 AND CAST(_2 AS FLOAT) >= 3",
	)
	require.NoError(t, err)
	rows := []selectRow{
		makeRow([]string{"_1", "_2"}, map[string]string{"_1": "4", "_2": "3"}),
		makeRow([]string{"_1", "_2"}, map[string]string{"_1": "6", "_2": "3"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestParseSQL_ParenthesisedCondition(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE (a = '1' OR a = '2') AND b = 'x'")
	require.NoError(t, err)
	rows := []selectRow{
		makeRow([]string{"a", "b"}, map[string]string{"a": "1", "b": "x"}),
		makeRow([]string{"a", "b"}, map[string]string{"a": "1", "b": "y"}),
		makeRow([]string{"a", "b"}, map[string]string{"a": "3", "b": "x"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "1", result[0].vals["a"])
}

func TestParseSQL_MultipleColumns(t *testing.T) {
	q, err := parseSQL("SELECT a, b, c FROM S3Object")
	require.NoError(t, err)
	require.Len(t, q.columns, 3)
	assert.Equal(t, "a", q.columns[0].name)
	assert.Equal(t, "c", q.columns[2].name)
}

func TestParseSQL_ScientificNotation(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE CAST(_1 AS FLOAT) > 1e2")
	require.NoError(t, err)
	rows := []selectRow{
		makeRow([]string{"_1"}, map[string]string{"_1": "50"}),
		makeRow([]string{"_1"}, map[string]string{"_1": "200"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "200", result[0].vals["_1"])
}

func TestMatchLike_Unicode(t *testing.T) {
	tests := []struct {
		s, pattern string
		want       bool
	}{
		{"café", "____", true}, // 4 runes, 4 underscores
		{"café", "___", false}, // 4 runes, 3 underscores
		{"中文", "__", true},     // 2 runes, 2 underscores
		{"中文", "_", false},     // 2 runes, 1 underscore
		{"café", "ca%", true},  // prefix wildcard
		{"café", "%é", true},   // suffix wildcard
		{"中文字", "_文_", true},   // middle wildcard
	}
	for _, tt := range tests {
		t.Run(tt.s+"/"+tt.pattern, func(t *testing.T) {
			assert.Equal(t, tt.want, matchLike(tt.s, tt.pattern))
		})
	}
}

// --- Additional select_data coverage ---

func TestReadCSVRows_EmptyFile(t *testing.T) {
	rows, _, err := readCSVRows(strings.NewReader(""), nil)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestReadCSVRows_CommentLines(t *testing.T) {
	csvData := "# comment\nname\nAlice\n"
	rows, _, err := readCSVRows(strings.NewReader(csvData), &xmlCSVInput{
		FileHeaderInfo: "USE",
		Comments:       "#",
	})
	require.NoError(t, err)
	// Header row is "name", data row is "Alice" (comment is skipped).
	require.Len(t, rows, 1)
	assert.Equal(t, "Alice", rows[0].vals["name"])
}

func TestReadJSONRows_EmptyInput(t *testing.T) {
	rows, _, err := readJSONRows(strings.NewReader(""), &xmlJSONInput{Type: "LINES"})
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestReadJSONRows_SingleObjectDocument(t *testing.T) {
	jsonData := `{"name":"Alice","score":90}`
	rows, _, err := readJSONRows(strings.NewReader(jsonData), &xmlJSONInput{Type: "DOCUMENT"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "Alice", rows[0].vals["name"])
}

func TestFormatJSONOutput_KeyOrder(t *testing.T) {
	// Keys should appear in headers order, not alphabetical order.
	rows := []selectRow{
		makeRow([]string{"z", "a", "m"}, map[string]string{"z": "1", "a": "2", "m": "3"}),
	}
	out, err := formatJSONOutput(rows, &xmlJSONOutput{RecordDelimiter: "\n"})
	require.NoError(t, err)
	s := string(out)
	zIdx := strings.Index(s, `"z"`)
	aIdx := strings.Index(s, `"a"`)
	mIdx := strings.Index(s, `"m"`)
	assert.Less(t, zIdx, aIdx, "z should appear before a (headers order, not alphabetical)")
	assert.Less(t, aIdx, mIdx, "a should appear before m")
}

// ---- errReader for io.ReadAll failure tests ----

type selectErrReader struct{}

func (e *selectErrReader) Read(_ []byte) (int, error) { return 0, errors.New("read error") }

func TestReadCSVRows_IOReadError(t *testing.T) {
	_, _, err := readCSVRows(&selectErrReader{}, nil)
	require.Error(t, err)
}

func TestReadJSONRows_IOReadError(t *testing.T) {
	_, _, err := readJSONRows(&selectErrReader{}, nil)
	require.Error(t, err)
}

func TestReadCSVRows_QuoteCharacterSet(t *testing.T) {
	rows, _, err := readCSVRows(strings.NewReader("name,score\nAlice,90\n"), &xmlCSVInput{
		FileHeaderInfo: "USE",
		QuoteCharacter: "|", // exercises the _ = q path
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "Alice", rows[0].vals["name"])
}

func TestReadCSVRows_VariableWidthRows(t *testing.T) {
	// Data row has more columns than header row → triggers headers slice growth
	rows, _, err := readCSVRows(strings.NewReader("a\n1,extra\n"), &xmlCSVInput{
		FileHeaderInfo: "USE",
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "1", rows[0].vals["a"])
	assert.Equal(t, "extra", rows[0].vals["_2"])
}

// ---- JSON parsing error paths ----

func TestJSONObjectToRow_EmptyInput(t *testing.T) {
	_, err := jsonObjectToRow([]byte{})
	require.Error(t, err)
}

func TestJSONObjectToRow_NonObjectToken(t *testing.T) {
	// First token is a string, not '{'
	_, err := jsonObjectToRow([]byte(`"hello"`))
	require.Error(t, err)
}

func TestJSONObjectToRow_ArrayToken(t *testing.T) {
	// First token is '[', not '{'
	_, err := jsonObjectToRow([]byte(`[1,2]`))
	require.Error(t, err)
}

func TestJSONObjectToRow_TruncatedKeyError(t *testing.T) {
	// After '{', the key string is unterminated → dec.Token() returns error
	_, err := jsonObjectToRow([]byte(`{"`))
	require.Error(t, err)
}

func TestJSONObjectToRow_TruncatedValue(t *testing.T) {
	// After the key, the value is missing → dec.Decode() returns error
	_, err := jsonObjectToRow([]byte(`{"key":`))
	require.Error(t, err)
}

func TestParseJSONLines_MalformedLine(t *testing.T) {
	// Second line is a JSON array, not an object
	_, err := parseJSONLines([]byte(`{"a":1}` + "\n" + `[1,2]` + "\n"))
	require.Error(t, err)
}

func TestParseJSONDocument_InvalidArrayJSON(t *testing.T) {
	_, err := parseJSONDocument([]byte(`[invalid]`))
	require.Error(t, err)
}

func TestParseJSONDocument_ArrayWithNonObjects(t *testing.T) {
	// Array elements are not objects
	_, err := parseJSONDocument([]byte(`[1, 2, 3]`))
	require.Error(t, err)
}

func TestParseJSONDocument_SingleNonObject(t *testing.T) {
	// Single JSON value that is not an object
	_, err := parseJSONDocument([]byte(`"hello"`))
	require.Error(t, err)
}

// ---- Handler: storage error and BZIP2 paths ----

func TestSelectObjectContent_StorageError(t *testing.T) {
	ro := newRouterWithMock(&mockStore{getObjectErr: errors.New("disk failure")})
	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object",
		"SQL",
		`<InputSerialization><CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV></InputSerialization>`,
		csvOutputFmt(),
	)
	req := httptest.NewRequest(
		http.MethodPost,
		"/my-bucket/data.csv?select&select-type=2",
		strings.NewReader(xmlBody),
	)
	w := httptest.NewRecorder()
	ro.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestSelectObjectContent_BZip2Path(t *testing.T) {
	// Non-BZIP2 data with CompressionType=BZIP2: bzip2.NewReader wraps the reader,
	// then io.ReadAll fails during decompression.
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "data.csv", "not bzip2 data")

	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<SelectObjectContentRequest>` +
		`<Expression>SELECT * FROM S3Object</Expression>` +
		`<ExpressionType>SQL</ExpressionType>` +
		`<InputSerialization>` +
		`<CompressionType>BZIP2</CompressionType>` +
		`<CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV>` +
		`</InputSerialization>` +
		`<OutputSerialization><CSV></CSV></OutputSerialization>` +
		`</SelectObjectContentRequest>`
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	// BZIP2 decompression of non-BZIP2 data fails → 400 InvalidDataType
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "InvalidDataType")
}

func TestSelectObjectContent_JSONParseError(t *testing.T) {
	// Malformed JSON input triggers readJSONRows error → handler lines 166-173
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "data.json", `[invalid json]`)

	xmlBody := selectXMLBody(
		"SELECT * FROM S3Object",
		"SQL",
		`<InputSerialization><JSON><Type>DOCUMENT</Type></JSON></InputSerialization>`,
		csvOutputFmt(),
	)
	w := doSelect(t, ro, "my-bucket", "data.json", xmlBody)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "InvalidDataType")
}

// ---- SQL AST eval coverage ----

func TestExecute_IsNotNull(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE col IS NOT NULL")
	require.NoError(t, err)
	rows := []selectRow{
		makeRow([]string{"col"}, map[string]string{"col": "val"}),
		makeRow([]string{"col"}, map[string]string{}), // missing col → NULL
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "val", result[0].vals["col"])
}

func TestExecute_LikeOnNullColumn(t *testing.T) {
	// LIKE applied to a missing (NULL) column → false
	q, err := parseSQL("SELECT * FROM S3Object WHERE missing LIKE 'x%'")
	require.NoError(t, err)
	rows := []selectRow{makeRow([]string{"name"}, map[string]string{"name": "Alice"})}
	result, err := q.execute(rows)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestExecute_NotLikeExecution(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE name NOT LIKE 'Al%'")
	require.NoError(t, err)
	rows := []selectRow{
		makeRow([]string{"name"}, map[string]string{"name": "Alice"}),
		makeRow([]string{"name"}, map[string]string{"name": "Bob"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "Bob", result[0].vals["name"])
}

func TestExecute_FloatLiteral(t *testing.T) {
	// Tests numLitNode.evalVal float path (non-integer value)
	q, err := parseSQL("SELECT * FROM S3Object WHERE _1 = 3.14")
	require.NoError(t, err)
	rows := []selectRow{
		makeRow([]string{"_1"}, map[string]string{"_1": "3.14"}),
		makeRow([]string{"_1"}, map[string]string{"_1": "2.71"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	require.Len(t, result, 1)
}

func TestExecute_CastNullColumn(t *testing.T) {
	// CAST of a missing column returns NULL, NULL > 5 is false
	q, err := parseSQL("SELECT * FROM S3Object WHERE CAST(missing AS INT) > 5")
	require.NoError(t, err)
	rows := []selectRow{makeRow([]string{"_1"}, map[string]string{"_1": "10"})}
	result, err := q.execute(rows)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestExecute_CastINTParseError(t *testing.T) {
	// CAST of a non-numeric value yields NULL; NULL > 0 is false
	q, err := parseSQL("SELECT * FROM S3Object WHERE CAST(_1 AS INT) > 0")
	require.NoError(t, err)
	rows := []selectRow{makeRow([]string{"_1"}, map[string]string{"_1": "abc"})}
	result, err := q.execute(rows)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestExecute_CastFLOATParseError(t *testing.T) {
	// CAST of a non-numeric value yields NULL; NULL = 0 is false
	q, err := parseSQL("SELECT * FROM S3Object WHERE CAST(_1 AS FLOAT) = 0")
	require.NoError(t, err)
	rows := []selectRow{makeRow([]string{"_1"}, map[string]string{"_1": "xyz"})}
	result, err := q.execute(rows)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestExecute_CastDefaultType(t *testing.T) {
	// CAST("abc" AS UNKNOWN_TYPE) passes through as-is
	q, err := parseSQL("SELECT * FROM S3Object WHERE CAST(_1 AS UNKNOWNTYPE) = 'abc'")
	require.NoError(t, err)
	rows := []selectRow{makeRow([]string{"_1"}, map[string]string{"_1": "abc"})}
	result, err := q.execute(rows)
	require.NoError(t, err)
	require.Len(t, result, 1)
}

func TestExecute_TrueFalseLiterals(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE flag = TRUE")
	require.NoError(t, err)
	rows := []selectRow{
		makeRow([]string{"flag"}, map[string]string{"flag": "true"}),
		makeRow([]string{"flag"}, map[string]string{"flag": "false"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	require.Len(t, result, 1)

	q2, err := parseSQL("SELECT * FROM S3Object WHERE flag = FALSE")
	require.NoError(t, err)
	result2, err := q2.execute(rows)
	require.NoError(t, err)
	require.Len(t, result2, 1)
}

// ---- SQL compareValues string <= >= paths ----

func TestCompareValues_StringLEGE(t *testing.T) {
	got, err := compareValues("abc", "<=", "abd")
	require.NoError(t, err)
	assert.True(t, got)

	got, err = compareValues("abc", ">=", "abc")
	require.NoError(t, err)
	assert.True(t, got)
}

// ---- matchLike edge case: % pattern exhausts without match ----

func TestMatchLike_PercentNoMatch(t *testing.T) {
	// '%xyz' with a string that doesn't end in 'xyz'
	assert.False(t, matchLike("abcdef", "%xyz"))
	assert.True(t, matchLike("abcxyz", "%xyz"))
}

// ---- SQL parser error path coverage ----

func TestParseSQL_Errors(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"no FROM", "SELECT * S3Object"},
		{"wrong table", "SELECT * FROM OtherTable"},
		{"WHERE parse error", "SELECT * FROM S3Object WHERE ="},
		{"LIMIT non-number", "SELECT * FROM S3Object LIMIT abc"},
		{"LIMIT float", "SELECT * FROM S3Object LIMIT 3.14"},
		{"LIMIT negative", "SELECT * FROM S3Object LIMIT -1"},
		{"trailing token", "SELECT * FROM S3Object s EXTRA_TOKEN"},
		{"COUNT no paren", "SELECT COUNT FROM S3Object"},
		{"COUNT no star", "SELECT COUNT(5) FROM S3Object"},
		{"COUNT no close", "SELECT COUNT(* FROM S3Object"},
		{"IS NULL missing NULL", "SELECT * FROM S3Object WHERE col IS SOMETHING"},
		{"LIKE non-string pattern", "SELECT * FROM S3Object WHERE col LIKE 123"},
		{"NOT without LIKE", "SELECT * FROM S3Object WHERE col NOT = 'x'"},
		{"no operator after value", "SELECT * FROM S3Object WHERE col"},
		{"CAST no paren", "SELECT * FROM S3Object WHERE CAST col AS INT = 1"},
		{"CAST no AS", "SELECT * FROM S3Object WHERE CAST(col INT) = 1"},
		{"CAST no type ident", "SELECT * FROM S3Object WHERE CAST(col AS 123) = 1"},
		{"CAST no close paren", "SELECT * FROM S3Object WHERE CAST(col AS INT = 1"},
		{"unexpected token in value", "SELECT * FROM S3Object WHERE = 5"},
		{"col ref after dot non-ident", "SELECT s.123 FROM S3Object s"},
		{"select col ref error", "SELECT 123 FROM S3Object"},
		{"unclosed paren", "SELECT * FROM S3Object WHERE (col = 'x'"},
		{"paren inner error", "SELECT * FROM S3Object WHERE (=)"},
		{"OR right error", "SELECT * FROM S3Object WHERE a = 'x' OR ="},
		{"AND right error", "SELECT * FROM S3Object WHERE a = 'x' AND ="},
		{"NOT inner error", "SELECT * FROM S3Object WHERE NOT ="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSQL(tc.sql)
			assert.Error(t, err, "expected parse error for: %s", tc.sql)
		})
	}
}

func TestParseSQL_SelectWithAlias(t *testing.T) {
	// SELECT col AS alias FROM S3Object — exercises AS alias path in parseSelectList
	q, err := parseSQL("SELECT name AS n, score AS s FROM S3Object")
	require.NoError(t, err)
	require.Len(t, q.columns, 2)
	assert.Equal(t, "name", q.columns[0].name)
	assert.Equal(t, "score", q.columns[1].name)
}

func TestParseSQL_ColRefAfterDotError(t *testing.T) {
	// "SELECT s.123" — dot followed by non-identifier
	_, err := parseSQL("SELECT s.123 FROM S3Object s")
	assert.Error(t, err)
}

func TestParseSQL_ColRefInSelectError(t *testing.T) {
	// Number as column reference is not valid
	_, err := parseSQL("SELECT 123 FROM S3Object")
	assert.Error(t, err)
}

func TestScanNumber_ScientificWithSign(t *testing.T) {
	// "1e+2" — scientific notation with explicit '+' sign exercises scanNumber lines 396-398
	q, err := parseSQL("SELECT * FROM S3Object WHERE _1 > 1e+2")
	require.NoError(t, err)
	rows := []selectRow{
		makeRow([]string{"_1"}, map[string]string{"_1": "50"}),
		makeRow([]string{"_1"}, map[string]string{"_1": "200"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "200", result[0].vals["_1"])
}

func TestScan_UnrecognisedChar(t *testing.T) {
	// '@' is not a valid SQL token → scan returns sqlTokIllegal → parse error
	_, err := parseSQL("SELECT * FROM S3Object WHERE _1 @ 5")
	assert.Error(t, err)
}

func TestParseSQL_GTOperatorAlone(t *testing.T) {
	// '>' without '=' → exercises case sqlTokGT in parseOp
	q, err := parseSQL("SELECT * FROM S3Object WHERE _1 > 3")
	require.NoError(t, err)
	rows := []selectRow{
		makeRow([]string{"_1"}, map[string]string{"_1": "2"}),
		makeRow([]string{"_1"}, map[string]string{"_1": "5"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	assert.Len(t, result, 1)
}

// ---- Targeted coverage for remaining uncovered lines ----

// select_data.go:138-139 — blank lines in NDJSON are skipped
func TestParseJSONLines_BlankLines(t *testing.T) {
	rows, err := parseJSONLines([]byte("\n" + `{"a":"1"}` + "\n\n" + `{"a":"2"}` + "\n"))
	require.NoError(t, err)
	require.Len(t, rows, 2)
}

// select_data.go:152-154 — empty input to parseJSONDocument
func TestParseJSONDocument_Empty(t *testing.T) {
	rows, err := parseJSONDocument([]byte{})
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// select_data.go:202-204 — JSON object with non-string key token
func TestJSONObjectToRow_NonStringKey(t *testing.T) {
	// Go's json.Decoder.Token() returns bool(true) for "true" at key position;
	// the string type-assertion then fails.
	_, err := jsonObjectToRow([]byte(`{true: "val"}`))
	require.Error(t, err)
}

// select_data.go:246-248 — custom field delimiter in formatCSVOutput
func TestFormatCSVOutput_CustomDelimiter(t *testing.T) {
	rows := []selectRow{makeRow([]string{"a", "b"}, map[string]string{"a": "1", "b": "2"})}
	out, err := formatCSVOutput(rows, &xmlCSVOutput{FieldDelimiter: "|"})
	require.NoError(t, err)
	assert.Contains(t, string(out), "1|2")
}

// select_sql.go:73-75 — orNode error propagation from left child
type errWhereNode struct{}

func (n *errWhereNode) evalRow(_ selectRow) (bool, error) {
	return false, errors.New("forced eval error")
}

func TestOrNodeEvalError(t *testing.T) {
	n := &orNode{left: &errWhereNode{}, right: &notNode{inner: &errWhereNode{}}}
	_, err := n.evalRow(selectRow{})
	require.Error(t, err)
}

// select_sql.go:181 — compareValues with unknown operator
func TestCompareValues_UnknownOperator(t *testing.T) {
	_, err := compareValues("a", "??", "b")
	require.Error(t, err)
}

// select_sql.go:346 + 667-668 — lone '<' token in lexer and parseOp
func TestParseSQL_LTOperator(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object WHERE _1 < 5")
	require.NoError(t, err)
	rows := []selectRow{
		makeRow([]string{"_1"}, map[string]string{"_1": "3"}),
		makeRow([]string{"_1"}, map[string]string{"_1": "7"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "3", result[0].vals["_1"])
}

// select_sql.go:654-656 — right-side value expr error in comparison
func TestParseSQL_ComparisonRightError(t *testing.T) {
	// After '=', the token '@' is unrecognised → scan returns sqlTokIllegal → parseValExpr errors
	_, err := parseSQL("SELECT * FROM S3Object WHERE _1 = @")
	require.Error(t, err)
}

// select_sql.go:688-690 — CAST inner expression error
func TestParseSQL_CastInnerError(t *testing.T) {
	_, err := parseSQL("SELECT * FROM S3Object WHERE CAST(@ AS INT) = 1")
	require.Error(t, err)
}

// select_sql.go:712-714 — multi-dot "number" fails ParseFloat
func TestParseSQL_InvalidNumericLiteral(t *testing.T) {
	// Lexer scans "1.2.3" as one token; ParseFloat("1.2.3") fails
	_, err := parseSQL("SELECT * FROM S3Object WHERE _1 = 1.2.3")
	require.Error(t, err)
}

// select_sql.go:735-737 — dot followed by non-identifier in parseValExpr (value position)
func TestParseSQL_ValExprDotNonIdent(t *testing.T) {
	// Right-hand side "s.123": '123' is sqlTokNumber, not sqlTokIdent
	_, err := parseSQL("SELECT * FROM S3Object WHERE _1 = s.123")
	require.Error(t, err)
}

func TestExecute_LimitZero(t *testing.T) {
	q, err := parseSQL("SELECT * FROM S3Object LIMIT 0")
	require.NoError(t, err)
	rows := []selectRow{
		makeRow([]string{"_1"}, map[string]string{"_1": "a"}),
		makeRow([]string{"_1"}, map[string]string{"_1": "b"}),
	}
	result, err := q.execute(rows)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestSelectObjectContent_UnknownCompressionType(t *testing.T) {
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "data.csv", "a\n1\n")

	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<SelectObjectContentRequest>` +
		`<Expression>SELECT * FROM S3Object</Expression>` +
		`<ExpressionType>SQL</ExpressionType>` +
		`<InputSerialization>` +
		`<CompressionType>SNAPPY</CompressionType>` +
		`<CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV>` +
		`</InputSerialization>` +
		`<OutputSerialization><CSV></CSV></OutputSerialization>` +
		`</SelectObjectContentRequest>`
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "InvalidArgument")
}

func TestSelectObjectContent_MultipleInputSerialization(t *testing.T) {
	ro := newTestRouter(t)
	putSelectObject(t, ro, "my-bucket", "data.csv", "a\n1\n")

	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<SelectObjectContentRequest>` +
		`<Expression>SELECT * FROM S3Object</Expression>` +
		`<ExpressionType>SQL</ExpressionType>` +
		`<InputSerialization>` +
		`<CSV><FileHeaderInfo>NONE</FileHeaderInfo></CSV>` +
		`<JSON><Type>LINES</Type></JSON>` +
		`</InputSerialization>` +
		`<OutputSerialization><CSV></CSV></OutputSerialization>` +
		`</SelectObjectContentRequest>`
	w := doSelect(t, ro, "my-bucket", "data.csv", xmlBody)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "MissingRequiredParameter")
}

func TestExecute_WrongTableAlias(t *testing.T) {
	// Column ref with wrong table alias resolves to NULL → comparison fails → no rows
	q, err := parseSQL("SELECT * FROM S3Object s WHERE t.col = 'x'")
	require.NoError(t, err)
	rows := []selectRow{makeRow([]string{"col"}, map[string]string{"col": "x"})}
	result, err := q.execute(rows)
	require.NoError(t, err)
	assert.Empty(t, result)
}
