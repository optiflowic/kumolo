# PutBucketOwnershipControls

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketOwnershipControls.html  
**SDK struct**: `s3.PutBucketOwnershipControlsInput` / `s3.PutBucketOwnershipControlsOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?ownershipControls HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores an `OwnershipControls` XML document (e.g. `BucketOwnerEnforced`, `BucketOwnerPreferred`, `ObjectWriter`). Configuration is stored verbatim but not enforced.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Ownership controls are stored but not enforced.
