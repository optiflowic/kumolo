# Cognito User Pools — Router / Protocol

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/
SDK: github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider
Last verified: 2026-06-22

## Protocol

- All User Pool API operations: POST to the service endpoint
- Routing header: `X-Amz-Target: AWSCognitoIdentityProviderService.{OperationName}`
- Content-Type (request and response): `application/x-amz-json-1.1`
- Real AWS endpoint: `https://cognito-idp.{region}.amazonaws.com/`
- kumolo dispatch: X-Amz-Target prefix `AWSCognitoIdentityProviderService.`

## Error Response Format

```json
{"__type": "ExceptionName", "message": "Error description"}
```

No namespace prefix (unlike DynamoDB which uses `com.amazonaws.dynamodb...#ExceptionName`).

## Common Errors

| Error Type | HTTP Status | Notes |
|-----------|------------|-------|
| UnknownOperationException | 400 | Unrecognized operation name (AWS docs say 404; kumolo uses 400 — needs verification against real AWS) |
| InvalidParameterException | 400 | Missing or malformed input |
| NotAuthorizedException | 400 | Invalid credentials or token |
| ResourceNotFoundException | 400 | User pool or client not found |
| UserNotFoundException | 400 | User does not exist |
| UserNotConfirmedException | 400 | User registered but not confirmed |
| UsernameExistsException | 400 | SignUp with already-taken username |
| InternalErrorException | 500 | Unexpected server error |

Note: Most Cognito errors use HTTP 400, including "not found" variants.

## JWKS Endpoint

`/{userPoolId}/.well-known/jwks.json` — path-based routing, not X-Amz-Target.
Implemented in #17 alongside JWT token issuance.

## CORS

- Real cognito-idp returns `access-control-allow-origin: *` on every response
  with no configuration required; browser SDKs (`amazon-cognito-identity-js`,
  Amplify) call it directly from a browser and depend on this. This is
  observed/documented-by-convention runtime behavior, not spelled out on a
  single AWS API reference page.
- kumolo matches this: Cognito-routed requests (`X-Amz-Target:
  AWSCognitoIdentityProviderService.*` and the `jwks.json` path) get
  `Access-Control-Allow-Origin: *` by default, even when
  `KUMOLO_CORS_ALLOW_ORIGIN` is unset. Setting the env var overrides the
  default with the configured origin, same as for the other services below.
- DynamoDB, DynamoDB Streams, KMS, and STS are **not** designed for direct
  browser use on real AWS, so kumolo keeps their CORS support opt-in via
  `KUMOLO_CORS_ALLOW_ORIGIN` only — Cognito is the one exception.
- Implementation lives in the shared dispatcher (`internal/server/server.go`,
  `defaultCognitoCORSAllowOrigin`), not in `internal/cognito`, since it's a
  cross-cutting concern of the request router that multiplexes all
  `X-Amz-Target`-routed services on one port.
- The `OPTIONS` preflight to `/` is answered by default regardless of which
  service the browser is about to call — a preflight request never carries
  `X-Amz-Target` (browsers only list it as an allowed header name, not its
  value), so the dispatcher can't scope the preflight step to Cognito alone.
  This is harmless: a non-Cognito *actual* response still omits the header
  without opt-in, so the browser still blocks reading it.
- Scope: broadening the preflight interceptor to `/{poolId}/.well-known/*`
  paths (for generic OIDC discovery clients) is tracked separately, not part
  of this default.
- Last verified: 2026-09-12 (#553).

## kumolo Deviations

- No IAM authorization enforced; any credentials accepted
- No AWS WAF, Pinpoint analytics, or Lambda trigger integration
- No actual SMS/email delivery; verification codes returned in-process for testing
