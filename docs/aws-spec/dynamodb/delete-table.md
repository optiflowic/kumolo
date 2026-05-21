# DynamoDB — DeleteTable

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DeleteTable.html
- SDK struct: `dynamodb.DeleteTableInput` / `dynamodb.DeleteTableOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | 1–1024 chars |

## Response

`TableDescription` object with `TableStatus: "DELETING"`. Same shape as CreateTable/DescribeTable response.

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ResourceNotFoundException` | 400 | Table does not exist |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- Deletion is synchronous; real AWS transitions to `DELETING` and deletes asynchronously.
- `ResourceInUseException` (table in CREATING/UPDATING state) is not enforced.
- `LimitExceededException` (concurrent operation limits) is not enforced.
- `DeletionProtectionEnabled` is not checked before deletion.
- ARN form of `TableName` is not accepted.
