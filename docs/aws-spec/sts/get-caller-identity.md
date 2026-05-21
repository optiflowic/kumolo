# STS — GetCallerIdentity

- Official URL: https://docs.aws.amazon.com/STS/latest/APIReference/API_GetCallerIdentity.html
- SDK struct: `sts.GetCallerIdentityInput` / `sts.GetCallerIdentityOutput`
- Last verified: 2026-05-21

## Request Parameters

None. No parameters are accepted.

## Response Elements

| Field | Type | Notes |
|---|---|---|
| `Account` | string | AWS account ID of the calling entity |
| `Arn` | string | ARN of the calling entity (min 20, max 2048 chars) |
| `UserId` | string | Unique identifier of the calling entity; format varies by entity type |

For a root user, `UserId` equals the account ID. For an IAM user, it is the user's unique ID (e.g. `AIDAXXXXXXXX`). For an assumed-role session, it is `{role-id}:{session-name}`.

## Implemented Errors

None specific to this operation (uses common error types only).

Notable: AWS always returns 200 for this operation even if the caller's identity policy explicitly denies `sts:GetCallerIdentity`.

## kumolo Deviations

- Always returns fixed values: `Account = "000000000000"`, `Arn = "arn:aws:iam::000000000000:root"`, `UserId = "000000000000"`.
- Multiple caller identities are not supported; all callers appear as the same root user.
