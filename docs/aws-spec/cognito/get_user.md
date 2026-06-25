# GetUser — Implementation Contract

**Official URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GetUser.html  
**SDK struct**: `cognitoidentityprovider.GetUserInput` / `GetUserOutput`  
**Last verified**: 2026-06-25

## Request

```json
{ "AccessToken": "<jwt>" }
```

- `AccessToken` (required): RS256 JWT issued by `InitiateAuth` or `RespondToAuthChallenge`.

## Access Token Validation (in order)

1. Parse the JWT claims without signature verification to extract `iss`.
2. Derive `poolID` from `iss`: `https://cognito-idp.<region>.amazonaws.com/<poolID>`.
3. Fetch the pool's RSA public key via `GetOrCreatePoolKeys(poolID)`.
4. Verify the RS256 signature with that public key → `NotAuthorizedException` on failure.
5. Check `exp > now` → `NotAuthorizedException` with "Access Token has expired".
6. Check `token_use == "access"` → `NotAuthorizedException` on mismatch.
7. Extract `sub` from verified claims.

## Response (HTTP 200)

```json
{
  "Username": "alice",
  "UserAttributes": [
    { "Name": "sub", "Value": "<uuid>" },
    { "Name": "email", "Value": "alice@example.com" }
  ]
}
```

- `sub` is always the first element of `UserAttributes`; any existing `sub` in the stored attributes is removed and replaced at index 0.
- `MFAOptions`, `PreferredMfaSetting`, `UserMFASettingList` are omitted (MFA not implemented).

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `InvalidParameterException` | 400 | `AccessToken` missing or unparseable request body |
| `NotAuthorizedException` | 400 | Malformed JWT, invalid signature, expired token, wrong `token_use`, or unknown pool |
| `UserNotFoundException` | 400 | `sub` not found in the pool |
| `InternalErrorException` | 500 | Storage failure |

## kumolo Deviations

- MFA fields (`MFAOptions`, `PreferredMfaSetting`, `UserMFASettingList`) are not returned.
- `PasswordResetRequiredException` is never returned — `RESET_REQUIRED` user status is not implemented.
- Unknown pool IDs (no persisted RSA keys) return `NotAuthorizedException` rather than `ResourceNotFoundException`.
- `exp` is checked with `<=` (expired at the exact expiry second), matching standard JWT semantics.
