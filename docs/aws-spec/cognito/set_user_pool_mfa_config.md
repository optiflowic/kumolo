# SetUserPoolMfaConfig

- URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_SetUserPoolMfaConfig.html
- SDK type: `cognitoidentityprovider.SetUserPoolMfaConfigInput` / `SetUserPoolMfaConfigOutput`
- X-Amz-Target: `AWSCognitoIdentityProviderService.SetUserPoolMfaConfig`
- Last verified: 2026-07-16

## Request

| Field                          | Type   | Required | Notes                                                  |
|---------------------------------|--------|----------|---------------------------------------------------------|
| UserPoolId                     | string | yes      | Pattern: `[\w-]+_[0-9a-zA-Z]+`                          |
| MfaConfiguration                | string | no       | `OFF` \| `ON` \| `OPTIONAL`                             |
| SoftwareTokenMfaConfiguration    | object | no       | `{"Enabled": bool}` — accepted but not persisted (TOTP not supported) |
| SmsMfaConfiguration              | object | no       | Accepted but not persisted (SMS delivery not supported) |
| EmailMfaConfiguration            | object | no       | Accepted but not persisted (email OTP not supported)    |
| WebAuthnConfiguration            | object | no       | Accepted but not persisted (passkeys not supported)     |

## Response (HTTP 200)

| Field                        | Type   | Notes                                                  |
|------------------------------|--------|---------------------------------------------------------|
| MfaConfiguration             | string | Echoes the stored `UserPoolMetadata.MfaConfiguration`   |
| SoftwareTokenMfaConfiguration | object | Always `{"Enabled": false}`                             |

`SmsMfaConfiguration`, `EmailMfaConfiguration`, `WebAuthnConfiguration` are always omitted from the response, matching `GetUserPoolMfaConfig`.

## Implemented errors

| Error type                | HTTP | Condition                      |
|---------------------------|------|--------------------------------|
| InvalidParameterException | 400  | UserPoolId missing             |
| ResourceNotFoundException | 400  | Pool not found                 |
| InternalErrorException    | 500  | Storage failure                |

## kumolo deviations

- Only `MfaConfiguration` is persisted, into the same `UserPoolMetadata.MfaConfiguration` field used by
  `CreateUserPool`/`UpdateUserPool`/`GetUserPoolMfaConfig`. No enum validation is performed (consistent with
  `UpdateUserPool`'s existing handling of this field).
- `SoftwareTokenMfaConfiguration`, `SmsMfaConfiguration`, `EmailMfaConfiguration`, and `WebAuthnConfiguration` are
  accepted (to avoid rejecting real-world SDK/Terraform payloads) but silently ignored at the *pool* level — this
  operation never gates per-user TOTP enrollment either way. This mirrors `GetUserPoolMfaConfig`'s existing
  deviations. See `associate_software_token.md`/`set_user_mfa_preference.md` for kumolo's actual (user-level,
  ungated) TOTP MFA support.
- Primary motivation: `terraform-provider-aws`'s `aws_cognito_user_pool` resource calls this operation on every
  `apply` (even with no config changes) to reconcile `software_token_mfa_configuration`; previously kumolo returned
  `UnknownOperationException`, breaking any second `apply`.
