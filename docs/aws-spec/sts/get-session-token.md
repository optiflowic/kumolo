# STS — GetSessionToken

- Official URL: https://docs.aws.amazon.com/STS/latest/APIReference/API_GetSessionToken.html
- SDK struct: `sts.GetSessionTokenInput` / `sts.GetSessionTokenOutput`
- Last verified: 2026-05-21

## Request Parameters

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `DurationSeconds` | integer | no | Valid range: 900–129600 (default 43200); out-of-range values return `ValidationError` |
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

| Error | HTTP | Condition |
|---|---|---|
| `ValidationError` | 400 | `DurationSeconds` is provided and is less than 900 or greater than 129600 |

## kumolo Deviations

- Always returns fixed credentials regardless of input.
- `DurationSeconds` is validated but expiration is always a far-future fixed timestamp (not computed from the duration).
- MFA parameters are accepted but not validated; MFA-protected call paths cannot be tested.
