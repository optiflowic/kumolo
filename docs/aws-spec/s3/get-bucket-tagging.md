# GetBucketTagging

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketTagging.html  
**SDK struct**: `s3.GetBucketTaggingInput` / `s3.GetBucketTaggingOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?tagging HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

```xml
<Tagging>
  <TagSet>
    <Tag><Key>string</Key><Value>string</Value></Tag>
    ...
  </TagSet>
</Tagging>
```

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchTagSet` | 404 | Bucket has no tags |
| `InternalError` | 500 | Storage failure |
