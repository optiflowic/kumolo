# DescribeStream

URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_streams_DescribeStream.html  
SDK: `dynamodbstreams.DescribeStreamInput` / `DescribeStreamOutput`  
Target: `DynamoDBStreams_20120810.DescribeStream`  
Last verified: 2026-05-28

## Request

- `StreamArn` (string, required)
- `ExclusiveStartShardId` (string, optional) — shard pagination cursor
- `Limit` (int, optional, 1–100) — max shards to return
- `ShardFilter` (object, optional) — not implemented

## Response

`StreamDescription`:
- `StreamArn`, `StreamLabel`, `StreamStatus` (ENABLED|DISABLED), `StreamViewType`
- `TableName`, `KeySchema[]`, `CreationRequestDateTime`
- `Shards[]` — each has `ShardId`, `SequenceNumberRange.StartingSequenceNumber`; no `EndingSequenceNumber` for the open shard
- `LastEvaluatedShardId` — present when more shards exist (not applicable for single-shard model)

## Implemented errors

- `ResourceNotFoundException` 400 — stream ARN not found
- `InternalServerError` 500

## kumolo deviations

- Single shard per stream; `ExclusiveStartShardId` pagination always returns the one shard unless cursor matches it.
- `ShardFilter` is ignored.
