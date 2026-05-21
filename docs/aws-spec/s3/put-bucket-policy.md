# PutBucketPolicy

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketPolicy.html  
**SDK struct**: `s3.PutBucketPolicyInput` / `s3.PutBucketPolicyOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?policy HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Request body

JSON object (must be a valid JSON object starting with `{`). The body is stored verbatim and returned on GET without validation of the policy content.

## Response

`HTTP/1.1 204 No Content`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedPolicy` | 400 | Body is not valid JSON or does not start with `{` |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Policy statements are stored but not enforced — kumolo does not apply access control based on bucket policies.
