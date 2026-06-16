# PutBucketVersioning

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketVersioning.html  
**SDK struct**: `s3.PutBucketVersioningInput` / `s3.PutBucketVersioningOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?versioning HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Request body

```xml
<VersioningConfiguration>
  <Status>Enabled | Suspended</Status>
</VersioningConfiguration>
```

Status must be exactly `Enabled` or `Suspended`. Once enabled, versioning cannot be fully disabled — only suspended.

### Not implemented headers

- `x-amz-mfa` — MFA delete token; ignored
- `x-amz-expected-bucket-owner` — ignored

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `IllegalVersioningConfigurationException` | 400 | Status is not `Enabled` or `Suspended` |
| `InvalidBucketState` | 409 | Attempt to suspend versioning on an Object Lock-enabled bucket |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- `x-amz-mfa` (MFA delete) is not supported; the header is ignored.
- `MfaDelete` element in the configuration body is accepted but not enforced.
