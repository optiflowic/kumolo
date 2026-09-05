# AdminConfirmSignUp

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminConfirmSignUp.html
- **Target**: `AWSCognitoIdentityProviderService.AdminConfirmSignUp`
- **SDK**: `cognitoidentityprovider.AdminConfirmSignUpInput` / `AdminConfirmSignUpOutput`
- **Last verified**: 2026-09-06

## Request

| Field          | Type              | Required | Notes |
|----------------|-------------------|----------|-------|
| UserPoolId     | string            | Yes      |       |
| Username       | string            | Yes      |       |
| ClientMetadata | map[string]string | No       | forwarded to the post-confirmation Lambda trigger when configured; kumolo ignores (no trigger support) |

## Response

HTTP 200 + empty body `{}`

## Behavior

- Transitions user status from `UNCONFIRMED` to `CONFIRMED` without requiring a verification code.
- If user is already `CONFIRMED`, returns 200 (no-op) — attribute verification below does not re-run.
- No confirmation code consumed.
- On the `UNCONFIRMED` → `CONFIRMED` transition, for each attribute name in the
  pool's `AutoVerifiedAttributes` (`email`, `phone_number`), if the user has
  that contact attribute set, kumolo also sets `<attribute>_verified = "true"`
  (see `confirm_sign_up.md`).

## Errors implemented

| Error type                | HTTP | Trigger |
|---------------------------|------|---------|
| ResourceNotFoundException | 400  | pool not found |
| UserNotFoundException     | 400  | username not found |
| InvalidParameterException | 400  | missing required field |
| InternalErrorException    | 500  | storage failure |

## kumolo deviations

- Lambda-related errors not returned (no Lambda trigger support).
- `TooManyFailedAttemptsException` and `LimitExceededException` not implemented (no rate limiting).
