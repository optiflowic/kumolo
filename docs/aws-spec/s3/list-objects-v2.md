# ListObjectsV2

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectsV2.html  
**SDK struct**: `s3.ListObjectsV2Input` / `s3.ListObjectsV2Output`  
**Last verified**: 2026-05-21

## Request

`GET /?list-type=2 HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

### Query parameters

| Parameter | Notes |
|---|---|
| `prefix` | Return only keys starting with this prefix |
| `delimiter` | Group keys by common prefix |
| `continuation-token` | Opaque token from previous response (base64 of last key) |
| `max-keys` | Max results; default/max is 1000 |
| `start-after` | Return keys lexicographically after this value |
| `fetch-owner` | Include `Owner` in each `Contents` element if `true` |
| `encoding-type` | Only `url` is valid |

### Not implemented parameters

- `encoding-type` — URL-encoding of keys with special characters

### Not implemented headers

- `x-amz-expected-bucket-owner` — owner account ID validation
- `x-amz-request-payer` — requester-pays
- `x-amz-optional-object-attributes` — optional attributes like `RestoreStatus`

## Response

`HTTP/1.1 200`

```xml
<ListBucketResult>
  <Name>string</Name>
  <Prefix>string</Prefix>
  <Delimiter>string</Delimiter>
  <MaxKeys>integer</MaxKeys>
  <KeyCount>integer</KeyCount>
  <IsTruncated>boolean</IsTruncated>
  <ContinuationToken>string</ContinuationToken>
  <NextContinuationToken>string</NextContinuationToken>
  <StartAfter>string</StartAfter>
  <Contents>
    <Key>string</Key>
    <LastModified>timestamp</LastModified>
    <ETag>string</ETag>
    <Size>long</Size>
    <StorageClass>string</StorageClass>
    <Owner>...</Owner>    <!-- only when fetch-owner=true -->
  </Contents>
  <CommonPrefixes><Prefix>string</Prefix></CommonPrefixes>
</ListBucketResult>
```

`NextContinuationToken` is only present when `IsTruncated=true`.
kumolo encodes the last returned key as base64 for the continuation token.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- `encoding-type=url` is not implemented; keys with special characters are returned as-is.
- `KeyCount` includes both `Contents` and `CommonPrefixes` entries (matches spec).
