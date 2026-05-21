# HeadBucket

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadBucket.html  
**SDK struct**: `s3.HeadBucketInput` / `s3.HeadBucketOutput`  
**Last verified**: 2026-05-21

## Request

`HEAD / HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Used to check whether a bucket exists and is accessible.

### Not implemented headers

- `x-amz-expected-bucket-owner` — owner account ID validation

## Response

`HTTP/1.1 200`

| Header | Value |
|---|---|
| `x-amz-bucket-region` | AWS Region code where the bucket resides |

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |

Note: AWS returns a generic 400/403/404 without a response body for HeadBucket errors.
kumolo returns 404 with no body for missing buckets, which matches the documented behavior.

## Kumolo deviations

- `x-amz-expected-bucket-owner` header is ignored (no multi-account support).
- `x-amz-access-point-alias` response header is not returned (no access point support).
