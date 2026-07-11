# Cognito — Password Policy Enforcement

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_PasswordPolicyType.html
URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_InvalidPasswordException.html
SDK: `cognitoidentityprovider.types.PasswordPolicyType`
Last verified: 2026-07-11

## Scope

`PasswordPolicyType` is a shared sub-object of `CreateUserPool`/`UpdateUserPool`'s `Policies.PasswordPolicy`
field. This memo documents kumolo's shared validation contract; it is enforced by every operation that
accepts a plaintext password:

- `SignUp` (`Password`)
- `AdminCreateUser` (`TemporaryPassword`, only validated when non-empty)
- `AdminSetUserPassword` (`Password`)
- `RespondToAuthChallenge` `NEW_PASSWORD_REQUIRED` (`NEW_PASSWORD`)

## Fields enforced

| Field | Type | Default when pool's Policies is unset | Enforcement |
|-------|------|------------------------------------|-------------|
| MinimumLength | int (6-99) | 8 | `len(password) < MinimumLength` |
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
2. RequireUppercase
3. RequireLowercase
4. RequireNumbers
5. RequireSymbols

`InvalidPasswordException` (HTTP 400) message format: `Password did not conform with policy: {reason}`,
where `{reason}` is one of:

- `Password not long enough`
- `Password must have uppercase characters`
- `Password must have lowercase characters`
- `Password must have numeric characters`
- `Password must have symbol characters`

## Implementation

- `internal/cognito/password_policy.go`: `passwordPolicyFromPool` reads the pool's stored `Policies` JSON
  blob and extracts `PasswordPolicy`, falling back to the AWS default policy (all complexity flags `true`,
  `MinimumLength=8`) when `Policies` is absent, malformed, or `MinimumLength` is unset/non-positive.
- `validatePassword(policy, password)` returns the formatted message and `ok=false` on the first violated
  rule.

## kumolo Deviations

- No `PasswordHistorySize` enforcement (no password history tracking).
- Symbol detection is a general "not letter/digit/whitespace" Unicode check rather than AWS's documented
  allowed-symbol character set.
