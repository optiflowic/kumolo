# AdminSetUserPassword

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminSetUserPassword.html
- **Target**: `AWSCognitoIdentityProviderService.AdminSetUserPassword`
- **SDK**: `cognitoidentityprovider.AdminSetUserPasswordInput` / `AdminSetUserPasswordOutput`
- **Last verified**: 2026-06-25

## Request

| Field      | Type   | Required | Notes |
|------------|--------|----------|-------|
| UserPoolId | string | Yes      |       |
| Username   | string | Yes      |       |
| Password   | string | Yes      | max 256 chars |
| Permanent  | bool   | No       | default false |

## Response

HTTP 200 + empty body `{}`

## Behavior

- `Permanent=true`: bcrypt-hash the new password, set user status to `CONFIRMED`.
- `Permanent=false` (default): bcrypt-hash the new password, set user status to `FORCE_CHANGE_PASSWORD`.
- Password history policy not enforced (PasswordHistoryPolicyViolationException not returned).

## Errors implemented

| Error type                | HTTP | Trigger |
|---------------------------|------|---------|
| ResourceNotFoundException | 400  | pool not found |
| UserNotFoundException     | 400  | username not found |
| InvalidPasswordException  | 400  | password fails minimum length policy |
| InvalidParameterException | 400  | missing required field |
| InternalErrorException    | 500  | storage failure |

## kumolo deviations

- `PasswordHistoryPolicyViolationException` not implemented (no history tracking).
- Password policy: only minimum length checked, complexity rules not enforced.
