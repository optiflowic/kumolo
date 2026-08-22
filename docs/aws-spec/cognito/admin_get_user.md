# AdminGetUser

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminGetUser.html
- **Target**: `AWSCognitoIdentityProviderService.AdminGetUser`
- **SDK**: `cognitoidentityprovider.AdminGetUserInput` / `AdminGetUserOutput`
- **Last verified**: 2026-08-22

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
  "UserStatus": "UNCONFIRMED | CONFIRMED | FORCE_CHANGE_PASSWORD | RESET_REQUIRED | EXTERNAL_PROVIDER",
  "MFAOptions": [],
  "UserMFASettingList": [],
  "PreferredMfaSetting": ""
}
```

- `UserAttributes` includes `sub` as first entry.
- `MFAOptions` is always `[]` (see kumolo deviations below).
- `UserMFASettingList`/`PreferredMfaSetting` reflect the user's `SoftwareTokenMFAEnabled` state:
  `["SOFTWARE_TOKEN_MFA"]`/`"SOFTWARE_TOKEN_MFA"` when TOTP MFA is enabled, empty otherwise.

## Errors implemented

| Error type                | HTTP | Trigger |
|---------------------------|------|---------|
| ResourceNotFoundException | 400  | pool not found |
| UserNotFoundException     | 400  | username not found |
| InvalidParameterException | 400  | missing required field |
| InternalErrorException    | 500  | storage failure |

## kumolo deviations

- `MFAOptions` is always `[]`, matching AWS's own deprecation of the field for SMS-only MFA,
  which kumolo doesn't implement — not a deviation from current AWS behavior.
