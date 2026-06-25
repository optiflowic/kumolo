# AdminDeleteUser

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminDeleteUser.html
- **Target**: `AWSCognitoIdentityProviderService.AdminDeleteUser`
- **SDK**: `cognitoidentityprovider.AdminDeleteUserInput` / `AdminDeleteUserOutput`
- **Last verified**: 2026-06-25

## Request

| Field      | Type   | Required |
|------------|--------|----------|
| UserPoolId | string | Yes      |
| Username   | string | Yes      |

## Response

HTTP 200 + empty body `{}`

## Behavior

- Deletes the user record and its username index entry from storage.
- Any active sessions (access tokens, refresh tokens) for the deleted user become invalid on next use — tokens are not proactively revoked.

## Errors implemented

| Error type                | HTTP | Trigger |
|---------------------------|------|---------|
| ResourceNotFoundException | 400  | pool not found |
| UserNotFoundException     | 400  | username not found |
| InvalidParameterException | 400  | missing required field |
| InternalErrorException    | 500  | storage failure |

## Storage

Requires a new `DeleteUser(poolID, username string) error` method on `Storage` that removes:
1. `pools/{poolID}/users/{sub}.json`
2. `pools/{poolID}/user_index/{hash}.json`
