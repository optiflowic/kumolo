# Cognito — RespondToAuthChallenge

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_RespondToAuthChallenge.html
SDK: `cognitoidentityprovider.RespondToAuthChallengeInput` / `cognitoidentityprovider.RespondToAuthChallengeOutput`
Last verified: 2026-07-13

## Request Parameters

| Field | Required | Notes |
|-------|----------|-------|
| ClientId | Yes | App client ID |
| ChallengeName | Yes | See supported challenges below |
| Session | Yes | Opaque session token from InitiateAuth response |
| ChallengeResponses | No | Key-value pairs; required fields depend on ChallengeName |
| ClientMetadata | No | kumolo ignores |
| AnalyticsMetadata | No | kumolo ignores |
| UserContextData | No | kumolo ignores |

## Supported Challenges

### NEW_PASSWORD_REQUIRED
ChallengeResponses required: `NEW_PASSWORD`
`USERNAME` is optional — if omitted, the username is taken from the `username` claim in the Session JWT (kumolo deviation; on real AWS, USERNAME is required).

Triggered when a FORCE_CHANGE_PASSWORD user authenticates. After responding:
- User status transitions from `FORCE_CHANGE_PASSWORD` to `CONFIRMED`
- Password is updated to NEW_PASSWORD
- Tokens are issued

### PASSWORD_VERIFIER
ChallengeResponses required: `PASSWORD_CLAIM_SECRET_BLOCK`, `TIMESTAMP`, `PASSWORD_CLAIM_SIGNATURE`
`USERNAME` is optional — same fallback-to-Session-claim behavior as NEW_PASSWORD_REQUIRED.

Completes the `USER_SRP_AUTH` flow started by `InitiateAuth`. See
`docs/aws-spec/cognito/srp_protocol.md` for the full protocol. On success:
tokens are issued directly, unless the user is `FORCE_CHANGE_PASSWORD` — in
which case the response is instead a `NEW_PASSWORD_REQUIRED` challenge
(chaining into the existing NEW_PASSWORD_REQUIRED handling above).

## Response

Same as InitiateAuth success: `AuthenticationResult` with AccessToken, IdToken, RefreshToken, ExpiresIn, TokenType.

```json
{
  "AuthenticationResult": {
    "AccessToken": "<jwt>",
    "ExpiresIn": 3600,
    "IdToken": "<jwt>",
    "RefreshToken": "<opaque-token>",
    "TokenType": "Bearer"
  },
  "ChallengeParameters": {}
}
```

## Errors

| Error | HTTP | Condition |
|-------|------|-----------|
| InvalidParameterException | 400 | Missing required field or unsupported challenge |
| ResourceNotFoundException | 400 | ClientId not found |
| UserNotFoundException | 400 | Username in ChallengeResponses not found |
| NotAuthorizedException | 400 | Session is invalid or expired, or (PASSWORD_VERIFIER) SRP signature verification failed |
| InvalidPasswordException | 400 | NEW_PASSWORD doesn't meet requirements |
| InternalErrorException | 500 | Storage or token generation failure |

## Session Format (kumolo-specific)

The Session token from InitiateAuth is a signed JWT (RS256, signed with the pool's private key):

```json
{
  "pool_id": "<poolID>",
  "username": "<username>",
  "challenge": "NEW_PASSWORD_REQUIRED",
  "iat": <unix>,
  "exp": <unix + 180>
}
```

kumolo validates that:
- Session is a valid JWT signed by the pool's key
- `exp` has not passed (3-minute window)
- `challenge` matches the ChallengeName parameter
- `pool_id` matches the pool resolved from ClientId

## kumolo Deviations

- Only NEW_PASSWORD_REQUIRED and PASSWORD_VERIFIER challenges are supported.
- Session is a kumolo-specific signed JWT (not the AWS opaque session token format).
- Password policy enforcement: see `docs/aws-spec/cognito/password_policy.md`.
- SecretHash, ClientMetadata, AnalyticsMetadata, UserContextData are accepted but ignored.
- `USERNAME` in ChallengeResponses is optional: if absent, the username is taken from the `username` claim in the Session JWT. On real AWS, USERNAME is required.
- PASSWORD_VERIFIER: `SECRET_BLOCK` is an opaque random nonce (not AWS's
  encrypted state blob); real session state (server ephemeral `b`, client's
  `A`) lives in the Session JWT instead. `TIMESTAMP` is not independently
  format/freshness-validated beyond the Session JWT's own expiry. See
  `docs/aws-spec/cognito/srp_protocol.md` for the full deviation list.
