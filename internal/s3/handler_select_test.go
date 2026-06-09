package s3

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
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
		dataLen := len(data)
		if uint32(dataLen) < totalLen { //nolint:gosec // test payloads are always < 4GB
			t.Fatalf("truncated event stream: have %d bytes, need %d", len(data), totalLen)
		}
		headersLen := binary.BigEndian.Uint32(data[4:8])
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

		payloadStart := 12 + headersLen
		payloadEnd := totalLen - 4
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
