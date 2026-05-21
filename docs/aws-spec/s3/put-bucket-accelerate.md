# PutBucketAccelerateConfiguration

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketAccelerateConfiguration.html  
**SDK struct**: `s3.PutBucketAccelerateConfigurationInput` / `s3.PutBucketAccelerateConfigurationOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?accelerate HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores an `AccelerateConfiguration` XML document. Configuration is stored verbatim; Transfer Acceleration is not actually applied.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Transfer Acceleration is not implemented; configuration is stored for API compatibility only.
