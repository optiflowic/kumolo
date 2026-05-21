# DynamoDB — GetItem

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_GetItem.html
- SDK struct: `dynamodb.GetItemInput` / `dynamodb.GetItemOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | 1–1024 chars |
| `Key` | map | yes | All primary key attributes required |
| `ProjectionExpression` | string | no | Comma-separated attribute paths to return |
| `ExpressionAttributeNames` | map | no | `#name` → actual attribute name |

## Ignored Parameters

`ConsistentRead`, `ReturnConsumedCapacity` — accepted without error, ignored.

Legacy: `AttributesToGet` — not implemented.

## Response

| Field | Notes |
|---|---|
| `Item` | Absent (key omitted, not null) when item does not exist |

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | Missing TableName; invalid ProjectionExpression; unused ExpressionAttributeNames refs |
| `ResourceNotFoundException` | 400 | Table does not exist |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- `ConsistentRead` is accepted but ignored; kumolo always reads from the single authoritative store.
- `ReturnConsumedCapacity` is accepted but not returned.
- TTL-expired items are filtered out (same as real AWS eventual TTL deletion, but kumolo applies it eagerly on read).
- ARN form of `TableName` is not accepted.
