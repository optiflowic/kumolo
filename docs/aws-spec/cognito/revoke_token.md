# RevokeToken

**URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_RevokeToken.html  
**SDK struct**: `cognitoidentityprovider.RevokeTokenInput`  
**Last verified**: 2026-06-30

## Contract

Revokes a refresh token and all access tokens issued in the same auth event.
After revocation, access tokens derived from that refresh token must be rejected by
operations that validate access tokens (e.g. GetUser).

## Request

| Field        | Type   | Required | Notes                               |
|--------------|--------|----------|-------------------------------------|
| ClientId     | string | Yes      | App client that issued the token    |
| Token        | string | Yes      | Refresh token to revoke             |
| ClientSecret | string | No       | Not implemented — ignored by kumolo |

## Response

HTTP 200, empty body `{}`.

Revocation is idempotent: if the token does not exist (already revoked or never issued),
return 200 without error.

## Errors implemented

| Error type               | HTTP | Condition                              |
|--------------------------|------|----------------------------------------|
| InvalidParameterException | 400 | ClientId or Token missing              |
| ResourceNotFoundException | 400 | ClientId not found (unknown pool)      |
| UnauthorizedException     | 400 | Token belongs to a different ClientId  |
| InternalErrorException    | 500 | Storage failure                        |

## kumolo deviations

- `ClientSecret` is accepted but ignored; kumolo does not validate client secrets.
- Revocation is implemented via `origin_jti`: all access tokens issued from the same
  refresh token family (initial auth + subsequent `REFRESH_TOKEN_AUTH` calls) share the
  same `origin_jti`, which is stored in the refresh token record. `RevokeToken` marks
  that `origin_jti` as revoked so that any token in the family is rejected.
