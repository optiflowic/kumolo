# SSE Algorithm Validation (X-Amz-Server-Side-Encryption)

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html  
**SDK struct**: `s3.PutObjectInput.ServerSideEncryption` / `types.ServerSideEncryption`  
**Last verified**: 2026-06-04

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

```http
HTTP 400 Bad Request
Code:    InvalidArgument
Message: The encryption method specified is not supported.
```

## SSE-KMS key resolution (aws:kms / aws:kms:dsse)

When the algorithm is `aws:kms` or `aws:kms:dsse`, kumolo calls the KMS service to
validate the key and resolve it to a canonical ARN before storing:

1. If no key ID is supplied, `alias/aws/s3` is used (auto-created on first use).
2. The key is validated via `DescribeKey`-equivalent logic: returns an S3 KMS error if
   the key is disabled (`KMS.DisabledException`) or pending deletion (`KMS.InvalidStateException`),
   or if the key does not exist (`KMS.NotFoundException`).
3. The resolved canonical ARN is stored in object metadata and returned in the
   `x-amz-server-side-encryption-aws-kms-key-id` response header.

## Kumolo deviations

- No actual encryption is applied. Algorithm and key ARN are stored in object metadata and echoed back in response headers.
- SSE-C (`x-amz-server-side-encryption-customer-*`) headers are not supported and rejected with `NotImplemented`.
- Key usage (`KeyUsage`) is not validated; only key state (Enabled / Disabled / PendingDeletion) is checked.
