# HeadObject

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadObject.html  
**SDK struct**: `s3.HeadObjectInput` / `s3.HeadObjectOutput`  
**Last verified**: 2026-05-21

## Request

`HEAD /{key} HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Returns the same metadata as GetObject but without the body.

### Query parameters

| Parameter | Notes |
|---|---|
| `versionId` | Return metadata for a specific version |

### Supported request headers

| Header | Notes |
|---|---|
| `If-Match` | ETag-based conditional; 412 on mismatch |
| `If-None-Match` | ETag-based conditional; 304 on match |
| `If-Modified-Since` | Time-based conditional; 304 if not modified |
| `If-Unmodified-Since` | Time-based conditional; 412 if modified |

### Not implemented headers

- `x-amz-expected-bucket-owner` — owner account ID validation
- `x-amz-request-payer` — requester-pays
- `x-amz-server-side-encryption-customer-*` — SSE-C
- `x-amz-checksum-mode` — return checksum in response

## Response

`HTTP/1.1 200`

| Header | Condition |
|---|---|
| `Content-Type` | Always |
| `Content-Length` | Always |
| `ETag` | Always |
| `Last-Modified` | Always |
| `Accept-Ranges` | Always (`bytes`) |
| `x-amz-version-id` | When object is versioned |
| `x-amz-delete-marker` | When the object is a delete marker |
| `x-amz-meta-*` | User metadata |
| `x-amz-server-side-encryption` | When SSE was specified |
| `x-amz-server-side-encryption-aws-kms-key-id` | When KMS key ID was specified |
| `x-amz-storage-class` | When non-STANDARD storage class |
| `x-amz-tagging-count` | When the object has tags |
| `x-amz-object-lock-mode` | When Object Lock retention is set |
| `x-amz-object-lock-retain-until-date` | When Object Lock retention is set |
| `x-amz-object-lock-legal-hold` | When legal hold is set |

## Errors

HeadObject returns status codes without a response body (per spec).

| HTTP Status | Condition |
|---|---|
| 404 | Object or bucket not found; or object is a delete marker (unversioned access) |
| 405 | Accessing a delete marker by VersionId |
| 304 | Conditional check: not modified |
| 412 | Conditional check: precondition failed |
| 500 | Storage failure |

## Kumolo deviations

- SSE-C headers are not supported.
- `x-amz-checksum-mode` / checksum response headers are not returned.
