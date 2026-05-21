# DynamoDB — Kinesis Streaming Destination

- Official URLs:
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeKinesisStreamingDestination.html
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_EnableKinesisStreamingDestination.html
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DisableKinesisStreamingDestination.html
- SDK structs:
  - `dynamodb.DescribeKinesisStreamingDestinationInput` / `dynamodb.DescribeKinesisStreamingDestinationOutput`
  - `dynamodb.EnableKinesisStreamingDestinationInput` / `dynamodb.EnableKinesisStreamingDestinationOutput`
  - `dynamodb.DisableKinesisStreamingDestinationInput` / `dynamodb.DisableKinesisStreamingDestinationOutput`
- Last verified: 2026-05-21

## DescribeKinesisStreamingDestination

### Request Parameters

| Parameter | Type | Required |
|---|---|---|
| `TableName` | string | yes |

### Response

| Field | Notes |
|---|---|
| `TableName` | 3–255 chars |
| `KinesisDataStreamDestinations` | []KinesisDataStreamDestination |

Each destination: `StreamArn`, `DestinationStatus` (ENABLING/ACTIVE/DISABLING/DISABLED/ENABLE_FAILED/UPDATING), `ApproximateCreationDateTimePrecision`.

## EnableKinesisStreamingDestination

### Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | |
| `StreamArn` | string | yes | Must match Kinesis stream ARN pattern: `arn:(aws|aws-cn|aws-us-gov):kinesis:<region>:<account>:stream/<name>` |
| `EnableKinesisStreamingConfiguration.ApproximateCreationDateTimePrecision` | string | no | `MILLISECOND` (default) or `MICROSECOND` |

### Response

| Field | Notes |
|---|---|
| `TableName` | |
| `StreamArn` | |
| `DestinationStatus` | `ENABLING` (new) or `UPDATING` (re-enabling existing) |
| `EnableKinesisStreamingConfiguration` | echo of precision setting |

## DisableKinesisStreamingDestination

### Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | |
| `StreamArn` | string | yes | Must match Kinesis stream ARN pattern |

### Response

| Field | Notes |
|---|---|
| `TableName` | |
| `StreamArn` | |
| `DestinationStatus` | `DISABLING` |

## Implemented Errors (all three operations)

| Error | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | Missing TableName/StreamArn; invalid StreamArn format; invalid precision value |
| `ResourceNotFoundException` | 400 | Table not found; stream destination not found (DisableKinesisStreamingDestination) |
| `LimitExceededException` | 400 | More than 2 Kinesis destinations per table (Enable) |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- `DestinationStatus` is set to `ENABLING`/`DISABLING` immediately and never transitions to `ACTIVE`/`DISABLED`; real AWS transitions asynchronously. Callers should poll DescribeKinesisStreamingDestination.
- No actual data is streamed to Kinesis; kumolo only tracks destination configuration.
- `ResourceInUseException` (conflicting state transition) is not enforced.
- ARN form of `TableName` is not accepted.
