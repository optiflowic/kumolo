# DynamoDB — UpdateTable

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateTable.html
- SDK struct: `dynamodb.UpdateTableInput` / `dynamodb.UpdateTableOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | 1–1024 chars |
| `BillingMode` | string | no | `PROVISIONED` or `PAY_PER_REQUEST` |
| `ProvisionedThroughput` | object | no | `ReadCapacityUnits`, `WriteCapacityUnits` |
| `AttributeDefinitions` | []object | no | Required when adding a new GSI |
| `GlobalSecondaryIndexUpdates` | []object | no | Each element: `Create`, `Update`, or `Delete` with `IndexName` |

## Ignored Parameters

`DeletionProtectionEnabled`, `SSESpecification`, `StreamSpecification`, `TableClass`, `OnDemandThroughput`, `WarmThroughput`, `ReplicaUpdates` — accepted without error, not stored.

## Response

`TableDescription` object. Same shape as CreateTable/DescribeTable. `TableStatus` is `ACTIVE` (kumolo does not transition to `UPDATING`).

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ResourceNotFoundException` | 400 | Table does not exist |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- Update is synchronous; real AWS transitions `ACTIVE` → `UPDATING` → `ACTIVE` asynchronously.
- `ResourceInUseException` (table in UPDATING state, concurrent update) is not enforced.
- `LimitExceededException` (concurrent operation limits) is not enforced.
- Real AWS allows only one GSI Create or Delete per UpdateTable call; kumolo does not enforce this.
- ARN form of `TableName` is not accepted.
