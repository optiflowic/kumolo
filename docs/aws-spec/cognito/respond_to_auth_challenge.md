# Cognito — RespondToAuthChallenge

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_RespondToAuthChallenge.html
SDK: `cognitoidentityprovider.RespondToAuthChallengeInput` / `cognitoidentityprovider.RespondToAuthChallengeOutput`
Last verified: 2026-07-16

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
(chaining into the existing NEW_PASSWORD_REQUIRED handling above) — or the
user has `SOFTWARE_TOKEN_MFA` enabled, in which case a `SOFTWARE_TOKEN_MFA`
challenge is returned instead (see below).

### SOFTWARE_TOKEN_MFA

ChallengeResponses required: `SOFTWARE_TOKEN_MFA_CODE`
`USERNAME` is optional — same fallback-to-Session-claim behavior as the other challenges.

Issued instead of tokens whenever primary authentication (`USER_PASSWORD_AUTH`,
`PASSWORD_VERIFIER`, or the post-`NEW_PASSWORD_REQUIRED` reset) succeeds for a user with
`UserMetadata.SoftwareTokenMFAEnabled == true`. See
`docs/aws-spec/cognito/set_user_mfa_preference.md` for how that flag is set.
`SOFTWARE_TOKEN_MFA_CODE` is verified as a TOTP code (RFC 6238, ±1 step / ±30s clock skew)
against `UserMetadata.TOTPSecret`. On success, tokens are issued exactly as for the
non-MFA success path.

### MFA_SETUP

ChallengeResponses required: none beyond the optional `USERNAME` fallback below.
`USERNAME` is optional — same fallback-to-Session-claim behavior as the other challenges.

Issued instead of tokens whenever primary authentication succeeds for a user in a pool with
`MfaConfiguration: "ON"` who has no MFA method enrolled (see `initiate_auth.md`). Completing it
requires the Session-authenticated `AssociateSoftwareToken`/`VerifySoftwareToken` flow first (see
`docs/aws-spec/cognito/associate_software_token.md` and
`docs/aws-spec/cognito/verify_software_token.md`):

1. `AssociateSoftwareToken` with the `MFA_SETUP` Session from `InitiateAuth` — returns a
   `SecretCode` and a new Session carrying the pending secret.
2. `VerifySoftwareToken` with that Session and the user's TOTP code — returns `Status: "SUCCESS"`
   and a new Session carrying the verified secret.
3. `RespondToAuthChallenge` with `ChallengeName: "MFA_SETUP"` and that Session — commits the
   secret to the user (`TOTPSecret`), sets `SoftwareTokenMFAEnabled = true` and
   `PreferredMfaSetting = "SOFTWARE_TOKEN_MFA"`, and issues tokens. Later sign-ins get a
   `SOFTWARE_TOKEN_MFA` challenge instead of `MFA_SETUP`.

A Session that has not been through both `AssociateSoftwareToken` and `VerifySoftwareToken` (i.e.
carries no verified secret) is rejected with `NotAuthorizedException`.

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
| NotAuthorizedException | 400 | Session is invalid or expired, (PASSWORD_VERIFIER) SRP signature verification failed, (SOFTWARE_TOKEN_MFA) MFA was disabled after the challenge was issued, or the user is disabled |
| InvalidPasswordException | 400 | NEW_PASSWORD doesn't meet requirements |
| CodeMismatchException | 400 | (SOFTWARE_TOKEN_MFA) SOFTWARE_TOKEN_MFA_CODE doesn't match |
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

- Only NEW_PASSWORD_REQUIRED, PASSWORD_VERIFIER, SOFTWARE_TOKEN_MFA, and MFA_SETUP challenges are
  supported.
- `MFA_SETUP` only supports TOTP enrollment (`SOFTWARE_TOKEN_MFA`); SMS/email MFA setup is not
  implemented, matching kumolo's lack of SMS/email MFA support elsewhere.
- Session is a kumolo-specific signed JWT (not the AWS opaque session token format).
- Password policy enforcement: see `docs/aws-spec/cognito/password_policy.md`.
- SecretHash, ClientMetadata, AnalyticsMetadata, UserContextData are accepted but ignored.
- `USERNAME` in ChallengeResponses is optional: if absent, the username is taken from the `username` claim in the Session JWT. On real AWS, USERNAME is required.
- PASSWORD_VERIFIER: `SECRET_BLOCK` is an opaque random nonce (not AWS's
  encrypted state blob); real session state (server ephemeral `b`, client's
  `A`) lives in the Session JWT instead. `TIMESTAMP` is not independently
  format/freshness-validated beyond the Session JWT's own expiry. See
  `docs/aws-spec/cognito/srp_protocol.md` for the full deviation list.
