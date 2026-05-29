package dynamodb

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
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
	target := r.Header.Get("X-Amz-Target")
	op := strings.TrimPrefix(target, "DynamoDBStreams_20120810.")

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
		slog.Debug( // #nosec G706 -- target comes from the X-Amz-Target header; log injection risk accepted for a local dev emulator
			"DynamoDB Streams operation not implemented",
			"target",
			target,
		)
		writeError(
			w,
			http.StatusNotImplemented,
			streamsErrPrefix+"NotImplemented",
			"Operation not implemented: "+op,
		)
	}
}
