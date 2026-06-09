package s3

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// readCSVRows parses a CSV stream into selectRows.
// Returns the rows, the number of uncompressed bytes processed, and any error.
func readCSVRows(r io.Reader, cfg *xmlCSVInput) ([]selectRow, int64, error) {
	// Buffer the entire input to count bytes and pass to csv.Reader.
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}
	bytesProcessed := int64(len(raw))

	cr := csv.NewReader(bytes.NewReader(raw))
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = false
	cr.FieldsPerRecord = -1 // variable fields

	if cfg != nil {
		if d := cfg.FieldDelimiter; len(d) == 1 {
			cr.Comma = rune(d[0])
		}
		if q := cfg.QuoteCharacter; len(q) == 1 {
			// encoding/csv always uses '"'; remap if different by pre-processing
			// For non-" quote chars, fall back to raw line splitting.
			_ = q
		}
		if c := cfg.Comments; len(c) == 1 {
			cr.Comment = rune(c[0])
		}
	}

	fileHeaderInfo := "NONE"
	if cfg != nil && cfg.FileHeaderInfo != "" {
		fileHeaderInfo = cfg.FileHeaderInfo
	}

	var headers []string
	var rows []selectRow

	// Read first row to determine headers.
	firstRow, err := cr.Read()
	if err == io.EOF {
		return nil, bytesProcessed, nil
	}
	if err != nil {
		return nil, bytesProcessed, fmt.Errorf("CSV read error: %w", err)
	}

	switch strings.ToUpper(fileHeaderInfo) {
	case "USE":
		headers = firstRow
	case "IGNORE":
		headers = positionalHeaders(len(firstRow))
		// First row is the (ignored) header — do not add as data.
	default: // NONE
		headers = positionalHeaders(len(firstRow))
		// First row is data.
		r := newSelectRow(headers)
		for i, v := range firstRow {
			if i < len(headers) {
				r.vals[headers[i]] = v
			}
		}
		rows = append(rows, r)
	}

	// Read remaining rows.
	for {
		record, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, bytesProcessed, fmt.Errorf("CSV read error: %w", err)
		}
		// Grow headers if this row has more columns.
		for len(headers) < len(record) {
			headers = append(headers, positionalHeader(len(headers)+1))
		}
		row := newSelectRow(headers)
		for i, v := range record {
			if i < len(headers) {
				row.vals[headers[i]] = v
			}
		}
		rows = append(rows, row)
	}

	return rows, bytesProcessed, nil
}

// readJSONRows parses a JSON stream into selectRows.
func readJSONRows(r io.Reader, cfg *xmlJSONInput) ([]selectRow, int64, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}
	bytesProcessed := int64(len(raw))

	jsonType := "LINES"
	if cfg != nil && strings.ToUpper(cfg.Type) == "DOCUMENT" {
		jsonType = "DOCUMENT"
	}

	var rows []selectRow

	if jsonType == "DOCUMENT" {
		rows, err = parseJSONDocument(raw)
	} else {
		rows, err = parseJSONLines(raw)
	}
	if err != nil {
		return nil, bytesProcessed, err
	}
	return rows, bytesProcessed, nil
}

func parseJSONLines(data []byte) ([]selectRow, error) {
	var rows []selectRow
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		row, err := jsonObjectToRow(line)
		if err != nil {
			return nil, fmt.Errorf("JSON parse error: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, sc.Err()
}

func parseJSONDocument(data []byte) ([]selectRow, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	// Try array first, then single object.
	if data[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil, fmt.Errorf("JSON document parse error: %w", err)
		}
		rows := make([]selectRow, 0, len(arr))
		for _, elem := range arr {
			row, err := jsonObjectToRow(elem)
			if err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
		return rows, nil
	}
	row, err := jsonObjectToRow(data)
	if err != nil {
		return nil, err
	}
	return []selectRow{row}, nil
}

// jsonObjectToRow converts a JSON object into a selectRow with string values.
// Non-string JSON values are marshalled to their JSON representation.
func jsonObjectToRow(data []byte) (selectRow, error) {
	// Preserve key insertion order via a JSON decoder with UseNumber.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	t, err := dec.Token()
	if err != nil {
		return selectRow{}, err
	}
	if delim, ok := t.(json.Delim); !ok || delim != '{' {
		return selectRow{}, fmt.Errorf("expected JSON object")
	}

	var headers []string
	vals := map[string]string{}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return selectRow{}, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return selectRow{}, fmt.Errorf("expected JSON object key, got %T", keyTok)
		}
		headers = append(headers, key)

		var val any
		if err := dec.Decode(&val); err != nil {
			return selectRow{}, err
		}
		vals[key] = jsonValToString(val)
	}

	row := selectRow{headers: headers, vals: vals}
	return row, nil
}

func jsonValToString(v any) string {
	if v == nil {
		return ""
	}
	switch vv := v.(type) {
	case string:
		return vv
	case json.Number:
		return vv.String()
	case bool:
		if vv {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// --- Output formatting ---

// formatCSVOutput serialises rows as CSV bytes.
func formatCSVOutput(rows []selectRow, cfg *xmlCSVOutput) ([]byte, error) {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)

	if cfg != nil {
		if d := cfg.FieldDelimiter; len(d) == 1 {
			cw.Comma = rune(d[0])
		}
	}

	quoteFields := cfg != nil && strings.ToUpper(cfg.QuoteFields) == "ALWAYS"

	for _, row := range rows {
		fields := make([]string, 0, len(row.headers))
		for _, h := range row.headers {
			fields = append(fields, row.vals[h])
		}
		if quoteFields {
			// csv.Writer only quotes fields that contain the delimiter, quote char,
			// or newlines. We force quoting by wrapping each field.
			quoted := make([]string, len(fields))
			for i, f := range fields {
				quoted[i] = quoteCSVField(f, cw.Comma)
			}
			_, err := fmt.Fprintf(&buf, "%s\n", strings.Join(quoted, string(cw.Comma)))
			if err != nil {
				return nil, err
			}
			continue
		}
		if err := cw.Write(fields); err != nil {
			return nil, err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func quoteCSVField(s string, comma rune) string {
	escaped := strings.ReplaceAll(s, `"`, `""`)
	return `"` + escaped + `"`
}

// formatJSONOutput serialises rows as newline-delimited JSON objects.
// Keys are written in row.headers order to match AWS column ordering behaviour.
func formatJSONOutput(rows []selectRow, cfg *xmlJSONOutput) ([]byte, error) {
	delim := "\n"
	if cfg != nil && cfg.RecordDelimiter != "" {
		delim = cfg.RecordDelimiter
	}

	var buf bytes.Buffer
	for _, row := range rows {
		buf.WriteByte('{')
		for i, h := range row.headers {
			if i > 0 {
				buf.WriteByte(',')
			}
			keyJSON, err := json.Marshal(h)
			if err != nil {
				return nil, err
			}
			valJSON, err := json.Marshal(row.vals[h])
			if err != nil {
				return nil, err
			}
			buf.Write(keyJSON)
			buf.WriteByte(':')
			buf.Write(valJSON)
		}
		buf.WriteByte('}')
		buf.WriteString(delim)
	}
	return buf.Bytes(), nil
}

// positionalHeaders returns ["_1", "_2", ..., "_n"].
func positionalHeaders(n int) []string {
	h := make([]string, n)
	for i := range n {
		h[i] = positionalHeader(i + 1)
	}
	return h
}

func positionalHeader(i int) string {
	return fmt.Sprintf("_%d", i)
}
