# VerifySoftwareToken

- URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_VerifySoftwareToken.html
- SDK type: `cognitoidentityprovider.VerifySoftwareTokenInput` / `VerifySoftwareTokenOutput`
- X-Amz-Target: `AWSCognitoIdentityProviderService.VerifySoftwareToken`
- Last verified: 2026-07-22

## Request

| Field              | Type   | Required     | Notes                                                    |
|---------------------|--------|--------------|-----------------------------------------------------------|
| AccessToken         | string | one of AccessToken/Session | Same flow selection as `AssociateSoftwareToken`. |
| UserCode            | string | yes          | 6-digit numeric TOTP code.                                |
| FriendlyDeviceName  | string | no           | Accepted but not persisted — kumolo has no device registry. |
| Session             | string | one of AccessToken/Session | The Session `AssociateSoftwareToken` returned for the forced-enrollment flow. |

## Response (HTTP 200)

| Field   | Type   | Notes                                          |
|---------|--------|--------------------------------------------------|
| Status  | string | Always `"SUCCESS"` on a 200 response (see deviations for the mismatch case) |
| Session | string | Session-authenticated flow only: a new `MFA_SETUP` Session carrying the verified secret, to pass to `RespondToAuthChallenge`. Omitted for the AccessToken-authenticated flow. |

## Implemented errors

| Error type                        | HTTP | Condition                                                     |
|-------------------------------------|------|-----------------------------------------------------------------|
| InvalidParameterException          | 400  | Invalid request body, missing UserCode, both AccessToken and Session provided, or neither provided |
| NotAuthorizedException             | 400  | Invalid/expired/revoked access token, or invalid/expired/wrong-challenge Session |
| UserNotFoundException              | 400  | Token's `sub` (or Session's `username`) does not resolve to a stored user |
| SoftwareTokenMFANotFoundException  | 400  | No pending secret — `AssociateSoftwareToken` was never called (or was already verified) |
| CodeMismatchException              | 400  | `UserCode` does not match the TOTP computed from the pending secret |
| InternalErrorException             | 500  | Storage failure                                                |

## kumolo deviations

- Two independent flows, selected by which of AccessToken/Session is set (exactly one must be) —
  see `associate_software_token.md` for the matching `AssociateSoftwareToken` deviations.
  - **AccessToken-authenticated**: on success, `UserMetadata.PendingTOTPSecret` is promoted to
    `UserMetadata.TOTPSecret` (the active secret used by the `SOFTWARE_TOKEN_MFA` sign-in
    challenge — see `respond_to_auth_challenge.md`) and cleared from pending. This only registers
    the authenticator; it does **not** enable MFA for sign-in — `SetUserMFAPreference` must be
    called separately to activate `SOFTWARE_TOKEN_MFA` as a sign-in requirement, matching AWS's
    documented behavior ("This operation doesn't reset an existing TOTP MFA... register a new TOTP
    factor... [then] use `SetUserMFAPreference`").
  - **Session-authenticated** (completes a forced `MFA_SETUP` challenge): the pending secret comes
    from the request Session's `pending_totp_secret` claim (set by `AssociateSoftwareToken`), not
    from user storage. On success, nothing is persisted yet — the verified secret travels in the
    *response* Session's `verified_totp_secret` claim. `RespondToAuthChallenge` commits it to
    `UserMetadata.TOTPSecret` and activates `SoftwareTokenMFAEnabled` in one step, since a pool
    requiring MFA has no other route to first-time enrollment.
- Verification allows ±1 time step (±30s) of clock skew, matching common TOTP validator behavior;
  AWS does not document its own tolerance window.
- A code mismatch returns `CodeMismatchException` (HTTP 400) rather than a 200 response with
  `Status: "ERROR"` — consistent with how every other code-verification endpoint in kumolo
  (`ConfirmSignUp`, `VerifyUserAttribute`, `ConfirmForgotPassword`) reports mismatches.
  `EnableSoftwareTokenMFAException` is not returned.
