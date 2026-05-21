# DynamoDB — ListTables

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListTables.html
- SDK struct: `dynamodb.ListTablesInput` / `dynamodb.ListTablesOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `Limit` | integer | no | Max table names per page; range 1–100; default 100 |
| `ExclusiveStartTableName` | string | no | Pagination cursor; value of `LastEvaluatedTableName` from prior response |

## Response

| Field | Notes |
|---|---|
| `TableNames` | []string; max 100 per page; sorted alphabetically |
| `LastEvaluatedTableName` | Omitted when on the last page; present when more pages remain |

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- **Pagination is not implemented**: `Limit` and `ExclusiveStartTableName` are parsed but ignored; all tables are always returned in a single response without `LastEvaluatedTableName`. Real AWS caps at 100 per page.
- `ValidationException` for out-of-range `Limit` (< 1 or > 100) is not enforced.
