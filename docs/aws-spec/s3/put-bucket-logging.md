# PutBucketLogging

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketLogging.html  
**SDK struct**: `s3.PutBucketLoggingInput` / `s3.PutBucketLoggingOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?logging HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores a `BucketLoggingStatus` XML document. Configuration is stored verbatim; access logs are not generated.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Logging configuration is stored but access logging is not implemented.
