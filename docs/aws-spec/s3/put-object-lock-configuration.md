# PutObjectLockConfiguration

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObjectLockConfiguration.html  
**SDK struct**: `s3.PutObjectLockConfigurationInput` / `s3.PutObjectLockConfigurationOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?object-lock HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores an `ObjectLockConfiguration` XML document. Requires versioning to be enabled on the bucket.

### Request body

```xml
<ObjectLockConfiguration>
  <ObjectLockEnabled>Enabled</ObjectLockEnabled>
  <Rule>
    <DefaultRetention>
      <Mode>GOVERNANCE | COMPLIANCE</Mode>
      <Days>integer</Days>    <!-- or Years -->
      <Years>integer</Years>
    </DefaultRetention>
  </Rule>
</ObjectLockConfiguration>
```

### Not implemented headers

- `x-amz-bucket-object-lock-token` — ignored
- `x-amz-expected-bucket-owner` — ignored

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InvalidBucketState` | 400 | Versioning is not enabled on the bucket |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

None.
