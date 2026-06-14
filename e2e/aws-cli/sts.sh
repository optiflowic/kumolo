#!/usr/bin/env bash
set -euo pipefail

ENDPOINT="${KUMOLO_ENDPOINT:-http://localhost:5566}"

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

AWS="aws --endpoint-url $ENDPOINT sts"
PASS=0
FAIL=0

ok()   { echo "  PASS: $*"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $*"; FAIL=$((FAIL + 1)); }

run() {
  local label="$1"; shift
  if "$@" > /dev/null 2>&1; then
    ok "$label"
  else
    fail "$label"
  fi
}

echo "=== STS ==="

# ---------------------------------------------------------------------------
# GetSessionToken: DurationSeconds validation (#335)
# ---------------------------------------------------------------------------

# Below minimum (900) → rejected.
# The AWS CLI validates DurationSeconds >= 900 client-side before sending to kumolo,
# so we check exit code rather than parsing a specific error string.
if ! $AWS get-session-token --duration-seconds 899 > /dev/null 2>&1; then
  ok "GetSessionToken (DurationSeconds=899 → rejected)"
else
  fail "GetSessionToken (DurationSeconds=899: expected rejection, but succeeded)"
fi

# Above maximum (129600) → ValidationError.
GST_HIGH_ERR=$($AWS get-session-token --duration-seconds 129601 2>&1 || true)
if echo "$GST_HIGH_ERR" | grep -q 'ValidationError\|ValidationException'; then
  ok "GetSessionToken (DurationSeconds=129601 → ValidationError)"
else
  fail "GetSessionToken (DurationSeconds=129601: expected ValidationError, got: $(echo "$GST_HIGH_ERR" | head -1))"
fi

# Valid minimum (900) → success.
run "GetSessionToken (DurationSeconds=900)" \
  $AWS get-session-token --duration-seconds 900

# ---------------------------------------------------------------------------
# AssumeRole: DurationSeconds validation (#335)
# ---------------------------------------------------------------------------
ROLE_ARN="arn:aws:iam::000000000000:role/kumolo-e2e-role"

# Below minimum (900) → rejected.
# The AWS CLI validates DurationSeconds >= 900 client-side before sending to kumolo,
# so we check exit code rather than parsing a specific error string.
if ! $AWS assume-role \
    --role-arn "$ROLE_ARN" \
    --role-session-name "e2e-test" \
    --duration-seconds 899 > /dev/null 2>&1; then
  ok "AssumeRole (DurationSeconds=899 → rejected)"
else
  fail "AssumeRole (DurationSeconds=899: expected rejection, but succeeded)"
fi

# Above maximum (43200) → ValidationError.
AR_HIGH_ERR=$($AWS assume-role \
  --role-arn "$ROLE_ARN" \
  --role-session-name "e2e-test" \
  --duration-seconds 43201 2>&1 || true)
if echo "$AR_HIGH_ERR" | grep -q 'ValidationError\|ValidationException'; then
  ok "AssumeRole (DurationSeconds=43201 → ValidationError)"
else
  fail "AssumeRole (DurationSeconds=43201: expected ValidationError, got: $(echo "$AR_HIGH_ERR" | head -1))"
fi

# Valid minimum (900) → success.
run "AssumeRole (DurationSeconds=900)" \
  $AWS assume-role \
    --role-arn "$ROLE_ARN" \
    --role-session-name "e2e-test" \
    --duration-seconds 900

# ---------------------------------------------------------------------------
echo ""
echo "STS results: ${PASS} passed, ${FAIL} failed"
[[ $FAIL -eq 0 ]]
