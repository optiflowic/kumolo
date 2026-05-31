# CreateKey

- **URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_CreateKey.html
- **Target**: `TrentService.CreateKey`
- **SDK input**: `kms.CreateKeyInput`
- **SDK output**: `kms.CreateKeyOutput`
- **Last verified**: 2026-05-30

## Request

All fields are optional.

| Field | Type | Notes |
|---|---|---|
| `Description` | string | max 8192 chars |
| `KeySpec` | string | default `SYMMETRIC_DEFAULT` |
| `KeyUsage` | string | default per KeySpec (see below) |
| `MultiRegion` | bool | stored as-is; kumolo does not replicate |
| `Origin` | string | only `AWS_KMS` accepted; others return `UnsupportedOperationException` |
| `Policy` | string | JSON policy document; default policy applied if omitted |
| `Tags` | array | accepted but not persisted |
| `BypassPolicyLockoutSafetyCheck` | bool | accepted, not enforced |

## Supported KeySpec values and default KeyUsage

| KeySpec | Default KeyUsage | Valid KeyUsage options |
|---|---|---|
| `SYMMETRIC_DEFAULT` | `ENCRYPT_DECRYPT` | only `ENCRYPT_DECRYPT` |
| `RSA_2048` / `RSA_3072` / `RSA_4096` | `ENCRYPT_DECRYPT` | `ENCRYPT_DECRYPT`, `SIGN_VERIFY` |
| `ECC_NIST_P256` / `ECC_NIST_P384` / `ECC_NIST_P521` | `SIGN_VERIFY` | `SIGN_VERIFY`, `KEY_AGREEMENT` |
| `ECC_SECG_P256K1` | `SIGN_VERIFY` | only `SIGN_VERIFY` |
| `ECC_NIST_EDWARDS25519` | `SIGN_VERIFY` | only `SIGN_VERIFY` |
| `ML_DSA_44` / `ML_DSA_65` / `ML_DSA_87` | `SIGN_VERIFY` | only `SIGN_VERIFY` |
| `HMAC_224` / `HMAC_256` / `HMAC_384` / `HMAC_512` | `GENERATE_VERIFY_MAC` | only `GENERATE_VERIFY_MAC` |
| `SM2` | `ENCRYPT_DECRYPT` | `ENCRYPT_DECRYPT`, `SIGN_VERIFY`, `KEY_AGREEMENT` |

## Response

Returns `{"KeyMetadata": {...}}`. See [KeyMetadata fields](#keymetadata-fields).

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | invalid KeySpec, incompatible KeyUsage; Description > 8192 chars |
| `UnsupportedOperationException` | 400 | Origin != `AWS_KMS` |
| `KMSInternalException` | 500 | storage failure |

## KeyMetadata fields

| Field | Notes |
|---|---|
| `KeyId` | UUID v4 |
| `Arn` | `arn:aws:kms:us-east-1:000000000000:key/<uuid>` |
| `AWSAccountId` | always `000000000000` |
| `KeyState` | `Enabled` on creation |
| `Enabled` | `true` on creation |
| `KeyManager` | always `CUSTOMER` |
| `Origin` | `AWS_KMS` |
| `CreationDate` | Unix epoch (float64) |
| `CustomerMasterKeySpec` | same as `KeySpec` (deprecated alias) |
| `EncryptionAlgorithms` | for symmetric / RSA encrypt keys |
| `SigningAlgorithms` | for RSA / ECC / ML-DSA signing keys |
| `MacAlgorithms` | for HMAC keys |
| `KeyAgreementAlgorithms` | for ECDH / SM2 key agreement keys |

## Storage

- `{dataDir}/kms/keys/<key-id>/meta.json` — KeyMetadata struct
- `{dataDir}/kms/keys/<key-id>/policy.json` — raw policy string
