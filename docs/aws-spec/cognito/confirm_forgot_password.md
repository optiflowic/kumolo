# ConfirmForgotPassword

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ConfirmForgotPassword.html
- **Target**: `AWSCognitoIdentityProviderService.ConfirmForgotPassword`
- **SDK**: `cognitoidentityprovider.ConfirmForgotPasswordInput` / `ConfirmForgotPasswordOutput`
- **Last verified**: 2026-07-26

## Request

| Field            | Type   | Required | Notes |
|------------------|--------|----------|-------|
| ClientId         | string | Yes      | resolves the user pool |
| Username         | string | Yes      | username or alias |
| ConfirmationCode | string | Yes      | code from `ForgotPassword` |
| Password         | string | Yes      | max 256 chars; must satisfy pool password policy |
| SecretHash       | string | No       | accepted but ignored |
| ClientMetadata   | map    | No       | Lambda trigger input only; kumolo ignores |
| AnalyticsMetadata | object | No      | Pinpoint; kumolo ignores |
| UserContextData   | object | No      | threat protection; kumolo ignores |

## Behavior

- Resolves the user pool from `ClientId`, then updates the user identified by `Username`.
- Rejects `UNCONFIRMED` users with `UserNotConfirmedException` (a password can't be reset
  before the account itself is confirmed).
- Compares `ConfirmationCode` against the code stored by `ForgotPassword` using
  `subtle.ConstantTimeCompare` (same pattern as `ConfirmSignUp`/`VerifyUserAttribute`). No
  pending code (empty) never matches.
- Validates `Password` against the pool's password policy (see
  `docs/aws-spec/cognito/password_policy.md`) before touching any state.
- On success: hashes `Password` via `hashPassword` (SHA-256 prehash + bcrypt, see
  `docs/aws-spec/cognito/password_policy.md` Implementation), replaces the user's password
  hash, and clears the pending reset code. User `Status` is left unchanged.

## Response

HTTP 200 with an empty body: `{}`.

## Errors implemented

| Error type                | HTTP | Trigger |
|----------------------------|------|---------|
| InvalidParameterException  | 400  | missing `ClientId`/`Username`/`ConfirmationCode`/`Password` |
| ResourceNotFoundException  | 400  | `ClientId` not found in any pool |
| UserNotFoundException      | 400  | `Username` not found in the resolved pool |
| UserNotConfirmedException  | 400  | user status is `UNCONFIRMED` |
| CodeMismatchException      | 400  | `ConfirmationCode` doesn't match (or none is pending) |
| InvalidPasswordException   | 400  | `Password` fails the pool's password policy |
| InternalErrorException     | 500  | storage failure |

## kumolo deviations

- No `ExpiredCodeException` — kumolo's reset codes don't carry an expiry timestamp; a stale
  code stays valid until overwritten by another `ForgotPassword` call (same deviation as
  `VerifyUserAttribute`).
- `TooManyFailedAttemptsException`, `PasswordHistoryPolicyViolationException`,
  `ForbiddenException`, `InvalidLambdaResponseException`, `LimitExceededException`,
  `NotAuthorizedException` (SecretHash mismatch), `OperationNotEnabledException`,
  `TooManyRequestsException`, `UnexpectedLambdaException`, `UserLambdaValidationException`
  (documented AWS errors) are not implemented.
