# ForgotPassword

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ForgotPassword.html
- **Target**: `AWSCognitoIdentityProviderService.ForgotPassword`
- **SDK**: `cognitoidentityprovider.ForgotPasswordInput` / `ForgotPasswordOutput`
- **Last verified**: 2026-07-11

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
- Requires a verified delivery attribute: the user must have `email_verified: "true"` or
  `phone_number_verified: "true"`. If neither is set, returns `InvalidParameterException`
  (matches AWS's documented "If neither a verified phone number nor a verified email exists"
  behavior). Email takes precedence over phone when both are verified.
- Generates a random 6-digit code (same generator as `SignUp`/`ConfirmSignUp`), stores it on
  the user record, and logs it at Info level (`pool_id`, `username`, `code`) — kumolo has no
  real email/SMS delivery.
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
| InvalidParameterException  | 400  | missing `ClientId`/`Username`; no verified email or phone attribute |
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
- No `AccountRecoverySetting`-based delivery-method selection — kumolo always prefers a verified
  email over a verified phone number, regardless of pool recovery configuration.
