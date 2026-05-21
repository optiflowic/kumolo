# DeleteBucketTagging

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketTagging.html  
**SDK struct**: `s3.DeleteBucketTaggingInput` / `s3.DeleteBucketTaggingOutput`  
**Last verified**: 2026-05-21

## Request

`DELETE /?tagging HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 204 No Content`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |
