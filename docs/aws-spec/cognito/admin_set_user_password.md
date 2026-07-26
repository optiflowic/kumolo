# AdminSetUserPassword

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminSetUserPassword.html
- **Target**: `AWSCognitoIdentityProviderService.AdminSetUserPassword`
- **SDK**: `cognitoidentityprovider.AdminSetUserPasswordInput` / `AdminSetUserPasswordOutput`
- **Last verified**: 2026-07-26

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

- `Permanent=true`: hash the new password via `hashPassword` (SHA-256 prehash + bcrypt, see
  `docs/aws-spec/cognito/password_policy.md` Implementation), set user status to `CONFIRMED`.
- `Permanent=false` (default): same hashing, set user status to `FORCE_CHANGE_PASSWORD`.
- Password history policy not enforced (PasswordHistoryPolicyViolationException not returned).

## Errors implemented

| Error type                | HTTP | Trigger |
|---------------------------|------|---------|
| ResourceNotFoundException | 400  | pool not found |
| UserNotFoundException     | 400  | username not found |
| InvalidPasswordException  | 400  | password fails pool's password policy |
| InvalidParameterException | 400  | missing required field |
| InternalErrorException    | 500  | storage failure |

## kumolo deviations

- `PasswordHistoryPolicyViolationException` not implemented (no history tracking).
- Password policy enforcement: see `docs/aws-spec/cognito/password_policy.md`.
