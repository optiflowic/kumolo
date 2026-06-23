# Cognito — InitiateAuth

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_InitiateAuth.html
SDK: `cognitoidentityprovider.InitiateAuthInput` / `cognitoidentityprovider.InitiateAuthOutput`
Last verified: 2026-06-23

## Request Parameters

| Field | Required | Notes |
|-------|----------|-------|
| ClientId | Yes | App client ID |
| AuthFlow | Yes | See supported flows below |
| AuthParameters | No | Key-value pairs; required fields depend on AuthFlow |
| Session | No | kumolo ignores |
| ClientMetadata | No | kumolo ignores |
| AnalyticsMetadata | No | kumolo ignores |
| UserContextData | No | kumolo ignores |

## Supported Auth Flows

### USER_PASSWORD_AUTH
AuthParameters required: `USERNAME`, `PASSWORD`

Success response (normal user): `AuthenticationResult` with all three tokens.
Success response (FORCE_CHANGE_PASSWORD user): `ChallengeName: "NEW_PASSWORD_REQUIRED"` with session.

### REFRESH_TOKEN_AUTH / REFRESH_TOKEN
AuthParameters required: `REFRESH_TOKEN`

Success response: `AuthenticationResult` with new `AccessToken` and `IdToken` (no new `RefreshToken`).

## Response

```json
{
  "AuthenticationResult": {
    "AccessToken": "<jwt>",
    "ExpiresIn": 3600,
    "IdToken": "<jwt>",
    "RefreshToken": "<opaque-token>",
    "TokenType": "Bearer"
  }
}
```

Or, when a challenge is required:

```json
{
  "ChallengeName": "NEW_PASSWORD_REQUIRED",
  "ChallengeParameters": {
    "USER_ID_FOR_SRP": "<username>",
    "requiredAttributes": "[]",
    "userAttributes": "{}"
  },
  "Session": "<signed-session-jwt>"
}
```

## Errors

| Error | HTTP | Condition |
|-------|------|-----------|
| InvalidParameterException | 400 | Missing required field or unsupported AuthFlow |
| ResourceNotFoundException | 400 | ClientId not found |
| UserNotFoundException | 400 | Username not found (USER_PASSWORD_AUTH) |
| UserNotConfirmedException | 400 | User is UNCONFIRMED |
| NotAuthorizedException | 400 | Wrong password or invalid refresh token |
| InternalErrorException | 500 | Storage or token generation failure |

## Token Structure

### Access Token (RS256 JWT)
```json
{
  "sub": "<user-uuid>",
  "iss": "https://cognito-idp.us-east-1.amazonaws.com/<poolID>",
  "version": 2,
  "client_id": "<clientID>",
  "origin_jti": "<uuid>",
  "token_use": "access",
  "scope": "aws.cognito.signin.user.admin",
  "auth_time": <unix>,
  "exp": <unix+3600>,
  "iat": <unix>,
  "jti": "<uuid>",
  "username": "<username>"
}
```

### ID Token (RS256 JWT)
```json
{
  "sub": "<user-uuid>",
  "iss": "https://cognito-idp.us-east-1.amazonaws.com/<poolID>",
  "aud": "<clientID>",
  "token_use": "id",
  "cognito:username": "<username>",
  "origin_jti": "<uuid>",
  "auth_time": <unix>,
  "exp": <unix+3600>,
  "iat": <unix>,
  "jti": "<uuid>"
}
```

User attributes (e.g. `email`, `email_verified`) are included in the ID token if present.

### Refresh Token
Opaque 256-bit hex token stored in `pools/{poolID}/refresh_tokens/{token}.json`.
Expiry is not enforced in kumolo (treated as non-expiring for local testing).

## JWKS Endpoint

`/{poolID}/.well-known/jwks.json` — path-based routing; returns the pool's RSA public key in JWK format.

## kumolo Deviations

- Only USER_PASSWORD_AUTH and REFRESH_TOKEN_AUTH/REFRESH_TOKEN flows supported.
- Token expiry (ExpiresIn=3600) is returned but not enforced.
- Refresh tokens never expire in kumolo.
- Session token for challenges is a signed JWT (kumolo-specific encoding).
- SecretHash, ClientMetadata, AnalyticsMetadata, UserContextData are accepted but ignored.
