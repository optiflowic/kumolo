# Cognito — SignUp

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_SignUp.html
SDK: `cognitoidentityprovider.SignUpInput` / `cognitoidentityprovider.SignUpOutput`
Last verified: 2026-06-23

## Request Parameters

| Field | Required | Notes |
|-------|----------|-------|
| ClientId | Yes | App client ID (1-128 chars) |
| Username | Yes | Username or alias attribute (1-128 chars) |
| Password | No | Must satisfy pool password policy |
| UserAttributes | No | Array of `{Name, Value}` pairs |
| SecretHash | No | HMAC for clients with secrets; kumolo ignores |
| ValidationData | No | Lambda trigger input only; kumolo ignores |
| ClientMetadata | No | Lambda trigger input only; kumolo ignores |
| AnalyticsMetadata | No | Pinpoint; kumolo ignores |
| UserContextData | No | Threat protection; kumolo ignores |

## Response

```json
{
  "UserSub": "<uuid>",
  "UserConfirmed": false,
  "CodeDeliveryDetails": {
    "AttributeName": "email",
    "DeliveryMedium": "EMAIL",
    "Destination": "***"
  }
}
```

## Errors

| Error | HTTP | Condition |
|-------|------|-----------|
| InvalidParameterException | 400 | Missing ClientId or Username |
| ResourceNotFoundException | 400 | ClientId not found in any pool |
| UsernameExistsException | 400 | Username already registered in this pool |
| InvalidPasswordException | 400 | Password too short or doesn't meet requirements |
| InternalErrorException | 500 | Storage failure |

## Behavior

- User is created in `UNCONFIRMED` state and must call ConfirmSignUp to activate.
- kumolo does not deliver email/SMS. The confirmation code is always `"123456"` (kumolo deviation).
- `CodeDeliveryDetails.Destination` is masked: for email, the full email value is stored but masked as `"***"` in the response.
- Password is hashed with bcrypt (cost 10).
- A UUID sub is generated and returned as `UserSub`.

## kumolo Deviations

- Confirmation code is always `"123456"` — no email/SMS delivery.
- SecretHash, ValidationData, ClientMetadata, AnalyticsMetadata, UserContextData are accepted but ignored.
- No password policy enforcement beyond minimum length (8 chars).
- Usernames are treated as case-insensitive: `"Alice"` and `"alice"` map to the same user. On real AWS the default pool configuration is case-sensitive.
