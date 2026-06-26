# Cognito — JWKS Endpoint

URL: https://docs.aws.amazon.com/cognito/latest/developerguide/amazon-cognito-user-pools-using-tokens-verifying-a-jwt.html
Last verified: 2026-06-26

## Overview

REST GET endpoint for retrieving public keys used to verify Cognito-issued JWTs.
Not an AWS API target — path-based routing with no `X-Amz-Target` header.

## Request

```http
GET /{userPoolId}/.well-known/jwks.json
```

No authentication required. No request body.

## Response (200 OK)

```json
{
  "keys": [
    {
      "kid": "<key-id>",
      "alg": "RS256",
      "kty": "RSA",
      "use": "sig",
      "n":   "<base64url-encoded RSA modulus>",
      "e":   "<base64url-encoded RSA exponent>"
    }
  ]
}
```

Real AWS returns two keys (one for access tokens, one for ID tokens).
kumolo returns one key used for both — acceptable simplification for local dev.

The `e` value for the standard exponent 65537 encodes as `AQAB`.
`n` and `e` are Base64urlUInt-encoded (no padding, RFC 7518 §2).

## Error Cases

| Condition | HTTP Status | Notes |
|-----------|-------------|-------|
| Unknown user pool ID | 404 Not Found | Plain-text body (`404 page not found`); not the JSON error envelope |

## SDK Struct Names

No SDK type — this endpoint is accessed directly via HTTP client.
AWS SDK `aws-jwt-verify` library (Node.js) and standard JWT libraries consume this endpoint.

## kumolo Deviations

- Returns one RSA key per pool (real AWS returns two: one per token type)
- Key is generated lazily on first use (real AWS pre-generates keys at pool creation)
