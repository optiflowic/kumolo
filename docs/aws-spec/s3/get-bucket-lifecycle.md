# GetBucketLifecycleConfiguration

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketLifecycleConfiguration.html  
**SDK struct**: `s3.GetBucketLifecycleConfigurationInput` / `s3.GetBucketLifecycleConfigurationOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?lifecycle HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

Returns the stored `LifecycleConfiguration` XML.

The response always includes the `x-amz-transition-default-minimum-object-size` response header. The value is whatever was stored by the last `PutBucketLifecycleConfiguration` call (default: `all_storage_classes_128K`). The AWS SDK Go v2 maps this header to `GetBucketLifecycleConfigurationOutput.TransitionDefaultMinimumObjectSize`.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchLifecycleConfiguration` | 404 | No lifecycle configuration is set |
| `InternalError` | 500 | Storage failure |
