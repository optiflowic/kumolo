# DeleteObject

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObject.html  
**SDK struct**: `s3.DeleteObjectInput` / `s3.DeleteObjectOutput`  
**Last verified**: 2026-05-21

## Request

`DELETE /{key} HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Query parameters

| Parameter | Notes |
|---|---|
| `versionId` | Permanently delete a specific object version |

### Request headers

| Header | Notes |
|---|---|
| `x-amz-bypass-governance-retention` | Set to `true` to bypass GOVERNANCE-mode Object Lock |

### Not implemented headers

- `x-amz-expected-bucket-owner` — owner account ID validation
- `x-amz-mfa` — MFA delete token
- `x-amz-request-payer` — requester-pays

## Response

`HTTP/1.1 204 No Content`

| Header | Condition |
|---|---|
| `x-amz-version-id` | When a specific version was deleted, or when versioning created a delete marker |
| `x-amz-delete-marker` | `true` when a delete marker was created or when the deleted version was itself a marker |

Deleting a non-existent object or non-existent version returns 204 (success, no error).

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `AccessDenied` | 403 | Object is protected by Object Lock |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- `x-amz-mfa` is ignored (no MFA delete support).
