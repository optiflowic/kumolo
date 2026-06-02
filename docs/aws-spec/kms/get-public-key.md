# GetPublicKey

**URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_GetPublicKey.html  
**SDK struct**: `kms.GetPublicKeyInput` / `kms.GetPublicKeyOutput`  
**Last verified**: 2026-06-02

## Request

| Field       | Type   | Required | Constraints                       |
|-------------|--------|----------|-----------------------------------|
| KeyId       | string | yes      | 1–2048 chars; key ID, key ARN, alias name, or alias ARN |
| GrantTokens | []string | no     | 0–10 items; ignored by kumolo     |

## Response (HTTP 200)

| Field                  | Condition                              |
|------------------------|----------------------------------------|
| KeyId                  | always — key ARN                       |
| KeySpec                | always                                 |
| CustomerMasterKeySpec  | always (deprecated alias for KeySpec)  |
| KeyUsage               | always                                 |
| PublicKey              | always — DER-encoded SubjectPublicKeyInfo (SPKI, RFC 5280) |
| EncryptionAlgorithms   | only when KeyUsage = ENCRYPT_DECRYPT   |
| SigningAlgorithms      | only when KeyUsage = SIGN_VERIFY       |
| KeyAgreementAlgorithms | only when KeyUsage = KEY_AGREEMENT     |

## Errors

| Error                      | HTTP | Condition                                                      |
|----------------------------|------|----------------------------------------------------------------|
| ValidationException        | 400  | missing or malformed KeyId                                     |
| InvalidArnException        | 400  | malformed ARN                                                  |
| NotFoundException          | 400  | key not found                                                  |
| KMSInvalidStateException   | 400  | key is in PendingDeletion state                                |
| InvalidKeyUsageException   | 400  | key is SYMMETRIC_DEFAULT or HMAC (not an asymmetric key)       |
| UnsupportedOperationException | 400 | kumolo deviation — see below                                 |
| KMSInternalException       | 500  | internal error                                                 |

Note: AWS also lists `DisabledException`, `KeyUnavailableException`, and `DependencyTimeoutException`.
In kumolo, GetPublicKey is allowed on Disabled keys (consistent with the AWS key-state table).

## Key State Compatibility

Allowed in: Enabled, Disabled  
Rejected in: PendingDeletion (→ KMSInvalidStateException)

## Supported Key Specs

Key material is generated (and GetPublicKey works) for:

| KeySpec              | Algorithm                   | Key size        |
|----------------------|-----------------------------|-----------------|
| RSA_2048             | `crypto/rsa`                | 2048-bit RSA    |
| RSA_3072             | `crypto/rsa`                | 3072-bit RSA    |
| RSA_4096             | `crypto/rsa`                | 4096-bit RSA    |
| ECC_NIST_P256        | `crypto/ecdsa` + P-256      | 256-bit ECC     |
| ECC_NIST_P384        | `crypto/ecdsa` + P-384      | 384-bit ECC     |
| ECC_NIST_P521        | `crypto/ecdsa` + P-521      | 521-bit ECC     |
| ECC_NIST_EDWARDS25519| `crypto/ed25519`            | 256-bit EdDSA   |

## kumolo Deviations

- **ECC_SECG_P256K1**: CreateKey succeeds (metadata only), but GetPublicKey returns `UnsupportedOperationException` because secp256k1 is not in the Go standard library and kumolo adds no third-party crypto dependency for it.
- **SM2**: Same as ECC_SECG_P256K1; SM2 is a Chinese national standard not in the Go stdlib.
- **ML_DSA_44 / ML_DSA_65 / ML_DSA_87**: Same; post-quantum lattice-based signatures are not in the Go stdlib.
- **GrantTokens**: Accepted and silently ignored (kumolo has no IAM/grants layer).

## Storage

Private key is stored as PKCS#8 DER in `keys/{keyID}/material.json`:

```json
{
  "PrivateKeyDER": "<base64>",
  "KeyMaterialId": "<64-char hex>"
}
```

The public key is derived on-the-fly by parsing the stored PKCS#8 private key and marshaling its public portion using `x509.MarshalPKIXPublicKey` (SPKI format).
