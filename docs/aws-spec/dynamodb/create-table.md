# DynamoDB — CreateTable

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_CreateTable.html
- SDK struct: `dynamodb.CreateTableInput` / `dynamodb.CreateTableOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | 1–1024 chars |
| `KeySchema` | []KeySchemaElement | yes | Exactly 1 HASH + optional 1 RANGE; each AttributeName must appear in AttributeDefinitions |
| `AttributeDefinitions` | []AttributeDefinition | yes | `AttributeType`: S/N/B; only key attributes |
| `BillingMode` | string | no | `PROVISIONED` (default) or `PAY_PER_REQUEST` |
| `ProvisionedThroughput` | object | no | Required when BillingMode=PROVISIONED; fields: `ReadCapacityUnits`, `WriteCapacityUnits` |
| `GlobalSecondaryIndexes` | []object | no | Max 20; fields: `IndexName`, `KeySchema`, `Projection`, `ProvisionedThroughput` |
| `LocalSecondaryIndexes` | []object | no | Max 5; share table partition key; must have sort key; cannot be added post-creation |

## Ignored Parameters

`StreamSpecification`, `SSESpecification`, `TableClass`, `Tags`, `DeletionProtectionEnabled`, `OnDemandThroughput`, `WarmThroughput`, `ResourcePolicy` — accepted without error, not stored.

## Response

`TableDescription` object. Key fields:

| Field | Notes |
|---|---|
| `TableName` | |
| `TableStatus` | kumolo returns `ACTIVE` immediately (real AWS: `CREATING` → `ACTIVE` async) |
| `TableArn` | synthetic ARN: `arn:aws:dynamodb:us-east-1:000000000000:table/{name}` |
| `CreationDateTime` | Unix epoch float64 |
| `KeySchema`, `AttributeDefinitions` | echoed from request |
| `BillingModeSummary` | present when BillingMode was specified |
| `ProvisionedThroughput` | present when PROVISIONED |
| `GlobalSecondaryIndexes`, `LocalSecondaryIndexes` | IndexStatus always `ACTIVE` |
| `ItemCount`, `TableSizeBytes` | always 0 at creation |

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ResourceInUseException` | 400 | Table already exists |
| `ValidationException` | 400 | Missing/invalid TableName, KeySchema, AttributeDefinitions; index validation failures |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- `TableStatus` is `ACTIVE` immediately; real AWS returns `CREATING` and transitions async.
- ARN form of `TableName` is not accepted.
- `TableId`, `SSEDescription`, `StreamSpecification`, `TableClassSummary`, `DeletionProtectionEnabled` are not returned.
- `LimitExceededException` (500 concurrent table operations, 2500 table soft quota) is not enforced.
- `ProvisionedThroughput` is not required when `BillingMode=PROVISIONED` (kumolo accepts both modes without enforcement).
