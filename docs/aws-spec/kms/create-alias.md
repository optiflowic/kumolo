# CreateAlias

- **URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_CreateAlias.html
- **Target**: `TrentService.CreateAlias`
- **SDK input**: `kms.CreateAliasInput`
- **SDK output**: `kms.CreateAliasOutput`
- **Last verified**: 2026-05-30

## Request

| Field | Type | Required | Notes |
|---|---|---|---|
| `AliasName` | string | Yes | 1–256 chars, pattern `^alias/[a-zA-Z0-9/_-]+$`; must not begin with `alias/aws/` |
| `TargetKeyId` | string | Yes | key ID or key ARN of the target customer managed key |

## Behavior

- Each alias maps one-to-one to a key; a key can have up to 256 aliases.
- Returns an empty HTTP 200 body on success.
- Key state of the target key must be `Enabled` or `Disabled` (not `PendingDeletion`).

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `AlreadyExistsException` | 400 | alias name already in use |
| `InvalidAliasNameException` | 400 | alias name fails pattern or begins with `alias/aws/` |
| `KMSInvalidStateException` | 400 | target key is in `PendingDeletion` state |
| `NotFoundException` | 400 | target key not found |
| `LimitExceededException` | 400 | target key already has 256 aliases |
| `KMSInternalException` | 500 | storage failure |
