# DeleteObjectTagging

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObjectTagging.html  
**SDK struct**: `s3.DeleteObjectTaggingInput` / `s3.DeleteObjectTaggingOutput`  
**Last verified**: 2026-05-21

## Request

`DELETE /{key}?tagging HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Removes all tags from an object.

### Query parameters

| Parameter | Notes |
|---|---|
| `versionId` | Delete tags for a specific version |

### Not implemented headers

- `x-amz-expected-bucket-owner` — ignored
- `x-amz-request-payer` — ignored

## Response

`HTTP/1.1 204 No Content`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchKey` | 404 | Object does not exist |
| `InternalError` | 500 | Storage failure |
