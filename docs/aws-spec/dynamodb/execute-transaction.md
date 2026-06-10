# ExecuteTransaction

- URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ExecuteTransaction.html
- SDK type: `dynamodb.ExecuteTransactionInput` / `dynamodb.ExecuteTransactionOutput`
- Target: `DynamoDB_20120810.ExecuteTransaction`
- Last verified: 2026-06-06

## Request

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| TransactStatements | []ParameterizedStatement | Yes | 1–100 items |
| ClientRequestToken | String | No | Idempotency token, 1–36 chars |
| ReturnConsumedCapacity | String | No | INDEXES \| TOTAL \| NONE |

### ParameterizedStatement

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| Statement | String | Yes | PartiQL string |
| Parameters | []AttributeValue | No | Positional `?` substitutions |
| ReturnValuesOnConditionCheckFailure | String | No | ALL_OLD \| NONE |

## Response

| Field | Notes |
|-------|-------|
| Responses | []ItemResponse — one per statement; for reads each `Item` holds the result, for writes each ItemResponse is empty (`{}`) |
| ConsumedCapacity | When ReturnConsumedCapacity != NONE |

### ItemResponse

| Field | Notes |
|-------|-------|
| Item | Result item; omitted (empty object `{}`) when item not found |

## Limits

- 1–100 statements per transaction.
- Must be all reads (SELECT) or all writes (INSERT/UPDATE/DELETE) — no mixing.
- SELECT in a transaction must specify exact key equality (point lookup only).
- Item size max: 400 KB.

## Errors

| Error | HTTP |
|-------|------|
| ValidationException | 400 |
| ResourceNotFoundException | 400 |
| TransactionCanceledException | 400 — with CancellationReasons |
| InternalServerError | 500 |

## Kumolo deviations

- `ClientRequestToken` (idempotency) is accepted but not enforced.
- Reads: translated to TransactGetItems.
- Writes: translated to TransactWriteItems (existing transaction storage).
