# PutBucketRequestPayment

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketRequestPayment.html  
**SDK struct**: `s3.PutBucketRequestPaymentInput` / `s3.PutBucketRequestPaymentOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?requestPayment HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores a `RequestPaymentConfiguration` XML document (`Payer`: `BucketOwner` or `Requester`). Not enforced.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Requester-pays configuration is stored but request payment is not enforced.
