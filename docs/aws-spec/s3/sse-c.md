# SSE-C — Server-Side Encryption with Customer-Provided Keys

**Official URL**: https://docs.aws.amazon.com/AmazonS3/latest/userguide/ServerSideEncryptionCustomerKeys.html

## SDK Structs
`s3.PutObjectInput.SSECustomerAlgorithm`, `SSECustomerKey`, `SSECustomerKeyMD5`
Copy-source variants: `CopySourceSSECustomerAlgorithm`, `CopySourceSSECustomerKey`, `CopySourceSSECustomerKeyMD5`

## Request Headers

| Header | Description |
|---|---|
| `X-Amz-Server-Side-Encryption-Customer-Algorithm` | Must be `AES256` |
| `X-Amz-Server-Side-Encryption-Customer-Key` | Base64-encoded 256-bit (32-byte) AES key |
| `X-Amz-Server-Side-Encryption-Customer-Key-MD5` | Base64-encoded MD5 of the raw key bytes |

For CopyObject and UploadPartCopy source:

| Header | Description |
|---|---|
| `X-Amz-Copy-Source-Server-Side-Encryption-Customer-Algorithm` | `AES256` |
| `X-Amz-Copy-Source-Server-Side-Encryption-Customer-Key` | Source key |
| `X-Amz-Copy-Source-Server-Side-Encryption-Customer-Key-MD5` | MD5 of source key |

## Response Headers (on success)

| Header | Value |
|---|---|
| `X-Amz-Server-Side-Encryption-Customer-Algorithm` | `AES256` |
| `X-Amz-Server-Side-Encryption-Customer-Key-MD5` | Base64-encoded MD5 of the provided key |

## Operations Updated

PutObject, GetObject, HeadObject, CopyObject, CreateMultipartUpload, UploadPart, UploadPartCopy

## Validation Rules

- All three request headers must be present together; any partial set → `InvalidArgument` 400
- Algorithm must be `AES256`; any other value → `InvalidArgument` 400
- Key must decode to exactly 32 bytes → `InvalidArgument` 400
- Key-MD5 must equal base64(MD5(rawKeyBytes)); mismatch → `InvalidArgument` 400
- SSE-C and `X-Amz-Server-Side-Encryption` must not coexist → `InvalidArgument` 400

### On read (GetObject, HeadObject)

- Object stored with SSE-C but request provides no SSE-C headers → `InvalidRequest` 400
- SSE-C headers provided but key MD5 does not match stored value → `AccessDenied` 403
- SSE-C headers provided but object was NOT stored with SSE-C → `AccessDenied` 403

### UploadPart

- SSE-C headers must match the key MD5 stored at CreateMultipartUpload → `AccessDenied` 403
- If upload was created with SSE-C but no headers provided → `InvalidRequest` 400

### CopyObject / UploadPartCopy (source)

- Source SSE-C headers follow the same rules as GetObject (applied to the copy source)

## Storage

kumolo does NOT encrypt object bytes (same as SSE-S3 and SSE-KMS).
Stores `SSECKeyMD5 string` in `ObjectMetadata`; uses it for key MD5 validation on reads.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `InvalidArgument` | 400 | Missing/invalid SSE-C headers or conflict with SSE headers |
| `InvalidRequest` | 400 | SSE-C object accessed without providing SSE-C headers |
| `AccessDenied` | 403 | Key MD5 mismatch |

## Deviations from AWS

- Bytes are not actually encrypted; only key MD5 is stored and validated.

## Last Verified
2026-06-06
