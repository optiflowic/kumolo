# SSE Algorithm Validation (X-Amz-Server-Side-Encryption)

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html  
**SDK struct**: `s3.PutObjectInput.ServerSideEncryption` / `types.ServerSideEncryption`  
**Last verified**: 2026-06-03

## Overview

Real AWS validates the `x-amz-server-side-encryption` request header on `PutObject`, `CopyObject`, and `CreateMultipartUpload`. An unrecognized value returns `400 InvalidArgument`. This validation applies to all three write operations.

## Valid Values

| Value | Algorithm |
|---|---|
| `AES256` | SSE-S3 (AES-256 managed by S3) |
| `aws:kms` | SSE-KMS |
| `aws:kms:dsse` | Dual-layer SSE-KMS |

Empty (header absent) means no SSE requested — also valid.

## Error on Invalid Value

```
HTTP 400 Bad Request
Code:    InvalidArgument
Message: The encryption method specified is not supported.
```

## Kumolo deviations

- No actual encryption is applied. The algorithm value is stored in object metadata and echoed back in response headers.
- SSE-C (`x-amz-server-side-encryption-customer-*`) headers are not supported and rejected with `NotImplemented`.
