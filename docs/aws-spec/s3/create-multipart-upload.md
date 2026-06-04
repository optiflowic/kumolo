# CreateMultipartUpload

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateMultipartUpload.html  
**SDK struct**: `s3.CreateMultipartUploadInput` / `s3.CreateMultipartUploadOutput`  
**Last verified**: 2026-05-21

## Request

`POST /{key}?uploads HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Initiates a multipart upload and returns an `UploadId`.

### Request headers

| Header | Notes |
|---|---|
| `Content-Type` | Stored; defaults to `application/octet-stream` |
| `x-amz-meta-*` | User metadata stored with the final object |
| `x-amz-server-side-encryption` | Stored as metadata (AES256 / aws:kms / aws:kms:dsse); no actual encryption |
| `x-amz-server-side-encryption-aws-kms-key-id` | For aws:kms / aws:kms:dsse: resolved to canonical ARN via KMS and stored; see `sse-algorithm-validation.md` |
| `x-amz-server-side-encryption-bucket-key-enabled` | Stored as metadata; only meaningful for `aws:kms` / `aws:kms:dsse` — see `sse-bucket-key-enabled.md` |
| `x-amz-storage-class` | Stored; returned on GetObject/HeadObject |
| `x-amz-object-lock-mode` | GOVERNANCE or COMPLIANCE |
| `x-amz-object-lock-retain-until-date` | RFC3339 timestamp |
| `x-amz-object-lock-legal-hold` | ON or OFF |

### Not implemented headers

- `x-amz-acl` / `x-amz-grant-*` — ACL on upload
- `x-amz-expected-bucket-owner` — owner account ID validation
- `x-amz-request-payer` — requester-pays
- `x-amz-server-side-encryption-customer-*` — SSE-C
- `x-amz-tagging` — URL-encoded tag set

## Response

`HTTP/1.1 200`

```xml
<InitiateMultipartUploadResult>
  <Bucket>string</Bucket>
  <Key>string</Key>
  <UploadId>string</UploadId>
</InitiateMultipartUploadResult>
```

| Header | Condition |
|---|---|
| `x-amz-server-side-encryption` | Explicit header, or bucket default encryption when no explicit header is provided |
| `x-amz-server-side-encryption-aws-kms-key-id` | Explicit header KMS key ID, or bucket default KMS key when no explicit header is provided |
| `x-amz-server-side-encryption-bucket-key-enabled` | `true` when BucketKeyEnabled was set and algorithm is `aws:kms` or `aws:kms:dsse` |

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- SSE headers are stored in metadata but no actual encryption is applied.
- Tags (`x-amz-tagging`) are not applied to the final object after CompleteMultipartUpload.
