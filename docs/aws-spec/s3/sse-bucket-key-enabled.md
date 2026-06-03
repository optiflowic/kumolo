# SSE Bucket Key (X-Amz-Server-Side-Encryption-Bucket-Key-Enabled)

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html  
**SDK struct**: `s3.PutObjectInput.BucketKeyEnabled` / response: `s3.PutObjectOutput.BucketKeyEnabled`  
**Last verified**: 2026-06-03

## Overview

`X-Amz-Server-Side-Encryption-Bucket-Key-Enabled` controls whether S3 uses an S3 Bucket Key to reduce
the number of KMS API calls when encrypting objects with SSE-KMS. The header is only meaningful with
`aws:kms` and `aws:kms:dsse` algorithms; it is ignored for `AES256` and absent-SSE cases.

## Request Header

- Header: `X-Amz-Server-Side-Encryption-Bucket-Key-Enabled`
- Value: `"true"` or `"false"`
- Applicable to: `PutObject`, `CopyObject`, `CreateMultipartUpload`

## Response Header

The same header is echoed in the response when `true`:

- `PutObject`, `CopyObject`, `CompleteMultipartUpload`, `GetObject`, `HeadObject`
- The header is **not** emitted when `AES256` is the algorithm, even if the stored metadata says true.

## Constraint

Only emitted when `SSEAlgorithm` is `aws:kms` or `aws:kms:dsse`. For `AES256` (SSE-S3), AWS never
returns this header.

## Kumolo deviations

- No actual KMS Bucket Key optimization is performed. The value is stored in object metadata and echoed
  back in response headers per the constraint above.
- `CreateMultipartUpload` echoes the header in the initiate response; `CompleteMultipartUpload` echoes
  it from the stored metadata after the object is finalized.
