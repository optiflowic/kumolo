# AssociateSoftwareToken

- URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AssociateSoftwareToken.html
- SDK type: `cognitoidentityprovider.AssociateSoftwareTokenInput` / `AssociateSoftwareTokenOutput`
- X-Amz-Target: `AWSCognitoIdentityProviderService.AssociateSoftwareToken`
- Last verified: 2026-07-16

## Request

| Field       | Type   | Required | Notes                                                        |
|-------------|--------|----------|---------------------------------------------------------------|
| AccessToken | string | yes (kumolo) | AWS allows AccessToken *or* Session; kumolo only supports the AccessToken-authenticated flow. |
| Session     | string | no       | Accepted for shape compatibility but always rejected — see deviations. |

## Response (HTTP 200)

| Field      | Type   | Notes                                                                 |
|------------|--------|------------------------------------------------------------------------|
| SecretCode | string | Base32-encoded, 20-byte random TOTP shared secret (RFC 4648 / RFC 6238) |

`Session` is never returned — kumolo's flow always completes via a follow-up `VerifySoftwareToken` call authenticated with the same access token, not a session handoff.

## Implemented errors

| Error type                | HTTP | Condition                                          |
|----------------------------|------|-----------------------------------------------------|
| InvalidParameterException | 400  | Invalid request body, or both AccessToken and Session missing, or Session provided instead of AccessToken |
| NotAuthorizedException    | 400  | Invalid, expired, or revoked access token          |
| UserNotFoundException     | 400  | Token's `sub` does not resolve to a stored user    |
| InternalErrorException    | 500  | Storage or entropy failure                          |

## kumolo deviations

- Only the AccessToken-authenticated flow is implemented (the common case: an already-signed-in
  user enrolling MFA from a settings screen). The Session-authenticated flow — used to satisfy a
  forced `MFA_SETUP` challenge during sign-in when a pool requires TOTP MFA — is not implemented;
  `InitiateAuth` never issues an `MFA_SETUP` challenge. A request with `Session` set and no
  `AccessToken` returns `InvalidParameterException`.
- The generated secret is stored as `UserMetadata.PendingTOTPSecret`, replacing any previous
  pending secret. It only becomes the user's active TOTP secret once confirmed via
  `VerifySoftwareToken`. Calling `AssociateSoftwareToken` again before verifying discards the
  previous pending secret (matches AWS: "Amazon Cognito disassociates an existing software token
  when you verify the new token").
- `ConcurrentModificationException`, `ForbiddenException`, `OperationNotEnabledException`,
  `ResourceNotFoundException`, and `SoftwareTokenMFANotFoundException` are not returned — kumolo
  does not gate TOTP setup on user pool MFA configuration (see `set_user_pool_mfa_config.md`
  deviations).
