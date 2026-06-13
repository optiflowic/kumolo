# STS — AssumeRole

- Official URL: https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRole.html
- SDK struct: `sts.AssumeRoleInput` / `sts.AssumeRoleOutput`
- Last verified: 2026-06-13

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `RoleArn` | string | yes | ARN of the role to assume (min 20, max 2048 chars) |
| `RoleSessionName` | string | yes | Identifier for the session; reflected in response ARN and role ID (min 2, max 64, pattern `[\w+=,.@-]*`) |
| `DurationSeconds` | integer | no | Valid range: 900–43200 (default 3600); non-numeric or out-of-range values return `ValidationError` |

Ignored parameters (accepted without error): `ExternalId`, `Policy`, `PolicyArns`, `SerialNumber`, `SourceIdentity`, `Tags`, `TokenCode`, `TransitiveTagKeys`, `ProvidedContexts`.

## Response Elements

| Field | Type | Notes |
|---|---|---|
| `Credentials.AccessKeyId` | string | Temporary access key ID |
| `Credentials.SecretAccessKey` | string | Temporary secret key |
| `Credentials.SessionToken` | string | Security token |
| `Credentials.Expiration` | string | ISO 8601 expiration timestamp |
| `AssumedRoleUser.Arn` | string | `arn:aws:sts::{account}:assumed-role/{role-name}/{session-name}` |
| `AssumedRoleUser.AssumedRoleId` | string | `{role-id}:{session-name}` |

`role-name` is the last path segment of `RoleArn` (after the final `/`).

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ValidationError` | 400 | `RoleArn` or `RoleSessionName` is missing |
| `ValidationError` | 400 | `RoleArn` is shorter than 20 chars or longer than 2048 chars |
| `ValidationError` | 400 | `RoleArn` contains no `/` or ends with `/` (role name segment is empty) |
| `ValidationError` | 400 | `RoleSessionName` is shorter than 2 chars or longer than 64 chars |
| `ValidationError` | 400 | `RoleSessionName` does not match `[\w+=,.@-]*` |
| `ValidationError` | 400 | `DurationSeconds` is provided and is less than 900 or greater than 43200 |

## kumolo Deviations

- All calls return the same fixed credentials regardless of which role is assumed.
- `DurationSeconds` is validated but expiration is always a far-future fixed timestamp (not computed from the duration).
- Session policy parameters (`Policy`, `PolicyArns`) are accepted but not evaluated.
- MFA parameters (`SerialNumber`, `TokenCode`) are accepted but not validated.
- `SourceIdentity`, `Tags`, `TransitiveTagKeys` are accepted but not stored or returned.
- The role ID portion of `AssumedRoleId` is always the fixed value `AROAIOSFODNN7EXAMPLE`.
