# ReEncrypt

URL: https://docs.aws.amazon.com/kms/latest/APIReference/API_ReEncrypt.html
SDK struct: `kms.ReEncryptInput` / `kms.ReEncryptOutput`
Last verified: 2026-06-02

## Request Parameters

| Parameter                      | Type    | Required | Constraints    | Notes                                       |
|-------------------------------|---------|----------|----------------|---------------------------------------------|
| CiphertextBlob                | blob    | Yes      | 1–6144 bytes   |                                             |
| DestinationKeyId              | string  | Yes      | 1–2048 chars   | key ID, ARN, alias name, or alias ARN       |
| SourceKeyId                   | string  | No       | 1–2048 chars   | Optional for symmetric; embedded in blob    |
| SourceEncryptionAlgorithm     | string  | No       | —              | Only SYMMETRIC_DEFAULT in kumolo            |
| SourceEncryptionContext       | map     | No       | —              | Must match context used during original encryption |
| DestinationEncryptionAlgorithm| string  | No       | —              | Only SYMMETRIC_DEFAULT in kumolo            |
| DestinationEncryptionContext  | map     | No       | —              |                                             |
| DryRun                        | boolean | No       | —              | Not supported in kumolo; ignored            |
| DryRunModifiers               | []string| No       | —              | Not supported in kumolo; ignored            |
| GrantTokens                   | []string| No       | 0–10 items     | Not supported in kumolo; ignored            |

## Response Fields

| Field                          | Notes                                  |
|-------------------------------|----------------------------------------|
| CiphertextBlob                | Re-encrypted data                      |
| KeyId                         | ARN of destination key                 |
| SourceKeyId                   | ARN of source key                      |
| SourceEncryptionAlgorithm     | SYMMETRIC_DEFAULT                      |
| SourceKeyMaterialId           | Key material ID of the source key      |
| DestinationEncryptionAlgorithm| SYMMETRIC_DEFAULT                      |
| DestinationKeyMaterialId      | Key material ID of the destination key |

## Errors

| Error                    | HTTP | Condition                                         |
|--------------------------|------|---------------------------------------------------|
| ValidationException      | 400  | Missing required params (CiphertextBlob, DestinationKeyId) |
| InvalidCiphertextException| 400  | Malformed ciphertext blob                        |
| IncorrectKeyException    | 400  | SourceKeyId does not match embedded key in blob   |
| NotFoundException        | 400  | Source or destination key not found               |
| DisabledException        | 400  | Source or destination key is disabled             |
| KMSInvalidStateException | 400  | Key is pending deletion                           |
| InvalidKeyUsageException | 400  | Key does not have ENCRYPT_DECRYPT usage; unsupported EncryptionAlgorithm value |
| KMSInternalException     | 500  | Entropy failure or internal error                 |

## kumolo Deviations

- Only `SYMMETRIC_DEFAULT` is supported for both source and destination algorithms.
- `DryRun`, `DryRunModifiers`, `GrantTokens` are silently ignored.
- Asymmetric key re-encryption is not supported (returns `InvalidKeyUsageException`).
- Source key ID is read from the ciphertext envelope; `SourceKeyId` is only used for validation.
