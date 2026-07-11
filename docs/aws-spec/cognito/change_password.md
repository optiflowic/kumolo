# ChangePassword

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ChangePassword.html
- **Target**: `AWSCognitoIdentityProviderService.ChangePassword`
- **SDK**: `cognitoidentityprovider.ChangePasswordInput` / `ChangePasswordOutput`
- **Last verified**: 2026-07-11

## Request

| Field            | Type   | Required | Notes |
|------------------|--------|----------|-------|
| AccessToken      | string | Yes      | authorizes the request |
| PreviousPassword | string | Conditional | required when the user has a password hash set |
| ProposedPassword | string | Yes      | max 256 chars; must satisfy pool password policy |

## Behavior

- Authorizes via the same access-token chain as `GetUser`/`UpdateUserAttributes`: resolve pool
  from the token issuer, verify the JWT signature/expiry/`token_use`, check revocation, then
  look up the user by `sub`.
- If the user has a stored password hash, `PreviousPassword` is required and must match via
  `bcrypt.CompareHashAndPassword`; a missing or wrong value returns `NotAuthorizedException`
  ("Incorrect username or password.", matching `InitiateAuth`'s `USER_PASSWORD_AUTH` message).
- Validates `ProposedPassword` against the pool's password policy (see
  `docs/aws-spec/cognito/password_policy.md`) before touching any state.
- On success: bcrypt-hashes `ProposedPassword` and replaces the user's password hash. `Status`
  is left unchanged.

## Response

HTTP 200 with an empty body: `{}`.

## Errors implemented

| Error type                | HTTP | Trigger |
|----------------------------|------|---------|
| InvalidParameterException  | 400  | missing `AccessToken`/`ProposedPassword` |
| NotAuthorizedException     | 400  | invalid/expired/revoked access token, wrong `token_use`, missing `PreviousPassword` when the user has a password, or `PreviousPassword` mismatch |
| UserNotFoundException      | 400  | token's `sub` has no matching user |
| InvalidPasswordException   | 400  | `ProposedPassword` fails the pool's password policy |
| InternalErrorException     | 500  | storage failure |

## kumolo deviations

- `UserNotConfirmedException`, `PasswordResetRequiredException`,
  `PasswordHistoryPolicyViolationException`, `ForbiddenException`, `LimitExceededException`,
  `OperationNotEnabledException`, `TooManyRequestsException` (documented AWS errors) are not
  implemented.
- Real AWS requires the access token's scope to include `aws.cognito.signin.user.admin`; kumolo
  doesn't model OAuth scopes on access tokens, so this is not checked.
