# PutPublicAccessBlock

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutPublicAccessBlock.html  
**SDK struct**: `s3.PutPublicAccessBlockInput` / `s3.PutPublicAccessBlockOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?publicAccessBlock HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Request body

```xml
<PublicAccessBlockConfiguration>
  <BlockPublicAcls>boolean</BlockPublicAcls>
  <IgnorePublicAcls>boolean</IgnorePublicAcls>
  <BlockPublicPolicy>boolean</BlockPublicPolicy>
  <RestrictPublicBuckets>boolean</RestrictPublicBuckets>
</PublicAccessBlockConfiguration>
```

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Configuration is stored verbatim but not enforced — public access is not actually blocked.
