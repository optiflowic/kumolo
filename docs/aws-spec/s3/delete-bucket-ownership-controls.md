# DeleteBucketOwnershipControls

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketOwnershipControls.html  
**SDK struct**: `s3.DeleteBucketOwnershipControlsInput` / `s3.DeleteBucketOwnershipControlsOutput`  
**Last verified**: 2026-05-21

## Request

`DELETE /?ownershipControls HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 204 No Content`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |
