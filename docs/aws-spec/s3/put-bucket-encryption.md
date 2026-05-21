# PutBucketEncryption

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketEncryption.html  
**SDK struct**: `s3.PutBucketEncryptionInput` / `s3.PutBucketEncryptionOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?encryption HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores a `ServerSideEncryptionConfiguration` XML document. The configuration is stored verbatim and returned on GET but no actual encryption is applied.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Encryption configuration is stored but not enforced — objects are not encrypted at rest.
