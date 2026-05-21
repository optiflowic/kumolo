# DynamoDB — UpdateTimeToLive / DescribeTimeToLive

- Official URLs:
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateTimeToLive.html
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeTimeToLive.html
- SDK structs:
  - `dynamodb.UpdateTimeToLiveInput` / `dynamodb.UpdateTimeToLiveOutput`
  - `dynamodb.DescribeTimeToLiveInput` / `dynamodb.DescribeTimeToLiveOutput`
- Last verified: 2026-05-21

## UpdateTimeToLive

### Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | 1–1024 chars |
| `TimeToLiveSpecification` | object | yes | |
| `TimeToLiveSpecification.AttributeName` | string | yes | Attribute name that holds epoch-seconds TTL value |
| `TimeToLiveSpecification.Enabled` | bool | yes | Enable or disable TTL |

### Response

| Field | Notes |
|---|---|
| `TimeToLiveSpecification` | `{AttributeName, Enabled}` echoed from request |

## DescribeTimeToLive

### Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | 1–1024 chars |

### Response

| Field | Notes |
|---|---|
| `TimeToLiveDescription.TimeToLiveStatus` | `ENABLED` or `DISABLED`; kumolo has no ENABLING/DISABLING transition |
| `TimeToLiveDescription.AttributeName` | Present only when status is `ENABLED` |

## Implemented Errors (both operations)

| Error | HTTP | Condition |
|---|---|---|
| `ResourceNotFoundException` | 400 | Table does not exist |
| `InternalServerError` | 500 | Storage failure |

## TTL Behavior

- TTL attribute must be a Number type containing epoch seconds.
- Items with TTL value < current time are filtered out on every read path (`GetItem`, `BatchGetItem`, `Query`, `Scan`).
- kumolo applies TTL expiry eagerly on read; real AWS deletes expired items eventually (within ~2 days).

## kumolo-Specific Deviations

- `TimeToLiveStatus` transitions directly between `ENABLED` and `DISABLED`; real AWS has intermediate `ENABLING`/`DISABLING` states that can take up to 1 hour.
- `LimitExceededException` and `ResourceInUseException` are not enforced.
- ARN form of `TableName` is not accepted.
