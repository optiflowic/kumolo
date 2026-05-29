# GetRecords

URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_streams_GetRecords.html  
SDK: `dynamodbstreams.GetRecordsInput` / `GetRecordsOutput`  
Target: `DynamoDBStreams_20120810.GetRecords`  
Last verified: 2026-05-28

## Request

- `ShardIterator` (string, required)
- `Limit` (int, optional, 1–1000, default 1000)

## Response

- `Records[]` — each record has:
  - `eventID` (string)
  - `eventName` (string): `INSERT` | `MODIFY` | `REMOVE`
  - `eventSource` = `"aws:dynamodb"`
  - `eventVersion` = `"1.0"`
  - `awsRegion` = `"us-east-1"`
  - `dynamodb.Keys` — always present
  - `dynamodb.NewImage` — present for INSERT/MODIFY (when view type includes it)
  - `dynamodb.OldImage` — present for MODIFY/REMOVE (when view type includes it)
  - `dynamodb.SequenceNumber` (21-digit zero-padded string)
  - `dynamodb.SizeBytes` (number)
  - `dynamodb.StreamViewType`
  - `dynamodb.ApproximateCreationDateTime` (Unix epoch seconds float)
- `NextShardIterator` — next position cursor; always present (shard never closes in kumolo)

## Implemented errors

- `ResourceNotFoundException` 400 — iterator references unknown stream
- `ExpiredIteratorException` 400 — not enforced; kumolo iterators do not expire
- `LimitExceededException` 400 — Limit > 1000
- `InternalServerError` 500

## kumolo deviations

- Iterators never expire.
- `SizeBytes` is the JSON-marshalled byte count of the record's `dynamodb` field — an approximation.
