# Decrypt

**URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_Decrypt.html  
**SDK input**: `kms.DecryptInput`  
**SDK output**: `kms.DecryptOutput`  
**Last verified**: 2026-05-30

## Behaviour

Decrypts ciphertext produced by Encrypt, GenerateDataKey, or GenerateDataKeyWithoutPlaintext.
For symmetric keys the `KeyId` in the request is optional because the key ID is embedded in the
kumolo envelope format; if provided it is verified to match.

## Request parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| CiphertextBlob | blob | yes | kumolo envelope ciphertext |
| KeyId | string | no | if provided, must match embedded key ID; 1–2048 chars |
| EncryptionAlgorithm | string | no | default `SYMMETRIC_DEFAULT` |
| EncryptionContext | map[string]string | no | must match exactly what was used at encrypt time; each key ≤ 2048 bytes, each value ≤ 2048 bytes, total serialised size ≤ 8192 bytes |
| GrantTokens | []string | no | ignored in kumolo |
| DryRun | boolean | no | ignored in kumolo |
| DryRunModifiers | []string | no | ignored in kumolo |
| Recipient | object | no | not supported in kumolo |

## Response fields

| Field | Type | Notes |
|---|---|---|
| Plaintext | blob | decrypted bytes |
| KeyId | string | ARN of the key used to decrypt |
| EncryptionAlgorithm | string | algorithm used |
| KeyMaterialId | string | 64-char hex (symmetric keys only) |

## Errors

| Error | HTTP | Condition |
|---|---|---|
| ValidationException | 400 | missing CiphertextBlob; EncryptionContext key/value > 2048 bytes or total > 8192 bytes |
| NotFoundException | 400 | key embedded in ciphertext not found |
| DisabledException | 400 | key is disabled |
| InvalidKeyUsageException | 400 | key KeyUsage is not ENCRYPT_DECRYPT |
| IncorrectKeyException | 400 | provided KeyId does not match key embedded in ciphertext |
| InvalidCiphertextException | 400 | ciphertext is malformed or AEAD authentication failed (wrong context or tampered) |
| KMSInvalidStateException | 400 | key not in Enabled state |
| KMSInternalException | 500 | storage or crypto failure |

## kumolo deviations

- Only `SYMMETRIC_DEFAULT` ciphertexts (produced by kumolo Encrypt/GenerateDataKey) can be decrypted.
- `Recipient`, `GrantTokens`, `DryRun`, `DryRunModifiers` are accepted and ignored.
- Ciphertext envelope format: `[version(1)][keyID(36)][algo(1)][nonce(12)][aesgcm_sealed(...)]`.
  Key ID is always embedded; `KeyId` param is validated against it when present.
