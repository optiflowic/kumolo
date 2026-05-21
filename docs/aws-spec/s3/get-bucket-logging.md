# GetBucketLogging

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketLogging.html  
**SDK struct**: `s3.GetBucketLoggingInput` / `s3.GetBucketLoggingOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?logging HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

Returns the stored `BucketLoggingStatus` XML. When no configuration is set, returns an empty element:

```xml
<BucketLoggingStatus xmlns="http://doc.s3.amazonaws.com/2006-03-01"/>
```

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |
