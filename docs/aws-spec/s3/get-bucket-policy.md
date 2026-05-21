# GetBucketPolicy

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketPolicy.html  
**SDK struct**: `s3.GetBucketPolicyInput` / `s3.GetBucketPolicyOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?policy HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

Returns the stored bucket policy as a JSON string (`Content-Type: application/json`).

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchBucketPolicy` | 404 | No policy is set on the bucket |
| `InternalError` | 500 | Storage failure |
