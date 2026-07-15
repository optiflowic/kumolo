# Cognito — InitiateAuth

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_InitiateAuth.html
SDK: `cognitoidentityprovider.InitiateAuthInput` / `cognitoidentityprovider.InitiateAuthOutput`
Last verified: 2026-07-13

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

The refresh token must have been issued by the same `ClientId`; presenting a token issued by a different client returns `NotAuthorizedException`.

Success response: `AuthenticationResult` with new `AccessToken` and `IdToken` (no new `RefreshToken`).

### USER_SRP_AUTH
AuthParameters required: `USERNAME`, `SRP_A`

See `docs/aws-spec/cognito/srp_protocol.md` for the shared SRP math. Always
returns `ChallengeName: "PASSWORD_VERIFIER"` with `ChallengeParameters`
(`SALT`, `SRP_B`, `SECRET_BLOCK`, `USER_ID_FOR_SRP`) and a `Session` token;
never returns `AuthenticationResult` directly. Complete the flow via
`RespondToAuthChallenge` with `ChallengeName: "PASSWORD_VERIFIER"`.

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
| UserNotFoundException | 400 | Username not found (USER_PASSWORD_AUTH, USER_SRP_AUTH) |
| UserNotConfirmedException | 400 | User is UNCONFIRMED |
| NotAuthorizedException | 400 | Wrong password, invalid refresh token, or (USER_SRP_AUTH) no stored SRP verifier |
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
  "exp": <unix+AccessTokenValidity>,
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
  "exp": <unix+IdTokenValidity>,
  "iat": <unix>,
  "jti": "<uuid>"
}
```

`AccessTokenValidity`/`IdTokenValidity` come from the requesting client's `UserPoolClient` config
(interpreted per `TokenValidityUnits`, defaulting to hours), falling back to AWS's own default of
1 hour (3600s) when the client leaves them unset.

User attributes (e.g. `email`, `email_verified`) are included in the ID token if present.

### Refresh Token

Opaque 256-bit hex token stored in `pools/{poolID}/refresh_tokens/{token}.json`.
`ExpiresAt` is set on issuance using the requesting client's `RefreshTokenValidity` and
`TokenValidityUnits.RefreshToken` (defaults to days per AWS spec).
Default validity: 30 days (matches AWS default). Tokens with `ExpiresAt == 0` (legacy) are not rejected.
Presenting an expired token returns `NotAuthorizedException`.

## JWKS Endpoint

`/{poolID}/.well-known/jwks.json` — path-based routing; returns the pool's RSA public key in JWK format.

## kumolo Deviations

- Only USER_PASSWORD_AUTH, USER_SRP_AUTH, and REFRESH_TOKEN_AUTH/REFRESH_TOKEN flows supported.
- USER_SRP_AUTH: no AWS-style anti-enumeration "fake verifier" trick — an
  unknown username fails fast with `UserNotFoundException`, consistent with
  USER_PASSWORD_AUTH. A user with no stored SRP verifier (e.g. created before
  this feature existed) fails closed with `NotAuthorizedException`. See
  `docs/aws-spec/cognito/srp_protocol.md` for full deviation list.
- `ExpiresIn` reflects the requesting client's `AccessTokenValidity` (default 3600s) and access
  token expiry is enforced on every authenticated request (see `get_user.md` / `validateAccessJWT`).
- Refresh tokens expire after `RefreshTokenValidity` (default 30 days), honoring `TokenValidityUnits.RefreshToken`.
  Tokens without `ExpiresAt` (legacy) are not expired.
- Session token for challenges is a signed JWT (kumolo-specific encoding).
- SecretHash, ClientMetadata, AnalyticsMetadata, UserContextData are accepted but ignored.
