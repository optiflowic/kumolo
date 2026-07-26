# Cognito — Password Policy Enforcement

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_PasswordPolicyType.html
URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_InvalidPasswordException.html
URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_SignUp.html
SDK: `cognitoidentityprovider.types.PasswordPolicyType`
Last verified: 2026-07-26

## Scope

`PasswordPolicyType` is a shared sub-object of `CreateUserPool`/`UpdateUserPool`'s `Policies.PasswordPolicy`
field. This memo documents kumolo's shared validation contract; it is enforced by every operation that
accepts a plaintext password:

- `SignUp` (`Password`)
- `AdminCreateUser` (`TemporaryPassword`, only validated when non-empty)
- `AdminSetUserPassword` (`Password`)
- `RespondToAuthChallenge` `NEW_PASSWORD_REQUIRED` (`NEW_PASSWORD`)
- `ConfirmForgotPassword` (`Password`)
- `ChangePassword` (`ProposedPassword`)

## Fields enforced

| Field | Type | Default when pool's Policies is unset | Enforcement |
|-------|------|------------------------------------|-------------|
| MinimumLength | int (6-99) | 8 | `utf8.RuneCountInString(password) < MinimumLength` (character count, not bytes) |
| MaximumLength | fixed at 256 | 256 | `utf8.RuneCountInString(password) > 256` (character count, matches AWS's `Password` field length constraint documented on `SignUp`) |
| RequireUppercase | bool | true | at least one Unicode uppercase rune |
| RequireLowercase | bool | true | at least one Unicode lowercase rune |
| RequireNumbers | bool | true | at least one Unicode digit rune |
| RequireSymbols | bool | true | at least one rune that is not a letter, digit, or whitespace |

`PasswordHistorySize` is accepted and stored (as part of the opaque `Policies` blob) but not enforced —
no password history is tracked.

## Validation order and error message

kumolo checks rules in this order and returns on the first violation (matches observed AWS behavior of
reporting a single failing rule per response):

1. MinimumLength
2. MaximumLength (256 characters, fixed — see Implementation for how kumolo avoids bcrypt's 72-byte input cap)
3. RequireUppercase
4. RequireLowercase
5. RequireNumbers
6. RequireSymbols

`InvalidPasswordException` (HTTP 400) message format: `Password did not conform with policy: {reason}`,
where `{reason}` is one of:

- `Password not long enough`
- `Password too long`
- `Password must have uppercase characters`
- `Password must have lowercase characters`
- `Password must have numeric characters`
- `Password must have symbol characters`

The AWS API reference documents only that `InvalidPasswordException` carries a `message` field; it does
not specify exact wording. The strings above are kumolo's best-effort reproduction based on observed real
AWS error output, not a documented contract — treat as lower-confidence and re-verify against a live pool
if a consuming test asserts on exact message text.

## Implementation

- `internal/cognito/password_policy.go`: `passwordPolicyFromPool` reads the pool's stored `Policies` JSON
  blob and overlays present `PasswordPolicy` fields onto the AWS default policy (all complexity flags
  `true`, `MinimumLength=8`). Fields omitted from the blob — including the whole `Policies` blob being
  absent or malformed — inherit the default rather than being zeroed out; `MinimumLength` additionally
  falls back to the default when present but non-positive.
- `validatePassword(policy, password)` returns the formatted message and `ok=false` on the first violated
  rule.
- `internal/cognito/handler_password.go`: `prehashPassword` (SHA-256, then base64-standard-encoded, 44
  bytes) runs on every plaintext password before it reaches bcrypt, at both hash-generation
  (`hashPassword`) and verification (`verifyPassword`) call sites. This keeps every possible input
  (up to the 256-character/1024-byte worst case for 4-byte UTF-8 runes) under bcrypt's 72-byte cap without
  truncating or rejecting policy-valid passwords. Hashing and verification always go through these two
  helpers so the prehash step stays consistent on both sides.

## kumolo Deviations

- No `PasswordHistorySize` enforcement (no password history tracking).
- Symbol detection is a general "not letter/digit/whitespace" Unicode check rather than AWS's documented
  allowed-symbol character set.
- The SHA-256 prehash step described in Implementation is a kumolo-internal storage detail to work around
  bcrypt's 72-byte input cap; it is not part of the AWS API contract. Password hashes created before
  this scheme was introduced are incompatible and will fail to verify; affected users require a
  password reset.
