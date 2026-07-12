# ForgotPassword

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ForgotPassword.html
- **Target**: `AWSCognitoIdentityProviderService.ForgotPassword`
- **SDK**: `cognitoidentityprovider.ForgotPasswordInput` / `ForgotPasswordOutput`
- **Last verified**: 2026-07-12

## Request

| Field          | Type   | Required | Notes |
|----------------|--------|----------|-------|
| ClientId       | string | Yes      | resolves the user pool |
| Username       | string | Yes      | username or alias |
| SecretHash     | string | No       | accepted but ignored |
| ClientMetadata | map    | No       | Lambda trigger input only; kumolo ignores |
| AnalyticsMetadata | object | No    | Pinpoint; kumolo ignores |
| UserContextData   | object | No    | threat protection; kumolo ignores |

## Behavior

- Resolves the user pool from `ClientId`.
- Looks up the user by `Username`. Unlike real AWS's default `PreventUserExistenceErrors`
  behavior, kumolo always reveals `UserNotFoundException` for a missing user — consistent
  with `InitiateAuth`'s `USER_PASSWORD_AUTH` flow (see `docs/aws-spec/cognito/initiate_auth.md`),
  which has the same deviation.
- Resolves the user pool's `AccountRecoverySetting.RecoveryMechanisms` (defaults to
  `verified_phone_number` priority 1, `verified_email` priority 2 when the pool has none —
  see `docs/aws-spec/cognito/user_pool.md`), sorts by `Priority` ascending, and picks the
  first mechanism the user satisfies:
  - `verified_phone_number` / `verified_email`: selected if the user has the matching
    `_verified: "true"` attribute.
  - `admin_only`: no self-service recovery; treated as no eligible mechanism.
- If no mechanism is satisfied (including an `admin_only`-only pool, or a pool whose
  configured attribute isn't verified for the user), returns `InvalidParameterException`
  (matches AWS's documented "users who don't have a valid recovery method" behavior).
- Generates a random 6-digit code (same generator as `SignUp`/`ConfirmSignUp`), stores it on
  the user record, and logs `pool_id`/`username` at Info level plus `code` at Debug level
  (same split as `ResendConfirmationCode`) — kumolo has no real email/SMS delivery, so the
  code must be recoverable locally without exposing it in default-level application logs.
- The code is independent of, and does not overwrite, `SignUp`'s `ConfirmationCode` or
  `GetUserAttributeVerificationCode`'s per-attribute verification codes.

## Response

HTTP 200:

```json
{"CodeDeliveryDetails": {"AttributeName": "email", "DeliveryMedium": "EMAIL", "Destination": "a***@e***"}}
```

## Errors implemented

| Error type                | HTTP | Trigger |
|----------------------------|------|---------|
| InvalidParameterException  | 400  | missing `ClientId`/`Username`; no eligible recovery mechanism |
| ResourceNotFoundException  | 400  | `ClientId` not found in any pool |
| UserNotFoundException      | 400  | `Username` not found in the resolved pool |
| InternalErrorException     | 500  | storage failure |

## kumolo deviations

- `PreventUserExistenceErrors` is stored (see `handler_user_pool_client.go`) but not enforced
  here — `UserNotFoundException` is always returned for a missing user.
- `CodeDeliveryFailureException`, `ForbiddenException`, `InvalidEmailRoleAccessPolicyException`,
  `InvalidLambdaResponseException`, `InvalidSmsRoleAccessPolicyException`,
  `InvalidSmsRoleTrustRelationshipException`, `LimitExceededException`, `NotAuthorizedException`
  (SecretHash mismatch), `OperationNotEnabledException`, `TooManyRequestsException`,
  `UnexpectedLambdaException`, `UserLambdaValidationException` (documented AWS errors) are not
  implemented.
- kumolo does not check whether the user's preferred MFA method conflicts with the selected
  recovery destination (AWS forbids sending a recovery code to the same channel used for MFA).
