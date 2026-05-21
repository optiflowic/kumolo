# GetBucketOwnershipControls

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketOwnershipControls.html  
**SDK struct**: `s3.GetBucketOwnershipControlsInput` / `s3.GetBucketOwnershipControlsOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?ownershipControls HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

Returns the stored `OwnershipControls` XML.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `OwnershipControlsNotFoundError` | 404 | No ownership controls configuration is set |
| `InternalError` | 500 | Storage failure |
