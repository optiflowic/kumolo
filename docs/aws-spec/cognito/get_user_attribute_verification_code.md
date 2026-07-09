# GetUserAttributeVerificationCode / VerifyUserAttribute

- **URLs**:
  https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GetUserAttributeVerificationCode.html,
  https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_VerifyUserAttribute.html
- **Targets**: `AWSCognitoIdentityProviderService.GetUserAttributeVerificationCode`,
  `AWSCognitoIdentityProviderService.VerifyUserAttribute`
- **SDK**: `cognitoidentityprovider.GetUserAttributeVerificationCodeInput`/`Output`,
  `VerifyUserAttributeInput`/`Output`
- **Last verified**: 2026-07-10

## GetUserAttributeVerificationCode

### Request

| Field          | Type   | Required |
|----------------|--------|----------|
| AccessToken    | string | Yes      |
| AttributeName  | string | Yes — kumolo only supports `email` or `phone_number` |
| ClientMetadata | map    | No — accepted but ignored (no Lambda triggers), not in kumolo's request struct |

- Authorized via access token (same chain as `GetUser`).
- The user must already have a non-empty value for `AttributeName`, or kumolo
  returns `InvalidParameterException`.

### Behavior

Generates a new 6-digit code (same generator as `SignUp`/`UpdateUserAttributes`),
overwrites any previously pending code for that attribute in
`UserMetadata.VerificationCodes`, and logs it at Info level. No message is
actually sent (kumolo has no SMS/email delivery).

### Response

HTTP 200:

```json
{"CodeDeliveryDetails": {"AttributeName": "email", "DeliveryMedium": "EMAIL", "Destination": "j***@e***"}}
```

Note the singular `CodeDeliveryDetails` object (not a list) — this operation's
response shape differs from `UpdateUserAttributes`' `CodeDeliveryDetailsList`.

## VerifyUserAttribute

### Request

| Field         | Type   | Required |
|---------------|--------|----------|
| AccessToken   | string | Yes      |
| AttributeName | string | Yes — kumolo only supports `email` or `phone_number` |
| Code          | string | Yes      |

### Behavior

Compares `Code` against `UserMetadata.VerificationCodes[AttributeName]` with
`subtle.ConstantTimeCompare` (same pattern as `ConfirmSignUp`). On match, sets
`{attribute}_verified` to `"true"` and clears the pending code. Since kumolo
already applies attribute value changes immediately (see `UpdateUserAttributes`
deviation), there is no separate "pending value" to promote here — only the
`_verified` flag changes.

### Response

HTTP 200 with an empty body: `{}`.

## Errors implemented (both operations)

| Error type                 | HTTP | Trigger |
|-----------------------------|------|---------|
| InvalidParameterException   | 400  | missing `AccessToken`/`AttributeName`(/`Code`), unsupported `AttributeName`, no value set for the attribute (`GetUserAttributeVerificationCode` only) |
| NotAuthorizedException      | 400  | invalid/expired/revoked access token, wrong `token_use` |
| UserNotFoundException       | 400  | token's `sub` has no matching user |
| CodeMismatchException       | 400  | `VerifyUserAttribute`: code doesn't match (or no code is pending) |
| InternalErrorException      | 500  | storage failure |

## kumolo deviations

- `AttributeName` restricted to `email`/`phone_number` — the only attributes
  kumolo tracks a `_verified` companion and pending-code slot for. Any other
  value is `InvalidParameterException`.
- No `ExpiredCodeException` — kumolo's codes don't carry an expiry timestamp;
  a stale code simply stays valid until overwritten by a new
  `GetUserAttributeVerificationCode`/`UpdateUserAttributes` call.
- `AliasExistsException`, `CodeDeliveryFailureException`, `ForbiddenException`,
  `InvalidEmailRoleAccessPolicyException`, `InvalidLambdaResponseException`,
  `InvalidSmsRoleAccessPolicyException`, `InvalidSmsRoleTrustRelationshipException`,
  `LimitExceededException`, `OperationNotEnabledException`, `PasswordResetRequiredException`,
  `TooManyRequestsException`, `UnexpectedLambdaException`, `UserLambdaValidationException`,
  `UserNotConfirmedException` (documented AWS errors) are not implemented.
