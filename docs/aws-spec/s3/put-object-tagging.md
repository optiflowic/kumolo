# PutObjectTagging

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectTagging.html  
**SDK struct**: `s3.PutObjectTaggingInput` / `s3.PutObjectTaggingOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /{key}?tagging HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Replaces all existing tags on an object.

### Query parameters

| Parameter | Notes |
|---|---|
| `versionId` | Apply tags to a specific version |

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
- Maximum 10 tags per object
- Tag key: max 128 Unicode characters
- Tag value: max 256 Unicode characters
- Duplicate keys are rejected

### Not implemented headers

- `x-amz-expected-bucket-owner` — ignored
- `x-amz-request-payer` — ignored

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchKey` | 404 | Object does not exist |
| `InvalidTag` | 400 | Too many tags, key/value too long, or duplicate key |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |
