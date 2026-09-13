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
- The `OPTIONS` preflight to `/` stays strictly opt-in
  (`KUMOLO_CORS_ALLOW_ORIGIN` only) **when the request's `Host` header
  doesn't name a recognized service** — the default single-endpoint usage
  (`http://localhost:5566` for everything). A preflight request never
  carries `X-Amz-Target` (browsers only list it as an allowed header name,
  not its value), so in that case the dispatcher cannot scope the preflight
  step to Cognito alone: answering it unconditionally would let a browser
  send the unauthenticated DynamoDB/KMS/STS request that follows the
  preflight — the actual response would still lack CORS headers and be
  unreadable by the page, but the side effect (`PutItem`, `CreateKey`,
  `AssumeRole`, ...) would already have happened server-side.
- Since #567, a client can identify itself before the preflight by pointing
  its `BaseEndpoint` at the convention hostname `cognito-idp.localhost:5566`
  instead of the shared endpoint (`*.localhost` resolves to `127.0.0.1` with
  no setup). The `Host` header is always present on a preflight, so the
  dispatcher recognizes it and answers with the Cognito default
  (`Access-Control-Allow-Origin: *`, or the configured origin if
  `KUMOLO_CORS_ALLOW_ORIGIN` is set) unconditionally — safe because the
  target service is now known before dispatch, not inferred after the fact.
  See `docs/service-dispatch.md` for the full mechanism and the equivalent
  hostnames for DynamoDB/DynamoDB Streams/KMS/STS (which keep no default
  origin, so their preflight is answered `200` but without the header,
  still blocking the browser from proceeding).
- Without `KUMOLO_CORS_ALLOW_ORIGIN` set and without using the convention
  Host, a browser calling Cognito directly still fails at the preflight
  step — the same posture as DynamoDB/KMS/STS on the shared endpoint.
- Last verified: 2026-09-13 (#567).

## kumolo Deviations

- No IAM authorization enforced; any credentials accepted
- No AWS WAF, Pinpoint analytics, or Lambda trigger integration
- No actual SMS/email delivery; verification codes returned in-process for testing
