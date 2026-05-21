# DynamoDB — DescribeContinuousBackups / UpdateContinuousBackups

- Official URLs:
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeContinuousBackups.html
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateContinuousBackups.html
- SDK structs:
  - `dynamodb.DescribeContinuousBackupsInput` / `dynamodb.DescribeContinuousBackupsOutput`
  - `dynamodb.UpdateContinuousBackupsInput` / `dynamodb.UpdateContinuousBackupsOutput`
- Last verified: 2026-05-21

## DescribeContinuousBackups

### Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | 1–1024 chars |

### Response

`ContinuousBackupsDescription` object:

| Field | Notes |
|---|---|
| `ContinuousBackupsStatus` | Always `ENABLED` (real AWS: always ENABLED at table creation) |
| `PointInTimeRecoveryDescription.PointInTimeRecoveryStatus` | `ENABLED` or `DISABLED` |
| `PointInTimeRecoveryDescription.EarliestRestorableDateTime` | Present when PITR enabled; set to (enabledAt + 5 min) |
| `PointInTimeRecoveryDescription.LatestRestorableDateTime` | Present when PITR enabled; set to (now - 5 min) |

### Errors

| Error | HTTP | Condition |
|---|---|---|
| `TableNotFoundException` | 400 | Table does not exist |
| `InternalServerError` | 500 | Storage failure |

## UpdateContinuousBackups

### Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | 1–1024 chars |
| `PointInTimeRecoverySpecification` | object | yes | |
| `PointInTimeRecoverySpecification.PointInTimeRecoveryEnabled` | bool | yes | Enable or disable PITR |

Spec also supports `RecoveryPeriodInDays` (1–35) in `PointInTimeRecoverySpecification`, but kumolo does not implement this field.

### Response

Same `ContinuousBackupsDescription` shape as DescribeContinuousBackups.

### Errors

| Error | HTTP | Condition |
|---|---|---|
| `TableNotFoundException` | 400 | Table does not exist |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- `RecoveryPeriodInDays` is accepted but ignored.
- `ContinuousBackupsUnavailableException` is not enforced.
- `EarliestRestorableDateTime` and `LatestRestorableDateTime` are synthetic approximations; no actual backup data is stored.
- ARN form of `TableName` is not accepted.
