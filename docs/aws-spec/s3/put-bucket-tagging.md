# PutBucketTagging

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketTagging.html  
**SDK struct**: `s3.PutBucketTaggingInput` / `s3.PutBucketTaggingOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?tagging HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Replaces all existing tags on a bucket.

### Request body

```xml
<Tagging>
  <TagSet>
    <Tag><Key>string</Key><Value>string</Value></Tag>
    ...
  </TagSet>
</Tagging>
```

Constraints enforced:
- Maximum 50 tags per bucket
- Tag key: max 128 Unicode characters
- Tag value: max 256 Unicode characters
- Duplicate keys are rejected

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InvalidTag` | 400 | Too many tags, key/value too long, or duplicate key |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |
