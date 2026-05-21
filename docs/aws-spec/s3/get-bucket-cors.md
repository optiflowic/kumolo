# GetBucketCors

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketCors.html  
**SDK struct**: `s3.GetBucketCorsInput` / `s3.GetBucketCorsOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?cors HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

```xml
<CORSConfiguration>
  <CORSRule>...</CORSRule>
  ...
</CORSConfiguration>
```

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchCORSConfiguration` | 404 | No CORS configuration set |
| `InternalError` | 500 | Storage failure |
