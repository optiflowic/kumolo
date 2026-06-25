# AdminCreateUser

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminCreateUser.html
- **Target**: `AWSCognitoIdentityProviderService.AdminCreateUser`
- **SDK**: `cognitoidentityprovider.AdminCreateUserInput` / `AdminCreateUserOutput`
- **Last verified**: 2026-06-25

## Request

Required: `UserPoolId`, `Username`

| Field                  | Type               | Required | Notes |
|------------------------|--------------------|----------|-------|
| UserPoolId             | string             | Yes      |       |
| Username               | string             | Yes      | 1–128 chars |
| TemporaryPassword      | string             | No       | max 256 chars; omit for passwordless (not implemented) |
| UserAttributes         | []AttributeType    | No       | name-value pairs including `email`, `phone_number`, etc. |
| MessageAction          | string             | No       | `SUPPRESS` or `RESEND`; kumolo always suppresses messages |
| DesiredDeliveryMediums | []string           | No       | `SMS` or `EMAIL`; ignored (no message delivery) |
| ForceAliasCreation     | bool               | No       | ignored (alias system not implemented) |
| ClientMetadata         | map[string]string  | No       | ignored (no Lambda triggers) |
| ValidationData         | []AttributeType    | No       | ignored (no Lambda triggers) |

## Response

HTTP 200 + `{"User": UserType}`

`UserType` shape:
```json
{
  "Username": "string",
  "Attributes": [{"Name": "string", "Value": "string"}],
  "UserCreateDate": 1234567890.0,
  "UserLastModifiedDate": 1234567890.0,
  "Enabled": true,
  "UserStatus": "FORCE_CHANGE_PASSWORD",
  "MFAOptions": []
}
```

## Behavior

- Creates a new user in `FORCE_CHANGE_PASSWORD` status when `TemporaryPassword` is provided.
- `sub` is auto-generated UUID; included in `Attributes` on response.
- `Enabled` is always `true` on creation.
- `MFAOptions` is always empty (MFA not implemented).
- `MessageAction=RESEND`: resends invitation to existing user — **not implemented**; return `NotAuthorizedException`.
- `MessageAction=SUPPRESS` or omitted: proceed normally, no message sent.
- kumolo does not send invitation messages regardless of `MessageAction`.

## Errors implemented

| Error type                  | HTTP | Trigger |
|-----------------------------|------|---------|
| ResourceNotFoundException   | 400  | pool not found |
| UsernameExistsException     | 400  | username already taken |
| InvalidPasswordException    | 400  | password fails policy (min length) |
| InvalidParameterException   | 400  | missing required field |
| InternalErrorException      | 500  | storage failure |

## Errors NOT implemented (return InternalErrorException or omit)

- CodeDeliveryFailureException — no message delivery
- InvalidLambdaResponseException / UnexpectedLambdaException / UserLambdaValidationException — no Lambda
- InvalidSmsRole* — no SMS

## kumolo deviations

- Password policy check: only minimum length (8 chars); complexity rules not enforced.
- `MessageAction=RESEND` returns `NotAuthorizedException` (operation not supported).
- `ForceAliasCreation` and `DesiredDeliveryMediums` are accepted but ignored.
