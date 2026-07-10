# UpdateUserAttributes

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_UpdateUserAttributes.html
- **Target**: `AWSCognitoIdentityProviderService.UpdateUserAttributes`
- **SDK**: `cognitoidentityprovider.UpdateUserAttributesInput` / `UpdateUserAttributesOutput`
- **Last verified**: 2026-07-10

## Request

| Field          | Type                    | Required |
|----------------|-------------------------|----------|
| AccessToken    | string                  | Yes      |
| UserAttributes | []AttributeType         | Yes (non-empty) |
| ClientMetadata | map[string]string       | No — accepted but ignored (no Lambda triggers) |

- Authorized via access token (same validation chain as `GetUser`): signature, expiry, `token_use=access`, revocation check.
- Submitting an attribute with an empty `Value` deletes it from the user (matches AWS).
- `sub` cannot be updated or deleted — `InvalidParameterException`.

## Behavior (kumolo simplification)

Real AWS holds a new `email`/`phone_number` value pending until the user calls
`VerifyUserAttribute` with the delivered code, gated by pool
`UserAttributeUpdateSettings`. kumolo does not implement the pending-value hold
(no email/SMS delivery, and `UserAttributeUpdateSettings` is stored as opaque
`json.RawMessage`, not parsed):

- Every submitted attribute (except `sub`) is applied **immediately**.
- If the new value for `email` or `phone_number` differs from the current value,
  kumolo additionally resets the paired `email_verified`/`phone_number_verified`
  attribute to `"false"`, generates a 6-digit verification code (logged at Info
  level, same pattern as `SignUp`), stores it for later confirmation via
  `VerifyUserAttribute`, and appends a `CodeDeliveryDetails` entry to the response.
- If `email`/`phone_number` is set to the same value it already had, no code is
  generated and no entry is added (matches real AWS: unchanged values don't
  trigger re-verification).
- Deleting `email`/`phone_number` (blank value) clears the paired `_verified`
  attribute and any pending verification code for that attribute; no
  `CodeDeliveryDetails` entry.

## Response

HTTP 200:

```json
{
  "CodeDeliveryDetailsList": [
    {"AttributeName": "email", "DeliveryMedium": "EMAIL", "Destination": "j***@e***"}
  ]
}
```

- Empty array when no email/phone_number value actually changed.

## Errors implemented

| Error type                 | HTTP | Trigger |
|-----------------------------|------|---------|
| InvalidParameterException   | 400  | missing `AccessToken`/`UserAttributes`, attempt to modify `sub` |
| NotAuthorizedException      | 400  | invalid/expired/revoked access token, wrong `token_use` |
| UserNotFoundException       | 400  | token's `sub` has no matching user (e.g. the user was deleted after the token was issued) |
| InternalErrorException      | 500  | storage failure |

## kumolo deviations

- No pending-value hold; all attributes apply immediately (see Behavior above).
- `AliasExistsException` not implemented — kumolo doesn't enforce cross-user
  alias uniqueness for `email`/`phone_number` as sign-in aliases.
- SMS/email delivery is simulated: codes are generated and logged, never sent.
- `ClientMetadata` accepted but ignored (no Lambda triggers).
- `CodeDeliveryFailureException`, `ForbiddenException`, `InvalidEmailRoleAccessPolicyException`,
  `InvalidLambdaResponseException`, `InvalidSmsRoleAccessPolicyException`,
  `InvalidSmsRoleTrustRelationshipException`, `OperationNotEnabledException`,
  `PasswordResetRequiredException`, `TooManyRequestsException`,
  `UnexpectedLambdaException`, `UserLambdaValidationException`,
  `UserNotConfirmedException`, `CodeMismatchException`, `ExpiredCodeException`
  (documented AWS errors) are not implemented — none apply to kumolo's
  synchronous, Lambda-free, always-delivered model in this operation.
