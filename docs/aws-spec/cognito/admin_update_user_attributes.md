# AdminUpdateUserAttributes

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminUpdateUserAttributes.html
- **Target**: `AWSCognitoIdentityProviderService.AdminUpdateUserAttributes`
- **SDK**: `cognitoidentityprovider.AdminUpdateUserAttributesInput` / `AdminUpdateUserAttributesOutput`
- **Last verified**: 2026-07-10

## Request

| Field          | Type              | Required |
|----------------|-------------------|----------|
| UserPoolId     | string            | Yes      |
| Username       | string            | Yes      |
| UserAttributes | []AttributeType   | Yes (non-empty) |
| ClientMetadata | map[string]string | No — accepted but ignored (no Lambda triggers) |

- Empty `Value` deletes the attribute (same as `UpdateUserAttributes`).
- `sub` cannot be updated or deleted — `InvalidParameterException`.

## Behavior

Same immediate-apply simplification as [`UpdateUserAttributes`](update_user_attributes.md)
(kumolo has no pending-value hold), plus the documented admin bypass:

- Changing `email`/`phone_number` to a new value resets the paired `_verified`
  attribute to `"false"` and generates a verification code for `VerifyUserAttribute`
  — unless the same request also includes `email_verified`/`phone_number_verified`
  set to `"true"`, in which case kumolo skips code generation entirely, clears
  any previously pending code for that attribute, and applies the values as
  plain attribute updates (the `_verified` attribute itself is applied via the
  normal attribute path).
- Unlike `UpdateUserAttributes`, this operation has no `CodeDeliveryDetailsList`
  in its response — the response body is empty on success, matching AWS.

## Response

HTTP 200 with an empty body: `{}`.

## Errors implemented

| Error type                 | HTTP | Trigger |
|-----------------------------|------|---------|
| InvalidParameterException   | 400  | missing `UserPoolId`/`Username`/`UserAttributes`, attempt to modify `sub` |
| ResourceNotFoundException   | 400  | pool not found |
| UserNotFoundException       | 400  | user not found |
| InternalErrorException      | 500  | storage failure |

## kumolo deviations

- No pending-value hold; all attributes apply immediately (see `UpdateUserAttributes` memo).
- `AliasExistsException` not implemented — no cross-user alias uniqueness enforcement.
- SMS/email delivery is simulated: codes are generated and logged, never sent.
- `ClientMetadata` accepted but ignored (no Lambda triggers).
- `InvalidEmailRoleAccessPolicyException`, `InvalidLambdaResponseException`,
  `InvalidSmsRoleAccessPolicyException`, `InvalidSmsRoleTrustRelationshipException`,
  `OperationNotEnabledException`, `TooManyRequestsException`, `UnexpectedLambdaException`,
  `UserLambdaValidationException`, `NotAuthorizedException` (documented AWS errors) are
  not implemented — kumolo has no IAM policy evaluation or Lambda triggers.
