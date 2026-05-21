# PutBucketAcl

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketAcl.html  
**SDK struct**: `s3.PutBucketAclInput` / `s3.PutBucketAclOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?acl HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |

## Kumolo deviations

- The request body and all ACL-related headers are accepted and ignored.
- ACL enforcement is not implemented.
