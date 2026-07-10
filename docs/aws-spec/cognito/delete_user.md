# DeleteUser

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_DeleteUser.html
- **Target**: `AWSCognitoIdentityProviderService.DeleteUser`
- **SDK**: `cognitoidentityprovider.DeleteUserInput` / `DeleteUserOutput`
- **Last verified**: 2026-07-10

## Request

| Field       | Type   | Required |
|-------------|--------|----------|
| AccessToken | string | Yes      |

- Authorized via access token (same validation chain as `GetUser`/`UpdateUserAttributes`):
  signature, expiry, `token_use=access`, revocation check.

## Behavior

Deletes the signed-in user's profile (matches `Storage.DeleteUser`, the same
storage call `AdminDeleteUser` uses). Consistent with the existing
`AdminDeleteUser` operation, kumolo does not clean up the user's refresh
tokens — a deleted user's existing access token becomes unusable on its next
call anyway, since every self-service operation looks the user up by `sub`
and gets `UserNotFoundException`.

## Response

HTTP 200 with an empty body: `{}`.

## Errors implemented

| Error type                 | HTTP | Trigger |
|-----------------------------|------|---------|
| InvalidParameterException   | 400  | missing `AccessToken` |
| NotAuthorizedException      | 400  | invalid/expired/revoked access token, wrong `token_use` |
| UserNotFoundException       | 400  | token's `sub` has no matching user |
| InternalErrorException      | 500  | storage failure |

## kumolo deviations

- No refresh-token cleanup on delete (matches existing `AdminDeleteUser` behavior).
- `ForbiddenException`, `OperationNotEnabledException`, `PasswordResetRequiredException`,
  `TooManyRequestsException`, `UserNotConfirmedException` (documented AWS errors) are
  not implemented — kumolo has no WAF, IAM, or password-reset-requirement modeling,
  and unconfirmed users can still call `DeleteUser` in kumolo.
