# ExecuteStatement

- URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ExecuteStatement.html
- SDK type: `dynamodb.ExecuteStatementInput` / `dynamodb.ExecuteStatementOutput`
- Target: `DynamoDB_20120810.ExecuteStatement`
- Last verified: 2026-06-06

## Request

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| Statement | String | Yes | PartiQL string, 1–8192 chars |
| Parameters | []AttributeValue | No | Positional `?` substitutions |
| ConsistentRead | Boolean | No | Default false |
| Limit | Integer | No | ≥ 1 |
| NextToken | String | No | Opaque pagination token, 1–32768 chars |
| ReturnConsumedCapacity | String | No | INDEXES \| TOTAL \| NONE |
| ReturnValuesOnConditionCheckFailure | String | No | ALL_OLD \| NONE |

## Response

| Field | Notes |
|-------|-------|
| Items | Array of items (read ops only) |
| NextToken | Present when more results exist |
| ConsumedCapacity | When ReturnConsumedCapacity != NONE |

## Supported PartiQL statements

- `SELECT * FROM "table" [WHERE pk = ? [AND sk op ?]] [LIMIT n]`
- `INSERT INTO "table" VALUE {'attr': ?, ...}`
- `UPDATE "table" SET attr = ? WHERE pk = ? [AND sk = ?]`
- `DELETE FROM "table" WHERE pk = ? [AND sk = ?]`

## Execution mapping

| PartiQL | Storage method |
|---------|---------------|
| SELECT with hash key equality | Query |
| SELECT with hash+sort key equality | GetItem |
| SELECT without key conditions | Scan |
| INSERT | PutItem |
| UPDATE | UpdateItem (key from WHERE, SET → updates map) |
| DELETE | DeleteItem (key from WHERE) |

## Errors

| Error | HTTP |
|-------|------|
| ValidationException | 400 |
| ResourceNotFoundException | 400 |
| ConditionalCheckFailedException | 400 |
| InternalServerError | 500 |

## Kumolo deviations

- `ConsistentRead` is accepted but ignored (storage is always consistent).
- `NextToken` is encoded as base64(JSON(LastEvaluatedKey)).
- Literal values in PartiQL statements (non-`?`) are supported for strings, numbers, booleans, NULL. Complex literals (list `<<...>>`, nested maps) may not be supported.
- WHERE filter conditions that cannot be satisfied by key-based lookup are evaluated in-memory using DynamoDB filter expression evaluation.
- Non-equality conditions on the hash key (e.g. `pk > ?`) fall through to a full Scan with in-memory filtering, which is more permissive than real AWS (real AWS rejects these for Query).
