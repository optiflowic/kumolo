#!/usr/bin/env bash
# Drives Cognito's CORS default (#553) through a real browser SDK call,
# unlike aws-cli/cors.sh which only inspects headers via curl.
set -euo pipefail

KUMOLO_BIN="${KUMOLO_BIN:-./build/kumolo}"

if [[ ! -x "$KUMOLO_BIN" ]]; then
  echo "SKIP: $KUMOLO_BIN not found — run 'make build' first"
  exit 0
fi

if ! command -v npm &>/dev/null; then
  echo "SKIP: npm not found — install Node.js to run the browser e2e test"
  exit 0
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=e2e/lib/kumolo.sh
source "$SCRIPT_DIR/../lib/kumolo.sh"
DATA_DIR=$(mktemp -d)
LOG_FILE=$(mktemp)
KUMOLO_PID=""

cleanup() {
  [[ -n "$KUMOLO_PID" ]] && kill "$KUMOLO_PID" 2>/dev/null || true
  rm -rf "$DATA_DIR" "$LOG_FILE"
}
trap cleanup EXIT

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

PORT=$(( (RANDOM % 40000) + 20000 ))
HARNESS_PORT=$((PORT + 1))
ENDPOINT="http://localhost:$PORT"

echo "=== Cognito CORS (browser) ==="

require_free_port "$PORT"
KUMOLO_DATA_DIR="$DATA_DIR" KUMOLO_CORS_ALLOW_ORIGIN="http://localhost:$HARNESS_PORT" \
  "$KUMOLO_BIN" -port "$PORT" >"$LOG_FILE" 2>&1 &
KUMOLO_PID=$!

wait_kumolo_ready "$KUMOLO_PID" "$PORT" "$LOG_FILE" curl -s -o /dev/null "$ENDPOINT/"

POOL_JSON=$(aws --endpoint-url "$ENDPOINT" cognito-idp create-user-pool \
  --pool-name "e2e-browser-cors-pool" \
  --auto-verified-attributes email)
POOL_ID=$(echo "$POOL_JSON" | jq -r '.UserPool.Id')

CLIENT_JSON=$(aws --endpoint-url "$ENDPOINT" cognito-idp create-user-pool-client \
  --user-pool-id "$POOL_ID" \
  --client-name "e2e-browser-cors-client" \
  --explicit-auth-flows ALLOW_USER_SRP_AUTH ALLOW_REFRESH_TOKEN_AUTH)
CLIENT_ID=$(echo "$CLIENT_JSON" | jq -r '.UserPoolClient.ClientId')

if [[ -z "$POOL_ID" || "$POOL_ID" == "null" || -z "$CLIENT_ID" || "$CLIENT_ID" == "null" ]]; then
  echo "ERROR: failed to create user pool / client"
  echo "$POOL_JSON"
  echo "$CLIENT_JSON"
  exit 1
fi

cd "$SCRIPT_DIR"
if [[ ! -d node_modules ]]; then
  npm ci
fi
npm run build

KUMOLO_ENDPOINT="$ENDPOINT" \
KUMOLO_POOL_ID="$POOL_ID" \
KUMOLO_CLIENT_ID="$CLIENT_ID" \
KUMOLO_LOG_FILE="$LOG_FILE" \
HARNESS_PORT="$HARNESS_PORT" \
  npx playwright test
