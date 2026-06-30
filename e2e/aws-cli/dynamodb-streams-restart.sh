#!/usr/bin/env bash
# Verifies that DynamoDB Streams records persist across a kumolo restart.
# Starts its own kumolo instance; does not require a pre-running server.
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

DDB="aws --endpoint-url $ENDPOINT dynamodb"
STREAMS="aws --endpoint-url $ENDPOINT dynamodbstreams"

PASS=0
FAIL=0
KUMOLO_PID=""

ok()   { echo "  PASS: $*"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $*"; FAIL=$((FAIL + 1)); }

cleanup() {
  [[ -n "$KUMOLO_PID" ]] && kill "$KUMOLO_PID" 2>/dev/null || true
  rm -rf "$DATA_DIR"
}
trap cleanup EXIT

start_kumolo() {
  KUMOLO_DATA_DIR="$DATA_DIR" KUMOLO_LOG_LEVEL=error "$KUMOLO_BIN" -port "$PORT" >/dev/null 2>&1 &
  KUMOLO_PID=$!
  local n=0
  until $DDB list-tables >/dev/null 2>&1; do
    sleep 0.25
    n=$((n + 1))
    if [[ $n -ge 40 ]]; then
      echo "  ERROR: kumolo did not start in time (port $PORT)"
      exit 1
    fi
  done
}

TABLE="kumolo-e2e-stream-restart"

echo ""
echo "=== DynamoDB Streams — persistence across restart ==="

# Phase 1: start kumolo and write records.
start_kumolo

if $DDB create-table \
    --table-name "$TABLE" \
    --attribute-definitions AttributeName=pk,AttributeType=S \
    --key-schema AttributeName=pk,KeyType=HASH \
    --billing-mode PAY_PER_REQUEST \
    --stream-specification StreamEnabled=true,StreamViewType=NEW_IMAGE >/dev/null 2>&1; then
  ok "CreateTable (stream-enabled)"
else
  fail "CreateTable (stream-enabled)"
fi

$DDB wait table-exists --table-name "$TABLE" 2>/dev/null || true

$DDB put-item --table-name "$TABLE" --item '{"pk":{"S":"restart-k1"}}' >/dev/null 2>&1
$DDB put-item --table-name "$TABLE" --item '{"pk":{"S":"restart-k2"}}' >/dev/null 2>&1
ok "PutItem x2 (before restart)"

# Capture stream ARN and shard ID before stopping.
STREAM_ARN=""
for _ in {1..10}; do
  STREAM_ARN=$($STREAMS list-streams --table-name "$TABLE" 2>/dev/null | jq -r '.Streams[0].StreamArn // empty')
  [[ -n "$STREAM_ARN" ]] && break
  sleep 0.5
done

if [[ -z "$STREAM_ARN" ]]; then
  fail "ListStreams (no stream ARN returned)"
  echo ""
  echo "DynamoDB Streams restart results: ${PASS} passed, ${FAIL} failed"
  exit 1
fi
ok "ListStreams (stream ARN obtained)"

SHARD_ID=$($STREAMS describe-stream --stream-arn "$STREAM_ARN" 2>/dev/null \
  | jq -r '.StreamDescription.Shards[0].ShardId // empty')
if [[ -n "$SHARD_ID" ]]; then
  ok "DescribeStream (ShardId obtained)"
else
  fail "DescribeStream (no ShardId)"
fi

# Phase 2: stop kumolo to simulate a process restart.
kill "$KUMOLO_PID"
wait "$KUMOLO_PID" 2>/dev/null || true
KUMOLO_PID=""
ok "kumolo stopped"

# Phase 3: restart kumolo with the same data directory.
start_kumolo
ok "kumolo restarted"

# Phase 4: read from stream and verify records survived the restart.
ITER=""
if [[ -n "$SHARD_ID" ]]; then
  ITER=$($STREAMS get-shard-iterator \
      --stream-arn "$STREAM_ARN" \
      --shard-id "$SHARD_ID" \
      --shard-iterator-type TRIM_HORIZON 2>/dev/null | jq -r '.ShardIterator // empty')
fi

if [[ -n "$ITER" ]]; then
  ok "GetShardIterator (TRIM_HORIZON after restart)"
else
  fail "GetShardIterator (TRIM_HORIZON returned no iterator)"
fi

REC_COUNT=0
if [[ -n "$ITER" ]]; then
  for _ in {1..10}; do
    RECORDS=$($STREAMS get-records --shard-iterator "$ITER" 2>/dev/null || true)
    REC_COUNT=$(echo "$RECORDS" | jq '.Records | length' 2>/dev/null || echo 0)
    [[ "$REC_COUNT" -ge 2 ]] && break
    sleep 0.5
  done
fi

if [[ "$REC_COUNT" -ge 2 ]]; then
  ok "GetRecords (records persisted across restart: $REC_COUNT)"
else
  fail "GetRecords (expected >=2 records after restart, got $REC_COUNT)"
fi

echo ""
echo "DynamoDB Streams restart results: ${PASS} passed, ${FAIL} failed"
[[ $FAIL -eq 0 ]]
