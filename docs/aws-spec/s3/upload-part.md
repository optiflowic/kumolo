# UploadPart

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_UploadPart.html  
**SDK struct**: `s3.UploadPartInput` / `s3.UploadPartOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /{key}?partNumber={n}&uploadId={id} HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Uploads a single part in a multipart upload. Part numbers must be between 1 and 10000.

### Query parameters

| Parameter | Notes |
|---|---|
| `uploadId` | Required; identifies the multipart upload |
| `partNumber` | Required; 1–10000 |

### Request headers

| Header | Notes |
|---|---|
| `Content-MD5` | Validated against actual body; 400 BadDigest on mismatch; part is rolled back |
| `Content-Length` | Standard HTTP header |
| `x-amz-checksum-crc32` / `crc32c` / `sha1` / `sha256` | Validated against body; part rolled back on mismatch |
| `x-amz-sdk-checksum-algorithm` | Selects which checksum header to validate |

### Not implemented headers

- `x-amz-expected-bucket-owner` — ignored
- `x-amz-request-payer` — ignored
- `x-amz-server-side-encryption-customer-*` — SSE-C

## Response

`HTTP/1.1 200`

| Header | Condition |
|---|---|
| `ETag` | Always; MD5 hex of the part body, quoted |

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchUpload` | 404 | Upload ID does not exist |
| `InvalidArgument` | 400 | Missing or invalid `uploadId` or `partNumber` |
| `BadDigest` | 400 | Content-MD5 or checksum mismatch (part is rolled back) |
| `InternalError` | 500 | Storage failure |
