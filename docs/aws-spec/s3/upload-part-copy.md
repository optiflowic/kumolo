# UploadPartCopy

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_UploadPartCopy.html  
**SDK struct**: `s3.UploadPartCopyInput` / `s3.UploadPartCopyOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /{key}?partNumber={n}&uploadId={id} HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`  
`x-amz-copy-source: /{source-bucket}/{source-key}`

Copies data from an existing object (or byte range thereof) into a multipart upload part.
Source may include a `?versionId=<id>` query string.

### Query parameters

| Parameter | Notes |
|---|---|
| `uploadId` | Required |
| `partNumber` | Required; 1–10000 |

### Request headers

| Header | Notes |
|---|---|
| `x-amz-copy-source` | Required; `/{bucket}/{key}[?versionId=<id>]` |
| `x-amz-copy-source-range` | Byte range to copy: `bytes=first-last` (inclusive) |
| `x-amz-copy-source-if-match` | ETag precondition on source; 412 if not matched |
| `x-amz-copy-source-if-none-match` | ETag precondition on source; 412 if matched |
| `x-amz-copy-source-if-modified-since` | Time precondition on source |
| `x-amz-copy-source-if-unmodified-since` | Time precondition on source |

### Not implemented headers

- `x-amz-expected-bucket-owner` / `x-amz-source-expected-bucket-owner` — ignored
- `x-amz-request-payer` — ignored
- `x-amz-server-side-encryption-customer-*` — SSE-C

## Response

`HTTP/1.1 200`

```xml
<CopyPartResult>
  <ETag>string</ETag>
  <LastModified>timestamp</LastModified>
</CopyPartResult>
```

| Header | Condition |
|---|---|
| `x-amz-copy-source-version-id` | When source object is versioned |

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchUpload` | 404 | Upload ID does not exist |
| `NoSuchBucket` | 404 | Source bucket does not exist |
| `NoSuchKey` | 404 | Source object does not exist |
| `AccessDenied` | 403 | Anonymous request denied by source object ACL or destination bucket ACL |
| `PreconditionFailed` | 412 | Copy source precondition failed |
| `InvalidArgument` | 400 | Missing or invalid parameters |
| `InternalError` | 500 | Storage failure |
