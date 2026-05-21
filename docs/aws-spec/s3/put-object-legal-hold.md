# PutObjectLegalHold

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectLegalHold.html  
**SDK struct**: `s3.PutObjectLegalHoldInput` / `s3.PutObjectLegalHoldOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /{key}?legal-hold HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Query parameters

| Parameter | Notes |
|---|---|
| `versionId` | Apply to a specific version |

### Request body

```xml
<LegalHold>
  <Status>ON | OFF</Status>
</LegalHold>
```

### Not implemented headers

- `x-amz-request-payer` — ignored
- `x-amz-expected-bucket-owner` — ignored

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchKey` | 404 | Object does not exist |
| `MethodNotAllowed` | 405 | Object is a delete marker |
| `InvalidArgument` | 400 | Status is not ON or OFF |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |
