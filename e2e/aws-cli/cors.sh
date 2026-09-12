#!/usr/bin/env bash
# Verifies CORS preflight handling for X-Amz-Target-routed services, in two
# configurations: with KUMOLO_CORS_ALLOW_ORIGIN opted in (all services), and
# with it left unset (Cognito still defaults to Access-Control-Allow-Origin:
# *, matching real cognito-idp; DynamoDB stays opt-in only — #553). Starts
# its own kumolo instances; does not require a pre-running server, and
# doesn't depend on whether an ambient instance has CORS enabled. The AWS
# CLI never issues a CORS preflight (that's a browser-only mechanism), so
# curl is used to simulate what a browser sends.
# Skips gracefully if the binary has not been built yet.
set -euo pipefail

KUMOLO_BIN="${KUMOLO_BIN:-./build/kumolo}"

if [[ ! -x "$KUMOLO_BIN" ]]; then
  echo "SKIP: $KUMOLO_BIN not found — run 'make build' first"
  exit 0
fi

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

PORT=$(( (RANDOM % 50000) + 10000 ))
ENDPOINT="http://localhost:$PORT"
DATA_DIR=$(mktemp -d)
ORIGIN="http://localhost:5173"

NO_ORIGIN_PORT=$(( PORT + 1 ))
NO_ORIGIN_ENDPOINT="http://localhost:$NO_ORIGIN_PORT"
NO_ORIGIN_DATA_DIR=$(mktemp -d)

DDB="aws --endpoint-url $ENDPOINT dynamodb"
NO_ORIGIN_DDB="aws --endpoint-url $NO_ORIGIN_ENDPOINT dynamodb"

PASS=0
FAIL=0
KUMOLO_PID=""
NO_ORIGIN_KUMOLO_PID=""

ok()   { echo "  PASS: $*"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $*"; FAIL=$((FAIL + 1)); }

cleanup() {
  [[ -n "$KUMOLO_PID" ]] && kill "$KUMOLO_PID" 2>/dev/null || true
  [[ -n "$NO_ORIGIN_KUMOLO_PID" ]] && kill "$NO_ORIGIN_KUMOLO_PID" 2>/dev/null || true
  rm -rf "$DATA_DIR" "$NO_ORIGIN_DATA_DIR"
}
trap cleanup EXIT

KUMOLO_DATA_DIR="$DATA_DIR" KUMOLO_LOG_LEVEL=error KUMOLO_CORS_ALLOW_ORIGIN="$ORIGIN" \
  "$KUMOLO_BIN" -port "$PORT" >/dev/null 2>&1 &
KUMOLO_PID=$!
n=0
until $DDB list-tables >/dev/null 2>&1; do
  sleep 0.25
  n=$((n + 1))
  if [[ $n -ge 40 ]]; then
    echo "  ERROR: kumolo did not start in time (port $PORT)"
    exit 1
  fi
done

KUMOLO_DATA_DIR="$NO_ORIGIN_DATA_DIR" KUMOLO_LOG_LEVEL=error \
  "$KUMOLO_BIN" -port "$NO_ORIGIN_PORT" >/dev/null 2>&1 &
NO_ORIGIN_KUMOLO_PID=$!
n=0
until $NO_ORIGIN_DDB list-tables >/dev/null 2>&1; do
  sleep 0.25
  n=$((n + 1))
  if [[ $n -ge 40 ]]; then
    echo "  ERROR: kumolo did not start in time (port $NO_ORIGIN_PORT)"
    exit 1
  fi
done

echo ""
echo "=== CORS ==="

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
# Without KUMOLO_CORS_ALLOW_ORIGIN: Cognito still defaults to
# Access-Control-Allow-Origin: * (matching real cognito-idp, which requires
# no configuration); DynamoDB stays opt-in only and gets no header. (#553)
# ---------------------------------------------------------------------------
NO_ORIGIN_PREFLIGHT=$(curl -s -i -X OPTIONS "$NO_ORIGIN_ENDPOINT/" \
  -H "Origin: $ORIGIN" \
  -H "Access-Control-Request-Method: POST" \
  -H "Access-Control-Request-Headers: content-type,x-amz-target")

if echo "$NO_ORIGIN_PREFLIGHT" | grep -qi "^Access-Control-Allow-Origin: \*"; then
  ok "OPTIONS preflight to / defaults to Access-Control-Allow-Origin: * without opt-in"
else
  fail "OPTIONS preflight to / missing default Access-Control-Allow-Origin: *"
fi

NO_ORIGIN_COGNITO=$(curl -s -i -X POST "$NO_ORIGIN_ENDPOINT/" \
  -H "Content-Type: application/x-amz-json-1.1" \
  -H "X-Amz-Target: AWSCognitoIdentityProviderService.InitiateAuth" \
  -H "Origin: $ORIGIN" \
  -d '{}')

if echo "$NO_ORIGIN_COGNITO" | grep -qi "^Access-Control-Allow-Origin: \*"; then
  ok "Actual Cognito response defaults to Access-Control-Allow-Origin: * without opt-in"
else
  fail "Actual Cognito response missing default Access-Control-Allow-Origin: *"
fi

NO_ORIGIN_DYNAMODB=$(curl -s -i -X POST "$NO_ORIGIN_ENDPOINT/" \
  -H "Content-Type: application/x-amz-json-1.0" \
  -H "X-Amz-Target: DynamoDB_20120810.ListTables" \
  -H "Origin: $ORIGIN" \
  -d '{}')

if echo "$NO_ORIGIN_DYNAMODB" | grep -qi "^Access-Control-Allow-Origin:"; then
  fail "Actual DynamoDB response unexpectedly includes Access-Control-Allow-Origin without opt-in"
else
  ok "Actual DynamoDB response has no Access-Control-Allow-Origin without opt-in"
fi

echo ""
echo "CORS results: ${PASS} passed, ${FAIL} failed"
[[ $FAIL -eq 0 ]]
