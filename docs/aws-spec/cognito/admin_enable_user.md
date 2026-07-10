# AdminEnableUser

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminEnableUser.html
- **Target**: `AWSCognitoIdentityProviderService.AdminEnableUser`
- **SDK**: `cognitoidentityprovider.AdminEnableUserInput` / `AdminEnableUserOutput`
- **Last verified**: 2026-07-10
- See also: [AdminDisableUser](admin_disable_user.md) (the inverse operation, same request/response shape).

## Request

| Field      | Type   | Required |
|------------|--------|----------|
| UserPoolId | string | Yes      |
| Username   | string | Yes      |

## Behavior

Sets `UserMetadata.Enabled = true`. No token action — a disabled user has no
live sessions left to restore (they were revoked by `AdminDisableUser`).

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

- `OperationNotEnabledException`, `TooManyRequestsException`, `NotAuthorizedException`
  (IAM-authorization variant) are not implemented — kumolo has no IAM policy
  evaluation.
