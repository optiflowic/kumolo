# GenerateMac / VerifyMac

URL (GenerateMac): https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateMac.html
URL (VerifyMac): https://docs.aws.amazon.com/kms/latest/APIReference/API_VerifyMac.html
SDK structs: `kms.GenerateMacInput` / `kms.GenerateMacOutput`, `kms.VerifyMacInput` / `kms.VerifyMacOutput`
Last verified: 2026-06-02

## GenerateMac Request Parameters

| Parameter    | Type    | Required | Constraints  | Notes                                     |
|-------------|---------|----------|--------------|-------------------------------------------|
| KeyId       | string  | Yes      | 1–2048 chars | Must be an HMAC key (GENERATE_VERIFY_MAC) |
| MacAlgorithm| string  | Yes      | —            | HMAC_SHA_224 / 256 / 384 / 512            |
| Message     | blob    | Yes      | 1–4096 bytes |                                           |
| DryRun      | boolean | No       | —            | Not supported; ignored                    |
| GrantTokens | []string| No       | 0–10 items   | Not supported; ignored                    |

## GenerateMac Response Fields

| Field        | Notes                  |
|-------------|------------------------|
| KeyId       | ARN of the HMAC key    |
| Mac         | Raw HMAC tag (blob)    |
| MacAlgorithm| Algorithm used         |

## VerifyMac Request Parameters

| Parameter    | Type    | Required | Constraints  |
|-------------|---------|----------|--------------|
| KeyId       | string  | Yes      | 1–2048 chars |
| MacAlgorithm| string  | Yes      | —            |
| Message     | blob    | Yes      | 1–4096 bytes |
| Mac         | blob    | Yes      | 1–6144 bytes |
| DryRun      | boolean | No       | Not supported; ignored |
| GrantTokens | []string| No       | Not supported; ignored |

## VerifyMac Response Fields

| Field        | Notes                                |
|-------------|--------------------------------------|
| KeyId       | ARN of the HMAC key                  |
| MacAlgorithm| Algorithm used                       |
| MacValid    | true if HMAC matches; error otherwise|

## Errors

| Error                   | HTTP | Condition                                  |
|-------------------------|------|--------------------------------------------|
| ValidationException     | 400  | Missing/invalid parameters                 |
| NotFoundException       | 400  | Key not found                              |
| DisabledException       | 400  | Key is disabled                            |
| KMSInvalidStateException| 400  | Key is pending deletion                    |
| InvalidKeyUsageException| 400  | Key is not GENERATE_VERIFY_MAC or algorithm mismatch |
| KMSInvalidMacException  | 400  | HMAC verification failed (VerifyMac only)  |
| KMSInternalException    | 500  | Internal error                             |

## kumolo Deviations

- `DryRun` and `GrantTokens` are silently ignored.
- HMAC key specs supported: `HMAC_224` (28 bytes), `HMAC_256` (32 bytes), `HMAC_384` (48 bytes), `HMAC_512` (64 bytes).
- `VerifyMac` returns `KMSInvalidMacException` (not `MacValid: false`) when verification fails.
