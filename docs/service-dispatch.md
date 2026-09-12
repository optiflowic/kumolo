# Service Dispatch — Host-based Service Identification (#567)

## Background

kumolo dispatches all `X-Amz-Target`-routed services (DynamoDB, DynamoDB
Streams, KMS, Cognito) plus STS through a single host:port:path. A browser's
CORS preflight (`OPTIONS /`) never carries `X-Amz-Target` — browsers only
list it as an allowed header name, not its value — so the dispatcher
historically had no way to tell which service a preflight was headed for.

#566 made the *actual* Cognito response default to
`Access-Control-Allow-Origin: *` (matching real `cognito-idp`), but kept the
`OPTIONS /` interceptor strictly opt-in (`KUMOLO_CORS_ALLOW_ORIGIN` required)
— answering it unconditionally would have let a browser's real request reach
unauthenticated DynamoDB/KMS/STS operations even though the response itself
would be unreadable by the page. This closes that remaining gap by giving
the dispatcher a way to identify the target service before the preflight is
answered.

## Convention hostnames

SDK clients that want per-service CORS behavior without setting
`KUMOLO_CORS_ALLOW_ORIGIN` point their per-service `BaseEndpoint` at one of:

| Service          | Host                              |
| ---------------- | ---------------------------------- |
| Cognito          | `cognito-idp.localhost:5566`       |
| DynamoDB         | `dynamodb.localhost:5566`          |
| DynamoDB Streams | `streams.dynamodb.localhost:5566`  |
| KMS              | `kms.localhost:5566`               |
| STS              | `sts.localhost:5566`               |

`*.localhost` resolves to `127.0.0.1` with no DNS/hosts-file setup on every
major OS and browser (RFC 6761), so this needs zero configuration beyond the
SDK endpoint override. The port stays `5566` — this is Host-header virtual
hosting on the one existing listener, not per-service ports.

Matching is an exact, case-insensitive match against the `Host` header (with
any `:port` suffix stripped first) against this fixed list — no
wildcard/prefix matching, so an unrelated domain that merely contains one of
these labels (e.g. `dynamodb.localhost.evil.example`) does not match.

## Preflight policy

The `OPTIONS /` interceptor in `internal/server/server.go` branches on the
`Host` header:

- **Host matches a known service hostname** — the preflight is always
  answered with `200`. `Access-Control-Allow-Origin` is set per that
  service's policy: `KUMOLO_CORS_ALLOW_ORIGIN` if configured, else the
  service's own default (Cognito: `*`; DynamoDB / DynamoDB Streams / KMS /
  STS: no default, so the header is omitted). Omitting the header still
  returns `200`, but a browser that doesn't see the header won't send the
  follow-up request — this is what makes it safe to always resolve a known
  Host to `200` instead of falling through to a router.
- **Host doesn't match any known service hostname** (the default
  single-endpoint usage described in the README) — unchanged pre-#567
  behavior: the preflight is answered only when `KUMOLO_CORS_ALLOW_ORIGIN`
  is set; otherwise it falls through to the normal dispatch chain
  (effectively reaching the S3 router, since path `/` never resolves to an
  S3 bucket/object either).

`KUMOLO_CORS_ALLOW_ORIGIN`, when set, always wins over a service's default —
this preserves existing behavior for anyone already relying on it (e.g. a
single explicit origin instead of `*` for Cognito).

## Actual request dispatch is pinned to the identified Host

A request whose Host names a known service is dispatched to that service's
router unconditionally — the existing `X-Amz-Target`-based switch in
`NewMux` is only consulted when the Host doesn't match a convention
hostname. This is not optional: if the Host-identified branch only affected
the *preflight* answer while the real dispatch kept trusting
`X-Amz-Target` alone, a request sent to `cognito-idp.localhost:5566` (CORS
default-open) with `X-Amz-Target: DynamoDB_20120810.PutItem` would still
reach `dynamoRouter` unauthenticated — reopening the exact preflight/dispatch
mismatch #566 closed, just reachable via a Host the browser was allowed to
identify itself with instead of an unidentified one. Pinning dispatch to
the Host closes that: a mismatched `X-Amz-Target` sent to a convention host
hits that service's own unknown-operation error instead of a different
service's router.

## Out of scope

- S3 is unaffected — its CORS behavior remains driven exclusively by
  `PutBucketCors`, matching real AWS.
- Existing single-endpoint usage (`http://localhost:5566` for everything) is
  unaffected and remains the default documented in the README.
- Docker/compose network aliases for these hostnames are not addressed here
  — `*.localhost` needs no configuration for a browser client, and this
  repository has no docker-compose file wiring container-internal aliases
  today.
