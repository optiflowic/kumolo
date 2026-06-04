# GetObject

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html  
**SDK struct**: `s3.GetObjectInput` / `s3.GetObjectOutput`  
**Last verified**: 2026-05-21

## Request

`GET /{key} HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Query parameters

| Parameter | Notes |
|---|---|
| `versionId` | Return a specific object version |
| `response-cache-control` | Override Cache-Control in response |
| `response-content-disposition` | Override Content-Disposition in response |
| `response-content-encoding` | Override Content-Encoding in response |
| `response-content-language` | Override Content-Language in response |
| `response-content-type` | Override Content-Type in response |
| `response-expires` | Override Expires in response |

### Supported request headers

| Header | Notes |
|---|---|
| `Range` | Partial content; 416 if not satisfiable |
| `If-Match` | ETag-based conditional; 412 on mismatch |
| `If-None-Match` | ETag-based conditional; 304 on match |
| `If-Modified-Since` | Time-based conditional; 304 if not modified |
| `If-Unmodified-Since` | Time-based conditional; 412 if modified |

### Not implemented headers

- `x-amz-expected-bucket-owner` — owner account ID validation
- `x-amz-request-payer` — requester-pays
- `x-amz-server-side-encryption-customer-*` — SSE-C

## Response

`HTTP/1.1 200` (or `206 Partial Content` for Range requests, `304 Not Modified` for conditional)

| Header | Condition |
|---|---|
| `Content-Type` | Always |
| `Content-Length` | Always |
| `ETag` | Always |
| `Last-Modified` | Always |
| `x-amz-version-id` | When object is versioned |
| `x-amz-delete-marker` | When object is a delete marker |
| `x-amz-meta-*` | User metadata set at upload |
| `x-amz-server-side-encryption` | When SSE was specified on upload |
| `x-amz-server-side-encryption-aws-kms-key-id` | When KMS key ID was specified |
| `x-amz-server-side-encryption-bucket-key-enabled` | `true` when BucketKeyEnabled was set and algorithm is `aws:kms` or `aws:kms:dsse` |
| `x-amz-storage-class` | When a non-STANDARD storage class was set |
| `x-amz-tagging-count` | When the object has tags |
| `x-amz-object-lock-mode` | When Object Lock retention is set |
| `x-amz-object-lock-retain-until-date` | When Object Lock retention is set |
| `x-amz-object-lock-legal-hold` | When legal hold is set |
| `Content-Range` | Only for partial content responses |

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchKey` | 404 | Object does not exist (or is a delete marker on unversioned access) |
| `NoSuchVersion` | 404 | Specified version does not exist |
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MethodNotAllowed` | 405 | Accessing a delete marker by VersionId |
| `InvalidRange` | 416 | Range header is not satisfiable |
| `InvalidObjectState` | 403 | Object is in archive storage class and not restored |
| `PreconditionFailed` | 412 | If-Match or If-Unmodified-Since failed |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- SSE-C headers are not supported.
- `response-*` query parameters override is delegated to `http.ServeContent`; only
  `response-content-type` is explicitly applied by kumolo.
