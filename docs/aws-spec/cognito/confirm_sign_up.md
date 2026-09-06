# Cognito — ConfirmSignUp

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ConfirmSignUp.html
SDK: `cognitoidentityprovider.ConfirmSignUpInput` / `cognitoidentityprovider.ConfirmSignUpOutput`
Last verified: 2026-09-06

## Request Parameters

| Field | Required | Notes |
|-------|----------|-------|
| ClientId | Yes | App client ID |
| Username | Yes | Username or alias (1-128 chars) |
| ConfirmationCode | Yes | Code sent after SignUp |
| ForceAliasCreation | No | Ignored in kumolo |
| SecretHash | No | kumolo ignores |
| Session | No | kumolo ignores |
| ClientMetadata | No | kumolo ignores |
| AnalyticsMetadata | No | kumolo ignores |
| UserContextData | No | kumolo ignores |

## Response

```json
{}
```

HTTP 200 with empty body on success.

## Errors

| Error | HTTP | Condition |
|-------|------|-----------|
| InvalidParameterException | 400 | Missing required field |
| ResourceNotFoundException | 400 | ClientId not found |
| UserNotFoundException | 400 | Username not found in pool |
| NotAuthorizedException | 400 | User already CONFIRMED |
| CodeMismatchException | 400 | Confirmation code doesn't match |
| InternalErrorException | 500 | Storage failure |

## Behavior

- Transitions user from `UNCONFIRMED` to `CONFIRMED`.
- kumolo generates a random 6-digit code per SignUp call (matches AWS format).
  The code is logged at INFO level on the server (`pool_id`, `username`, `code`)
  so developers can retrieve it from the server log without intercepting email delivery.
- Once CONFIRMED, a subsequent ConfirmSignUp returns NotAuthorizedException.
- On successful confirmation, for each attribute name in the pool's
  `AutoVerifiedAttributes` (`email`, `phone_number`), if the user has that
  contact attribute set, kumolo also sets `<attribute>_verified = "true"`.
  Matches AWS: pools that auto-verify email/phone consider the contact info
  confirmed once the user proves ownership via ConfirmSignUp.
