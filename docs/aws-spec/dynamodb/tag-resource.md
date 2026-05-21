# DynamoDB — TagResource / UntagResource / ListTagsOfResource

- Official URLs:
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TagResource.html
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UntagResource.html
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListTagsOfResource.html
- SDK structs:
  - `dynamodb.TagResourceInput` / `dynamodb.TagResourceOutput`
  - `dynamodb.UntagResourceInput` / `dynamodb.UntagResourceOutput`
  - `dynamodb.ListTagsOfResourceInput` / `dynamodb.ListTagsOfResourceOutput`
- Last verified: 2026-05-21

## TagResource

### Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `ResourceArn` | string | yes | Table ARN; 1–1283 chars |
| `Tags` | []Tag | yes | Each Tag: `Key` (1–128 chars), `Value` (0–256 chars) |

### Response

HTTP 200, empty body.

## UntagResource

### Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `ResourceArn` | string | yes | Table ARN; 1–1283 chars |
| `TagKeys` | []string | yes | Keys to remove; non-existent keys silently ignored |

### Response

HTTP 200, empty body.

## ListTagsOfResource

### Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `ResourceArn` | string | yes | Table ARN; 1–1283 chars |
| `NextToken` | string | no | Pagination cursor (ignored by kumolo) |

### Response

| Field | Notes |
|---|---|
| `Tags` | []Tag with `Key` and `Value` |
| `NextToken` | Absent in kumolo (pagination not implemented) |

## Implemented Errors (all three operations)

| Error | HTTP | Condition |
|---|---|---|
| `ResourceNotFoundException` | 400 | Table ARN not found |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- `ResourceArn` must be a table ARN; index ARNs are not supported.
- `LimitExceededException` and `ResourceInUseException` are not enforced.
- **ListTagsOfResource pagination is not implemented**: `NextToken` is accepted but ignored; all tags are returned in a single response.
- Real AWS rate limits (TagResource/UntagResource: 5/sec; ListTagsOfResource: 10/sec) are not enforced.
