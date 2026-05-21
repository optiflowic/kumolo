# DeleteObjects

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObjects.html  
**SDK struct**: `s3.DeleteObjectsInput` / `s3.DeleteObjectsOutput`  
**Last verified**: 2026-05-21

## Request

`POST /?delete HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Deletes up to 1,000 objects in a single request.

### Request headers

| Header | Notes |
|---|---|
| `x-amz-bypass-governance-retention` | Bypass Governance-mode Object Lock |

### Not implemented headers

- `x-amz-expected-bucket-owner` — owner account ID validation
- `x-amz-mfa` — MFA delete token
- `x-amz-request-payer` — requester-pays
- `x-amz-sdk-checksum-algorithm` — checksum validation of request body

### Request body

```xml
<Delete>
  <Quiet>boolean</Quiet>   <!-- optional; omit Deleted elements on success -->
  <Object>
    <Key>string</Key>
    <VersionId>string</VersionId>   <!-- optional; targets a specific version -->
  </Object>
  ...
</Delete>
```

When `VersionId` is omitted on a versioning-enabled bucket, a delete marker is created.

## Response

`HTTP/1.1 200`

```xml
<DeleteResult>
  <Deleted>
    <Key>string</Key>
    <VersionId>string</VersionId>                    <!-- when VersionId was specified -->
    <DeleteMarker>boolean</DeleteMarker>             <!-- true when a marker was created or deleted -->
    <DeleteMarkerVersionId>string</DeleteMarkerVersionId>
  </Deleted>
  <Error>
    <Key>string</Key>
    <VersionId>string</VersionId>
    <Code>string</Code>
    <Message>string</Message>
  </Error>
</DeleteResult>
```

Deleting a non-existent object is treated as success (no `Error` element).
Quiet mode (`<Quiet>true</Quiet>`) suppresses `Deleted` elements; only errors are returned.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |

Per-object errors are returned inline in `<Error>` elements with HTTP 200:

| Code | Condition |
|---|---|
| `AccessDenied` | Object is protected by Object Lock |
| `InternalError` | Storage failure |

## Kumolo deviations

- `x-amz-mfa` is ignored (no MFA delete support).
- `x-amz-sdk-checksum-algorithm` is not validated against the request body.
- Per-object `ETag`, `LastModifiedTime`, `Size` conditional delete fields are not supported.
