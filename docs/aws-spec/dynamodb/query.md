# DynamoDB — Query

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Query.html
- SDK struct: `dynamodb.QueryInput` / `dynamodb.QueryOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | 1–1024 chars |
| `KeyConditionExpression` | string | yes | Partition key equality required; sort key: =, <, <=, >, >=, BETWEEN, begins_with |
| `FilterExpression` | string | no | Applied after read; cannot reference partition or sort key attributes |
| `IndexName` | string | no | LSI or GSI name; 3–255 chars |
| `ExclusiveStartKey` | map | no | Pagination cursor |
| `Limit` | integer | no | Max items to evaluate (before FilterExpression); min 1 |
| `ScanIndexForward` | bool | no | `true` (default) = ascending; `false` = descending by sort key |
| `Select` | string | no | `ALL_ATTRIBUTES` / `ALL_PROJECTED_ATTRIBUTES` (GSI/LSI only) / `SPECIFIC_ATTRIBUTES` / `COUNT` |
| `ProjectionExpression` | string | no | Comma-separated attribute paths |
| `ExpressionAttributeNames` | map | no | `#name` substitutions |
| `ExpressionAttributeValues` | map | no | `:val` substitutions |

## Ignored Parameters

`ConsistentRead`, `ReturnConsumedCapacity` — accepted without error, ignored.

Legacy: `KeyConditions`, `QueryFilter`, `ConditionalOperator`, `AttributesToGet` — not implemented.

## Response

| Field | Notes |
|---|---|
| `Items` | Matching items after FilterExpression; absent when `Select=COUNT` |
| `Count` | Items returned after filter |
| `ScannedCount` | Items read before FilterExpression |
| `LastEvaluatedKey` | Pagination cursor; omitted on last page; presence does not guarantee more items |

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | Missing TableName or KeyConditionExpression; invalid Limit (< 1); invalid Select; unused expression refs |
| `ResourceNotFoundException` | 400 | Table or index does not exist |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- `ConsistentRead=true` for GSI (which real AWS rejects) is accepted without error.
- `ReturnConsumedCapacity` is not returned.
- A Query that hits the 1 MB page limit mid-result returns `LastEvaluatedKey`; kumolo loads all results into memory first (no 1 MB streaming limit per call, but Limit is still respected).
- ARN form of `TableName` is not accepted.
