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
| `ValidationException` | 400 | Invalid request body; `Limit` out of range [1–100] |
| `InternalServerError` | 500 | Storage failure |
