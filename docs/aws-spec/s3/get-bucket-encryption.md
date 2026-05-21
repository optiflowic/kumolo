# GetBucketEncryption

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketEncryption.html  
**SDK struct**: `s3.GetBucketEncryptionInput` / `s3.GetBucketEncryptionOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?encryption HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

Returns the stored `ServerSideEncryptionConfiguration` XML.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `ServerSideEncryptionConfigurationNotFoundError` | 404 | No encryption configuration is set |
| `InternalError` | 500 | Storage failure |
