# Encrypt

**URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_Encrypt.html  
**SDK input**: `kms.EncryptInput`  
**SDK output**: `kms.EncryptOutput`  
**Last verified**: 2026-05-30

## Behaviour

Encrypts up to 4096 bytes of plaintext using the specified KMS key.
Plaintext and the CiphertextBlob are base64-encoded in the JSON wire format;
the SDK handles this automatically.

## Request parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| KeyId | string | yes | key ID, ARN, alias name, alias ARN; max 2048 chars |
| Plaintext | blob | yes | 1–4096 bytes |
| EncryptionAlgorithm | string | no | default `SYMMETRIC_DEFAULT`; must match key spec |
| EncryptionContext | map[string]string | no | AEAD additional data; same context required for Decrypt |
| GrantTokens | []string | no | ignored in kumolo |
| DryRun | boolean | no | ignored in kumolo |

## Response fields

| Field | Type | Notes |
|---|---|---|
| CiphertextBlob | blob | kumolo envelope format ciphertext |
| KeyId | string | ARN of the key used |
| EncryptionAlgorithm | string | algorithm that was used |

## Errors

| Error | HTTP | Condition |
|---|---|---|
| ValidationException | 400 | missing KeyId/Plaintext; Plaintext > 4096 bytes |
| NotFoundException | 400 | key not found |
| DisabledException | 400 | key is disabled |
| InvalidKeyUsageException | 400 | key KeyUsage is not ENCRYPT_DECRYPT |
| KMSInvalidStateException | 400 | key not in Enabled state |
| KMSInternalException | 500 | storage or crypto failure |

## kumolo deviations

- Only `SYMMETRIC_DEFAULT` keys are supported; asymmetric keys (RSA, SM2) return `InvalidKeyUsageException`.
- `EncryptionAlgorithm` must be absent or `SYMMETRIC_DEFAULT`; other values return `InvalidKeyUsageException`.
- `GrantTokens` and `DryRun` are accepted and ignored.
- CiphertextBlob uses kumolo's internal binary envelope: `[version(1)][keyID(36)][algo(1)][nonce(12)][aesgcm_sealed(...)]`.
