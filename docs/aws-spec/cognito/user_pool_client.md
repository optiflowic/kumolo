---
service: cognito
operations:
  - CreateUserPoolClient
  - DescribeUserPoolClient
  - UpdateUserPoolClient
  - DeleteUserPoolClient
  - ListUserPoolClients
url: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_CreateUserPoolClient.html
sdk_types:
  input: cognitoidentityprovider.CreateUserPoolClientInput
  output: cognitoidentityprovider.CreateUserPoolClientOutput
last_verified: 2026-06-23
---

## CreateUserPoolClient

Creates an app client in a user pool.

**Required**: `UserPoolId` (pattern `[\w-]+_[0-9a-zA-Z]+`), `ClientName` (1–128 chars, pattern `[\w\s+=,.@-]+`)

**Notable request fields:**
- `GenerateSecret` (bool): if true, AWS generates a random `ClientSecret`. Cannot be combined with explicit `ClientSecret`.
- `ClientSecret` (string, 24–64 chars, pattern `[\w+]+`): custom secret. Mutually exclusive with `GenerateSecret=true`.
- `EnableTokenRevocation` (bool): defaults to `true` for new clients.
- `AllowedOAuthFlowsUserPoolClient` (bool): defaults to `false`; must be `true` to use OAuth features.
- `EnablePropagateAdditionalUserContextData` (bool): defaults to `false`.
- `ExplicitAuthFlows` ([]string): defaults to `ALLOW_REFRESH_TOKEN_AUTH, ALLOW_USER_SRP_AUTH, ALLOW_CUSTOM_AUTH` if omitted.
- `PreventUserExistenceErrors` (string): `LEGACY | ENABLED`, defaults to `LEGACY`.
- `RefreshTokenValidity` (int, 0–315360000): 0 treated as default (30 days).
- `AccessTokenValidity` (int, 1–86400): default 1 hour.
- `IdTokenValidity` (int, 1–86400): default 1 hour.
- `AuthSessionValidity` (int, 3–15 minutes): default 3 minutes.

**Response**: `{ "UserPoolClient": UserPoolClientType }` — HTTP 200. Includes generated `ClientId` and `ClientSecret` (if generated).

**kumolo ClientId**: 26 lowercase alphanumeric chars (no region prefix, unlike pool IDs).
**kumolo ClientSecret**: 51 alphanumeric chars when `GenerateSecret=true`.

**Errors**: `InvalidParameterException` (400), `ResourceNotFoundException` (400, pool not found), `InternalErrorException` (500).

## DescribeUserPoolClient

**Required**: `UserPoolId`, `ClientId` (1–128 chars, pattern `[\w+]+`).

**Response**: `{ "UserPoolClient": UserPoolClientType }` — HTTP 200.

Returns the same `ClientSecret` that was generated/provided on creation.

**Errors**: `InvalidParameterException` (400), `ResourceNotFoundException` (400, pool or client not found), `InternalErrorException` (500).

## UpdateUserPoolClient

Full-replace semantics: unset fields revert to AWS defaults (per spec).
**Required**: `UserPoolId`, `ClientId`.
`ClientSecret` is preserved from storage (cannot be changed via Update).
`ClientName` is optional in the request but strongly recommended.

**Response**: `{ "UserPoolClient": UserPoolClientType }` — HTTP 200.

**Errors**: `InvalidParameterException` (400), `ResourceNotFoundException` (400), `InternalErrorException` (500).

**kumolo deviation**: kumolo stores only what is explicitly provided. Fields omitted from the request are cleared (not defaulted to AWS defaults).

## DeleteUserPoolClient

**Required**: `UserPoolId`, `ClientId`.

**Response**: HTTP 200 empty body.

**Errors**: `InvalidParameterException` (400), `ResourceNotFoundException` (400), `InternalErrorException` (500).

## ListUserPoolClients

**Required**: `UserPoolId`.
**Optional**: `MaxResults` (int, 1–60, default 60), `NextToken` (pagination cursor).

**Response**: `{ "UserPoolClients": [ { ClientId, ClientName, UserPoolId } ], "NextToken"?: string }` — HTTP 200.

Returns only `ClientId`, `ClientName`, `UserPoolId` per entry (not full metadata). Use `DescribeUserPoolClient` for full details.

**kumolo pagination**: `NextToken` is the last-returned `ClientId` (sorted lexicographically), same strategy as `ListUserPools`.

**Errors**: `InvalidParameterException` (400), `ResourceNotFoundException` (400, pool not found), `InternalErrorException` (500).

## Storage Layout

```
{dataDir}/cognito/pools/{poolId}/clients/{clientId}.json
```

Each client is a standalone JSON file. The `clients/` directory is created lazily on the first `CreateUserPoolClient` call. It is removed recursively when the parent pool is deleted.
