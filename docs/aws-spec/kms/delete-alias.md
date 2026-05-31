# DeleteAlias

- **URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_DeleteAlias.html
- **Target**: `TrentService.DeleteAlias`
- **SDK input**: `kms.DeleteAliasInput`
- **SDK output**: `kms.DeleteAliasOutput`
- **Last verified**: 2026-05-30

## Request

| Field | Type | Required | Notes |
|---|---|---|---|
| `AliasName` | string | Yes | 1–256 chars, pattern `^[a-zA-Z0-9:/_-]+$`; must begin with `alias/` |

## Behavior

- Returns an empty HTTP 200 body on success.
- Does not affect the underlying KMS key.
- Succeeds regardless of the key state of the associated key (including `PendingDeletion`).

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | AliasName is empty, > 256 chars, or does not match `^alias/[a-zA-Z0-9/_-]+$` |
| `NotFoundException` | 400 | alias not found |
| `KMSInternalException` | 500 | storage failure |
