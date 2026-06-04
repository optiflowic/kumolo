# KMS GenerateDataKeyPair

**URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateDataKeyPair.html  
**SDK struct**: `kms.GenerateDataKeyPairInput` / `kms.GenerateDataKeyPairOutput`  
**Last verified**: 2026-06-04

## Operation

Generates an asymmetric key pair. The private key is encrypted with a specified symmetric ENCRYPT_DECRYPT KMS key. The caller receives the plaintext public key (SPKI DER), the encrypted private key, and optionally the plaintext private key.

## Request Parameters

| Field | Type | Required | Notes |
|---|---|---|---|
| KeyId | string | yes | Symmetric ENCRYPT_DECRYPT key used to encrypt the generated private key |
| KeyPairSpec | string | yes | Type of key pair to generate (see below) |
| EncryptionContext | map[string]string | no | Additional authenticated data for the private key encryption |
| GrantTokens | []string | no | Not implemented in kumolo |
| DryRun | bool | no | Not implemented in kumolo |
| Recipient | object | no | Nitro enclave recipient; not implemented in kumolo |

### Valid KeyPairSpec values

RSA_2048, RSA_3072, RSA_4096, ECC_NIST_P256, ECC_NIST_P384, ECC_NIST_P521, ECC_SECG_P256K1, SM2, ECC_NIST_EDWARDS25519

**Kumolo-supported specs**: RSA_2048, RSA_3072, RSA_4096, ECC_NIST_P256, ECC_NIST_P384, ECC_NIST_P521, ECC_NIST_EDWARDS25519  
**Unsupported specs** (UnsupportedOperationException): ECC_SECG_P256K1, SM2

## Response Fields

| Field | Notes |
|---|---|
| KeyId | ARN of the symmetric KMS key used to encrypt the private key |
| KeyPairSpec | The requested key pair spec |
| PublicKey | SubjectPublicKeyInfo (SPKI) DER-encoded public key |
| PrivateKeyPlaintext | PKCS#8 DER-encoded plaintext private key |
| PrivateKeyCiphertextBlob | Private key encrypted with kumolo envelope (same as Encrypt) |
| KeyMaterialId | 64-char hex identifier of the symmetric key material used |

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| ValidationException | 400 | Missing or invalid KeyId/KeyPairSpec, EncryptionContext too large |
| NotFoundException | 400 | Symmetric key not found |
| DisabledException | 400 | Symmetric key is disabled |
| KMSInvalidStateException | 400 | Symmetric key is pending deletion |
| InvalidKeyUsageException | 400 | KeyId does not refer to a SYMMETRIC_DEFAULT / ENCRYPT_DECRYPT key |
| UnsupportedOperationException | 400 | KeyPairSpec valid but not supported in kumolo (ECC_SECG_P256K1, SM2) |
| KMSInternalException | 500 | Key pair generation or encryption failure |

## Kumolo Deviations

- `GrantTokens`, `DryRun`, and `Recipient` are silently ignored.
- Private key ciphertext uses the same kumolo envelope format as Encrypt/GenerateDataKey.
- ECC_SECG_P256K1 and SM2 return UnsupportedOperationException.
- `KeyMaterialId` is a kumolo-specific response field (not returned by real AWS); it is a 64-char hex identifier of the symmetric key material used to encrypt the private key.
