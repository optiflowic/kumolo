#!/usr/bin/env bash
set -euo pipefail

ENDPOINT="${KUMOLO_ENDPOINT:-http://localhost:5566}"

PASS=0
FAIL=0

ok()   { echo "  PASS: $*"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $*"; FAIL=$((FAIL + 1)); }

echo "=== CORS ==="

# The AWS CLI never issues a CORS preflight (that's a browser-only mechanism),
# so this uses curl to simulate what a browser sends before a cross-origin
# fetch/XHR call to a Cognito/DynamoDB/KMS/STS endpoint. CORS support is
# opt-in via KUMOLO_CORS_ALLOW_ORIGIN, which must be set on the running
# kumolo instance itself (it's a startup-time flag, not a per-request one).
if [[ -z "${KUMOLO_CORS_ALLOW_ORIGIN:-}" ]]; then
  echo "KUMOLO_CORS_ALLOW_ORIGIN not set; skipping (CORS support is opt-in)."
  echo "Restart kumolo with e.g. KUMOLO_CORS_ALLOW_ORIGIN=http://localhost:5173 to exercise this."
  exit 0
fi

ORIGIN="$KUMOLO_CORS_ALLOW_ORIGIN"

# ---------------------------------------------------------------------------
# Root OPTIONS preflight, as sent before a Cognito/DynamoDB/KMS/STS call
# carrying the custom X-Amz-Target header.
# ---------------------------------------------------------------------------
PREFLIGHT=$(curl -s -i -X OPTIONS "$ENDPOINT/" \
  -H "Origin: $ORIGIN" \
  -H "Access-Control-Request-Method: POST" \
  -H "Access-Control-Request-Headers: content-type,x-amz-target")

if echo "$PREFLIGHT" | grep -qi '^HTTP/[0-9.]* 200'; then
  ok "OPTIONS preflight to / returns 200"
else
  fail "OPTIONS preflight to / expected 200, got: $(echo "$PREFLIGHT" | head -1)"
fi

if echo "$PREFLIGHT" | grep -qi "^Access-Control-Allow-Origin: $ORIGIN"; then
  ok "OPTIONS preflight includes Access-Control-Allow-Origin"
else
  fail "OPTIONS preflight missing Access-Control-Allow-Origin"
fi

# ---------------------------------------------------------------------------
# The actual DynamoDB request that follows a successful preflight must also
# carry Access-Control-Allow-Origin, or the browser discards the response.
# ---------------------------------------------------------------------------
ACTUAL=$(curl -s -i -X POST "$ENDPOINT/" \
  -H "Content-Type: application/x-amz-json-1.0" \
  -H "X-Amz-Target: DynamoDB_20120810.ListTables" \
  -H "Origin: $ORIGIN" \
  -d '{}')

if echo "$ACTUAL" | grep -qi "^Access-Control-Allow-Origin: $ORIGIN"; then
  ok "Actual DynamoDB response includes Access-Control-Allow-Origin"
else
  fail "Actual DynamoDB response missing Access-Control-Allow-Origin"
fi

# ---------------------------------------------------------------------------
echo ""
echo "CORS results: ${PASS} passed, ${FAIL} failed"
[[ $FAIL -eq 0 ]]
