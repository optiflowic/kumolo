# SetUserMFAPreference

- URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_SetUserMFAPreference.html
- SDK type: `cognitoidentityprovider.SetUserMFAPreferenceInput` / `SetUserMFAPreferenceOutput`
- X-Amz-Target: `AWSCognitoIdentityProviderService.SetUserMFAPreference`
- Last verified: 2026-07-25

## Request

| Field                     | Type   | Required | Notes                                                        |
|----------------------------|--------|----------|-----------------------------------------------------------------|
| AccessToken                | string | yes      |                                                                   |
| SoftwareTokenMfaSettings   | object | no       | `{"Enabled": bool, "PreferredMfa": bool}` — the only factor kumolo persists. |
| SMSMfaSettings              | object | no       | Accepted but ignored — SMS MFA not implemented.                 |
| EmailMfaSettings            | object | no       | Accepted but ignored — email MFA not implemented.                |
| WebAuthnMfaSettings         | object | no       | Accepted but ignored — passkey MFA not implemented.              |

## Response (HTTP 200)

Empty body, matching AWS.

## Implemented errors

| Error type                 | HTTP | Condition                                                                 |
|------------------------------|------|-----------------------------------------------------------------------------|
| InvalidParameterException   | 400  | Invalid request body; `SoftwareTokenMfaSettings.Enabled: true` while the user has no verified TOTP secret; or `Enabled: false` while the user's pool has `MfaConfiguration: "ON"` (both kumolo-specific — see deviations) |
| NotAuthorizedException      | 400  | Invalid, expired, or revoked access token                                 |
| UserNotFoundException       | 400  | Token's `sub` does not resolve to a stored user                            |
| InternalErrorException      | 500  | Storage failure                                                             |

## kumolo deviations

- Only `SoftwareTokenMfaSettings` is persisted, into `UserMetadata.SoftwareTokenMFAEnabled` and
  `UserMetadata.PreferredMfaSetting`. `SMSMfaSettings`, `EmailMfaSettings`, `WebAuthnMfaSettings`
  are accepted (to avoid rejecting real-world SDK payloads) but silently ignored.
- Setting `Enabled: true` without a prior successful `VerifySoftwareToken` call (i.e.
  `UserMetadata.TOTPSecret == ""`) is rejected with `InvalidParameterException`. AWS's own
  documented error list for this operation doesn't include this exact case, but its prose states
  "Users must register a TOTP authenticator before they set this as their preferred MFA method" —
  kumolo enforces this for `Enabled` too, since kumolo has no other path to detect an
  unregistered factor at sign-in time.
- Setting `Enabled: false` also clears `PreferredMfaSetting` if it was `"SOFTWARE_TOKEN_MFA"`
  (there is no other factor left to be preferred). It does **not** clear `TOTPSecret` — the
  registered authenticator survives being disabled and can be re-enabled without another
  `AssociateSoftwareToken`/`VerifySoftwareToken` round trip, matching AWS's statement that this
  operation "doesn't reset an existing TOTP MFA".
- `PreferredMfa: true` is only honored when `Enabled: true` in the same request.
- Setting `Enabled: false` is rejected with `InvalidParameterException` when the user's pool has
  `MfaConfiguration: "ON"` and the user currently has `SoftwareTokenMFAEnabled == true`. Per the
  AWS user guide ("Details of MFA logic at user runtime"), a pool with MFA required doesn't let
  users enable or disable MFA methods — only the preferred method can be changed — so kumolo
  rejects the disable attempt instead of silently accepting a state that would force the user
  back through `MFA_SETUP` on their next sign-in (see `initiate_auth.md` and
  `respond_to_auth_challenge.md`).
- `ForbiddenException`, `OperationNotEnabledException`, `PasswordResetRequiredException`,
  `UserNotConfirmedException` are not returned.
