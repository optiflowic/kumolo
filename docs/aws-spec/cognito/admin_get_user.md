# AdminGetUser

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminGetUser.html
- **Target**: `AWSCognitoIdentityProviderService.AdminGetUser`
- **SDK**: `cognitoidentityprovider.AdminGetUserInput` / `AdminGetUserOutput`
- **Last verified**: 2026-06-25

## Request

| Field      | Type   | Required |
|------------|--------|----------|
| UserPoolId | string | Yes      |
| Username   | string | Yes      |

## Response

HTTP 200:

```json
{
  "Username": "string",
  "UserAttributes": [{"Name": "string", "Value": "string"}],
  "UserCreateDate": 1234567890.0,
  "UserLastModifiedDate": 1234567890.0,
  "Enabled": true,
  "UserStatus": "CONFIRMED",
  "MFAOptions": [],
  "UserMFASettingList": [],
  "PreferredMfaSetting": ""
}
```

- `UserAttributes` includes `sub` as first entry.
- `MFAOptions`, `UserMFASettingList`, `PreferredMfaSetting` always empty (MFA not implemented).

## Errors implemented

| Error type                | HTTP | Trigger |
|---------------------------|------|---------|
| ResourceNotFoundException | 400  | pool not found |
| UserNotFoundException     | 400  | username not found |
| InvalidParameterException | 400  | missing required field |
| InternalErrorException    | 500  | storage failure |

## kumolo deviations

- `MFAOptions`, `UserMFASettingList`, `PreferredMfaSetting` always empty.
