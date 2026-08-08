# Cognito User Pool Operations

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/
SDK: github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider
Last verified: 2026-08-09

## Operations

CreateUserPool, DescribeUserPool, UpdateUserPool, DeleteUserPool, ListUserPools

## Pool ID Format

`{region}_{alphanumeric}` — e.g., `us-east-1_EXAMPLE123`
kumolo generates: `{region}_` + 9 random alphanumeric chars (A-Z, a-z, 0-9). `region` is
resolved once at `CreateUserPool` time — see "Region resolution" below — and baked into the
pool ID for the life of the pool.

## ARN Format

`arn:{partition}:cognito-idp:{region}:000000000000:userpool/{poolId}`

`{region}` is derived from the pool ID's `{region}_` prefix (not stored separately), so it
always matches the region the pool was created with. `DescribeUserPool` and the JWT `iss`
claim (`https://cognito-idp.{region}.amazonaws.com/{poolId}`) derive their region the same
way.

`{partition}` matches AWS's own ARN partition scheme: `aws-us-gov` for GovCloud (US) regions
(`us-gov-west-1`, `us-gov-east-1`), `aws` for everything else. kumolo does not support the
`aws-cn` partition (no China region emulation).

## Region resolution

`CreateUserPool` resolves the region for a new pool in this order:

1. The region segment of the caller's SigV4 credential scope (`Credential=.../{region}/...`
   in the `Authorization` header or a presigned URL's `X-Amz-Credential`) — reflects the
   AWS SDK/CLI/Terraform provider's configured region, matching real AWS.
2. `AWS_REGION` env var, then `AWS_DEFAULT_REGION` env var, on the kumolo process.
3. `us-east-1`, if none of the above are set.

Pools created before this resolution existed keep their `us-east-1_...` IDs and continue to
work unchanged, since the region is parsed from the ID rather than tracked separately.

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

- Region reflects the caller's SigV4 credential scope at `CreateUserPool` time (see "Region
  resolution" above), not a real AWS availability constraint — kumolo does not validate that
  the region is a real AWS region name. Fixed account: `000000000000`.
- No SMS/email delivery; no Lambda trigger invocation
- `EstimatedNumberOfUsers` always returns 0 (user counting not yet implemented)
- `Policies.PasswordPolicy` enforcement: see `docs/aws-spec/cognito/password_policy.md`
