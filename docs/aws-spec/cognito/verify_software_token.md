# VerifySoftwareToken

- URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_VerifySoftwareToken.html
- SDK type: `cognitoidentityprovider.VerifySoftwareTokenInput` / `VerifySoftwareTokenOutput`
- X-Amz-Target: `AWSCognitoIdentityProviderService.VerifySoftwareToken`
- Last verified: 2026-07-16

## Request

| Field              | Type   | Required     | Notes                                                    |
|---------------------|--------|--------------|-----------------------------------------------------------|
| AccessToken         | string | yes (kumolo) | Same restriction as `AssociateSoftwareToken` — Session-based flow not supported. |
| UserCode            | string | yes          | 6-digit numeric TOTP code.                                |
| FriendlyDeviceName  | string | no           | Accepted but not persisted — kumolo has no device registry. |
| Session             | string | no           | Accepted for shape compatibility but always rejected.     |

## Response (HTTP 200)

| Field  | Type   | Notes                                          |
|--------|--------|--------------------------------------------------|
| Status | string | Always `"SUCCESS"` on a 200 response (see deviations for the mismatch case) |

`Session` is never returned (see `associate_software_token.md`).

## Implemented errors

| Error type                        | HTTP | Condition                                                     |
|-------------------------------------|------|-----------------------------------------------------------------|
| InvalidParameterException          | 400  | Invalid request body, missing UserCode, or Session provided instead of AccessToken |
| NotAuthorizedException             | 400  | Invalid, expired, or revoked access token                     |
| UserNotFoundException              | 400  | Token's `sub` does not resolve to a stored user                |
| SoftwareTokenMFANotFoundException  | 400  | No pending secret — `AssociateSoftwareToken` was never called (or was already verified) |
| CodeMismatchException              | 400  | `UserCode` does not match the TOTP computed from the pending secret |
| InternalErrorException             | 500  | Storage failure                                                |

## kumolo deviations

- On success, `UserMetadata.PendingTOTPSecret` is promoted to `UserMetadata.TOTPSecret` (the
  active secret used by the `SOFTWARE_TOKEN_MFA` sign-in challenge — see
  `respond_to_auth_challenge.md`) and cleared from pending. This only registers the authenticator;
  it does **not** enable MFA for sign-in — `SetUserMFAPreference` must be called separately to
  activate `SOFTWARE_TOKEN_MFA` as a sign-in requirement, matching AWS's documented behavior
  ("This operation doesn't reset an existing TOTP MFA... register a new TOTP factor... [then] use
  `SetUserMFAPreference`").
- Verification allows ±1 time step (±30s) of clock skew, matching common TOTP validator behavior;
  AWS does not document its own tolerance window.
- A code mismatch returns `CodeMismatchException` (HTTP 400) rather than a 200 response with
  `Status: "ERROR"` — consistent with how every other code-verification endpoint in kumolo
  (`ConfirmSignUp`, `VerifyUserAttribute`, `ConfirmForgotPassword`) reports mismatches.
  `EnableSoftwareTokenMFAException` is not returned.
- Only the AccessToken-authenticated flow is implemented; see `associate_software_token.md`.
