# DeletePublicAccessBlock

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeletePublicAccessBlock.html  
**SDK struct**: `s3.DeletePublicAccessBlockInput` / `s3.DeletePublicAccessBlockOutput`  
**Last verified**: 2026-05-21

## Request

`DELETE /?publicAccessBlock HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 204 No Content`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |
