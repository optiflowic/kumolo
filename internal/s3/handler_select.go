package s3

import (
	"compress/bzip2"
	"compress/gzip"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

// xmlSelectRequest is the parsed body of a SelectObjectContent request.
type xmlSelectRequest struct {
	XMLName             xml.Name                     `xml:"SelectObjectContentRequest"`
	Expression          string                       `xml:"Expression"`
	ExpressionType      string                       `xml:"ExpressionType"`
	InputSerialization  xmlSelectInputSerialization  `xml:"InputSerialization"`
	OutputSerialization xmlSelectOutputSerialization `xml:"OutputSerialization"`
}

type xmlSelectInputSerialization struct {
	CompressionType string        `xml:"CompressionType"`
	CSV             *xmlCSVInput  `xml:"CSV"`
	JSON            *xmlJSONInput `xml:"JSON"`
	Parquet         *struct{}     `xml:"Parquet"`
}

type xmlCSVInput struct {
	FileHeaderInfo             string `xml:"FileHeaderInfo"`
	RecordDelimiter            string `xml:"RecordDelimiter"`
	FieldDelimiter             string `xml:"FieldDelimiter"`
	QuoteCharacter             string `xml:"QuoteCharacter"`
	QuoteEscapeCharacter       string `xml:"QuoteEscapeCharacter"`
	Comments                   string `xml:"Comments"`
	AllowQuotedRecordDelimiter bool   `xml:"AllowQuotedRecordDelimiter"`
}

type xmlJSONInput struct {
	Type string `xml:"Type"`
}

type xmlSelectOutputSerialization struct {
	CSV  *xmlCSVOutput  `xml:"CSV"`
	JSON *xmlJSONOutput `xml:"JSON"`
}

type xmlCSVOutput struct {
	RecordDelimiter      string `xml:"RecordDelimiter"`
	FieldDelimiter       string `xml:"FieldDelimiter"`
	QuoteCharacter       string `xml:"QuoteCharacter"`
	QuoteEscapeCharacter string `xml:"QuoteEscapeCharacter"`
	QuoteFields          string `xml:"QuoteFields"`
}

type xmlJSONOutput struct {
	RecordDelimiter string `xml:"RecordDelimiter"`
}

func (ro *Router) handleSelectObjectContent(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	var req xmlSelectRequest
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Debug( // #nosec G706 -- bucket/key from URL path; log injection accepted for local dev emulator
			"malformed SelectObjectContentRequest XML",
			"bucket",
			bucket,
			"key",
			key,
		)
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}

	if req.Expression == "" || req.ExpressionType == "" {
		writeError(w, r, http.StatusBadRequest, "MissingRequiredParameter",
			"SelectObjectContentRequest is missing Expression or ExpressionType.")
		return
	}

	hasInput := req.InputSerialization.CSV != nil ||
		req.InputSerialization.JSON != nil ||
		req.InputSerialization.Parquet != nil
	hasOutput := req.OutputSerialization.CSV != nil || req.OutputSerialization.JSON != nil
	if !hasInput || !hasOutput {
		writeError(w, r, http.StatusBadRequest, "MissingRequiredParameter",
			"SelectObjectContentRequest is missing InputSerialization or OutputSerialization.")
		return
	}

	if req.ExpressionType != "SQL" {
		writeError(w, r, http.StatusBadRequest, "InvalidExpressionType",
			"The ExpressionType is invalid. Only SQL expressions are supported.")
		return
	}

	if req.InputSerialization.Parquet != nil {
		writeError(w, r, http.StatusNotImplemented, "NotImplemented",
			"Parquet InputSerialization is not supported.")
		return
	}

	f, _, err := ro.storage.GetObject(bucket, key)
	if err != nil {
		var dme *DeleteMarkerError
		switch {
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug( // #nosec G706
				"bucket not found", "bucket", bucket, "key", key,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		case errors.As(err, &dme), errors.Is(err, ErrObjectNotFound):
			slog.Debug( // #nosec G706
				"object not found", "bucket", bucket, "key", key,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchKey",
				"The specified key does not exist.")
		default:
			slog.Error( // #nosec G706
				"failed to get object for SelectObjectContent",
				"bucket", bucket, "key", key, "err", err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	defer f.Close() //nolint:errcheck

	fi, statErr := f.Stat()
	var rawSize int64
	if statErr == nil {
		rawSize = fi.Size()
	}

	var reader io.Reader = f
	switch req.InputSerialization.CompressionType {
	case "GZIP":
		gr, err := gzip.NewReader(f)
		if err != nil {
			slog.Error( // #nosec G706
				"failed to create gzip reader", "bucket", bucket, "key", key, "err", err,
			)
			writeError(w, r, http.StatusBadRequest, "InvalidDataSource",
				"Object is not a valid GZIP file.")
			return
		}
		defer gr.Close() //nolint:errcheck
		reader = gr
	case "BZIP2":
		reader = bzip2.NewReader(f)
	}

	var rows []selectRow
	var bytesProcessed int64

	if req.InputSerialization.CSV != nil {
		rows, bytesProcessed, err = readCSVRows(reader, req.InputSerialization.CSV)
	} else {
		rows, bytesProcessed, err = readJSONRows(reader, req.InputSerialization.JSON)
	}
	if err != nil {
		slog.Error( // #nosec G706
			"failed to parse select input", "bucket", bucket, "key", key, "err", err,
		)
		writeError(w, r, http.StatusBadRequest, "InvalidDataType",
			"Failed to parse object data: "+err.Error())
		return
	}

	if rawSize == 0 {
		rawSize = bytesProcessed
	}

	query, err := parseSQL(req.Expression)
	if err != nil {
		slog.Debug( // #nosec G706
			"SQL parse error", "bucket", bucket, "key", key, "err", err,
		)
		writeError(w, r, http.StatusBadRequest, "ParseUnexpectedToken",
			"SQL parse error: "+err.Error())
		return
	}

	resultRows, err := query.execute(rows)
	if err != nil {
		slog.Error( // #nosec G706
			"SQL execution error", "bucket", bucket, "key", key, "err", err,
		)
		writeError(w, r, http.StatusBadRequest, "InvalidDataType",
			"SQL execution error: "+err.Error())
		return
	}

	var payload []byte
	if req.OutputSerialization.CSV != nil {
		payload, err = formatCSVOutput(resultRows, req.OutputSerialization.CSV)
	} else {
		payload, err = formatJSONOutput(resultRows, req.OutputSerialization.JSON)
	}
	if err != nil {
		slog.Error( // #nosec G706
			"failed to format select output", "bucket", bucket, "key", key, "err", err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	bytesReturned := int64(len(payload))

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)

	if len(payload) > 0 {
		if err := writeRecordsEvent(w, payload); err != nil {
			slog.Error("failed to write SelectObjectContent Records event", "err", err)
			return
		}
	}
	if err := writeStatsEvent(w, rawSize, bytesProcessed, bytesReturned); err != nil {
		slog.Error("failed to write SelectObjectContent Stats event", "err", err)
		return
	}
	if err := writeEndEvent(w); err != nil {
		slog.Error("failed to write SelectObjectContent End event", "err", err)
		return
	}

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	slog.Info( // #nosec G706
		"SelectObjectContent",
		"bucket", bucket, "key", key,
		"result_rows", len(resultRows), "bytes_returned", bytesReturned,
	)
}
