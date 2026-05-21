# DynamoDB — TransactGetItems

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactGetItems.html
- SDK struct: `dynamodb.TransactGetItemsInput` / `dynamodb.TransactGetItemsOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TransactItems` | []TransactGetItem | yes | 1–100 items |

Each `TransactGetItem.Get` fields:

| Field | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | |
| `Key` | map | yes | All PK attributes |
| `ProjectionExpression` | string | no | Comma-separated attribute paths |
| `ExpressionAttributeNames` | map | no | `#name` substitutions |

## Ignored Parameters

`ReturnConsumedCapacity` — accepted without error, ignored.

## Response

| Field | Notes |
|---|---|
| `Responses` | []ItemResponse; positionally aligned with TransactItems; `{}` entry (empty object) when item not found |

Note: kumolo returns `{}` for missing items; real AWS returns an `ItemResponse` with a null/absent `Item` field.

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | Missing/empty TransactItems; > 100 items; missing Get sub-object; invalid ProjectionExpression; unused expression refs |
| `ResourceNotFoundException` | 400 | Table does not exist |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- `TransactionCanceledException` (concurrent write conflict) is not enforced; real AWS returns this when a concurrent write conflicts with the transactional read.
- `ReturnConsumedCapacity` is not returned.
- Cannot read from indexes (consistent with real AWS — base table only).
- Aggregate 4 MB response size limit is not enforced.
- ARN form of `TableName` is not accepted.
