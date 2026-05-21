# GetObjectRetention

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectRetention.html  
**SDK struct**: `s3.GetObjectRetentionInput` / `s3.GetObjectRetentionOutput`  
**Last verified**: 2026-05-21

## Request

`GET /{key}?retention HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Query parameters

| Parameter | Notes |
|---|---|
| `versionId` | Get retention for a specific version |

### Not implemented headers

- `x-amz-request-payer` — ignored
- `x-amz-expected-bucket-owner` — ignored

## Response

`HTTP/1.1 200`

```xml
<Retention>
  <Mode>GOVERNANCE | COMPLIANCE</Mode>
  <RetainUntilDate>ISO 8601 timestamp</RetainUntilDate>
</Retention>
```

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchKey` | 404 | Object does not exist |
| `NoSuchObjectLockConfiguration` | 404 | Object has no retention configuration |
| `MethodNotAllowed` | 405 | Object is a delete marker |
| `InternalError` | 500 | Storage failure |
