# GenerateDataKey

**URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateDataKey.html  
**SDK input**: `kms.GenerateDataKeyInput`  
**SDK output**: `kms.GenerateDataKeyOutput`  
**Last verified**: 2026-05-30

## Behaviour

Returns a new random symmetric data key. The response contains both the plaintext key
and a copy of it encrypted under the specified KMS key.

## Request parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| KeyId | string | yes | key ID, ARN, alias name, alias ARN; max 2048 chars |
| KeySpec | string | no | `AES_256` (32 bytes) or `AES_128` (16 bytes) |
| NumberOfBytes | integer | no | 1–1024; cannot be used together with KeySpec |
| EncryptionContext | map[string]string | no | AEAD additional data; same context must be passed to Decrypt |
| GrantTokens | []string | no | ignored in kumolo |
| DryRun | boolean | no | ignored in kumolo |
| Recipient | object | no | Nitro Enclave support; not supported in kumolo |

Exactly one of `KeySpec` or `NumberOfBytes` must be provided.

## Response fields

| Field | Type | Notes |
|---|---|---|
| CiphertextBlob | blob | data key encrypted under the KMS key (kumolo envelope format) |
| Plaintext | blob | raw data key bytes |
| KeyId | string | ARN of the KMS key used to encrypt |
| KeyMaterialId | string | 64-char hex; identifies the KMS key material used |

## Errors

| Error | HTTP | Condition |
|---|---|---|
| ValidationException | 400 | both or neither KeySpec/NumberOfBytes; invalid KeySpec; NumberOfBytes out of 1–1024 |
| NotFoundException | 400 | key not found |
| DisabledException | 400 | key is disabled |
| InvalidKeyUsageException | 400 | key KeyUsage is not ENCRYPT_DECRYPT, or key is not SYMMETRIC_DEFAULT |
| KMSInvalidStateException | 400 | key not in Enabled state |
| KMSInternalException | 500 | storage or crypto failure |

## kumolo deviations

- Only `SYMMETRIC_DEFAULT` keys are supported; RSA/SM2 ENCRYPT_DECRYPT keys return `InvalidKeyUsageException`.
- `Recipient`, `GrantTokens`, and `DryRun` are accepted and ignored.
- CiphertextBlob uses kumolo's internal envelope format (see `handler_dataplane.go`).
- `KeyMaterialId` is a random 64-char hex value generated when the key is created.
