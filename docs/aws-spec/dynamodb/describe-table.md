# DynamoDB — DescribeTable

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeTable.html
- SDK struct: `dynamodb.DescribeTableInput` / `dynamodb.DescribeTableOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | 1–1024 chars |

## Response

`Table` object (TableDescription). Key fields returned by kumolo:

| Field | Notes |
|---|---|
| `TableName` | |
| `TableStatus` | `ACTIVE` (kumolo always ACTIVE for existing tables) |
| `TableArn` | synthetic ARN |
| `CreationDateTime` | Unix epoch float64 |
| `KeySchema`, `AttributeDefinitions` | |
| `BillingModeSummary` | present when BillingMode was set at creation |
| `ProvisionedThroughput` | present when PROVISIONED |
| `GlobalSecondaryIndexes`, `LocalSecondaryIndexes` | IndexStatus always `ACTIVE` |
| `ItemCount`, `TableSizeBytes` | always 0 (real AWS: approximate, updated every ~6h) |

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ResourceNotFoundException` | 400 | Table does not exist |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- ARN form of `TableName` is not accepted.
- `TableId`, `SSEDescription`, `StreamSpecification`, `TableClassSummary`, `DeletionProtectionEnabled`, `RestoreSummary`, `ArchivalSummary`, `Replicas`, `GlobalTableVersion`, `WarmThroughput`, `OnDemandThroughput` are not returned.
- `ItemCount` and `TableSizeBytes` are always 0; real AWS approximates from periodic snapshots.
- Real AWS DescribeTable may return `ResourceNotFoundException` briefly after CreateTable due to eventual consistency; kumolo returns the table immediately.
