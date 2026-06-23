# GetUserPoolMfaConfig

- URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GetUserPoolMfaConfig.html
- SDK type: `cognitoidentityprovider.GetUserPoolMfaConfigInput` / `GetUserPoolMfaConfigOutput`
- X-Amz-Target: `AWSCognitoIdentityProviderService.GetUserPoolMfaConfig`
- Last verified: 2026-06-24

## Request

| Field      | Type   | Required | Notes                          |
|------------|--------|----------|--------------------------------|
| UserPoolId | string | yes      | Pattern: `[\w-]+_[0-9a-zA-Z]+` |

## Response (HTTP 200)

| Field                        | Type   | Notes                                                  |
|------------------------------|--------|--------------------------------------------------------|
| MfaConfiguration             | string | `OFF` \| `ON` \| `OPTIONAL`                            |
| SoftwareTokenMfaConfiguration | object | `{"Enabled": bool}` — TOTP enabled/disabled state     |
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

- `SoftwareTokenMfaConfiguration.Enabled` is always `false` (TOTP not supported).
- `SmsMfaConfiguration`, `EmailMfaConfiguration`, `WebAuthnConfiguration` are omitted.
- MfaConfiguration is read directly from the stored UserPoolMetadata.
