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
If `x-amz-transition-default-minimum-object-size` was set on the last `PutBucketLifecycleConfiguration`, kumolo injects `<TransitionDefaultMinimumObjectSize>value</TransitionDefaultMinimumObjectSize>` as a direct child of `<LifecycleConfiguration>` just before the closing tag.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchLifecycleConfiguration` | 404 | No lifecycle configuration is set |
| `InternalError` | 500 | Storage failure |
