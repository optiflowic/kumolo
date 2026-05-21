# GetObjectLockConfiguration

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectLockConfiguration.html  
**SDK struct**: `s3.GetObjectLockConfigurationInput` / `s3.GetObjectLockConfigurationOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?object-lock HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Not implemented headers

- `x-amz-expected-bucket-owner` — ignored

## Response

`HTTP/1.1 200`

Returns the stored `ObjectLockConfiguration` XML.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `ObjectLockConfigurationNotFoundError` | 404 | No Object Lock configuration is set |
| `InternalError` | 500 | Storage failure |
