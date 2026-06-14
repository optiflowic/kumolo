# DynamoDB — TagResource / UntagResource / ListTagsOfResource

- Official URLs:
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TagResource.html
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UntagResource.html
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListTagsOfResource.html
- SDK structs:
  - `dynamodb.TagResourceInput` / `dynamodb.TagResourceOutput`
  - `dynamodb.UntagResourceInput` / `dynamodb.UntagResourceOutput`
  - `dynamodb.ListTagsOfResourceInput` / `dynamodb.ListTagsOfResourceOutput`
- Last verified: 2026-06-14

## TagResource

### Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `ResourceArn` | string | yes | Table ARN or GSI/LSI index ARN; 1–1283 chars |
| `Tags` | []Tag | yes | Each Tag: `Key` (1–128 chars), `Value` (0–256 chars) |

### Response

HTTP 200, empty body.

## UntagResource

### Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `ResourceArn` | string | yes | Table ARN or GSI/LSI index ARN; 1–1283 chars |
| `TagKeys` | []string | yes | Keys to remove; non-existent keys silently ignored |

### Response

HTTP 200, empty body.

## ListTagsOfResource

### Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `ResourceArn` | string | yes | Table ARN or GSI/LSI index ARN; 1–1283 chars |
| `NextToken` | string | no | Pagination cursor (ignored by kumolo) |

### Response

| Field | Notes |
|---|---|
| `Tags` | []Tag with `Key` and `Value` |
| `NextToken` | Absent in kumolo (pagination not implemented) |

## Implemented Errors

### TagResource

| Error | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | Tag key is empty or exceeds 128 chars; tag value exceeds 256 chars |
| `LimitExceededException` | 400 | Adding tags would result in more than 50 tags on the table |
| `ResourceNotFoundException` | 400 | Table or index ARN not found |
| `InternalServerError` | 500 | Storage failure |

### UntagResource

| Error | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | Any key in `TagKeys` is empty |
| `ResourceNotFoundException` | 400 | Table or index ARN not found |
| `InternalServerError` | 500 | Storage failure |

### ListTagsOfResource

| Error | HTTP | Condition |
|---|---|---|
| `ResourceNotFoundException` | 400 | Table or index ARN not found |
| `InternalServerError` | 500 | Storage failure |

## kumolo-Specific Deviations

- `ResourceInUseException` is not enforced.
- **ListTagsOfResource pagination is not implemented**: `NextToken` is accepted but ignored; all tags are returned in a single response.
- Real AWS rate limits (TagResource/UntagResource: 5/sec; ListTagsOfResource: 10/sec) are not enforced.
