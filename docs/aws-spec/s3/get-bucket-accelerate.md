# GetBucketAccelerateConfiguration

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketAccelerateConfiguration.html  
**SDK struct**: `s3.GetBucketAccelerateConfigurationInput` / `s3.GetBucketAccelerateConfigurationOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?accelerate HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

Returns the stored `AccelerateConfiguration` XML. When no configuration is set, returns an empty element:

```xml
<AccelerateConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"/>
```

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |
