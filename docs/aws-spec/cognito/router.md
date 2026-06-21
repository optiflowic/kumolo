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

## kumolo Deviations

- No IAM authorization enforced; any credentials accepted
- No AWS WAF, Pinpoint analytics, or Lambda trigger integration
- No actual SMS/email delivery; verification codes returned in-process for testing
