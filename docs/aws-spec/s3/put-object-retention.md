# PutObjectRetention

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectRetention.html  
**SDK struct**: `s3.PutObjectRetentionInput` / `s3.PutObjectRetentionOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /{key}?retention HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Sets or updates the retention configuration for an object version.

### Query parameters

| Parameter | Notes |
|---|---|
| `versionId` | Apply retention to a specific version |

### Request headers

| Header | Notes |
|---|---|
| `x-amz-bypass-governance-retention` | Set to `true` to override GOVERNANCE-mode retention |

### Request body

```xml
<Retention>
  <Mode>GOVERNANCE | COMPLIANCE</Mode>
  <RetainUntilDate>ISO 8601 timestamp</RetainUntilDate>
</Retention>
```

`RetainUntilDate` must be in the future. Both `Mode` and `RetainUntilDate` are required.

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
| `InvalidArgument` | 400 | Invalid mode, missing fields, or date in the past |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |
