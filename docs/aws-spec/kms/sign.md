# Sign / Verify

URL (Sign): https://docs.aws.amazon.com/kms/latest/APIReference/API_Sign.html
URL (Verify): https://docs.aws.amazon.com/kms/latest/APIReference/API_Verify.html
SDK structs: `kms.SignInput` / `kms.SignOutput`, `kms.VerifyInput` / `kms.VerifyOutput`
Last verified: 2026-06-02

## Sign Request Parameters

| Parameter        | Type    | Required | Constraints    | Notes                                      |
|-----------------|---------|----------|----------------|--------------------------------------------|
| KeyId           | string  | Yes      | 1–2048 chars   | Must be a SIGN_VERIFY asymmetric key       |
| Message         | blob    | Yes      | 1–4096 bytes   |                                            |
| SigningAlgorithm| string  | Yes      | —              | Must be compatible with key spec           |
| MessageType     | string  | No       | RAW\|DIGEST    | Default: RAW                               |
| DryRun          | boolean | No       | —              | Not supported; ignored                     |
| GrantTokens     | []string| No       | 0–10 items     | Not supported; ignored                     |

## Sign Response Fields

| Field            | Notes                                            |
|-----------------|--------------------------------------------------|
| KeyId           | ARN of the signing key                           |
| Signature       | DER-encoded signature (blob)                     |
| SigningAlgorithm | Algorithm used                                  |

## Verify Request Parameters

| Parameter        | Type    | Required | Constraints    |
|-----------------|---------|----------|----------------|
| KeyId           | string  | Yes      | 1–2048 chars   |
| Message         | blob    | Yes      | 1–4096 bytes   |
| Signature       | blob    | Yes      | 1–6144 bytes   |
| SigningAlgorithm| string  | Yes      | —              |
| MessageType     | string  | No       | RAW\|DIGEST (default RAW) |
| DryRun          | boolean | No       | Not supported; ignored |
| GrantTokens     | []string| No       | Not supported; ignored |

## Verify Response Fields

| Field             | Notes                                                        |
|------------------|--------------------------------------------------------------|
| KeyId            | ARN of the signing key                                       |
| SignatureValid   | true on success; otherwise KMSInvalidSignatureException      |
| SigningAlgorithm | Algorithm used                                               |

## Supported Signing Algorithms

| KeySpec             | Algorithms                                                                         |
|--------------------|------------------------------------------------------------------------------------|
| RSA_2048/3072/4096  | RSASSA_PKCS1_V1_5_SHA_256/384/512, RSASSA_PSS_SHA_256/384/512                     |
| ECC_NIST_P256       | ECDSA_SHA_256                                                                      |
| ECC_NIST_P384       | ECDSA_SHA_384                                                                      |
| ECC_NIST_P521       | ECDSA_SHA_512                                                                      |
| ECC_NIST_EDWARDS25519| ED25519_SHA_512 (RAW only)                                                        |

## MessageType

- `RAW` (default): kumolo hashes the message before signing.
- `DIGEST`: caller provides the pre-computed digest; kumolo signs it directly.
- ED25519_SHA_512 only supports `RAW`; if `DIGEST` is specified, return `ValidationException`.

## Errors

| Error                      | HTTP | Condition                                           |
|---------------------------|------|-----------------------------------------------------|
| ValidationException        | 400  | Missing/invalid params, DIGEST with Ed25519         |
| NotFoundException          | 400  | Key not found                                       |
| DisabledException          | 400  | Key is disabled                                     |
| KMSInvalidStateException   | 400  | Key is pending deletion                             |
| InvalidKeyUsageException   | 400  | Key is not SIGN_VERIFY or algorithm mismatch        |
| KMSInvalidSignatureException| 400  | Signature verification failed (Verify only)         |
| KMSInternalException       | 500  | Internal error                                      |

## kumolo Deviations

- `DryRun` and `GrantTokens` are silently ignored.
- `ED25519_PH_SHA_512` (prehash variant) is not supported; returns `UnsupportedOperationException`.
- SM2DSA and ML_DSA variants are not supported; returns `UnsupportedOperationException`.
- RSA PSS salt length: signed with `PSSSaltLengthEqualsHash`; verified with `PSSSaltLengthAuto`.
