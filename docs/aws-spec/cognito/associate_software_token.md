# AssociateSoftwareToken

- URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AssociateSoftwareToken.html
- SDK type: `cognitoidentityprovider.AssociateSoftwareTokenInput` / `AssociateSoftwareTokenOutput`
- X-Amz-Target: `AWSCognitoIdentityProviderService.AssociateSoftwareToken`
- Last verified: 2026-07-22

## Request

| Field       | Type   | Required | Notes                                                        |
|-------------|--------|----------|---------------------------------------------------------------|
| AccessToken | string | one of AccessToken/Session | Authenticates the settings-screen enrollment flow. |
| Session     | string | one of AccessToken/Session | The `MFA_SETUP` Session from `InitiateAuth`/`RespondToAuthChallenge`; authenticates the forced-enrollment flow — see deviations. |

## Response (HTTP 200)

| Field      | Type   | Notes                                                                 |
|------------|--------|------------------------------------------------------------------------|
| SecretCode | string | Base32-encoded, 20-byte random TOTP shared secret (RFC 4648 / RFC 6238) |
| Session    | string | Session-authenticated flow only: a new `MFA_SETUP` Session carrying the pending secret, to pass to `VerifySoftwareToken`. Omitted for the AccessToken-authenticated flow. |

## Implemented errors

| Error type                        | HTTP | Condition                                          |
|-------------------------------------|------|-----------------------------------------------------|
| InvalidParameterException          | 400  | Invalid request body, both AccessToken and Session provided, or neither provided |
| NotAuthorizedException             | 400  | Invalid/expired/revoked access token, or invalid/expired/wrong-challenge Session |
| UserNotFoundException              | 400  | Token's `sub` (or Session's `username`) does not resolve to a stored user |
| InternalErrorException             | 500  | Storage or entropy failure                          |

## kumolo deviations

- Two independent flows, selected by which of AccessToken/Session is set (exactly one must be):
  - **AccessToken-authenticated** (the common case: an already-signed-in user enrolling MFA from
    a settings screen). The generated secret is stored as `UserMetadata.PendingTOTPSecret`,
    replacing any previous pending secret. It only becomes the user's active TOTP secret once
    confirmed via `VerifySoftwareToken`. Calling `AssociateSoftwareToken` again before verifying
    discards the previous pending secret (matches AWS: "Amazon Cognito disassociates an existing
    software token when you verify the new token").
  - **Session-authenticated** (satisfies a forced `MFA_SETUP` challenge — see
    `docs/aws-spec/cognito/respond_to_auth_challenge.md`). The Session must have been issued for
    challenge `MFA_SETUP` (by `InitiateAuth` or the `SOFTWARE_TOKEN_MFA`-adjacent primary-auth
    completion path). The generated secret travels in the *response* Session's own claims
    (`pending_totp_secret`) rather than in user storage — nothing is persisted until
    `RespondToAuthChallenge` commits it. A malformed, expired, or wrong-challenge Session returns
    `NotAuthorizedException`.
- `ConcurrentModificationException`, `ForbiddenException`, `OperationNotEnabledException`,
  `ResourceNotFoundException`, and `SoftwareTokenMFANotFoundException` are not returned — kumolo
  does not gate TOTP setup on user pool MFA configuration beyond the `MFA_SETUP` challenge itself
  (see `set_user_pool_mfa_config.md` deviations).
