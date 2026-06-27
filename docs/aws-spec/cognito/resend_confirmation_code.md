# Cognito — ResendConfirmationCode

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ResendConfirmationCode.html
SDK: `cognitoidentityprovider.ResendConfirmationCodeInput` / `cognitoidentityprovider.ResendConfirmationCodeOutput`
Last verified: 2026-06-28

## Request Parameters

| Field | Required | Notes |
|-------|----------|-------|
| ClientId | Yes | App client ID (1-128 chars) |
| Username | Yes | Username or alias attribute (1-128 chars) |
| SecretHash | No | HMAC; kumolo ignores |
| AnalyticsMetadata | No | Pinpoint; kumolo ignores |
| ClientMetadata | No | Lambda trigger input only; kumolo ignores |
| UserContextData | No | Threat protection; kumolo ignores |

## Response

```json
{
  "CodeDeliveryDetails": {
    "AttributeName": "email",
    "DeliveryMedium": "EMAIL",
    "Destination": "a***@example.com"
  }
}
```

## Errors

| Error | HTTP | Condition |
|-------|------|-----------|
| InvalidParameterException | 400 | Missing ClientId or Username |
| ResourceNotFoundException | 400 | ClientId not found in any pool |
| UserNotFoundException | 400 | Username not found in the pool |
| NotAuthorizedException | 400 | User is not in UNCONFIRMED status (e.g. already CONFIRMED, or FORCE_CHANGE_PASSWORD for admin-created users); message includes the actual current status string |
| InternalErrorException | 500 | Storage failure |

## Behavior

- Generates a new 6-digit confirmation code and overwrites the previous one stored on the user.
- Only allowed when the user is in `UNCONFIRMED` status; returns `NotAuthorizedException` otherwise. The error message includes the actual current status (e.g. `"Current status is CONFIRMED."` or `"Current status is FORCE_CHANGE_PASSWORD."`). Admin-created users start in `FORCE_CHANGE_PASSWORD` and cannot use this operation.
- kumolo does not deliver email/SMS. The operation is logged at INFO level (`pool_id` only); the code and username are logged at DEBUG level.
- `CodeDeliveryDetails` is derived from the user's first contact attribute: `email` → EMAIL medium, `phone_number` → SMS medium. If neither is present, returns `AttributeName: "email"`, `DeliveryMedium: "EMAIL"`, `Destination: "***"`.
- Email destination is masked: first char + `***` + `@domain` (e.g., `a***@example.com`). Phone destination: first char + `***` + last 4 digits (e.g., `+***1234`).
- The new code replaces the old one so a subsequent `ConfirmSignUp` must use this latest code.

## kumolo Deviations

- No real email/SMS delivery; code is logged at DEBUG level only (`username` + `code`).
- Delivery medium is inferred from user attributes (email → EMAIL, phone_number → SMS); no pool-level auto-verified attribute config is consulted.
- SecretHash, AnalyticsMetadata, ClientMetadata, UserContextData accepted but ignored.
