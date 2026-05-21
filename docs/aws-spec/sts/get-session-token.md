# STS — GetSessionToken

- Official URL: https://docs.aws.amazon.com/STS/latest/APIReference/API_GetSessionToken.html
- SDK struct: `sts.GetSessionTokenInput` / `sts.GetSessionTokenOutput`
- Last verified: 2026-05-21

## Request Parameters

All parameters are optional and ignored by kumolo.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `DurationSeconds` | integer | no | Valid range: 900–129600 (default 43200); ignored |
| `SerialNumber` | string | no | MFA device serial or ARN; ignored |
| `TokenCode` | string | no | 6-digit MFA code; ignored |

## Response Elements

| Field | Type | Notes |
|---|---|---|
| `Credentials.AccessKeyId` | string | Temporary access key ID |
| `Credentials.SecretAccessKey` | string | Temporary secret key |
| `Credentials.SessionToken` | string | Security token |
| `Credentials.Expiration` | string | ISO 8601 expiration timestamp |

## Implemented Errors

None. `RegionDisabled` (HTTP 403) is not applicable to a local emulator.

## kumolo Deviations

- Always returns fixed credentials regardless of input.
- `DurationSeconds` is ignored; expiration is always a far-future fixed timestamp.
- MFA parameters are accepted but not validated; MFA-protected call paths cannot be tested.
