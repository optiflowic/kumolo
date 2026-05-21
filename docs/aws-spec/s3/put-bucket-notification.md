# PutBucketNotificationConfiguration

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketNotificationConfiguration.html  
**SDK struct**: `s3.PutBucketNotificationConfigurationInput` / `s3.PutBucketNotificationConfigurationOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?notification HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores a `NotificationConfiguration` XML document. The configuration is stored verbatim; no notifications are actually delivered.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Notification configuration is stored but notifications are never sent.
