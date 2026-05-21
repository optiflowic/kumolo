# DynamoDB — Scan

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Scan.html
- SDK struct: `dynamodb.ScanInput` / `dynamodb.ScanOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | 1–1024 chars |
| `FilterExpression` | string | no | Applied after read |
| `IndexName` | string | no | LSI or GSI name |
| `ExclusiveStartKey` | map | no | Pagination cursor |
| `Limit` | integer | no | Max items to evaluate (before FilterExpression); min 1 |
| `Segment` | integer | no | 0-based segment ID for parallel scan; range 0–999999; must pair with TotalSegments |
| `TotalSegments` | integer | no | Total parallel workers; range 1–1000000; must pair with Segment |
| `Select` | string | no | `ALL_ATTRIBUTES` / `ALL_PROJECTED_ATTRIBUTES` (index only) / `SPECIFIC_ATTRIBUTES` / `COUNT` |
| `ProjectionExpression` | string | no | Comma-separated attribute paths |
| `ExpressionAttributeNames` | map | no | `#name` substitutions |
| `ExpressionAttributeValues` | map | no | `:val` substitutions |

## Ignored Parameters

`ConsistentRead`, `ReturnConsumedCapacity` — accepted without error, ignored.

Legacy: `ScanFilter`, `ConditionalOperator`, `AttributesToGet` — not implemented.

## Response

| Field | Notes |
|---|---|
| `Items` | Items after FilterExpression; absent when `Select=COUNT` |
| `Count` | Items returned after filter |
| `ScannedCount` | Items read before FilterExpression |
| `LastEvaluatedKey` | Pagination cursor; omitted on last page |

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | Missing TableName; invalid Limit (< 1); invalid Segment/TotalSegments range or missing pair; invalid Select; unused expression refs |
| `ResourceNotFoundException` | 400 | Table does not exist |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- `ConsistentRead=true` for GSI is accepted without error (real AWS rejects it).
- `ReturnConsumedCapacity` is not returned.
- Parallel scan: `Segment`/`TotalSegments` are validated but the keyspace is NOT physically partitioned; all segments see the same full dataset (filtering by segment is not implemented). Suitable for testing parallel scan SDK usage but not for actual workload distribution.
- No 1 MB hard page limit per call; kumolo loads all items into memory before applying Limit.
- ARN form of `TableName` is not accepted.
