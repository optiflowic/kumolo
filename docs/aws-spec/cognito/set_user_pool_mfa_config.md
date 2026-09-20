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
| SoftwareTokenMfaConfiguration    | object | no       | `{"Enabled": bool}` — persisted, full-replace semantics (#555); see deviations below |
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
  and echoed back by this operation and `GetUserPoolMfaConfig`. Unlike `MfaConfiguration`, this uses **full-replace
  semantics, not omit-to-keep**: a request that omits `SoftwareTokenMfaConfiguration` resets it to `false`, the
  same as an explicit `{"Enabled": false}` — it does not preserve whatever was stored before. Evidence:
  [aws/aws-sdk-js#4186](https://github.com/aws/aws-sdk-js/issues/4186) — a `SetUserPoolMfaConfig` call with only
  `MfaConfiguration` set, omitting an already-configured `SmsMfaConfiguration`, failed with `InvalidParameterException:
  can't disable all MFAs with a required or optional configuration`, which only makes sense if the omitted factor
  was treated as cleared rather than kept. kumolo does not replicate that specific validation (rejecting
  `MfaConfiguration: ON`/`OPTIONAL` when no factor ends up enabled) — see the enforcement note below for why. This
  field is **not** part of `DescribeUserPool`'s response (`UserPoolType` has no such field on real AWS either) —
  `handleDescribeUserPool` clears it before serializing.
- Whether `MfaConfiguration` itself resets to `OFF` when omitted (matching `SoftwareTokenMfaConfiguration`'s
  behavior) rather than kumolo's current omit-to-keep handling is **unconfirmed** — the aws-sdk-js#4186 evidence
  only covers the factor sub-objects, not this top-level field, and no primary source was found either way.
  Untouched by #555; `terraform-provider-aws`'s `aws_cognito_user_pool` resource always sends `mfa_configuration`
  (its schema defaults it to `"OFF"`), so this ambiguity doesn't affect the Terraform reconcile-apply flow #463/#555
  were driven by. Flagged here rather than changed without evidence — verify against a real pool before changing.
- Persisting this value does **not** gate `InitiateAuth`/`RespondToAuthChallenge` on its own: pool-level
  `MfaConfiguration: "ON"` challenge behavior is driven by `MfaConfiguration` (see `associate_software_token.md`
  and `set_user_mfa_preference.md` for kumolo's per-user TOTP enrollment and enforcement), not by this flag —
  `SoftwareTokenMfaConfiguration.Enabled` is stored purely so Terraform/CLI reads reflect what was last set,
  matching the "works on kumolo ⇒ works on AWS" round-trip goal without kumolo needing to model a delivery-less
  admin toggle as an enforcement switch.
- kumolo does **not** implement real AWS's validation that rejects `MfaConfiguration: "ON"`/`"OPTIONAL"` with
  `InvalidParameterException` when the resulting config would have no MFA factor enabled at all (the exact
  condition #4186 above hit). Deliberate: kumolo's `MfaConfiguration: "ON"` enforcement is per-user (an enrolled
  user gets challenged regardless of `SoftwareTokenMfaConfigEnabled`; see `handler_mfa.go`'s `mfaConfigurationOn`
  checks), and many existing tests (`handler_mfa_test.go`) call this operation with `MfaConfiguration: "ON"` and no
  factor config at all — replicating AWS's rejection would break that established, documented model rather than
  extend it. Flagged as a known gap, not fixed by #555.
- `SmsMfaConfiguration`, `EmailMfaConfiguration`, and `WebAuthnConfiguration` are accepted (to avoid rejecting
  real-world SDK/Terraform payloads) but silently ignored — kumolo has no SMS/email delivery backend or WebAuthn
  support. This mirrors `GetUserPoolMfaConfig`'s existing deviations.
- Primary motivation: `terraform-provider-aws`'s `aws_cognito_user_pool` resource calls this operation on every
  `apply` (even with no config changes) to reconcile `software_token_mfa_configuration`; previously kumolo returned
  `UnknownOperationException` (#463), then persisted `MfaConfiguration` but hardcoded
  `SoftwareTokenMfaConfiguration.Enabled: false` regardless of input (#555's original bug — caused a perpetual
  `terraform plan` diff on `software_token_mfa_configuration`).
