# DynamoDB — BatchWriteItem

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchWriteItem.html
- SDK struct: `dynamodb.BatchWriteItemInput` / `dynamodb.BatchWriteItemOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `RequestItems` | map[string][]WriteRequest | yes | Key = table name; max 25 total write operations |

**WriteRequest fields (one of):**

| Field | Notes |
|---|---|
| `PutRequest.Item` | Full item to write; must include all PK attributes |
| `DeleteRequest.Key` | All PK attributes required |

## Ignored Parameters

`ReturnConsumedCapacity`, `ReturnItemCollectionMetrics` — accepted without error, ignored.

## Response

| Field | Notes |
|---|---|
| `UnprocessedItems` | Always `{}` in kumolo (real AWS may return unprocessed items on partial failure) |

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | Missing RequestItems; total write ops > 25; invalid item content |
| `ResourceNotFoundException` | 400 | Table does not exist |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- `UnprocessedItems` is always empty; real AWS may return unprocessed items on throttle.
- `ReturnConsumedCapacity` and `ReturnItemCollectionMetrics` are not returned.
- Duplicate PK within the same table is not detected (real AWS: `ValidationException`).
- No `ConditionExpression` support — consistent with real AWS (BatchWriteItem does not support conditions).
- ARN form of table name keys is not accepted.
