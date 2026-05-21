# PutBucketWebsite

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketWebsite.html  
**SDK struct**: `s3.PutBucketWebsiteInput` / `s3.PutBucketWebsiteOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?website HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores a `WebsiteConfiguration` XML document. Configuration is stored verbatim; website hosting is not actually served.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Website configuration is stored but static website hosting is not implemented.
