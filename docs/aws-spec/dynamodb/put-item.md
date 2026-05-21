# DynamoDB — PutItem

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_PutItem.html
- SDK struct: `dynamodb.PutItemInput` / `dynamodb.PutItemOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | 1–1024 chars |
| `Item` | map | yes | Must include all primary key attributes; string/binary keys must have length > 0 |
| `ConditionExpression` | string | no | Condition that must hold for put to proceed |
| `ExpressionAttributeNames` | map | no | `#name` → actual attribute name |
| `ExpressionAttributeValues` | map | no | `:ref` → typed DynamoDB value |
| `ReturnValues` | string | no | `NONE` (default) or `ALL_OLD`; `UPDATED_OLD`/`ALL_NEW`/`UPDATED_NEW` rejected with ValidationException (real AWS also rejects these for PutItem) |

## Ignored Parameters

`ReturnValuesOnConditionCheckFailure`, `ReturnConsumedCapacity`, `ReturnItemCollectionMetrics` — accepted without error, ignored.

Legacy: `ConditionalOperator`, `Expected` — not implemented.

## Response

| Field | Notes |
|---|---|
| `Attributes` | Old item; only when `ReturnValues=ALL_OLD` and item previously existed |

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | Missing TableName; invalid ReturnValues; unused ExpressionAttributeNames/Values refs |
| `ConditionalCheckFailedException` | 400 | ConditionExpression evaluated to false |
| `ResourceNotFoundException` | 400 | Table does not exist |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- `ReturnConsumedCapacity`, `ReturnItemCollectionMetrics`, `ReturnValuesOnConditionCheckFailure` are accepted but ignored.
- `ConditionalCheckFailedException` does not include the conflicting `Item` field (real AWS includes it when `ReturnValuesOnConditionCheckFailure=ALL_OLD`).
- `ItemCollectionSizeLimitExceededException` (10 GB LSI limit) is not enforced.
- `TransactionConflictException` is not enforced.
- ARN form of `TableName` is not accepted.
