# PutObjectAcl

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectAcl.html  
**SDK struct**: `s3.PutObjectAclInput` / `s3.PutObjectAclOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /{key}?acl HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchKey` | 404 | Object does not exist |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- The request body and all ACL headers are accepted and ignored.
- ACL enforcement is not implemented; all objects are implicitly fully accessible.
