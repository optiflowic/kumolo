package dynamodb

import (
	"io"
	"net/http"
	"strings"
	"time"
)

const streamsErrPrefix = "com.amazonaws.dynamodb.v20120810#"

// StreamsRouter handles DynamoDB Streams API requests dispatched via the X-Amz-Target header.
type StreamsRouter struct {
	storage streamStore
}

func NewStreamsRouter(storage *Storage) *StreamsRouter {
	return &StreamsRouter{storage: storage}
}

func (sr *StreamsRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := newResponseRecorder(w)
	start := time.Now()
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "DynamoDBStreams_20120810.")
	sr.serveHTTP(rec, r, op)
	emitRequestLog(op, rec, time.Since(start))
}

func (sr *StreamsRouter) serveHTTP(w http.ResponseWriter, r *http.Request, op string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// unreachable: io.ReadAll on an in-process httptest request body never errors.
		writeError(
			w,
			http.StatusBadRequest,
			streamsErrPrefix+"ValidationException",
			"failed to read request body",
		)
		return
	}

	switch op {
	case "ListStreams":
		sr.handleListStreams(w, body)
	case "DescribeStream":
		sr.handleDescribeStream(w, body)
	case "GetShardIterator":
		sr.handleGetShardIterator(w, body)
	case "GetRecords":
		sr.handleGetRecords(w, body)
	default:
		writeError(
			w,
			http.StatusNotImplemented,
			streamsErrPrefix+"NotImplemented",
			"Operation not implemented: "+op,
		)
	}
}
