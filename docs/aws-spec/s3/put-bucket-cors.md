# PutBucketCors

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketCors.html  
**SDK struct**: `s3.PutBucketCorsInput` / `s3.PutBucketCorsOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?cors HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Request body

```xml
<CORSConfiguration>
  <CORSRule>
    <AllowedOrigin>string</AllowedOrigin>  <!-- required; supports * wildcard -->
    <AllowedMethod>GET|PUT|POST|DELETE|HEAD</AllowedMethod>  <!-- required -->
    <AllowedHeader>string</AllowedHeader>  <!-- optional; supports * wildcard -->
    <ExposeHeader>string</ExposeHeader>    <!-- optional -->
    <MaxAgeSeconds>integer</MaxAgeSeconds> <!-- optional -->
    <ID>string</ID>                        <!-- optional -->
  </CORSRule>
  ...
</CORSConfiguration>
```

Constraints:
- At least one rule is required
- Each rule must have at least one `AllowedOrigin` and one `AllowedMethod`
- `AllowedMethod` must be one of GET, PUT, POST, DELETE, HEAD

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Invalid XML or missing required fields |
| `InvalidArgument` | 400 | Invalid HTTP method in rule |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- CORS rules are stored and used for actual CORS preflight/simple-request header injection.
- `x-amz-sdk-checksum-algorithm` is not validated against the request body.
