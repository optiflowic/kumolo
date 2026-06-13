# ListObjects

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjects.html  
**SDK struct**: `s3.ListObjectsInput` / `s3.ListObjectsOutput`  
**Last verified**: 2026-05-21

## Request

`GET / HTTP/1.1` (no query params distinguishing v1; absent `list-type=2`)  
`Host: {bucket}.s3.amazonaws.com`

AWS recommends using ListObjectsV2 instead. Kept for backward compatibility.

### Query parameters

| Parameter | Notes |
|---|---|
| `prefix` | Return only keys starting with this prefix |
| `delimiter` | Group keys by common prefix |
| `marker` | Start after this key (exclusive) |
| `max-keys` | Max results; default/max is 1000 |
| `encoding-type` | Only `url` is valid |

### Implemented parameters

- `encoding-type` — only `url` is valid; if absent or empty, no encoding applied; invalid values return `InvalidArgument`

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
  <Marker>string</Marker>
  <MaxKeys>integer</MaxKeys>
  <IsTruncated>boolean</IsTruncated>
  <NextMarker>string</NextMarker>       <!-- only when IsTruncated and delimiter set -->
  <Delimiter>string</Delimiter>
  <Contents>
    <Key>string</Key>
    <LastModified>timestamp</LastModified>
    <ETag>string</ETag>
    <Size>long</Size>
    <StorageClass>string</StorageClass>
    <Owner>...</Owner>
  </Contents>
  <CommonPrefixes><Prefix>string</Prefix></CommonPrefixes>
</ListBucketResult>
```

Per spec, `NextMarker` should only be included when `delimiter` is specified.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |

## Response with encoding-type=url

When `encoding-type=url` is set, the response includes `<EncodingType>url</EncodingType>` and URL-encodes (`url.QueryEscape`) these fields:
`Prefix`, `Marker`, `NextMarker`, `Delimiter`, each `Contents.Key`, each `CommonPrefixes.Prefix`.

## Kumolo deviations

- `NextMarker` is included in truncated responses even when `delimiter` is not set.
  Per spec, clients should use the last `Key` in the response as marker when `delimiter` is absent.
  This is a benign over-provision that does not break SDK clients.
- `Owner` is never included in `Contents` elements.
