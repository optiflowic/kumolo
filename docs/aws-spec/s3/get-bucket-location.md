# GetBucketLocation

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketLocation.html  
**SDK struct**: `s3.GetBucketLocationInput` / `s3.GetBucketLocationOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?location HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Not implemented headers

- `x-amz-expected-bucket-owner` — owner account ID validation

## Response

`HTTP/1.1 200`

```xml
<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-west-2</LocationConstraint>
```

The `LocationConstraint` element is the region code. For `us-east-1`, the value is an empty
string (not `us-east-1`) per the AWS specification.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- `x-amz-expected-bucket-owner` header is ignored (no multi-account support).
- AWS recommends using `HeadBucket` instead of `GetBucketLocation` for region discovery;
  kumolo supports both.
