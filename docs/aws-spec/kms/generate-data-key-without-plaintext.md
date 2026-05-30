# GenerateDataKeyWithoutPlaintext

**URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateDataKeyWithoutPlaintext.html  
**SDK input**: `kms.GenerateDataKeyWithoutPlaintextInput`  
**SDK output**: `kms.GenerateDataKeyWithoutPlaintextOutput`  
**Last verified**: 2026-05-30

## Behaviour

Identical to GenerateDataKey except the plaintext data key is NOT returned.
Useful when the caller wants to defer encryption and avoid holding the plaintext.

## Request parameters

Same as GenerateDataKey except no `Recipient` parameter.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| KeyId | string | yes | key ID, ARN, alias name, alias ARN; max 2048 chars |
| KeySpec | string | no | `AES_256` (32 bytes) or `AES_128` (16 bytes) |
| NumberOfBytes | integer | no | 1–1024; cannot be used together with KeySpec |
| EncryptionContext | map[string]string | no | AEAD additional data |
| GrantTokens | []string | no | ignored in kumolo |
| DryRun | boolean | no | ignored in kumolo |

## Response fields

| Field | Type | Notes |
|---|---|---|
| CiphertextBlob | blob | data key encrypted under the KMS key |
| KeyId | string | ARN of the KMS key used to encrypt |
| KeyMaterialId | string | 64-char hex |

No `Plaintext` field in the response.

## Errors

Same as GenerateDataKey.

## kumolo deviations

Same as GenerateDataKey.
