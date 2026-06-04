# PutObject

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html  
**SDK struct**: `s3.PutObjectInput` / `s3.PutObjectOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /{key} HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Supported request headers

| Header | Notes |
|---|---|
| `Content-Type` | Stored; defaults to `application/octet-stream` |
| `Content-MD5` | Validated against actual body; 400 BadDigest on mismatch |
| `Content-Length` | Standard HTTP header |
| `x-amz-meta-*` | Arbitrary user metadata (lowercased key suffix) |
| `x-amz-tagging` | URL-encoded tag set |
| `x-amz-storage-class` | Stored; returned on GetObject/HeadObject |
| `x-amz-server-side-encryption` | Stored as metadata only (AES256 / aws:kms); no actual encryption |
| `x-amz-server-side-encryption-aws-kms-key-id` | Stored as metadata only |
| `x-amz-server-side-encryption-bucket-key-enabled` | Stored as metadata; only meaningful for `aws:kms` / `aws:kms:dsse` — see `sse-bucket-key-enabled.md` |
| `x-amz-checksum-crc32` / `crc32c` / `sha1` / `sha256` | Validated against body |
| `x-amz-sdk-checksum-algorithm` | Selects which checksum header to validate |
| `x-amz-object-lock-mode` | GOVERNANCE or COMPLIANCE; requires Object Lock enabled bucket |
| `x-amz-object-lock-retain-until-date` | ISO 8601 timestamp for retention |
| `x-amz-object-lock-legal-hold` | ON or OFF |
| `If-None-Match: *` | Conditional PUT; fails with 412 if object already exists |

### Not implemented headers

- `x-amz-acl` / `x-amz-grant-*` — ACL on upload
- `x-amz-expected-bucket-owner` — owner account ID validation
- `x-amz-request-payer` — requester-pays
- `x-amz-website-redirect-location` — stored but not applied
- `x-amz-server-side-encryption-customer-*` — SSE-C

## Response

`HTTP/1.1 200`

| Header | Condition |
|---|---|
| `ETag` | Always; MD5 hex quoted (or checksum-based for multipart) |
| `x-amz-version-id` | When versioning is enabled on the bucket |
| `x-amz-checksum-crc32` / `crc32c` / `sha1` / `sha256` | When checksum was provided |
| `x-amz-server-side-encryption` | When SSE is applied (explicit header or bucket default encryption) |
| `x-amz-server-side-encryption-aws-kms-key-id` | When KMS key ID is applied (explicit header or bucket default encryption) |
| `x-amz-server-side-encryption-bucket-key-enabled` | `true` when BucketKeyEnabled was set and algorithm is `aws:kms` or `aws:kms:dsse` |

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `BadDigest` | 400 | Content-MD5 or checksum header mismatch |
| `PreconditionFailed` | 412 | `If-None-Match: *` and object already exists |
| `NoSuchBucket` | 404 | Bucket does not exist |
| `AccessDenied` | 403 | Object Lock prevents overwrite |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- SSE (x-amz-server-side-encryption) headers are stored in metadata but no actual encryption is applied.
- ACL-related headers are ignored.
- `x-amz-website-redirect-location` is accepted but not used in redirects.
