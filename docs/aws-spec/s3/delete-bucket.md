# DeleteBucket

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucket.html  
**SDK struct**: `s3.DeleteBucketInput` / `s3.DeleteBucketOutput`  
**Last verified**: 2026-05-21

## Request

`DELETE / HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

All objects (including all versions and delete markers) must be removed before deleting the bucket.

### Not implemented headers

- `x-amz-expected-bucket-owner` — owner account ID validation

## Response

`HTTP/1.1 204` (empty body)

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `BucketNotEmpty` | 409 | Bucket still contains objects or versions |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- `x-amz-expected-bucket-owner` header is ignored (no multi-account support).
