# GetObjectTagging

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectTagging.html  
**SDK struct**: `s3.GetObjectTaggingInput` / `s3.GetObjectTaggingOutput`  
**Last verified**: 2026-05-21

## Request

`GET /{key}?tagging HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Query parameters

| Parameter | Notes |
|---|---|
| `versionId` | Get tags for a specific version |

### Not implemented headers

- `x-amz-expected-bucket-owner` — ignored
- `x-amz-request-payer` — ignored

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

Returns an empty `<TagSet/>` when the object has no tags.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchKey` | 404 | Object does not exist |
| `InternalError` | 500 | Storage failure |
