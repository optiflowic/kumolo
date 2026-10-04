#!/usr/bin/env bash
# Verifies CORS handling for X-Amz-Target-routed services with and without
# KUMOLO_CORS_ALLOW_ORIGIN (#553), and the convention-hostname dispatch (#567).
# Starts its own kumolo instances; skips if the binary has not been built.
# The AWS CLI never sends a preflight, so curl simulates the browser.
set -euo pipefail

KUMOLO_BIN="${KUMOLO_BIN:-./build/kumolo}"

if [[ ! -x "$KUMOLO_BIN" ]]; then
  echo "SKIP: $KUMOLO_BIN not found — run 'make build' first"
  exit 0
fi

# shellcheck source=e2e/lib/kumolo.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/kumolo.sh"

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

PORT=$(pick_free_port 10000 50000 2)
ENDPOINT="http://localhost:$PORT"
DATA_DIR=""
LOG_FILE=""
ORIGIN="http://localhost:5173"

NO_ORIGIN_PORT=$(( PORT + 1 ))
NO_ORIGIN_ENDPOINT="http://localhost:$NO_ORIGIN_PORT"
NO_ORIGIN_DATA_DIR=""
NO_ORIGIN_LOG_FILE=""

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
  rm -rf "$DATA_DIR" "$NO_ORIGIN_DATA_DIR" "$LOG_FILE" "$NO_ORIGIN_LOG_FILE"
}
trap cleanup EXIT

DATA_DIR=$(mktemp -d)
LOG_FILE=$(mktemp)
NO_ORIGIN_DATA_DIR=$(mktemp -d)
NO_ORIGIN_LOG_FILE=$(mktemp)

require_free_port "$PORT"
KUMOLO_DATA_DIR="$DATA_DIR" KUMOLO_LOG_LEVEL=error KUMOLO_CORS_ALLOW_ORIGIN="$ORIGIN" \
  "$KUMOLO_BIN" -port "$PORT" >"$LOG_FILE" 2>&1 &
KUMOLO_PID=$!
wait_kumolo_ready "$KUMOLO_PID" "$PORT" "$LOG_FILE" $DDB list-tables

require_free_port "$NO_ORIGIN_PORT"
(
  unset KUMOLO_CORS_ALLOW_ORIGIN
  KUMOLO_DATA_DIR="$NO_ORIGIN_DATA_DIR" KUMOLO_LOG_LEVEL=error \
    "$KUMOLO_BIN" -port "$NO_ORIGIN_PORT" >"$NO_ORIGIN_LOG_FILE" 2>&1
) &
NO_ORIGIN_KUMOLO_PID=$!
wait_kumolo_ready "$NO_ORIGIN_KUMOLO_PID" "$NO_ORIGIN_PORT" "$NO_ORIGIN_LOG_FILE" $NO_ORIGIN_DDB list-tables

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
# Without KUMOLO_CORS_ALLOW_ORIGIN: the root preflight goes unanswered, but
# Cognito's actual response still defaults to Access-Control-Allow-Origin: *.
# DynamoDB gets no header. (#553)
# ---------------------------------------------------------------------------
NO_ORIGIN_PREFLIGHT=$(curl -s -i -X OPTIONS "$NO_ORIGIN_ENDPOINT/" \
  -H "Origin: $ORIGIN" \
  -H "Access-Control-Request-Method: POST" \
  -H "Access-Control-Request-Headers: content-type,x-amz-target")

if echo "$NO_ORIGIN_PREFLIGHT" | grep -qi "^Access-Control-Allow-Origin:"; then
  fail "OPTIONS preflight to / unexpectedly includes Access-Control-Allow-Origin without opt-in"
else
  ok "OPTIONS preflight to / has no Access-Control-Allow-Origin without opt-in"
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

# ---------------------------------------------------------------------------
# #567 convention hostnames, against the NO_ORIGIN instance. The Host header
# is overridden with -H because *.localhost DNS resolution isn't guaranteed on
# every CI resolver.
# ---------------------------------------------------------------------------
COGNITO_HOST="cognito-idp.localhost:$NO_ORIGIN_PORT"
DYNAMODB_HOST="dynamodb.localhost:$NO_ORIGIN_PORT"

HOST_COGNITO_PREFLIGHT=$(curl -s -i -X OPTIONS "$NO_ORIGIN_ENDPOINT/" \
  -H "Host: $COGNITO_HOST" \
  -H "Origin: $ORIGIN" \
  -H "Access-Control-Request-Method: POST")

if echo "$HOST_COGNITO_PREFLIGHT" | grep -qi '^HTTP/[0-9.]* 200' \
  && echo "$HOST_COGNITO_PREFLIGHT" | grep -qi "^Access-Control-Allow-Origin: \*"; then
  ok "OPTIONS preflight to the Cognito convention Host defaults open without opt-in"
else
  fail "OPTIONS preflight to the Cognito convention Host did not default open"
fi

HOST_DYNAMODB_PREFLIGHT=$(curl -s -i -X OPTIONS "$NO_ORIGIN_ENDPOINT/" \
  -H "Host: $DYNAMODB_HOST" \
  -H "Origin: $ORIGIN" \
  -H "Access-Control-Request-Method: POST")

if echo "$HOST_DYNAMODB_PREFLIGHT" | grep -qi '^HTTP/[0-9.]* 200' \
  && ! echo "$HOST_DYNAMODB_PREFLIGHT" | grep -qi "^Access-Control-Allow-Origin:"; then
  ok "OPTIONS preflight to the DynamoDB convention Host returns 200 with no default origin"
else
  fail "OPTIONS preflight to the DynamoDB convention Host had unexpected CORS headers"
fi

# Dispatch must be pinned to the Host-identified service regardless of
# X-Amz-Target, or the Cognito host would let a DynamoDB operation through.
HOST_MISMATCH=$(curl -s -i -X POST "$NO_ORIGIN_ENDPOINT/" \
  -H "Host: $COGNITO_HOST" \
  -H "Content-Type: application/x-amz-json-1.1" \
  -H "X-Amz-Target: DynamoDB_20120810.PutItem" \
  -H "Origin: $ORIGIN" \
  -d '{}')

if echo "$HOST_MISMATCH" | grep -qi '^HTTP/[0-9.]* 400' \
  && echo "$HOST_MISMATCH" | grep -q "UnknownOperationException"; then
  ok "Dispatch to the Cognito convention Host is pinned even when X-Amz-Target names DynamoDB"
else
  fail "Dispatch to the Cognito convention Host was not pinned (X-Amz-Target mismatch reached another service)"
fi

HOST_DYNAMODB_ACTUAL=$(curl -s -i -X POST "$NO_ORIGIN_ENDPOINT/" \
  -H "Host: $DYNAMODB_HOST" \
  -H "Content-Type: application/x-amz-json-1.0" \
  -H "X-Amz-Target: DynamoDB_20120810.ListTables" \
  -H "Origin: $ORIGIN" \
  -d '{}')

if echo "$HOST_DYNAMODB_ACTUAL" | grep -qi '^HTTP/[0-9.]* 200' \
  && ! echo "$HOST_DYNAMODB_ACTUAL" | grep -qi "^Access-Control-Allow-Origin:"; then
  ok "Actual DynamoDB response over the DynamoDB convention Host has no Access-Control-Allow-Origin"
else
  fail "Actual DynamoDB response over the DynamoDB convention Host had unexpected result"
fi

echo ""
echo "CORS results: ${PASS} passed, ${FAIL} failed"
[[ $FAIL -eq 0 ]]
