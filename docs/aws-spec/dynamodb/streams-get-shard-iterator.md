# GetShardIterator

URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_streams_GetShardIterator.html  
SDK: `dynamodbstreams.GetShardIteratorInput` / `GetShardIteratorOutput`  
Target: `DynamoDBStreams_20120810.GetShardIterator`  
Last verified: 2026-05-28

## Request

- `StreamArn` (string, required)
- `ShardId` (string, required)
- `ShardIteratorType` (string, required): `TRIM_HORIZON` | `LATEST` | `AT_SEQUENCE_NUMBER` | `AFTER_SEQUENCE_NUMBER`
- `SequenceNumber` (string, required for AT/AFTER_SEQUENCE_NUMBER)

## Response

- `ShardIterator` (string) — opaque cursor; expires after 15 minutes (not enforced in kumolo)

## Implemented errors

- `ResourceNotFoundException` 400 — stream or shard not found
- `TrimmedDataAccessException` 400 — not returned; see deviations
- `InternalServerError` 500

## kumolo deviations

- Iterator encoded as base64-JSON `{"t":"<tableName>","p":<nextIndex>}`; stateless, no server-side expiry.
- `TRIM_HORIZON` → position 0; `LATEST` → position equal to current record count; AT/AFTER decode sequence number to index.
- `TrimmedDataAccessException` is not returned when `AT_SEQUENCE_NUMBER` / `AFTER_SEQUENCE_NUMBER` references a record that has been trimmed (older than 24 h). kumolo silently falls back to the nearest available position (the first record with a higher SeqNum), whereas real AWS returns 400.
