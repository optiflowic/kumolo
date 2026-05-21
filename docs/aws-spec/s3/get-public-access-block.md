# GetPublicAccessBlock

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetPublicAccessBlock.html  
**SDK struct**: `s3.GetPublicAccessBlockInput` / `s3.GetPublicAccessBlockOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?publicAccessBlock HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

Returns the stored `PublicAccessBlockConfiguration` XML.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchPublicAccessBlockConfiguration` | 404 | No configuration is set |
| `InternalError` | 500 | Storage failure |
