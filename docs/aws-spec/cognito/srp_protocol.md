# Cognito — SRP Protocol (shared implementation notes)

Not an AWS API reference page — this documents the shared math behind
`USER_SRP_AUTH` (`initiate_auth.md`) and the `PASSWORD_VERIFIER` challenge
(`respond_to_auth_challenge.md`). AWS does not publish this protocol on its
API reference pages (it's a client-SDK concern); these notes are our own
paraphrase, cross-checked against AWS Amplify's `amazon-cognito-identity-js`
/ `packages/auth` reference implementation (Apache-2.0), not copied from it.

Last verified: 2026-07-13

## Roles

kumolo acts as the SRP *server* (verifier holder). A real Cognito SDK client
(e.g. AWS Amplify) acts as the SRP *client* and never transmits the plaintext
password or the verifier.

## Constants

- `N` — a fixed 3072-bit safe prime (the standard Cognito SRP group; hex
  literal in `internal/cognito/srp.go`).
- `g = 2`
- `k = SHA256(padHex(N) || padHex(g))`, computed once as a `*big.Int`.

## padHex

Every big integer in this protocol (`N`, `g`, `A`, `B`, `S`, `U`, salt,
verifier) is always non-negative, so `padHex` only implements the
positive-value branch of the two's-complement encoding real SDK clients use:

1. Hex-encode the integer's absolute value.
2. Left-pad with one `"0"` if the hex string has odd length.
3. Prepend `"00"` if the top nibble is `8`–`f` (top bit of the first byte
   set) — this avoids the byte sequence being misread as a negative
   two's-complement number by any implementation that treats it that way.

This exact padding must be bit-for-bit identical to what a real client
computes, because it is hashed/HMAC'd on both sides — any divergence breaks
every derived value downstream.

## Per-user verifier (computed whenever a password is set)

Whenever kumolo bcrypt-hashes a password (SignUp, AdminCreateUser,
AdminSetUserPassword, ConfirmForgotPassword, ChangePassword, the
`NEW_PASSWORD_REQUIRED` challenge response), it also derives and stores an
SRP salt and verifier on the user record (`UserMetadata.SRPSalt` /
`SRPVerifier`, hex-encoded):

1. Generate 16 random salt bytes, interpret as `saltBig`, encode as
   `saltHex = hex(padHex(saltBig))`. This is the exact string returned to
   clients later as `SALT` — `padHex` is idempotent, so round-tripping
   through hex and back is safe.
2. `userPoolName` = the part of the pool ID after the `_` (kumolo pool IDs
   are `{region}_{9-char-random}`, matching real AWS's format).
3. `innerHash = SHA256(userPoolName + username + ":" + password)`.
4. `x = SHA256(padHex(saltBig) bytes || innerHash)`, as a `*big.Int` (not
   reduced mod `N` — the SRP-6a exponent is used as-is, since reducing an
   exponent mod the modulus `N` rather than mod the group order would be
   mathematically wrong).
5. `verifier = g^x mod N`.

Users created/migrated before this feature existed have no stored verifier;
`USER_SRP_AUTH` fails closed for them (`NotAuthorizedException`) rather than
silently falling back to another flow.

## Session state between InitiateAuth and RespondToAuthChallenge

Real AWS Cognito's `SECRET_BLOCK` is an opaque, presumably KMS-encrypted
blob. kumolo has no KMS, so it deviates: `SECRET_BLOCK` is just a random
32-byte nonce, and all actual session state (the server's ephemeral `b`, the
client's `A`, the username, pool ID) is carried in the existing signed
session JWT (`buildSessionToken`/`parseSessionToken`, RS256, 180s expiry —
the same mechanism the `NEW_PASSWORD_REQUIRED` challenge already uses).

This is intentionally safe to expose in a JWT payload (signed but not
encrypted): recovering the shared secret `S` from `A`, `b`, and the protocol
math still requires the verifier `v`, which is never transmitted and is
re-looked-up from storage by username at verification time.

## Server-side math (InitiateAuth)

Given the client's `SRP_A` and the stored verifier `v`:

1. Reject `A mod N == 0` (`InvalidParameterException`).
2. Generate random server ephemeral `b` (uniform in `[0, N)`, regenerate on
   the near-impossible `0`).
3. `B = (k*v + g^b mod N) mod N`.
4. Return `SALT` (stored salt), `SRP_B = hex(padHex(B))`, `SECRET_BLOCK`
   (random nonce), `USER_ID_FOR_SRP` in `ChallengeParameters`; embed `A`, `b`
   in the session JWT.

## Server-side math (RespondToAuthChallenge, `PASSWORD_VERIFIER`)

1. Recompute `B` from the session's `b` and the (re-fetched) verifier `v`.
2. `U = SHA256(padHex(A) || padHex(B)) mod N`; reject `U == 0`.
3. `S = (A * (v^U mod N) mod N)^b mod N` — the server-side SRP-6a formula
   (the client computes the same `S` via a different formula:
   `(B - k*g^x)^(a+U*x) mod N`; both must converge on the identical value).
4. Derive the 16-byte session key `K` via Cognito's non-standard "HKDF"
   (note the swapped salt/IKM order relative to RFC 5869):
   - `prk = HMAC-SHA256(key=padHex(U) bytes, msg=padHex(S) bytes)`
   - `K = HMAC-SHA256(key=prk, msg="Caldera Derived Key" + 0x01)[:16]`
5. Verify `PASSWORD_CLAIM_SIGNATURE`:
   - `message = userPoolName || username || secretBlockRawBytes || TIMESTAMP`
     (`TIMESTAMP` and `SECRET_BLOCK` are used exactly as submitted by the
     client — kumolo does not independently validate the `TIMESTAMP` format;
     the session JWT's own 180s expiry is the effective replay/clock-skew
     bound).
   - `expected = base64(HMAC-SHA256(key=K, msg=message))`
   - Constant-time compare against the submitted signature. Mismatch →
     `NotAuthorizedException` "Incorrect username or password." — kumolo
     does not distinguish "wrong password" from "tampered proof" to callers,
     matching `USER_PASSWORD_AUTH`'s behavior for a wrong password.

## kumolo Deviations (summary)

- No AWS-style anti-enumeration "fake verifier" trick: an unknown username
  fails fast with `UserNotFoundException` at `InitiateAuth`, consistent with
  `USER_PASSWORD_AUTH`'s existing behavior.
- `SECRET_BLOCK` is an opaque random nonce, not an encrypted state blob;
  actual session state lives in the signed session JWT instead.
- `TIMESTAMP` is not format- or freshness-validated beyond the session JWT's
  180s expiry.
