# GlobalSignOut

**URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GlobalSignOut.html  
**SDK struct**: `cognitoidentityprovider.GlobalSignOutInput`  
**Last verified**: 2026-06-30

## Contract

Invalidates all tokens for the currently authenticated user:
- The `origin_jti` of every refresh token belonging to the user is revoked, blocking all
  outstanding access tokens across every concurrent session.
- The presented token's own `origin_jti` is also revoked directly (covers the edge case
  where the associated refresh token was already deleted before this call).
- All refresh tokens for the user's sub are deleted (no new sessions can be started via
  token refresh).

Subsequent calls to token-validated operations (e.g. GetUser) with any previously issued
access token must return `NotAuthorizedException` with message "Access Token has been
revoked".

Access tokens issued in a new session after this call are valid; each new auth event
generates a new `origin_jti`.

## Request

| Field       | Type   | Required | Notes                                      |
|-------------|--------|----------|--------------------------------------------|
| AccessToken | string | Yes      | Valid access token for the signed-in user  |

## Response

HTTP 200, empty body `{}`.

## Errors implemented

| Error type               | HTTP | Condition                                        |
|--------------------------|------|--------------------------------------------------|
| InvalidParameterException | 400 | AccessToken missing                              |
| NotAuthorizedException    | 400 | Invalid, expired, or already-revoked access token |
| InternalErrorException    | 500 | Storage failure                                  |

## kumolo deviations

- Does not clear a managed login / hosted-UI session cookie (not applicable).
- Scope claim (`aws.cognito.signin.user.admin`) is NOT validated; kumolo accepts any
  valid access token for this operation.
