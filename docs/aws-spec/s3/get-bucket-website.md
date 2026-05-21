# GetBucketWebsite

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketWebsite.html  
**SDK struct**: `s3.GetBucketWebsiteInput` / `s3.GetBucketWebsiteOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?website HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

Returns the stored `WebsiteConfiguration` XML.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchWebsiteConfiguration` | 404 | No website configuration is set |
| `InternalError` | 500 | Storage failure |
