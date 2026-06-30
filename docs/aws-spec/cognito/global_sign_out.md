# GlobalSignOut

**URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GlobalSignOut.html  
**SDK struct**: `cognitoidentityprovider.GlobalSignOutInput`  
**Last verified**: 2026-06-30

## Contract

Invalidates tokens for the currently authenticated user:
- All refresh tokens for the user's sub are deleted (no new sessions can be started).
- The supplied access token's JTI is marked as revoked.

Subsequent calls to token-validated operations (e.g. GetUser) with the revoked token
must return `NotAuthorizedException` with message "Access Token has been revoked".

Other active access tokens for the same user (from concurrent sessions) are not
proactively revoked, but can no longer be refreshed after this call.

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
