# GetBucketVersioning

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketVersioning.html  
**SDK struct**: `s3.GetBucketVersioningInput` / `s3.GetBucketVersioningOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?versioning HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

```xml
<VersioningConfiguration>
  <Status>Enabled | Suspended</Status>
</VersioningConfiguration>
```

`Status` is omitted (empty element) when versioning has never been configured.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |
