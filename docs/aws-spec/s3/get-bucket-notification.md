# GetBucketNotificationConfiguration

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketNotificationConfiguration.html  
**SDK struct**: `s3.GetBucketNotificationConfigurationInput` / `s3.GetBucketNotificationConfigurationOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?notification HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

Returns the stored `NotificationConfiguration` XML. When no configuration is set, returns an empty element:

```xml
<NotificationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"/>
```

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |
