# RestoreObject

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_RestoreObject.html  
**SDK struct**: `s3.RestoreObjectInput` / `s3.RestoreObjectOutput`  
**Last verified**: 2026-05-21

## Request

`POST /{key}?restore HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Initiates a restore request for an object in GLACIER or DEEP_ARCHIVE storage class.
The request body is accepted but its contents (Days, Tier, etc.) are ignored — kumolo
treats any well-formed restore request as an immediate restore.

### Not implemented

- `versionId` query parameter — always restores the current version
- Restore parameters (Days, GlacierJobParameters, Tier) are parsed but not applied
- `x-amz-request-payer`, `x-amz-expected-bucket-owner` — ignored

## Response

| Status | Condition |
|---|---|
| `202 Accepted` | Restore initiated for the first time |
| `200 OK` | Restore was already initiated for this object |

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchKey` | 404 | Object does not exist |
| `InvalidObjectState` | 409 | Object is not in an archive storage class (GLACIER or DEEP_ARCHIVE) |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Restore is instantaneous — the restored state is set immediately with no delay.
- Restore expiry (`Days`) is not enforced; once restored, the object remains accessible.
- `versionId` query parameter is not supported; only current version can be restored.
