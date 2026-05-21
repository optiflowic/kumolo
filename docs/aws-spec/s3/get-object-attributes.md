# GetObjectAttributes

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectAttributes.html  
**SDK struct**: `s3.GetObjectAttributesInput` / `s3.GetObjectAttributesOutput`  
**Last verified**: 2026-05-21

## Request

`GET /{key}?attributes HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Returns a subset of object metadata without downloading the object body.

### Query parameters

| Parameter | Notes |
|---|---|
| `versionId` | Return attributes for a specific version |

### Request headers

| Header | Notes |
|---|---|
| `x-amz-object-attributes` | Required; comma-separated list of attributes to return |

Valid attribute names: `ETag`, `Checksum`, `ObjectParts`, `StorageClass`, `ObjectSize`

### Not implemented headers

- `x-amz-expected-bucket-owner` — ignored
- `x-amz-request-payer` — ignored

## Response

`HTTP/1.1 200`

```xml
<GetObjectAttributesResponse>
  <ETag>string</ETag>               <!-- when ETag requested; no surrounding quotes -->
  <StorageClass>string</StorageClass> <!-- when StorageClass requested; defaults to STANDARD -->
  <ObjectSize>integer</ObjectSize>  <!-- when ObjectSize requested -->
  <ObjectParts>
    <TotalPartsCount>integer</TotalPartsCount>
  </ObjectParts>                    <!-- when ObjectParts requested and object is multipart -->
</GetObjectAttributesResponse>
```

| Header | Condition |
|---|---|
| `Last-Modified` | Always |
| `x-amz-version-id` | When object is versioned |
| `x-amz-delete-marker` | When the object is a delete marker |

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchKey` | 404 | Object or bucket not found |
| `MethodNotAllowed` | 405 | Accessing a delete marker |
| `InvalidArgument` | 400 | Missing or invalid `x-amz-object-attributes` header |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- `Checksum` attribute is always absent in the response (checksum data is not stored in metadata).
- `ObjectParts.TotalPartsCount` is derived by parsing the multipart ETag suffix (`-N`); per-part details are not stored.
