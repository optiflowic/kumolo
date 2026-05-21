# GetBucketRequestPayment

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketRequestPayment.html  
**SDK struct**: `s3.GetBucketRequestPaymentInput` / `s3.GetBucketRequestPaymentOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?requestPayment HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

Returns the stored `RequestPaymentConfiguration` XML. When no configuration is set, returns the default:

```xml
<RequestPaymentConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Payer>BucketOwner</Payer>
</RequestPaymentConfiguration>
```

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |
