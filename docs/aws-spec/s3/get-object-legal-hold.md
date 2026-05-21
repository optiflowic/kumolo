# GetObjectLegalHold

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectLegalHold.html  
**SDK struct**: `s3.GetObjectLegalHoldInput` / `s3.GetObjectLegalHoldOutput`  
**Last verified**: 2026-05-21

## Request

`GET /{key}?legal-hold HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Query parameters

| Parameter | Notes |
|---|---|
| `versionId` | Get legal hold status for a specific version |

### Not implemented headers

- `x-amz-request-payer` — ignored
- `x-amz-expected-bucket-owner` — ignored

## Response

`HTTP/1.1 200`

```xml
<LegalHold>
  <Status>ON | OFF</Status>
</LegalHold>
```

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchKey` | 404 | Object does not exist |
| `NoSuchObjectLockConfiguration` | 404 | Object has no legal hold configuration |
| `MethodNotAllowed` | 405 | Object is a delete marker |
| `InternalError` | 500 | Storage failure |
