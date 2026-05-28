# ListStreams

URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_streams_ListStreams.html  
SDK: `dynamodbstreams.ListStreamsInput` / `ListStreamsOutput`  
Target: `DynamoDBStreams_20120810.ListStreams`  
Last verified: 2026-05-28

## Request
- `TableName` (string, optional) — filter to streams for this table
- `Limit` (int, optional, 1–100, default 100) — max streams to return
- `ExclusiveStartStreamArn` (string, optional) — pagination cursor

## Response
- `Streams[]` — array of `{StreamArn, StreamLabel, TableName}`
- `LastEvaluatedStreamArn` — present when more pages exist

## Implemented errors
- `ResourceNotFoundException` 400 — TableName given but table does not exist
- `InternalServerError` 500

## kumolo deviations
- Each stream-enabled table has exactly one active stream (single-shard model).
- Records are in-memory; lost on restart (real AWS: 24-hour retention).
