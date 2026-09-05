# Cognito — SignUp

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_SignUp.html
SDK: `cognitoidentityprovider.SignUpInput` / `cognitoidentityprovider.SignUpOutput`
Last verified: 2026-09-06

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
- kumolo does not deliver email/SMS. A random 6-digit confirmation code is generated per SignUp call and logged at INFO level (`pool_id`, `username`, `code`) so developers can retrieve it from server logs.
- `CodeDeliveryDetails.Destination` is masked: for email, the full email value is stored but masked as `"***"` in the response.
- Password is hashed via `hashPassword` (SHA-256 prehash + bcrypt, cost 10; see
  `docs/aws-spec/cognito/password_policy.md` Implementation).
- A UUID sub is generated and returned as `UserSub`.

- If `UserAttributes` includes `email` or `phone_number`, kumolo also stores a
  matching `email_verified` / `phone_number_verified` attribute defaulting to
  `"false"` (unless the caller already supplied one), matching AWS's "present
  but false" state. ConfirmSignUp / AdminConfirmSignUp flip it to `"true"` for
  attributes named in the pool's `AutoVerifiedAttributes` (see
  `confirm_sign_up.md`).

## kumolo Deviations

- Confirmation code is a random 6-digit number logged at INFO level on the server — no email/SMS delivery.
- SecretHash, ValidationData, ClientMetadata, AnalyticsMetadata, UserContextData are accepted but ignored.
- Password policy enforcement: see `docs/aws-spec/cognito/password_policy.md`.
- Usernames are treated as case-insensitive: `"Alice"` and `"alice"` map to the same user. On real AWS the default pool configuration is case-sensitive.
- `AdminCreateUser` does not default `email_verified`/`phone_number_verified`
  when the caller omits them — the attribute is left absent, unlike `SignUp`.
