# Cognito User Pools — Input Validation

URL: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/
Model: botocore `cognito-idp/2016-04-18/service-2.json` (upstream `develop`)
SDK: github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider
Last verified: 2026-10-17

Applies to every operation the router dispatches (all `case "<Operation>":` entries in
`internal/cognito/router.go`). Runs on the request as read by `input_deserialization.md`.

## Contract

- The router validates the request body against the model **before** it calls the handler.
  A violation is reported ahead of any pool, client, user or group lookup and ahead of
  access-token and session verification.
- A violation returns HTTP 400 `InvalidParameterException`. Nothing is read from or written
  to storage.
- Every member is checked, at any depth: structure members, list elements, map keys and map
  values, plus the size of lists and maps.
- If the operation requires a top-level `UserPoolId` and it is missing or `null`, the message
  is only `UserPoolId must not be null`, and nothing else is checked.

| Constraint | Applies to | Constraint text in the message |
|---|---|---|
| required | any member type | `Member must not be null` |
| max length | string, list, map | `Member must have length less than or equal to <n>` |
| min length | string, list, map | `Member must have length greater than or equal to <n>` |
| max value | integer | `Member must have value less than or equal to <n>` |
| min value | integer | `Member must have value greater than or equal to <n>` |
| pattern (full match) | string | `Member must satisfy regular expression pattern: <pattern>` |
| enum | string | `Member must satisfy enum value set: [A, B]` |

- An omitted member and a JSON `null` are the same: a violation only if the member is required.
- An omitted integer member of a nested structure is checked as 0
  (`Policies.PasswordPolicy` without `MinimumLength`, `RecoveryMechanisms[]` without
  `Priority`).
- In a printed structure value an omitted integer member is also `0`
  (`RecoveryOptionType(priority=0, name=verified_phone_number)`); omitted boolean, string and
  structure members print `null` (`developerOnlyAttribute=null`).
- An explicit empty string is a value. It fails `min >= 1`, any pattern that needs one
  character, and any enum.
- A `null` list element or map value is not checked.
- Every violation is reported, not only the first.

## Message format

```
<N> validation error(s) detected: Value '<value>' at '<path>' failed to satisfy constraint: <constraint>[; Value ...]
```

- `error` for one violation, `errors` for more.
- `<path>`: member names with a lower-case first letter, joined by `.`. A list element is
  `<list>.<1-based index>.member`, a map value is `<map>.<key>.member`:
  `userAttributes.2.member.name`, `policies.passwordPolicy.minimumLength`,
  `acrConfiguration.Level1.member.acrValue`.
- A missing required member is `Value null at '<path>'`.
- A sensitive shape (`AccessToken`, `Session`, `ClientId`, `Username`, passwords, ...) omits
  the value, `null` included: `Value at '<path>'`.
- A list of scalars is reported once at the list, whatever element breaks it:
  `Member must satisfy constraint: [<every constraint of the element shape>]`.
- Map keys and map values of scalar shapes are reported once each at the map:
  `Map keys must satisfy constraint: [...]`, `Map value must satisfy constraint: [...]`.
- Values are printed as AWS's Java service prints them: lists `[a, b]`, maps `{k=v, ...}`,
  structures `ShapeName(member=value, ...)` with members in model order.

### Order

- Across the members of a structure: Java `HashMap` iteration order (table size 64) of the
  lower-camel member names. Violations inside a member follow that member's position.
- Within one scalar: max, min, pattern, enum. A list or map reports its size before its
  elements, and map keys before map values.
- List elements in index order; map entries, enum value sets and printed maps in Java
  `HashMap`/`HashSet` iteration order (table size 16, doubled past 75% load).

## Verified against real AWS

Live calls on 2026-10-11 (#609) and 2026-10-17 (#624, `cognito-idp.ap-northeast-1`), all to a
pool that does not exist. Observed and reproduced exactly:

- Pattern, length and range texts; sensitive values omitted, `null` included.
- `UserPoolId must not be null` for a missing `UserPoolId` (DescribeUserPool, AdminGetUser,
  AdminAddUserToGroup, DescribeUserPoolClient), and the normal format for any other missing
  member (`GetUser` without `AccessToken`).
- Member order: UpdateUserPool (`mfaConfiguration`, `deletionProtection`, `userPoolTier`;
  `autoVerifiedAttributes`, `policies...`, `userPoolTags`), AdminAddUserToGroup, AdminCreateUser,
  AdminGetUser, UpdateUserPoolClient (`allowedOAuthFlows` before `explicitAuthFlows`).
- Enum order: `[SUPPRESS, RESEND]`, `[OPTIONAL, OFF, ON]`, `[ESSENTIALS, LITE, PLUS]`,
  `[ACTIVE, INACTIVE]`, `[phone_number, email]`.
- Nested paths and printed values for lists of structures, lists of strings, maps of strings,
  maps of structures (`{Level4=AcrLevelConfigType(acrValue=d), ...}`), list and map sizes, and
  the omitted nested integers read as 0 and printed as 0 (omitted booleans print `null`).
- Length in code points: 2048 emoji pass a 2048 limit.

Fitted, not derived: the 64-entry member table and the enum/map table sizes match every
observation above; AWS's implementation is not visible.

## Implementation

- `internal/cognito/validation.go`: `validateInput`, called from `Router.serveHTTP` after
  `decodeInput`.
- `internal/cognito/validation_gen.go`: the input shape graph, generated by
  `tools/genvalidation` (`make genvalidation`). Do not edit by hand.
- Regenerate when the model is updated or an operation is added to the router.
  `TestInputRules_CoverEveryDispatchedOperation` fails if an operation has no table entry.
- The table is generated from upstream botocore, not from the awscli2 pinned in the dev shell
  (2.35.11), whose model still has `SessionType` max 2048 instead of 4096. Pass
  `COGNITO_MODEL=<service-2.json>` to `make genvalidation` / `make gencases` until the pinned
  awscli2 catches up.
- Handlers keep only checks the model does not express (for example, `AccessToken` and
  `Session` being mutually exclusive, or attribute names kumolo supports).

## kumolo Deviations

- The live service is ahead of the published model in places, and kumolo follows the model:
  `ExplicitAuthFlowsType` has 12 values on AWS and 9 in the model, and the element pattern of
  `ListUsers.AttributesToGet` is `[\p{L}\p{M}\p{S}\p{N}\p{P}]+` on AWS but also allows
  `\t\n\r ` in the model. The 12-value enum's order on AWS fits no `HashSet` table size.
- Map keys that share a hash bucket are printed in sorted order; AWS uses request order,
  which `decodeInput` does not keep.
- Sensitive members inside a printed list, map or structure value are printed as is; how AWS
  prints them is not confirmed.
- The SRP `Session` kumolo issues is about 2,600 characters, longer than the sessions AWS
  issues but within the model limit of 4096.
