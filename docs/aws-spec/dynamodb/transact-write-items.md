# DynamoDB — TransactWriteItems

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactWriteItems.html
- SDK struct: `dynamodb.TransactWriteItemsInput` / `dynamodb.TransactWriteItemsOutput`
- Last verified: 2026-05-21

## Overview

Atomic batch of up to 100 write actions across one or more tables. Either all succeed or all
fail. Two actions may not target the same item.

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TransactItems` | array | yes | 1–100 `TransactWriteItem` objects |
| `ClientRequestToken` | string | no | Idempotency key; accepted but not enforced |

## TransactWriteItem Sub-actions

### Update
Equivalent to UpdateItem semantics. Fields: `TableName`, `Key`, `UpdateExpression`,
`ConditionExpression`, `ExpressionAttributeNames`, `ExpressionAttributeValues`,
`ReturnValuesOnConditionCheckFailure`.

UpdateExpression supports nested document paths (SET, REMOVE) — same rules as UpdateItem.
See `update-item.md` and `expressions-document-path.md`.

### Put
Equivalent to PutItem: `TableName`, `Item`, `ConditionExpression`, expression attribute maps.

### Delete
Equivalent to DeleteItem: `TableName`, `Key`, `ConditionExpression`, expression attribute maps.

### ConditionCheck
Evaluates a condition against an item without modifying it: `TableName`, `Key`,
`ConditionExpression`, expression attribute maps.

## Failure Semantics

The transaction is cancelled (all-or-nothing) when:
- Any `ConditionExpression` is false → cancellation reason `ConditionalCheckFailed`
- Two actions target the same item → `ValidationException` before any action runs
- A validation error occurs in any action (invalid expression, invalid path, etc.)

Cancellation reasons are returned in `CancellationReasons` in order of `TransactItems`.
Items with no error get code `None` and null message.

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | Invalid expression, duplicate item target, invalid path |
| `TransactionCanceledException` | 400 | One or more cancellation reasons (condition check failure, validation error) |
| `ResourceNotFoundException` | 400 | Table does not exist |

## kumolo-Specific Deviations

- `ReturnConsumedCapacity`, `ReturnItemCollectionMetrics` are accepted but ignored.
- `ReturnValuesOnConditionCheckFailure` is accepted but ignored (items not returned on condition failure).
- `ClientRequestToken` idempotency is not enforced.
- Cross-table atomicity is implemented with a single in-process mutex; sufficient for local testing.
