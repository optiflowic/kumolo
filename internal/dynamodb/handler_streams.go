package dynamodb

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

func (sr *StreamsRouter) handleListStreams(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName               string `json:"TableName"`
		Limit                   *int   `json:"Limit"`
		ExclusiveStartStreamArn string `json:"ExclusiveStartStreamArn"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			streamsErrPrefix+"ValidationException",
			"invalid request body",
		)
		return
	}

	limit := 100
	if req.Limit != nil {
		if *req.Limit < 1 {
			writeError(
				w,
				http.StatusBadRequest,
				streamsErrPrefix+"ValidationException",
				"Limit must be >= 1",
			)
			return
		}
		if *req.Limit < limit {
			limit = *req.Limit
		}
	}

	entries, err := sr.storage.ListStreamARNs(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			writeError(w, http.StatusBadRequest,
				streamsErrPrefix+"ResourceNotFoundException",
				"Requested resource not found")
			return
		}
		slog.Error("ListStreams failed", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			streamsErrPrefix+"InternalServerError",
			"internal server error",
		)
		return
	}

	// Apply ExclusiveStartStreamArn cursor.
	if req.ExclusiveStartStreamArn != "" {
		start := len(entries)
		for i, e := range entries {
			if e.StreamARN == req.ExclusiveStartStreamArn {
				start = i + 1
				break
			}
		}
		entries = entries[start:]
	}

	type streamItem struct {
		StreamArn   string `json:"StreamArn"`
		StreamLabel string `json:"StreamLabel"`
		TableName   string `json:"TableName"`
	}
	resp := map[string]any{}
	if len(entries) > limit {
		resp["LastEvaluatedStreamArn"] = entries[limit-1].StreamARN
		entries = entries[:limit]
	}
	items := make([]streamItem, len(entries))
	for i, e := range entries {
		items[i] = streamItem{
			StreamArn:   e.StreamARN,
			StreamLabel: e.StreamLabel,
			TableName:   e.TableName,
		}
	}
	resp["Streams"] = items
	slog.Debug("listed DynamoDB streams", "count", len(items))
	writeJSON(w, http.StatusOK, resp)
}

func (sr *StreamsRouter) handleDescribeStream(w http.ResponseWriter, body []byte) {
	var req struct {
		StreamArn             string `json:"StreamArn"`
		ExclusiveStartShardId string `json:"ExclusiveStartShardId"`
		Limit                 *int   `json:"Limit"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			streamsErrPrefix+"ValidationException",
			"invalid request body",
		)
		return
	}
	if req.StreamArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			streamsErrPrefix+"ValidationException",
			"StreamArn is required",
		)
		return
	}
	if req.Limit != nil && *req.Limit < 1 {
		writeError(w, http.StatusBadRequest, streamsErrPrefix+"ValidationException",
			"Limit must be >= 1")
		return
	}

	desc, err := sr.storage.DescribeStream(req.StreamArn)
	if err != nil {
		if errors.Is(err, ErrStreamNotFound) {
			slog.Debug("DescribeStream: stream not found", "arn", req.StreamArn)
			writeError(
				w,
				http.StatusBadRequest,
				streamsErrPrefix+"ResourceNotFoundException",
				"Requested resource not found",
			)
			return
		}
		slog.Error("DescribeStream failed", "arn", req.StreamArn, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			streamsErrPrefix+"InternalServerError",
			"internal server error",
		)
		return
	}

	type seqRange struct {
		StartingSequenceNumber string `json:"StartingSequenceNumber,omitempty"`
	}
	type shardDesc struct {
		ShardId             string   `json:"ShardId"`
		SequenceNumberRange seqRange `json:"SequenceNumberRange"`
	}
	type keySchemaElem struct {
		AttributeName string `json:"AttributeName"`
		KeyType       string `json:"KeyType"`
	}
	type streamDescResp struct {
		StreamArn               string          `json:"StreamArn"`
		StreamLabel             string          `json:"StreamLabel"`
		StreamStatus            string          `json:"StreamStatus"`
		StreamViewType          string          `json:"StreamViewType"`
		TableName               string          `json:"TableName"`
		KeySchema               []keySchemaElem `json:"KeySchema"`
		CreationRequestDateTime float64         `json:"CreationRequestDateTime"`
		Shards                  []shardDesc     `json:"Shards"`
	}

	ks := make([]keySchemaElem, len(desc.KeySchema))
	for i, k := range desc.KeySchema {
		ks[i] = keySchemaElem(k)
	}

	shards := []shardDesc{}
	// Single shard: only include it if cursor doesn't skip past it.
	if req.ExclusiveStartShardId == "" || req.ExclusiveStartShardId != desc.ShardID {
		shards = []shardDesc{{
			ShardId:             desc.ShardID,
			SequenceNumberRange: seqRange{StartingSequenceNumber: desc.StartingSequenceNumber},
		}}
	}

	slog.Debug("described DynamoDB stream", "arn", req.StreamArn)
	writeJSON(w, http.StatusOK, map[string]any{
		"StreamDescription": streamDescResp{
			StreamArn:               desc.StreamARN,
			StreamLabel:             desc.StreamLabel,
			StreamStatus:            desc.StreamStatus,
			StreamViewType:          desc.StreamViewType,
			TableName:               desc.TableName,
			KeySchema:               ks,
			CreationRequestDateTime: desc.CreationRequestDateTime,
			Shards:                  shards,
		},
	})
}

func (sr *StreamsRouter) handleGetShardIterator(w http.ResponseWriter, body []byte) {
	var req struct {
		StreamArn         string `json:"StreamArn"`
		ShardId           string `json:"ShardId"`
		ShardIteratorType string `json:"ShardIteratorType"`
		SequenceNumber    string `json:"SequenceNumber"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			streamsErrPrefix+"ValidationException",
			"invalid request body",
		)
		return
	}
	if req.StreamArn == "" || req.ShardId == "" || req.ShardIteratorType == "" {
		writeError(w, http.StatusBadRequest, streamsErrPrefix+"ValidationException",
			"StreamArn, ShardId, and ShardIteratorType are required")
		return
	}

	iter, err := sr.storage.GetShardIterator(
		req.StreamArn,
		req.ShardId,
		req.ShardIteratorType,
		req.SequenceNumber,
	)
	if err != nil {
		if errors.Is(err, ErrStreamNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				streamsErrPrefix+"ResourceNotFoundException",
				"Requested resource not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			writeError(
				w,
				http.StatusBadRequest,
				streamsErrPrefix+"ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("GetShardIterator failed", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			streamsErrPrefix+"InternalServerError",
			"internal server error",
		)
		return
	}

	slog.Debug("got shard iterator", "stream", req.StreamArn)
	writeJSON(w, http.StatusOK, map[string]any{"ShardIterator": iter})
}

func (sr *StreamsRouter) handleGetRecords(w http.ResponseWriter, body []byte) {
	var req struct {
		ShardIterator string `json:"ShardIterator"`
		Limit         *int   `json:"Limit"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			streamsErrPrefix+"ValidationException",
			"invalid request body",
		)
		return
	}
	if req.ShardIterator == "" {
		writeError(
			w,
			http.StatusBadRequest,
			streamsErrPrefix+"ValidationException",
			"ShardIterator is required",
		)
		return
	}

	limit := 1000
	if req.Limit != nil {
		if *req.Limit > 1000 {
			writeError(
				w,
				http.StatusBadRequest,
				streamsErrPrefix+"LimitExceededException",
				"GetRecords was called with a value of more than 1000 for the limit request parameter",
			)
			return
		}
		if *req.Limit < 1 {
			writeError(
				w,
				http.StatusBadRequest,
				streamsErrPrefix+"ValidationException",
				"Limit must be >= 1",
			)
			return
		}
		limit = *req.Limit
	}

	records, nextIter, err := sr.storage.GetStreamRecords(req.ShardIterator, limit)
	if err != nil {
		if errors.Is(err, ErrValidationException) {
			writeError(
				w,
				http.StatusBadRequest,
				streamsErrPrefix+"ResourceNotFoundException",
				"Requested resource not found",
			)
			return
		}
		slog.Error("GetRecords failed", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			streamsErrPrefix+"InternalServerError",
			"internal server error",
		)
		return
	}

	type dynamoDBRecord struct {
		ApproximateCreationDateTime float64        `json:"ApproximateCreationDateTime"`
		Keys                        map[string]any `json:"Keys"`
		NewImage                    map[string]any `json:"NewImage,omitempty"`
		OldImage                    map[string]any `json:"OldImage,omitempty"`
		SequenceNumber              string         `json:"SequenceNumber"`
		SizeBytes                   int            `json:"SizeBytes"`
		StreamViewType              string         `json:"StreamViewType"`
	}
	type record struct {
		AwsRegion    string         `json:"awsRegion"`
		DynamoDB     dynamoDBRecord `json:"dynamodb"`
		EventID      string         `json:"eventID"`
		EventName    string         `json:"eventName"`
		EventSource  string         `json:"eventSource"`
		EventVersion string         `json:"eventVersion"`
	}

	out := make([]record, len(records))
	for i, r := range records {
		dr := dynamoDBRecord{
			ApproximateCreationDateTime: float64(r.CreatedAt.Unix()),
			Keys:                        r.Keys,
			NewImage:                    r.NewImage,
			OldImage:                    r.OldImage,
			SequenceNumber:              seqNumStr(r.SeqNum),
			StreamViewType:              r.ViewType,
		}
		// Approximate size as JSON byte count of the dynamodb field.
		if b, err := json.Marshal(dr); err == nil {
			dr.SizeBytes = len(b)
		}
		out[i] = record{
			AwsRegion:    "us-east-1",
			DynamoDB:     dr,
			EventID:      r.EventID,
			EventName:    r.EventName,
			EventSource:  "aws:dynamodb",
			EventVersion: "1.0",
		}
	}

	slog.Debug("GetRecords", "count", len(out))
	writeJSON(w, http.StatusOK, map[string]any{
		"Records":           out,
		"NextShardIterator": nextIter,
	})
}
