# KMS GenerateDataKeyPairWithoutPlaintext

**URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateDataKeyPairWithoutPlaintext.html  
**SDK struct**: `kms.GenerateDataKeyPairWithoutPlaintextInput` / `kms.GenerateDataKeyPairWithoutPlaintextOutput`  
**Last verified**: 2026-06-04

## Operation

Same as GenerateDataKeyPair but does NOT return the plaintext private key. Only the encrypted private key is returned alongside the plaintext public key.

## Request Parameters

Identical to GenerateDataKeyPair. See `generate-data-key-pair.md`.

## Response Fields

| Field | Notes |
|---|---|
| KeyId | ARN of the symmetric KMS key used to encrypt the private key |
| KeyPairSpec | The requested key pair spec |
| PublicKey | SubjectPublicKeyInfo (SPKI) DER-encoded public key |
| PrivateKeyCiphertextBlob | Private key encrypted with kumolo envelope (same as Encrypt) |
| KeyMaterialId | 64-char hex identifier of the symmetric key material used |

Note: `PrivateKeyPlaintext` is NOT returned.

## Implemented Errors

Same as GenerateDataKeyPair. See `generate-data-key-pair.md`.

## Kumolo Deviations

Same as GenerateDataKeyPair. See `generate-data-key-pair.md`.
