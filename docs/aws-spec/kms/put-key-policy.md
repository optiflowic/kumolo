# PutKeyPolicy

- **URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_PutKeyPolicy.html
- **Target**: `TrentService.PutKeyPolicy`
- **SDK input**: `kms.PutKeyPolicyInput`
- **SDK output**: `kms.PutKeyPolicyOutput`
- **Last verified**: 2026-05-30

## Request

| Field | Type | Required | Notes |
|---|---|---|---|
| `KeyId` | string | Yes | key ID or ARN |
| `Policy` | string | Yes | JSON policy document, max 32768 bytes |
| `PolicyName` | string | No | must be `"default"` if provided |
| `BypassPolicyLockoutSafetyCheck` | bool | No | accepted, not enforced |

## Response

HTTP 200 with empty body on success.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NotFoundException` | 400 | key not found |
| `InvalidArnException` | 400 | malformed ARN |
| `LimitExceededException` | 400 | policy > 32768 bytes |
| `KMSInvalidStateException` | 400 | key not in a state that allows this operation |
| `KMSInternalException` | 500 | storage failure |
