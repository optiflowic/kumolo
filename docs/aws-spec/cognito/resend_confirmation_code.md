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
| NotAuthorizedException | 400 | User is already CONFIRMED |
| InternalErrorException | 500 | Storage failure |

## Behavior

- Generates a new 6-digit confirmation code and overwrites the previous one stored on the user.
- Only allowed when the user is in `UNCONFIRMED` status; returns `NotAuthorizedException` otherwise.
- kumolo does not deliver email/SMS. The new code is logged at INFO level (`pool_id`, `username`, `code`).
- `CodeDeliveryDetails.Destination` is masked like SignUp: first char + `***` + `@domain`.
- The new code replaces the old one so a subsequent `ConfirmSignUp` must use this latest code.

## kumolo Deviations

- No real email/SMS delivery; code is logged at INFO level.
- SecretHash, AnalyticsMetadata, ClientMetadata, UserContextData accepted but ignored.
