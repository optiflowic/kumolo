# DeleteBucketEncryption

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketEncryption.html  
**SDK struct**: `s3.DeleteBucketEncryptionInput` / `s3.DeleteBucketEncryptionOutput`  
**Last verified**: 2026-05-21

## Request

`DELETE /?encryption HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 204 No Content`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |
