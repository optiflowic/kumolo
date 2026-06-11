# CompleteMultipartUpload

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CompleteMultipartUpload.html  
**SDK struct**: `s3.CompleteMultipartUploadInput` / `s3.CompleteMultipartUploadOutput`  
**Last verified**: 2026-05-21

## Request

`POST /{key}?uploadId={id} HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Assembles the uploaded parts into the final object. Parts must be listed in ascending order by part number.
All parts except the last must be at least 5 MB.

### Query parameters

| Parameter | Notes |
|---|---|
| `uploadId` | Required |

### Request body

```xml
<CompleteMultipartUpload>
  <Part>
    <PartNumber>integer</PartNumber>
    <ETag>string</ETag>
  </Part>
  ...
</CompleteMultipartUpload>
```

### Not implemented headers

- `x-amz-expected-bucket-owner` — ignored
- `x-amz-request-payer` — ignored
- `x-amz-checksum-*` — checksum of the complete request body

## Response

`HTTP/1.1 200`

```xml
<CompleteMultipartUploadResult>
  <Location>string</Location>
  <Bucket>string</Bucket>
  <Key>string</Key>
  <ETag>string</ETag>
</CompleteMultipartUploadResult>
```

The ETag format for multipart objects is `"<md5>-<partCount>"`.

| Header | Condition |
|---|---|
| `x-amz-version-id` | When versioning is enabled |
| `x-amz-server-side-encryption` | When SSE was specified at upload initiation |
| `x-amz-server-side-encryption-aws-kms-key-id` | When KMS key ID was specified |
| `x-amz-server-side-encryption-bucket-key-enabled` | `true` when BucketKeyEnabled was set and algorithm is `aws:kms` or `aws:kms:dsse` |

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchUpload` | 404 | Upload ID does not exist |
| `NoSuchBucket` | 404 | Bucket does not exist |
| `AccessDenied` | 403 | Anonymous request denied by bucket ACL |
| `InvalidPart` | 400 | A specified part was not found |
| `InvalidPartOrder` | 400 | Parts are not in ascending order |
| `EntityTooSmall` | 400 | A non-final part is below the 5 MB minimum |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InvalidArgument` | 400 | Missing `uploadId` |
| `InternalError` | 500 | Storage failure |
