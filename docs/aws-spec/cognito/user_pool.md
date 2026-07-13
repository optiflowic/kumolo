# Cognito User Pool Operations

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/
SDK: github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider
Last verified: 2026-07-12

## Operations

CreateUserPool, DescribeUserPool, UpdateUserPool, DeleteUserPool, ListUserPools

## Pool ID Format

`{region}_{alphanumeric}` — e.g., `us-east-1_EXAMPLE123`
kumolo generates: `us-east-1_` + 9 random alphanumeric chars (A-Z, a-z, 0-9).

## ARN Format

`arn:aws:cognito-idp:us-east-1:000000000000:userpool/{poolId}`

## CreateUserPool

- Required: `PoolName` (1–128 chars, pattern `[\w\s+=,.@-]+`)
- Returns: `{"UserPool": {...}}` HTTP 200
- Response includes generated `Id`, `Arn`, `CreationDate`, `LastModifiedDate`, `Status: "Active"`, `EstimatedNumberOfUsers: 0`
- `SchemaAttributes` always includes standard OIDC attributes merged with any caller-provided `Schema`
- `MfaConfiguration` defaults to `"OFF"` if not provided
- `UserPoolTier` defaults to `"ESSENTIALS"` if not provided
- `AccountRecoverySetting` defaults to `{"RecoveryMechanisms":[{"Name":"verified_phone_number","Priority":1},{"Name":"verified_email","Priority":2}]}`
  if not provided (matches AWS: phone first, falling back to email) — see
  `docs/aws-spec/cognito/forgot_password.md` for how `ForgotPassword` consumes this setting.
- Errors: InvalidParameterException (400), InternalErrorException (500)

## DescribeUserPool

- Required: `UserPoolId`
- Returns: `{"UserPool": {...}}` HTTP 200 (same shape as CreateUserPool response)
- Errors: ResourceNotFoundException (400), InvalidParameterException (400), InternalErrorException (500)

## UpdateUserPool

- Required: `UserPoolId`
- Optional: `PoolName` (can be renamed), `MfaConfiguration`, `DeletionProtection`, `Policies`, `LambdaConfig`,
  `EmailConfiguration`, `SmsConfiguration`, `DeviceConfiguration`, `AdminCreateUserConfig`,
  `AccountRecoverySetting`, `UserAttributeUpdateSettings`, `UserPoolAddOns`, `VerificationMessageTemplate`,
  `UserPoolTags`, `UserPoolTier`, `AutoVerifiedAttributes`, `SmsAuthenticationMessage`,
  `SmsVerificationMessage`, `EmailVerificationMessage`, `EmailVerificationSubject`
- Immutable after creation (not accepted in Update): `Schema`, `AliasAttributes`, `UsernameAttributes`, `UsernameConfiguration`
- Returns: `{}` HTTP 200
- Errors: ResourceNotFoundException (400), InvalidParameterException (400), InternalErrorException (500)

## DeleteUserPool

- Required: `UserPoolId`
- Returns: `{}` HTTP 200
- Rejects deletion with InvalidParameterException when the pool's `DeletionProtection` is `"ACTIVE"`; caller must first `UpdateUserPool` it to `"INACTIVE"`
- Errors: ResourceNotFoundException (400), InvalidParameterException (400 — missing `UserPoolId` or `DeletionProtection` is `"ACTIVE"`), InternalErrorException (500)

## ListUserPools

- Required: `MaxResults` (1–60)
- Optional: `NextToken` (pagination cursor = ID of last item from previous page)
- Returns: `{"UserPools": [...], "NextToken": "..."}` HTTP 200
- `UserPools` uses summary format (UserPoolDescriptionType): `Id`, `Name`, `CreationDate`, `LastModifiedDate`, `LambdaConfig`, `Status`
- `NextToken` in response is the ID of the last pool returned; absent when no more pages
- Errors: InvalidParameterException (400), InternalErrorException (500)

## Standard OIDC SchemaAttributes (always included)

| Name | Type | Required | Mutable | Constraints |
|------|------|----------|---------|-------------|
| sub | String | true | false | min=1, max=2048 |
| name | String | false | true | min=0, max=2048 |
| given_name | String | false | true | min=0, max=2048 |
| family_name | String | false | true | min=0, max=2048 |
| middle_name | String | false | true | min=0, max=2048 |
| nickname | String | false | true | min=0, max=2048 |
| preferred_username | String | false | true | min=0, max=2048 |
| profile | String | false | true | min=0, max=2048 |
| picture | String | false | true | min=0, max=2048 |
| website | String | false | true | min=0, max=2048 |
| email | String | false | true | min=0, max=2048 |
| email_verified | Boolean | false | true | — |
| gender | String | false | true | min=0, max=2048 |
| birthdate | String | false | true | min=10, max=10 |
| zoneinfo | String | false | true | min=0, max=2048 |
| locale | String | false | true | min=0, max=2048 |
| phone_number | String | false | true | min=0, max=2048 |
| phone_number_verified | Boolean | false | true | — |
| address | String | false | true | min=0, max=2048 |
| updated_at | Number | false | true | min=0 |

## AccountRecoverySetting

`{"RecoveryMechanisms":[{"Name":"...","Priority":N}, ...]}` — accepted/stored/returned as opaque
JSON on `CreateUserPool`/`UpdateUserPool`/`DescribeUserPool`, and consumed by `ForgotPassword`
(see `docs/aws-spec/cognito/forgot_password.md`).

- `Name`: `verified_email` | `verified_phone_number` | `admin_only` (1-2 array entries)
- `Priority`: `1` (highest) or `2`
- `admin_only` is mutually exclusive with the other mechanisms per AWS docs; kumolo does not
  validate this — it is passed through as-is.
- kumolo does not validate `Name`/`Priority` values or uniqueness on write, matching the
  pass-through treatment of other JSON blob fields (`Policies`, `LambdaConfig`, etc.).

## kumolo Deviations

- Fixed region: `us-east-1`, fixed account: `000000000000`
- No SMS/email delivery; no Lambda trigger invocation
- `EstimatedNumberOfUsers` always returns 0 (user counting not yet implemented)
- `Policies.PasswordPolicy` enforcement: see `docs/aws-spec/cognito/password_policy.md`
