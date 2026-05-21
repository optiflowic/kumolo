# PutBucketLifecycleConfiguration

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketLifecycleConfiguration.html  
**SDK struct**: `s3.PutBucketLifecycleConfigurationInput` / `s3.PutBucketLifecycleConfigurationOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?lifecycle HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores a `LifecycleConfiguration` XML document. Rules are parsed and stored; kumolo evaluates lifecycle rules for archival transitions and expiration.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Lifecycle rule evaluation (transitions, expiration) is performed at object access time on a best-effort basis, not on a schedule. See `internal/s3/lifecycle.go`.
- NoncurrentVersionTransition / NoncurrentVersionExpiration rules are stored but not evaluated.
