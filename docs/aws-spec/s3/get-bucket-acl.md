# GetBucketAcl

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketAcl.html  
**SDK struct**: `s3.GetBucketAclInput` / `s3.GetBucketAclOutput`  
**Last verified**: 2026-06-10

## Request

`GET /?acl HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200` with `AccessControlPolicy` XML body.

If no ACL has been explicitly set via `PutBucketACL`, returns the default private ACL (owner `FULL_CONTROL` only).

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |

## Kumolo deviations

- Owner ID and DisplayName are always `"owner"` (no real identity system).
