# BatchExecuteStatement

- URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchExecuteStatement.html
- SDK type: `dynamodb.BatchExecuteStatementInput` / `dynamodb.BatchExecuteStatementOutput`
- Target: `DynamoDB_20120810.BatchExecuteStatement`
- Last verified: 2026-06-06

## Request

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| Statements | []BatchStatementRequest | Yes | 1–25 items |
| ReturnConsumedCapacity | String | No | INDEXES \| TOTAL \| NONE |

### BatchStatementRequest

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| Statement | String | Yes | PartiQL string |
| Parameters | []AttributeValue | No | Positional `?` substitutions |
| ConsistentRead | Boolean | No | Default false |
| ReturnValuesOnConditionCheckFailure | String | No | ALL_OLD \| NONE |

## Response

| Field | Notes |
|-------|-------|
| Responses | []BatchStatementResponse — one per statement, same order |
| ConsumedCapacity | When ReturnConsumedCapacity != NONE |

### BatchStatementResponse

| Field | Notes |
|-------|-------|
| Item | Result item (SELECT only, if found) |
| Error | BatchStatementError (Code + Message + optional Item) if statement failed |
| TableName | Table name |

## Limits

- 1–25 statements per batch.
- Must be all reads (SELECT) or all writes (INSERT/UPDATE/DELETE) — no mixing.
- Each SELECT returns at most 1 item (must specify exact key equality).

## Errors (HTTP)

HTTP 200 is returned even when individual statements fail — check each `Error` field.
Top-level HTTP errors:

| Error | HTTP |
|-------|------|
| ValidationException | 400 |
| InternalServerError | 500 |

## Kumolo deviations

- `ConsistentRead` accepted but ignored.
- Batch is non-transactional; each statement is executed independently.
