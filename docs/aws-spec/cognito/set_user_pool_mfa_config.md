# SetUserPoolMfaConfig

- URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_SetUserPoolMfaConfig.html
- SDK type: `cognitoidentityprovider.SetUserPoolMfaConfigInput` / `SetUserPoolMfaConfigOutput`
- X-Amz-Target: `AWSCognitoIdentityProviderService.SetUserPoolMfaConfig`
- Last verified: 2026-09-20

## Request

| Field                          | Type   | Required | Notes                                                  |
|---------------------------------|--------|----------|---------------------------------------------------------|
| UserPoolId                     | string | yes      | Pattern: `[\w-]+_[0-9a-zA-Z]+`                          |
| MfaConfiguration                | string | no       | `OFF` \| `ON` \| `OPTIONAL`                             |
| SoftwareTokenMfaConfiguration    | object | no       | `{"Enabled": bool}` — persisted (#555); see deviations below |
| SmsMfaConfiguration              | object | no       | Accepted but not persisted (SMS delivery not supported) |
| EmailMfaConfiguration            | object | no       | Accepted but not persisted (email OTP not supported)    |
| WebAuthnConfiguration            | object | no       | Accepted but not persisted (passkeys not supported)     |

## Response (HTTP 200)

| Field                        | Type   | Notes                                                  |
|------------------------------|--------|---------------------------------------------------------|
| MfaConfiguration             | string | Echoes the stored `UserPoolMetadata.MfaConfiguration`   |
| SoftwareTokenMfaConfiguration | object | Echoes the stored `UserPoolMetadata.SoftwareTokenMfaConfigEnabled` (#555) |

`SmsMfaConfiguration`, `EmailMfaConfiguration`, `WebAuthnConfiguration` are always omitted from the response, matching `GetUserPoolMfaConfig`.

## Implemented errors

| Error type                | HTTP | Condition                      |
|---------------------------|------|--------------------------------|
| InvalidParameterException | 400  | UserPoolId missing             |
| ResourceNotFoundException | 400  | Pool not found                 |
| InternalErrorException    | 500  | Storage failure                |

## kumolo deviations

- `MfaConfiguration` is persisted into `UserPoolMetadata.MfaConfiguration`, the same field used by
  `CreateUserPool`/`UpdateUserPool`/`GetUserPoolMfaConfig`/`DescribeUserPool`. No enum validation is performed
  (consistent with `UpdateUserPool`'s existing handling of this field). Only updated when the request sends a
  non-empty value — omitting it (e.g. a Terraform reconcile `apply` that only touches
  `software_token_mfa_configuration`) leaves the stored value unchanged.
- `SoftwareTokenMfaConfiguration.Enabled` is persisted into `UserPoolMetadata.SoftwareTokenMfaConfigEnabled` (#555)
  and echoed back by this operation and `GetUserPoolMfaConfig`. Only updated when the request includes the
  `SoftwareTokenMfaConfiguration` object at all — an absent object leaves the stored value unchanged, matching
  `MfaConfiguration`'s omit-to-keep semantics; an explicit `{"Enabled": false}` does reset it to `false`. This
  field is **not** part of `DescribeUserPool`'s response (`UserPoolType` has no such field on real AWS either) —
  `handleDescribeUserPool` clears it before serializing.
- Persisting this value does **not** gate `InitiateAuth`/`RespondToAuthChallenge` on its own: pool-level
  `MfaConfiguration: "ON"` challenge behavior is driven by `MfaConfiguration` (see `associate_software_token.md`
  and `set_user_mfa_preference.md` for kumolo's per-user TOTP enrollment and enforcement), not by this flag —
  `SoftwareTokenMfaConfiguration.Enabled` is stored purely so Terraform/CLI reads reflect what was last set,
  matching the "works on kumolo ⇒ works on AWS" round-trip goal without kumolo needing to model a delivery-less
  admin toggle as an enforcement switch.
- `SmsMfaConfiguration`, `EmailMfaConfiguration`, and `WebAuthnConfiguration` are accepted (to avoid rejecting
  real-world SDK/Terraform payloads) but silently ignored — kumolo has no SMS/email delivery backend or WebAuthn
  support. This mirrors `GetUserPoolMfaConfig`'s existing deviations.
- Primary motivation: `terraform-provider-aws`'s `aws_cognito_user_pool` resource calls this operation on every
  `apply` (even with no config changes) to reconcile `software_token_mfa_configuration`; previously kumolo returned
  `UnknownOperationException` (#463), then persisted `MfaConfiguration` but hardcoded
  `SoftwareTokenMfaConfiguration.Enabled: false` regardless of input (#555's original bug — caused a perpetual
  `terraform plan` diff on `software_token_mfa_configuration`).
