# GlobalSignOut

**URL**: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GlobalSignOut.html  
**SDK struct**: `cognitoidentityprovider.GlobalSignOutInput`  
**Last verified**: 2026-06-30

## Contract

Invalidates all tokens for the currently authenticated user:
- All refresh tokens for the user are deleted.
- The supplied access token's JTI is marked as revoked.

Subsequent calls to token-validated operations (e.g. GetUser) with revoked tokens
must return `NotAuthorizedException` with message "Access Token has been revoked".

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
