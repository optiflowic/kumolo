# CreateBucket

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateBucket.html  
**SDK struct**: `s3.CreateBucketInput` / `s3.CreateBucketOutput`  
**Last verified**: 2026-05-21

## Request

`PUT / HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Supported headers

| Header | Notes |
|---|---|
| `x-amz-bucket-object-lock-enabled` | Enables Object Lock on the bucket |

### Not implemented headers

- `x-amz-acl` — canned ACL (ACL support not in scope)
- `x-amz-grant-*` — explicit grants (ACL support not in scope)
- `x-amz-object-ownership` — Object Ownership setting
- `x-amz-bucket-namespace` — account-regional vs global namespace

### Request body

`CreateBucketConfiguration` XML with `LocationConstraint` is accepted by the AWS SDK but
kumolo ignores it. The region is taken from the SigV4 credential scope instead.

## Response

`HTTP/1.1 200`

| Header | Value |
|---|---|
| `Location` | `/{bucket}` |

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `BucketAlreadyOwnedByYou` | 409 | Bucket already exists and is owned by the same (logical) account |
| `InternalError` | 500 | Storage failure |

Note: AWS also returns `BucketAlreadyExists` (409) when another account owns the bucket.
kumolo has no multi-account concept, so it always returns `BucketAlreadyOwnedByYou`.

## Kumolo deviations

- Region is read from SigV4 credential scope, not from `LocationConstraint` in the request body.
  This works correctly when the SDK is configured with an endpoint URL override.
- ACL and Object Ownership headers are ignored.
- `BucketAlreadyExists` is never returned (single-account emulator).
