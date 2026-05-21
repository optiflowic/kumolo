# DynamoDB — BatchGetItem

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchGetItem.html
- SDK struct: `dynamodb.BatchGetItemInput` / `dynamodb.BatchGetItemOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `RequestItems` | map[string]KeysAndAttributes | yes | Key = table name; max 100 total items across all tables |

**KeysAndAttributes fields:**

| Field | Type | Notes |
|---|---|---|
| `Keys` | []map | All PK attributes required per key |
| `ProjectionExpression` | string | Optional; preferred over legacy AttributesToGet |
| `ExpressionAttributeNames` | map | `#name` substitutions for ProjectionExpression |

## Ignored Parameters

`ReturnConsumedCapacity` — accepted without error, ignored.

Per-table `ConsistentRead` — accepted without error, ignored.

## Response

| Field | Notes |
|---|---|
| `Responses` | map[tableName][]item; non-existent items are absent from the list (no error) |
| `UnprocessedKeys` | Always `{}` in kumolo (real AWS may return unprocessed keys on throttle or partial failure) |

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | Missing RequestItems; total keys > 100; invalid ProjectionExpression; unused ExpressionAttributeNames refs |
| `ResourceNotFoundException` | 400 | Table does not exist |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- `ConsistentRead` per-table is accepted but ignored.
- `ReturnConsumedCapacity` is not returned.
- `UnprocessedKeys` is always empty; real AWS may return unprocessed items on throttle.
- Duplicate key within the same table is not detected (real AWS: `ValidationException`).
- ARN form of table name keys is not accepted.
