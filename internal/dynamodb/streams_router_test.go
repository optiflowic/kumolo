package dynamodb

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errStore returns an error for every method, used to exercise InternalServerError paths.
type errStore struct{}

func (errStore) ListStreamARNs(_ string) ([]StreamEntry, error) {
	return nil, errors.New("storage error")
}
func (errStore) DescribeStream(_ string) (StreamDesc, error) {
	return StreamDesc{}, errors.New("storage error")
}
func (errStore) GetShardIterator(_, _, _, _ string) (string, error) {
	return "", errors.New("storage error")
}
func (errStore) GetStreamRecords(_ string, _ int) ([]streamRecord, string, error) {
	return nil, "", errors.New("storage error")
}

func newTestStreamsRouter(t *testing.T) (*StreamsRouter, *Storage) {
	t.Helper()
	s := newTestStorage(t)
	return NewStreamsRouter(s), s
}

func streams(t *testing.T, router http.Handler, op, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Amz-Target", "DynamoDBStreams_20120810."+op)
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// ---- StreamsRouter dispatch ----

func TestStreamsRouterUnknownOp(t *testing.T) {
	sr, _ := newTestStreamsRouter(t)
	w := streams(t, sr, "UnknownOp", `{}`)
	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestStreamsRouterBadBody(t *testing.T) {
	sr, _ := newTestStreamsRouter(t)
	for _, op := range []string{"ListStreams", "DescribeStream", "GetShardIterator", "GetRecords"} {
		t.Run(op, func(t *testing.T) {
			w := streams(t, sr, op, `{bad json`)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

// ---- InternalServerError paths (requires storage-level failure) ----

func TestStreamsHandlersInternalError(t *testing.T) {
	sr := &StreamsRouter{storage: errStore{}}
	for _, tc := range []struct {
		op   string
		body string
	}{
		{"ListStreams", `{}`},
		{"DescribeStream", `{"StreamArn":"arn:aws:dynamodb:us-east-1:000000000000:table/t/stream/l"}`},
		{"GetShardIterator", `{"StreamArn":"arn:aws:dynamodb:us-east-1:000000000000:table/t/stream/l","ShardId":"s","ShardIteratorType":"TRIM_HORIZON"}`},
		{"GetRecords", fmt.Sprintf(`{"ShardIterator":"%s"}`, func() string {
			iter, _ := encodeIterator("t", 0)
			return iter
		}())},
	} {
		t.Run(tc.op, func(t *testing.T) {
			w := streams(t, sr, tc.op, tc.body)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	}
}

// ---- ListStreams ----

func TestHandleListStreams(t *testing.T) {
	sr, s := newTestStreamsRouter(t)
	mustCreateStreamTable(t, s, "tbl-a", "KEYS_ONLY")
	mustCreateStreamTable(t, s, "tbl-b", "NEW_IMAGE")

	t.Run("returns all streams", func(t *testing.T) {
		w := streams(t, sr, "ListStreams", `{}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Streams []struct {
				StreamArn   string `json:"StreamArn"`
				StreamLabel string `json:"StreamLabel"`
				TableName   string `json:"TableName"`
			} `json:"Streams"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Len(t, resp.Streams, 2)
	})

	t.Run("filters by TableName", func(t *testing.T) {
		w := streams(t, sr, "ListStreams", `{"TableName":"tbl-a"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Streams []struct{ TableName string } `json:"Streams"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		require.Len(t, resp.Streams, 1)
		assert.Equal(t, "tbl-a", resp.Streams[0].TableName)
	})

	t.Run("Limit=1 returns LastEvaluatedStreamArn", func(t *testing.T) {
		w := streams(t, sr, "ListStreams", `{"Limit":1}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Streams                []any  `json:"Streams"`
			LastEvaluatedStreamArn string `json:"LastEvaluatedStreamArn"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Len(t, resp.Streams, 1)
		assert.NotEmpty(t, resp.LastEvaluatedStreamArn)
	})

	t.Run("Limit<1 returns ValidationException", func(t *testing.T) {
		w := streams(t, sr, "ListStreams", `{"Limit":0}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("ExclusiveStartStreamArn pagination", func(t *testing.T) {
		w := streams(t, sr, "ListStreams", `{}`)
		var all struct {
			Streams []struct{ StreamArn string } `json:"Streams"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&all))
		require.Len(t, all.Streams, 2)

		body, _ := json.Marshal(map[string]any{
			"ExclusiveStartStreamArn": all.Streams[0].StreamArn,
		})
		w2 := streams(t, sr, "ListStreams", string(body))
		var page2 struct {
			Streams []struct{ StreamArn string } `json:"Streams"`
		}
		require.NoError(t, json.NewDecoder(w2.Body).Decode(&page2))
		assert.Len(t, page2.Streams, 1)
		assert.Equal(t, all.Streams[1].StreamArn, page2.Streams[0].StreamArn)
	})

	t.Run("non-existent TableName returns ResourceNotFoundException", func(t *testing.T) {
		w := streams(t, sr, "ListStreams", `{"TableName":"no-such-table"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Contains(t, resp["__type"], "ResourceNotFoundException")
	})
}

// ---- DescribeStream ----

func TestHandleDescribeStream(t *testing.T) {
	sr, s := newTestStreamsRouter(t)
	meta := mustCreateStreamTable(t, s, "ds-tbl", "NEW_AND_OLD_IMAGES")
	arn := streamARN("ds-tbl", meta.StreamLabel)

	t.Run("returns stream description", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"StreamArn": arn})
		w := streams(t, sr, "DescribeStream", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			StreamDescription struct {
				StreamArn      string `json:"StreamArn"`
				StreamStatus   string `json:"StreamStatus"`
				StreamViewType string `json:"StreamViewType"`
				TableName      string `json:"TableName"`
				Shards         []struct {
					ShardId string `json:"ShardId"`
				} `json:"Shards"`
				KeySchema []struct{ AttributeName string } `json:"KeySchema"`
			} `json:"StreamDescription"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		sd := resp.StreamDescription
		assert.Equal(t, arn, sd.StreamArn)
		assert.Equal(t, "ENABLED", sd.StreamStatus)
		assert.Equal(t, "NEW_AND_OLD_IMAGES", sd.StreamViewType)
		assert.Equal(t, "ds-tbl", sd.TableName)
		assert.Len(t, sd.Shards, 1)
		assert.NotEmpty(t, sd.Shards[0].ShardId)
		assert.Len(t, sd.KeySchema, 1)
	})

	t.Run("StreamArn required", func(t *testing.T) {
		w := streams(t, sr, "DescribeStream", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("unknown stream returns ResourceNotFoundException", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"StreamArn": "arn:aws:dynamodb:us-east-1:000000000000:table/no-table/stream/bad",
		})
		w := streams(t, sr, "DescribeStream", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Limit<1 returns ValidationException", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"StreamArn": arn, "Limit": 0})
		w := streams(t, sr, "DescribeStream", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("ExclusiveStartShardId matching shard hides it", func(t *testing.T) {
		sid := shardID("ds-tbl", meta.StreamLabel)
		body, _ := json.Marshal(map[string]string{"StreamArn": arn, "ExclusiveStartShardId": sid})
		w := streams(t, sr, "DescribeStream", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			StreamDescription struct {
				Shards []any `json:"Shards"`
			} `json:"StreamDescription"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Len(t, resp.StreamDescription.Shards, 0)
	})

	t.Run("malformed ARN returns ResourceNotFoundException", func(t *testing.T) {
		// ARN without "/stream/" triggers parseStreamARN error in storage
		body, _ := json.Marshal(map[string]string{
			"StreamArn": "arn:aws:dynamodb:us-east-1:000000000000:table/no-stream-suffix",
		})
		w := streams(t, sr, "DescribeStream", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("stream with records includes StartingSequenceNumber", func(t *testing.T) {
		sr2, s2 := newTestStreamsRouter(t)
		meta2 := mustCreateStreamTable(t, s2, "ds-tbl2", "NEW_IMAGE")
		_, err := s2.PutItem("ds-tbl2", map[string]any{
			"pk": map[string]any{"S": "k"},
		}, nil)
		require.NoError(t, err)
		arn2 := streamARN("ds-tbl2", meta2.StreamLabel)
		body, _ := json.Marshal(map[string]string{"StreamArn": arn2})
		w := streams(t, sr2, "DescribeStream", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			StreamDescription struct {
				Shards []struct {
					SequenceNumberRange struct {
						StartingSequenceNumber string `json:"StartingSequenceNumber"`
					} `json:"SequenceNumberRange"`
				} `json:"Shards"`
			} `json:"StreamDescription"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		require.Len(t, resp.StreamDescription.Shards, 1)
		assert.NotEmpty(
			t,
			resp.StreamDescription.Shards[0].SequenceNumberRange.StartingSequenceNumber,
		)
	})

	t.Run("Limit=0 returns ValidationException before storage call", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"StreamArn": arn, "Limit": 0})
		w := streams(t, sr, "DescribeStream", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Contains(t, resp["__type"], "ValidationException")
	})

	t.Run(
		"Limit=0 with invalid StreamArn returns ValidationException not ResourceNotFoundException",
		func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"StreamArn": "arn:aws:dynamodb:us-east-1:000000000000:table/no-table/stream/bad",
				"Limit":     0,
			})
			w := streams(t, sr, "DescribeStream", string(body))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp map[string]string
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Contains(t, resp["__type"], "ValidationException")
		},
	)
}

// ---- GetShardIterator ----

func TestHandleGetShardIterator(t *testing.T) {
	sr, s := newTestStreamsRouter(t)
	meta := mustCreateStreamTable(t, s, "gsi-tbl", "NEW_IMAGE")
	arn := streamARN("gsi-tbl", meta.StreamLabel)
	sid := shardID("gsi-tbl", meta.StreamLabel)

	t.Run("TRIM_HORIZON returns iterator", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"StreamArn": arn, "ShardId": sid, "ShardIteratorType": "TRIM_HORIZON",
		})
		w := streams(t, sr, "GetShardIterator", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct{ ShardIterator string }
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.NotEmpty(t, resp.ShardIterator)
	})

	t.Run("required fields missing", func(t *testing.T) {
		w := streams(t, sr, "GetShardIterator", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("unknown stream returns ResourceNotFoundException", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"StreamArn":         "arn:aws:dynamodb:us-east-1:000000000000:table/x/stream/bad",
			"ShardId":           sid,
			"ShardIteratorType": "TRIM_HORIZON",
		})
		w := streams(t, sr, "GetShardIterator", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid ShardIteratorType returns ValidationException", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"StreamArn": arn, "ShardId": sid, "ShardIteratorType": "BOGUS",
		})
		w := streams(t, sr, "GetShardIterator", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run(
		"AT_SEQUENCE_NUMBER without SequenceNumber returns ValidationException",
		func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{
				"StreamArn": arn, "ShardId": sid, "ShardIteratorType": "AT_SEQUENCE_NUMBER",
			})
			w := streams(t, sr, "GetShardIterator", string(body))
			assert.Equal(t, http.StatusBadRequest, w.Code)
		},
	)

	t.Run("malformed ARN returns ResourceNotFoundException", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"StreamArn":         "arn:aws:dynamodb:us-east-1:000000000000:table/no-stream-suffix",
			"ShardId":           sid,
			"ShardIteratorType": "TRIM_HORIZON",
		})
		w := streams(t, sr, "GetShardIterator", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("wrong ShardId returns ResourceNotFoundException", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"StreamArn":         arn,
			"ShardId":           "shardId-00000000000000000001-wrongid1",
			"ShardIteratorType": "TRIM_HORIZON",
		})
		w := streams(t, sr, "GetShardIterator", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("LATEST returns iterator", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"StreamArn": arn, "ShardId": sid, "ShardIteratorType": "LATEST",
		})
		w := streams(t, sr, "GetShardIterator", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct{ ShardIterator string }
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.NotEmpty(t, resp.ShardIterator)
	})

	t.Run(
		"AT_SEQUENCE_NUMBER with invalid sequence returns ValidationException",
		func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{
				"StreamArn": arn, "ShardId": sid, "ShardIteratorType": "AT_SEQUENCE_NUMBER",
				"SequenceNumber": "not-a-number",
			})
			w := streams(t, sr, "GetShardIterator", string(body))
			assert.Equal(t, http.StatusBadRequest, w.Code)
		},
	)

	t.Run("AFTER_SEQUENCE_NUMBER with valid sequence returns iterator", func(t *testing.T) {
		_, err := s.PutItem("gsi-tbl", map[string]any{
			"pk": map[string]any{"S": "seq-test"},
		}, nil)
		require.NoError(t, err)
		// Get the real sequence number via TRIM_HORIZON + GetRecords.
		thBody, _ := json.Marshal(map[string]string{
			"StreamArn": arn, "ShardId": sid, "ShardIteratorType": "TRIM_HORIZON",
		})
		thW := streams(t, sr, "GetShardIterator", string(thBody))
		var thResp struct{ ShardIterator string }
		require.NoError(t, json.NewDecoder(thW.Body).Decode(&thResp))
		recBody, _ := json.Marshal(map[string]string{"ShardIterator": thResp.ShardIterator})
		recW := streams(t, sr, "GetRecords", string(recBody))
		var recResp struct {
			Records []struct {
				DynamoDB struct {
					SequenceNumber string `json:"SequenceNumber"`
				} `json:"dynamodb"`
			} `json:"Records"`
		}
		require.NoError(t, json.NewDecoder(recW.Body).Decode(&recResp))
		require.NotEmpty(t, recResp.Records)
		seqNum := recResp.Records[0].DynamoDB.SequenceNumber

		body, _ := json.Marshal(map[string]string{
			"StreamArn": arn, "ShardId": sid, "ShardIteratorType": "AFTER_SEQUENCE_NUMBER",
			"SequenceNumber": seqNum,
		})
		w := streams(t, sr, "GetShardIterator", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct{ ShardIterator string }
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.NotEmpty(t, resp.ShardIterator)
	})
}

// ---- GetRecords ----

func TestHandleGetRecords(t *testing.T) {
	sr, s := newTestStreamsRouter(t)
	meta := mustCreateStreamTable(t, s, "gr-tbl", "NEW_AND_OLD_IMAGES")
	arn := streamARN("gr-tbl", meta.StreamLabel)
	sid := shardID("gr-tbl", meta.StreamLabel)

	for i := 0; i < 3; i++ {
		_, err := s.PutItem("gr-tbl", map[string]any{
			"pk": map[string]any{"S": "k"},
			"v":  map[string]any{"N": "0"},
		}, nil)
		require.NoError(t, err)
	}

	iterBody, _ := json.Marshal(map[string]string{
		"StreamArn": arn, "ShardId": sid, "ShardIteratorType": "TRIM_HORIZON",
	})
	iterW := streams(t, sr, "GetShardIterator", string(iterBody))
	require.Equal(t, http.StatusOK, iterW.Code)
	var iterResp struct{ ShardIterator string }
	require.NoError(t, json.NewDecoder(iterW.Body).Decode(&iterResp))

	t.Run("returns records and NextShardIterator", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"ShardIterator": iterResp.ShardIterator})
		w := streams(t, sr, "GetRecords", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Records []struct {
				EventName   string `json:"eventName"`
				EventSource string `json:"eventSource"`
				DynamoDB    struct {
					Keys           map[string]any `json:"Keys"`
					SequenceNumber string         `json:"SequenceNumber"`
					StreamViewType string         `json:"StreamViewType"`
					SizeBytes      int            `json:"SizeBytes"`
				} `json:"dynamodb"`
			} `json:"Records"`
			NextShardIterator string `json:"NextShardIterator"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Len(t, resp.Records, 3)
		assert.NotEmpty(t, resp.NextShardIterator)
		assert.Equal(t, "aws:dynamodb", resp.Records[0].EventSource)
		assert.Len(t, resp.Records[0].DynamoDB.SequenceNumber, 21)
		assert.Greater(t, resp.Records[0].DynamoDB.SizeBytes, 0)
	})

	t.Run("ShardIterator required", func(t *testing.T) {
		w := streams(t, sr, "GetRecords", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Limit>1000 returns LimitExceededException", func(t *testing.T) {
		body, _ := json.Marshal(
			map[string]any{"ShardIterator": iterResp.ShardIterator, "Limit": 1001},
		)
		w := streams(t, sr, "GetRecords", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp struct {
			Type string `json:"__type"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Contains(t, resp.Type, "LimitExceededException")
	})

	t.Run("Limit<1 returns ValidationException", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"ShardIterator": iterResp.ShardIterator, "Limit": 0})
		w := streams(t, sr, "GetRecords", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid iterator returns ResourceNotFoundException", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"ShardIterator": "bm90LWJhc2U2NAo="})
		w := streams(t, sr, "GetRecords", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Limit=2 returns at most 2 records", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"ShardIterator": iterResp.ShardIterator, "Limit": 2})
		w := streams(t, sr, "GetRecords", string(body))
		require.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Records []any `json:"Records"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Len(t, resp.Records, 2)
	})
}

// ---- validateStreamViewType ----

func TestValidateStreamViewType(t *testing.T) {
	for _, vt := range []string{"KEYS_ONLY", "NEW_IMAGE", "OLD_IMAGE", "NEW_AND_OLD_IMAGES"} {
		assert.NoError(t, validateStreamViewType(vt))
	}
	assert.Error(t, validateStreamViewType(""))
	assert.Error(t, validateStreamViewType("INVALID"))
}

// ---- CreateTable / UpdateTable StreamSpecification validation ----

func TestCreateTableStreamViewTypeValidation(t *testing.T) {
	ro := newTestRouter(t)

	t.Run("invalid StreamViewType rejected", func(t *testing.T) {
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "sv-test",
			"KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}],
			"AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}],
			"BillingMode": "PAY_PER_REQUEST",
			"StreamSpecification": {"StreamEnabled": true, "StreamViewType": "BAD_TYPE"}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("empty StreamViewType rejected when enabled", func(t *testing.T) {
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "sv-test2",
			"KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}],
			"AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}],
			"BillingMode": "PAY_PER_REQUEST",
			"StreamSpecification": {"StreamEnabled": true}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("disabled StreamSpec ignores ViewType", func(t *testing.T) {
		w := dynamo(t, ro, "CreateTable", `{
			"TableName": "sv-test3",
			"KeySchema": [{"AttributeName": "pk", "KeyType": "HASH"}],
			"AttributeDefinitions": [{"AttributeName": "pk", "AttributeType": "S"}],
			"BillingMode": "PAY_PER_REQUEST",
			"StreamSpecification": {"StreamEnabled": false, "StreamViewType": "BAD_TYPE"}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestUpdateTableStreamViewTypeValidation(t *testing.T) {
	ro := newTestRouter(t)
	dynamo(t, ro, "CreateTable", createTableBody)

	t.Run("invalid StreamViewType rejected", func(t *testing.T) {
		w := dynamo(t, ro, "UpdateTable", `{
			"TableName": "test-table",
			"StreamSpecification": {"StreamEnabled": true, "StreamViewType": "BAD"}
		}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("valid StreamViewType accepted", func(t *testing.T) {
		w := dynamo(t, ro, "UpdateTable", `{
			"TableName": "test-table",
			"StreamSpecification": {"StreamEnabled": true, "StreamViewType": "KEYS_ONLY"}
		}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// ---- storage_streams edge cases ----

func TestStreamLabelToUnixFallback(t *testing.T) {
	assert.NotZero(t, streamLabelToUnix("2026-05-28T10:00:00.000000000"))
	assert.NotZero(t, streamLabelToUnix("2026-05-28T10:00:00.000"))
	assert.Zero(t, streamLabelToUnix("not-a-timestamp"))
}

func TestDecodeIteratorInvalid(t *testing.T) {
	t.Run("bad base64", func(t *testing.T) {
		_, err := decodeIterator("not-base64!!!!")
		assert.Error(t, err)
	})

	t.Run("valid base64 but bad JSON", func(t *testing.T) {
		// base64("invalid")
		_, err := decodeIterator("aW52YWxpZA==")
		assert.Error(t, err)
	})
}

func TestParseSeqNumInvalid(t *testing.T) {
	_, err := parseSeqNum("not-a-number")
	assert.Error(t, err)
}

func TestFindSeqPosExhausted(t *testing.T) {
	recs := []streamRecord{{SeqNum: 1}, {SeqNum: 2}}
	assert.Equal(t, 2, findSeqPos(recs, 99, false))
	assert.Equal(t, 2, findSeqPos(recs, 99, true))
}

func TestEmitStreamRecordDefaultViewType(t *testing.T) {
	s := newTestStorage(t)
	meta := TableMetadata{
		Name:      "vt-default",
		KeySchema: []KeySchemaElement{{AttributeName: "pk", KeyType: "HASH"}},
		AttributeDefinitions: []AttributeDefinition{
			{AttributeName: "pk", AttributeType: "S"},
		},
		StreamSpec: &StreamSpecification{StreamEnabled: true, StreamViewType: "NEW_AND_OLD_IMAGES"},
	}
	require.NoError(t, s.CreateTable(meta))

	// Directly call emitStreamRecord with an unrecognised viewType to hit the default branch.
	s.emitStreamRecord(
		"vt-default", "INSERT", "UNKNOWN_TYPE",
		map[string]any{"pk": map[string]any{"S": "k"}},
		nil,
		map[string]any{"pk": map[string]any{"S": "k"}},
	)

	buf := s.getStreamBuffer("vt-default")
	require.NotNil(t, buf)
	buf.mu.RLock()
	defer buf.mu.RUnlock()
	require.Len(t, buf.records, 1)
	assert.NotNil(t, buf.records[0].NewImage)
}

func TestEnsureStreamBufferIdempotent(t *testing.T) {
	s := newTestStorage(t)
	buf1 := s.ensureStreamBuffer("tbl", "label-1")
	buf2 := s.ensureStreamBuffer("tbl", "label-1")
	assert.Same(t, buf1, buf2)
}

// TestEmitStreamRecordNilBuffer exercises the early return when the stream buffer
// has been cleared (simulating a server restart with streaming metadata intact).
func TestEmitStreamRecordNilBuffer(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "sim-restart", "NEW_IMAGE")
	// Remove buffer to simulate in-memory state loss after restart.
	s.streamsMu.Lock()
	delete(s.streams, "sim-restart")
	s.streamsMu.Unlock()

	_, err := s.PutItem("sim-restart", map[string]any{
		"pk": map[string]any{"S": "k"},
	}, nil)
	require.NoError(t, err)
	assert.Nil(t, s.getStreamBuffer("sim-restart"))
}

// TestGetStreamRecordsNilBuffer exercises the path where an iterator points to a table
// whose in-memory buffer has been cleared (simulating a restart).
func TestGetStreamRecordsNilBuffer(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "nil-buf-tbl", "NEW_IMAGE")
	s.streamsMu.Lock()
	delete(s.streams, "nil-buf-tbl")
	s.streamsMu.Unlock()

	iter, err := encodeIterator("nil-buf-tbl", 0)
	require.NoError(t, err)
	records, next, err := s.GetStreamRecords(iter, 100)
	require.NoError(t, err)
	assert.Nil(t, records)
	assert.NotEmpty(t, next)
}

// TestGetStreamRecordsPositionBeyondEnd exercises the start-clamping when an iterator
// position is beyond the current record count.
func TestGetStreamRecordsPositionBeyondEnd(t *testing.T) {
	s := newTestStorage(t)
	mustCreateStreamTable(t, s, "beyond-tbl", "NEW_IMAGE")
	for i := range 2 {
		_, err := s.PutItem("beyond-tbl", map[string]any{
			"pk": map[string]any{"S": fmt.Sprintf("k%d", i)},
		}, nil)
		require.NoError(t, err)
	}
	iter, err := encodeIterator("beyond-tbl", 100)
	require.NoError(t, err)
	records, next, err := s.GetStreamRecords(iter, 100)
	require.NoError(t, err)
	assert.Empty(t, records)
	assert.NotEmpty(t, next)
}

// TestParseStreamARNEmptyParts exercises the empty-tableName / empty-label branch
// that fires when "/stream/" is present but one side is blank.
func TestParseStreamARNEmptyParts(t *testing.T) {
	prefix := "arn:aws:dynamodb:us-east-1:000000000000:table/"
	_, _, err := parseStreamARN(prefix + "/stream/label") // empty tableName
	assert.Error(t, err)
	_, _, err = parseStreamARN(prefix + "table/stream/") // empty label
	assert.Error(t, err)
}

// TestFindSeqPosFirstGreater exercises the early-return branch when the very first
// record already has a higher SeqNum than the target.
func TestFindSeqPosFirstGreater(t *testing.T) {
	recs := []streamRecord{{SeqNum: 5}, {SeqNum: 10}}
	assert.Equal(t, 0, findSeqPos(recs, 3, false))
	assert.Equal(t, 0, findSeqPos(recs, 3, true))
}
