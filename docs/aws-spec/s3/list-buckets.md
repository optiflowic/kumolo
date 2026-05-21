# ListBuckets

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListBuckets.html  
**SDK struct**: `s3.ListBucketsInput` / `s3.ListBucketsOutput`  
**Last verified**: 2026-05-21

## Request

`GET / HTTP/1.1`  
`Host: s3.amazonaws.com`

### Query parameters

| Parameter | Type | Notes |
|---|---|---|
| `max-buckets` | integer (1–10000) | Page size; defaults to all buckets |
| `continuation-token` | string | Opaque token from previous response |
| `prefix` | string | Return only buckets whose name begins with this prefix |
| `bucket-region` | string | Filter by AWS Region code |

## Response

`HTTP/1.1 200`

```xml
<ListAllMyBucketsResult>
  <Buckets>
    <Bucket>
      <Name>string</Name>
      <CreationDate>timestamp</CreationDate>
      <BucketRegion>string</BucketRegion>
    </Bucket>
  </Buckets>
  <Owner>
    <ID>string</ID>
    <DisplayName>string</DisplayName>
  </Owner>
  <ContinuationToken>string</ContinuationToken>
  <Prefix>string</Prefix>
</ListAllMyBucketsResult>
```

`ContinuationToken` is present in the response only when there are more pages.
`Prefix` is echoed back when the request included a prefix.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Pagination (`max-buckets`, `continuation-token`) is not implemented; all buckets are
  returned in a single response.
- `prefix` and `bucket-region` filtering are not implemented.
- `BucketRegion` is optional per the AWS spec and is included only when the bucket has a
  region set (i.e., parsed from the `Authorization` Credential scope at creation time).
  Buckets created without an explicit region header omit the field.
- Owner `ID` and `DisplayName` are hardcoded to `"owner"`.
