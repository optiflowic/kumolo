# PutObjectAcl

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectAcl.html  
**SDK struct**: `s3.PutObjectAclInput` / `s3.PutObjectAclOutput`  
**Last verified**: 2026-06-10

## Request

`PUT /{key}?acl HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

ACL can be specified as:
- `x-amz-acl` header (canned ACL — takes precedence over body)
- Request body: full `AccessControlPolicy` XML

Also set at object creation time via `PutObject` with `x-amz-acl` header.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchKey` | 404 | Object does not exist |
| `InvalidArgument` | 400 | Unrecognized canned ACL value |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Individual grant headers are not supported.
- `versionId` query parameter is not supported (ACL is applied to the current version only).
- ACL enforcement only applies when an ACL has been explicitly configured.
