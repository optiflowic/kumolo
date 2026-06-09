# GetObjectAcl

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectAcl.html  
**SDK struct**: `s3.GetObjectAclInput` / `s3.GetObjectAclOutput`  
**Last verified**: 2026-06-10

## Request

`GET /{key}?acl HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200` with `AccessControlPolicy` XML body.

If no ACL has been explicitly set, returns the default private ACL (owner `FULL_CONTROL` only).

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `NoSuchKey` | 404 | Object does not exist |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Owner ID and DisplayName are always `"owner"`.
- `versionId` query parameter is not supported.
