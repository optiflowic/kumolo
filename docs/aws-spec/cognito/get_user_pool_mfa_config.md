# GetUserPoolMfaConfig

- URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GetUserPoolMfaConfig.html
- SDK type: `cognitoidentityprovider.GetUserPoolMfaConfigInput` / `GetUserPoolMfaConfigOutput`
- X-Amz-Target: `AWSCognitoIdentityProviderService.GetUserPoolMfaConfig`
- Last verified: 2026-09-21

## Request

| Field      | Type   | Required | Notes                          |
|------------|--------|----------|--------------------------------|
| UserPoolId | string | yes      | Pattern: `[\w-]+_[0-9a-zA-Z]+` |

## Response (HTTP 200)

| Field                        | Type   | Notes                                                  |
|------------------------------|--------|--------------------------------------------------------|
| MfaConfiguration             | string | `OFF` \| `ON` \| `OPTIONAL`                            |
| SoftwareTokenMfaConfiguration | object | `{"Enabled": bool}` — omitted if never configured (#555, same as the sibling fields below) |
| SmsMfaConfiguration          | object | `{"SmsAuthenticationMessage": string, "SmsConfiguration": {...}}` — omitted if not configured |
| EmailMfaConfiguration        | object | omitted if not configured                              |
| WebAuthnConfiguration        | object | omitted if not configured                              |

## Implemented errors

| Error type                | HTTP | Condition                      |
|---------------------------|------|--------------------------------|
| InvalidParameterException | 400  | UserPoolId missing             |
| ResourceNotFoundException | 400  | Pool not found                 |
| InternalErrorException    | 500  | Storage failure                |

## kumolo deviations

- `SoftwareTokenMfaConfiguration.Enabled` reflects the stored `UserPoolMetadata.SoftwareTokenMfaConfigEnabled`
  (`*bool`, #555) — whatever `SetUserPoolMfaConfig` last persisted for this pool. A pool that's never called
  `SetUserPoolMfaConfig` (nil) gets the whole `SoftwareTokenMfaConfiguration` key **omitted from the response**,
  not `{"Enabled": false}` — matching how the sibling `SmsMfaConfiguration`/`EmailMfaConfiguration`/
  `WebAuthnConfiguration` fields are already documented below. See
  `docs/aws-spec/cognito/set_user_pool_mfa_config.md`'s deviations for the supporting evidence
  (`aws-sdk-go-v2`'s pointer-typed output field, `terraform-provider-aws`'s nil-guarded flatten function, and a
  locally-reproduced/fixed `terraform plan` drift). This is a pool-level admin toggle only: it is independent of
  per-user TOTP MFA enrollment, which kumolo does support and enforce (see `associate_software_token.md`,
  `set_user_mfa_preference.md`) — a user can enroll and be challenged for `SOFTWARE_TOKEN_MFA` regardless of what
  this field reports.
- `SmsMfaConfiguration`, `EmailMfaConfiguration`, `WebAuthnConfiguration` are omitted (no SMS/email delivery
  backend, no WebAuthn support).
- `MfaConfiguration` is read directly from the stored `UserPoolMetadata`; round-trips through `DescribeUserPool`
  too, since both read the same field.
