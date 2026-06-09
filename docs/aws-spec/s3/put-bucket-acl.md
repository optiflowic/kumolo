# PutBucketAcl

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketAcl.html  
**SDK struct**: `s3.PutBucketAclInput` / `s3.PutBucketAclOutput`  
**Last verified**: 2026-06-10

## Request

`PUT /?acl HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

ACL can be specified as:
- `x-amz-acl` header (canned ACL — takes precedence over body)
- Request body: full `AccessControlPolicy` XML

Supported canned ACLs: `private`, `public-read`, `public-read-write`, `authenticated-read`, `bucket-owner-read`, `bucket-owner-full-control`, `log-delivery-write`.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InvalidArgument` | 400 | Unrecognized canned ACL value |
| `MalformedXML` | 400 | Request body is not valid AccessControlPolicy XML |

## Kumolo deviations

- Individual grant headers (`x-amz-grant-read`, `x-amz-grant-write`, etc.) are not supported.
- `bucket-owner-read`, `bucket-owner-full-control`, `log-delivery-write` are stored and returned correctly but enforce the same as `private` (kumolo has a single owner identity).
- ACL enforcement only applies when an ACL has been explicitly configured; buckets/objects with no stored ACL are unrestricted (backward-compatible default).
