# AdminDisableUser

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminDisableUser.html
- **Target**: `AWSCognitoIdentityProviderService.AdminDisableUser`
- **SDK**: `cognitoidentityprovider.AdminDisableUserInput` / `AdminDisableUserOutput`
- **Last verified**: 2026-07-10
- See also: [AdminEnableUser](admin_enable_user.md) (the inverse operation, same request/response shape).

## Request

| Field      | Type   | Required |
|------------|--------|----------|
| UserPoolId | string | Yes      |
| Username   | string | Yes      |

## Behavior

Sets `UserMetadata.Enabled = false`, then revokes every outstanding session for
the user: all refresh-token families are revoked (`RevokeOriginJTIsForSub`) and
all refresh tokens are deleted (`DeleteRefreshTokensBySub`) — the same
mechanism `GlobalSignOut` uses. Matches AWS: "revokes all access tokens for
the user."

A disabled user can't sign in: `InitiateAuth` (`USER_PASSWORD_AUTH` and
`REFRESH_TOKEN_AUTH`) reject with `NotAuthorizedException` / `"User is disabled."`
before any other check. The user still appears in `ListUsers`/`AdminGetUser`
results with `Enabled: false`, matching AWS.

## Response

HTTP 200 with an empty body: `{}`.

## Errors implemented

| Error type                 | HTTP | Trigger |
|-----------------------------|------|---------|
| InvalidParameterException   | 400  | missing `UserPoolId`/`Username` |
| ResourceNotFoundException   | 400  | pool not found |
| UserNotFoundException       | 400  | user not found |
| InternalErrorException      | 500  | storage failure |

## kumolo deviations

- The `NEW_PASSWORD_REQUIRED` challenge (mid-flow after a successful
  `USER_PASSWORD_AUTH`) does not re-check `Enabled` — only the initial
  `InitiateAuth` call and refresh-token exchange do. A user disabled in the
  narrow window between those two calls could still complete the challenge.
  `USER_SRP_AUTH` isn't implemented yet (tracked separately in #429), so its
  gating isn't addressed here either.
- `OperationNotEnabledException`, `TooManyRequestsException`, `NotAuthorizedException`
  (IAM-authorization variant, as opposed to the "User is disabled." sign-in
  variant kumolo does implement) are not implemented — kumolo has no IAM
  policy evaluation.
