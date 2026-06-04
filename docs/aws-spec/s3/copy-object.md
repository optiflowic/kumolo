# CopyObject

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CopyObject.html  
**SDK struct**: `s3.CopyObjectInput` / `s3.CopyObjectOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /{key} HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`  
`x-amz-copy-source: /{source-bucket}/{source-key}`

Destination bucket and key are from the URL. Source is specified via the `x-amz-copy-source` header.
Source may include a `?versionId=<id>` query string to copy a specific version.

### Request headers

| Header | Notes |
|---|---|
| `x-amz-copy-source` | Required; `/{bucket}/{key}[?versionId=<id>]`; URL-encoded |
| `x-amz-metadata-directive` | `COPY` (default) or `REPLACE`; controls whether metadata is inherited or replaced |
| `Content-Type` | Only applied when `x-amz-metadata-directive: REPLACE` |
| `x-amz-meta-*` | Only applied when `x-amz-metadata-directive: REPLACE` |
| `x-amz-server-side-encryption` | Stored as metadata on the destination |
| `x-amz-server-side-encryption-aws-kms-key-id` | Stored as metadata on the destination |
| `x-amz-server-side-encryption-bucket-key-enabled` | Stored as metadata; only meaningful for `aws:kms` / `aws:kms:dsse` — see `sse-bucket-key-enabled.md` |
| `x-amz-storage-class` | Storage class for the destination object |
| `x-amz-object-lock-mode` | GOVERNANCE or COMPLIANCE |
| `x-amz-object-lock-retain-until-date` | RFC3339 timestamp |
| `x-amz-object-lock-legal-hold` | ON or OFF |

### Not implemented headers

- `x-amz-copy-source-if-*` — conditional copy preconditions
- `x-amz-tagging` / `x-amz-tagging-directive` — tag copying
- `x-amz-acl` / `x-amz-grant-*` — ACL on copy
- `x-amz-expected-bucket-owner` / `x-amz-source-expected-bucket-owner`
- `x-amz-server-side-encryption-customer-*` — SSE-C

## Response

`HTTP/1.1 200`

```xml
<CopyObjectResult>
  <ETag>string</ETag>
  <LastModified>timestamp</LastModified>
</CopyObjectResult>
```

| Header | Condition |
|---|---|
| `x-amz-version-id` | When versioning is enabled on the destination bucket |
| `x-amz-server-side-encryption` | Explicit header, or bucket default encryption when no explicit header is provided |
| `x-amz-server-side-encryption-aws-kms-key-id` | Explicit header KMS key ID, or bucket default KMS key when no explicit header is provided |
| `x-amz-server-side-encryption-bucket-key-enabled` | `true` when BucketKeyEnabled was set and algorithm is `aws:kms` or `aws:kms:dsse` |

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Source or destination bucket does not exist |
| `NoSuchKey` | 404 | Source object does not exist |
| `InvalidArgument` | 400 | Missing or malformed `x-amz-copy-source` |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- `x-amz-copy-source-if-*` conditional headers are not evaluated.
- Tags are not copied regardless of `x-amz-tagging-directive`.
- SSE headers are stored in metadata but no actual encryption is applied.
