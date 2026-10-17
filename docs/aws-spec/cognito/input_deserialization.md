# Cognito User Pools — Input Deserialization

URL: https://smithy.io/2.0/aws/protocols/aws-json-1_1-protocol.html
Model: botocore `cognito-idp/2016-04-18/service-2.json` (upstream `develop`)
SDK: github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider
Last verified: 2026-10-17

Applies to every operation the router dispatches. Runs before input validation
(`input_validation.md`), so a body that cannot be read as the input shape never reaches the
constraint checks.

## Contract

- The body is read against the operation's input shape from the model. A failure returns
  HTTP 400 `SerializationException`.
- The error body carries the message under `Message` (capital M), not `message`:
  `{"__type":"SerializationException","Message":"..."}`.
- The handler receives the body as read: only members the model declares, under their exact
  names, with the coercions below applied.

### Body

| Body | Result |
|---|---|
| empty, malformed, trailing data, `null`, a string or a number | `SerializationException` with no `Message` |
| a JSON array | `Start of list found where not expected` |
| a JSON object | read as the input structure |

### Member names

- Matched case-sensitively. A member under any other spelling (`userpoolid`) is ignored, as
  is any member the model does not declare. This applies at every nesting level.
- A duplicated member: the last occurrence is used. Each occurrence is still read, so an
  earlier occurrence of the wrong type fails.
- `null` for a structure member is the same as omitting it. `null` as a list element or a map
  value is kept.

### Type mismatch messages

| Expected \ Got | string | number | `true` / `false` | object | array |
|---|---|---|---|---|---|
| string | — | `NUMBER_VALUE can not be converted to a String` | `TRUE_VALUE` / `FALSE_VALUE can not be converted to a String` | `Start of structure or map found where not expected.` | `Start of list found where not expected` |
| integer | `STRING_VALUE can not be converted to an Integer` | — | `TRUE_VALUE` / `FALSE_VALUE can not be converted to an Integer` | same as above | same as above |
| boolean | see coercion | `NUMBER_VALUE can not be converted to a Boolean` | — | same as above | same as above |
| structure | `Expected null` | `Expected null` | `Expected null` | — | `Start of list found where not expected` |
| map | `Expected map or null` | `Expected map or null` | `Expected map or null` | — | `Start of list found where not expected` |
| list | `Expected list or null` | `Expected list or null` | `Expected list or null` | `Start of structure or map found where not expected.` | — |

The punctuation is as AWS returns it: the structure/map message ends with a period, the list
message does not. Boolean uses `an Boolean` after `STRING_VALUE` and `a Boolean` after
`NUMBER_VALUE`.

### Coercion

- Integer: a JSON number with a fraction or an exponent is accepted and truncated toward zero
  (`5.5` → 5, `1e1` → 10). A value outside the 32-bit range is clamped to `2147483647` /
  `-2147483648` and then validated.
- Boolean: a string is accepted when it is, ignoring case, one of `true`, `t`, `yes`, `y`, `1`
  (true) or `false`, `f`, `no`, `n`, `0` (false). Any other string (`""`, `2`, `on`, `" true"`)
  returns `STRING_VALUE can not be converted to an Boolean`.

## Verified against real AWS

Live calls on 2026-10-17 (`cognito-idp.ap-northeast-1`), recorded in issue #625. All requests
targeted a pool that does not exist. Every row of the tables above was observed, except
`FALSE_VALUE` into an integer, which follows the `TRUE_VALUE` row.

## Implementation

- `internal/cognito/deserialize.go`: `decodeInput`, called from `Router.serveHTTP` before
  `validateInput`.
- The shape graph comes from `internal/cognito/validation_gen.go` (`make genvalidation`).

## kumolo Deviations

- The 1 MiB request body limit and its error are kumolo's own.
- The order in which keys reach the handler follows `encoding/json` map encoding (sorted). AWS
  does not expose an order, so this is not observable through the API.
