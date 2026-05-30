# UpdateAlias

- **URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_UpdateAlias.html
- **Target**: `TrentService.UpdateAlias`
- **SDK input**: `kms.UpdateAliasInput`
- **SDK output**: `kms.UpdateAliasOutput`
- **Last verified**: 2026-05-30

## Request

| Field | Type | Required | Notes |
|---|---|---|---|
| `AliasName` | string | Yes | 1–256 chars, pattern `^alias/[a-zA-Z0-9/_-]+$` |
| `TargetKeyId` | string | Yes | key ID or key ARN of the new target key |

## Behavior

- Re-points an existing alias to a different key.
- Returns an empty HTTP 200 body on success.
- The new target key must have the same `KeySpec` and `KeyUsage` as the current target key.
  - "Same type" means: both SYMMETRIC_DEFAULT, or both asymmetric (RSA/ECC/SM2/ML_DSA), or both HMAC.
  - AWS enforces matching `KeyUsage` as well.
- `alias/aws/` prefixed aliases cannot be updated (reserved for AWS managed keys).

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `InvalidAliasNameException` | 400 | alias name begins with `alias/aws/` |
| `NotFoundException` | 400 | alias or target key not found |
| `KMSInvalidStateException` | 400 | current or new key has incompatible type/usage |
| `KMSInternalException` | 500 | storage failure |
