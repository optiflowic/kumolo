# PutBucketLogging

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketLogging.html  
**SDK struct**: `s3.PutBucketLoggingInput` / `s3.PutBucketLoggingOutput`  
**Last verified**: 2026-06-07

## Request

`PUT /?logging HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores a `BucketLoggingStatus` XML document. When `LoggingEnabled` is present,
kumolo appends a server access log record to the target bucket after each request.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Log delivery errors (e.g. target bucket missing) are silently dropped; the
  originating request is not affected.
- Each request produces exactly one log object (AWS batches multiple records per file).
- Fields not tracked by kumolo (`bucket-owner`, `requester`, `request-id`,
  `object-size`, `total-time`, `turn-around-time`, `version-id`, `host-id`,
  `sig-version`, `cipher`, `auth-type`, `host-name`, `tls-version`,
  `access-point`, `acl-required`) are emitted as `-`.
- Log format: S3 server access log format
  (https://docs.aws.amazon.com/AmazonS3/latest/userguide/LogFormat.html).
