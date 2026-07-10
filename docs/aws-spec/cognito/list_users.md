# ListUsers

- **URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ListUsers.html
- **Target**: `AWSCognitoIdentityProviderService.ListUsers`
- **SDK**: `cognitoidentityprovider.ListUsersInput` / `ListUsersOutput`
- **Last verified**: 2026-07-09

## Request

| Field           | Type   | Required |
|-----------------|--------|----------|
| UserPoolId      | string | Yes      |
| Filter          | string | No       |
| Limit           | int    | No (default/max 60) |
| PaginationToken | string | No       |

- `AttributesToGet` is accepted by AWS but not implemented by kumolo (full attribute set is always returned).

## Filter syntax (kumolo subset)

`"AttributeName Op \"Value\""`, e.g. `family_name = "Reddy"` or `given_name ^= "Jon"`.

- `Op`: `=` (exact match) or `^=` (prefix match).
- Only one attribute per filter (matches AWS: server-side filter is single-attribute).
- Searchable attributes: `username` (case-sensitive), `sub`, `cognito:user_status` (case-insensitive,
  compares against `UserStatus`), `status` (compares against `Enabled`, values `"true"`/`"false"`
  — no official AWS example documents this filter's literal value format, but kumolo follows the
  same `"true"`/`"false"` String convention Cognito uses for every other Boolean-typed attribute,
  e.g. `email_verified`/`phone_number_verified`), and `email`, `phone_number`, `name`, `given_name`,
  `family_name`, `preferred_username` (matched case-sensitively against `UserMetadata.Attributes`).
  Any other attribute name — including custom attributes — is rejected with `InvalidParameterException`.
- No escape-sequence handling for embedded quotes in the value (kumolo deviation — AWS requires
  backslash-escaping embedded quotes; kumolo's parser does not need it since filter values in
  practice don't contain `"`).
- Empty filter returns all users in the pool.

## Response

HTTP 200:

```json
{
  "PaginationToken": "string",
  "Users": [
    {
      "Username": "string",
      "Attributes": [{"Name": "string", "Value": "string"}],
      "UserCreateDate": 1234567890.0,
      "UserLastModifiedDate": 1234567890.0,
      "Enabled": true,
      "UserStatus": "UNCONFIRMED | CONFIRMED | FORCE_CHANGE_PASSWORD",
      "MFAOptions": []
    }
  ]
}
```

- Users are sorted by `Username` for stable pagination.
- `Attributes` includes `sub` as first entry (matches `AdminGetUser`/`GetUser` behavior).
- `MFAOptions` always empty (MFA not implemented).

## Errors implemented

| Error type                | HTTP | Trigger |
|----------------------------|------|---------|
| ResourceNotFoundException  | 400  | pool not found |
| InvalidParameterException  | 400  | `Limit` out of `[1, 60]`, malformed `Filter` string, unsupported filter attribute, invalid pagination token |
| InternalErrorException     | 500  | storage failure |

## kumolo deviations

- `AttributesToGet` accepted but ignored (all attributes always returned).
- `MFAOptions` always empty.
- Filter parser has no escape-sequence support (see above).
- `OperationNotEnabledException` / `TooManyRequestsException` (documented AWS errors) are not implemented.
