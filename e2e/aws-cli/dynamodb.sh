#!/usr/bin/env bash
set -euo pipefail

ENDPOINT="${KUMOLO_ENDPOINT:-http://localhost:5566}"
TABLE="kumolo-cli-ddb-verify"

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

AWS="aws --endpoint-url $ENDPOINT dynamodb"
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

# ---------------------------------------------------------------------------
# Setup: remove table if it already exists from a previous run.
# ---------------------------------------------------------------------------
$AWS delete-table --table-name "$TABLE" > /dev/null 2>&1 || true

echo "=== DynamoDB ==="

# Table management
run "CreateTable" \
  $AWS create-table \
    --table-name "$TABLE" \
    --attribute-definitions \
      AttributeName=user_id,AttributeType=S \
      AttributeName=created_at,AttributeType=N \
      AttributeName=email,AttributeType=S \
    --key-schema \
      AttributeName=user_id,KeyType=HASH \
      AttributeName=created_at,KeyType=RANGE \
    --billing-mode PAY_PER_REQUEST \
    --global-secondary-indexes \
      '[{"IndexName":"email-index","KeySchema":[{"AttributeName":"email","KeyType":"HASH"}],"Projection":{"ProjectionType":"ALL"}}]'

run "DescribeTable"  $AWS describe-table  --table-name "$TABLE"
run "DescribeLimits" $AWS describe-limits
run "ListTables"     $AWS list-tables

# TTL
run "UpdateTimeToLive" \
  $AWS update-time-to-live \
    --table-name "$TABLE" \
    --time-to-live-specification Enabled=true,AttributeName=expires_at
run "DescribeTimeToLive" \
  $AWS describe-time-to-live --table-name "$TABLE"

# Tags
run "TagResource" \
  $AWS tag-resource \
    --resource-arn "arn:aws:dynamodb:us-east-1:000000000000:table/$TABLE" \
    --tags Key=env,Value=local Key=managed-by,Value=cli
run "ListTagsOfResource" \
  $AWS list-tags-of-resource \
    --resource-arn "arn:aws:dynamodb:us-east-1:000000000000:table/$TABLE"

# Item operations
run "PutItem (Alice)" \
  $AWS put-item \
    --table-name "$TABLE" \
    --item '{"user_id":{"S":"usr-001"},"created_at":{"N":"1700000001"},"email":{"S":"alice@example.com"},"name":{"S":"Alice"}}'

run "PutItem (Bob)" \
  $AWS put-item \
    --table-name "$TABLE" \
    --item '{"user_id":{"S":"usr-002"},"created_at":{"N":"1700000002"},"email":{"S":"bob@example.com"},"name":{"S":"Bob"}}'

run "PutItem (Carol)" \
  $AWS put-item \
    --table-name "$TABLE" \
    --item '{"user_id":{"S":"usr-001"},"created_at":{"N":"1700000100"},"email":{"S":"carol@example.com"},"name":{"S":"Carol"}}'

run "GetItem" \
  $AWS get-item \
    --table-name "$TABLE" \
    --key '{"user_id":{"S":"usr-001"},"created_at":{"N":"1700000001"}}'

run "UpdateItem (SET)" \
  $AWS update-item \
    --table-name "$TABLE" \
    --key '{"user_id":{"S":"usr-001"},"created_at":{"N":"1700000001"}}' \
    --update-expression "SET #n = :name" \
    --expression-attribute-names '{"#n":"name"}' \
    --expression-attribute-values '{":name":{"S":"Alice Updated"}}'

run "UpdateItem (ADD)" \
  $AWS update-item \
    --table-name "$TABLE" \
    --key '{"user_id":{"S":"usr-002"},"created_at":{"N":"1700000002"}}' \
    --update-expression "ADD login_count :one" \
    --expression-attribute-values '{":one":{"N":"1"}}'

# Condition expressions
run "PutItem (ConditionExpression: attribute_not_exists)" \
  $AWS put-item \
    --table-name "$TABLE" \
    --item '{"user_id":{"S":"usr-003"},"created_at":{"N":"1700000003"},"email":{"S":"dave@example.com"},"name":{"S":"Dave"}}' \
    --condition-expression "attribute_not_exists(user_id)"

# Projection and filter
run "GetItem (ProjectionExpression)" \
  $AWS get-item \
    --table-name "$TABLE" \
    --key '{"user_id":{"S":"usr-001"},"created_at":{"N":"1700000001"}}' \
    --projection-expression "#n,email" \
    --expression-attribute-names '{"#n":"name"}'

run "Query (KeyConditionExpression)" \
  $AWS query \
    --table-name "$TABLE" \
    --key-condition-expression "user_id = :uid" \
    --expression-attribute-values '{":uid":{"S":"usr-001"}}'

run "Query (ScanIndexForward=false)" \
  $AWS query \
    --table-name "$TABLE" \
    --key-condition-expression "user_id = :uid" \
    --expression-attribute-values '{":uid":{"S":"usr-001"}}' \
    --no-scan-index-forward

run "Query (GSI)" \
  $AWS query \
    --table-name "$TABLE" \
    --index-name email-index \
    --key-condition-expression "email = :email" \
    --expression-attribute-values '{":email":{"S":"alice@example.com"}}'

run "Scan" \
  $AWS scan --table-name "$TABLE"

run "Scan (FilterExpression)" \
  $AWS scan \
    --table-name "$TABLE" \
    --filter-expression "begins_with(email, :prefix)" \
    --expression-attribute-values '{":prefix":{"S":"alice"}}'

run "Scan (Limit + pagination)" \
  $AWS scan --table-name "$TABLE" --limit 1

# Batch operations
run "BatchWriteItem" \
  $AWS batch-write-item \
    --request-items "{\"$TABLE\":[
      {\"PutRequest\":{\"Item\":{\"user_id\":{\"S\":\"usr-004\"},\"created_at\":{\"N\":\"1700000004\"},\"email\":{\"S\":\"eve@example.com\"},\"name\":{\"S\":\"Eve\"}}}},
      {\"DeleteRequest\":{\"Key\":{\"user_id\":{\"S\":\"usr-003\"},\"created_at\":{\"N\":\"1700000003\"}}}}
    ]}"

run "BatchGetItem" \
  $AWS batch-get-item \
    --request-items "{\"$TABLE\":{\"Keys\":[
      {\"user_id\":{\"S\":\"usr-001\"},\"created_at\":{\"N\":\"1700000001\"}},
      {\"user_id\":{\"S\":\"usr-002\"},\"created_at\":{\"N\":\"1700000002\"}}
    ]}}"

# Transactions
run "TransactWriteItems" \
  $AWS transact-write-items \
    --transact-items "[
      {\"Put\":{\"TableName\":\"$TABLE\",\"Item\":{\"user_id\":{\"S\":\"usr-005\"},\"created_at\":{\"N\":\"1700000005\"},\"email\":{\"S\":\"frank@example.com\"},\"name\":{\"S\":\"Frank\"}}}},
      {\"Update\":{\"TableName\":\"$TABLE\",\"Key\":{\"user_id\":{\"S\":\"usr-001\"},\"created_at\":{\"N\":\"1700000001\"}},\"UpdateExpression\":\"SET verified = :t\",\"ExpressionAttributeValues\":{\":t\":{\"BOOL\":true}}}}
    ]"

run "TransactGetItems" \
  $AWS transact-get-items \
    --transact-items "[
      {\"Get\":{\"TableName\":\"$TABLE\",\"Key\":{\"user_id\":{\"S\":\"usr-001\"},\"created_at\":{\"N\":\"1700000001\"}}}},
      {\"Get\":{\"TableName\":\"$TABLE\",\"Key\":{\"user_id\":{\"S\":\"usr-005\"},\"created_at\":{\"N\":\"1700000005\"}}}}
    ]"

# Delete item
run "DeleteItem" \
  $AWS delete-item \
    --table-name "$TABLE" \
    --key '{"user_id":{"S":"usr-004"},"created_at":{"N":"1700000004"}}'

# UpdateTable
run "UpdateTable (add GSI)" \
  $AWS update-table \
    --table-name "$TABLE" \
    --attribute-definitions AttributeName=name,AttributeType=S \
    --global-secondary-index-updates \
      '[{"Create":{"IndexName":"name-index","KeySchema":[{"AttributeName":"name","KeyType":"HASH"}],"Projection":{"ProjectionType":"ALL"}}}]'

# Cleanup
$AWS delete-table --table-name "$TABLE" > /dev/null 2>&1 || true

# ---------------------------------------------------------------------------
# DynamoDB Streams
# ---------------------------------------------------------------------------
STREAM_TABLE="kumolo-cli-ddb-streams-verify"
DDBSTREAMS="aws --endpoint-url $ENDPOINT dynamodbstreams"

$AWS delete-table --table-name "$STREAM_TABLE" > /dev/null 2>&1 || true

echo ""
echo "=== DynamoDB Streams ==="

run "CreateTable (stream-enabled, NEW_AND_OLD_IMAGES)" \
  $AWS create-table \
    --table-name "$STREAM_TABLE" \
    --attribute-definitions \
      AttributeName=pk,AttributeType=S \
      AttributeName=sk,AttributeType=N \
    --key-schema \
      AttributeName=pk,KeyType=HASH \
      AttributeName=sk,KeyType=RANGE \
    --billing-mode PAY_PER_REQUEST \
    --stream-specification StreamEnabled=true,StreamViewType=NEW_AND_OLD_IMAGES

run "WaitTableExists (stream table)" \
  $AWS wait table-exists --table-name "$STREAM_TABLE"

# Stream metadata may appear slightly after the table becomes ACTIVE; poll until StreamArn is present.
STREAM_ARN=""
for _ in {1..10}; do
  STREAM_LIST=$($DDBSTREAMS list-streams --table-name "$STREAM_TABLE" 2>/dev/null || true)
  STREAM_ARN=$(echo "$STREAM_LIST" | jq -r '.Streams[0].StreamArn // empty' 2>/dev/null || true)
  [[ -n "$STREAM_ARN" ]] && break
  sleep 1
done

# Write items to generate INSERT events.
run "PutItem (INSERT event 1)" \
  $AWS put-item \
    --table-name "$STREAM_TABLE" \
    --item '{"pk":{"S":"item-001"},"sk":{"N":"1"},"value":{"S":"initial"}}'

run "PutItem (INSERT event 2)" \
  $AWS put-item \
    --table-name "$STREAM_TABLE" \
    --item '{"pk":{"S":"item-002"},"sk":{"N":"2"},"value":{"S":"hello"}}'

# Update to generate a MODIFY event.
run "UpdateItem (MODIFY event)" \
  $AWS update-item \
    --table-name "$STREAM_TABLE" \
    --key '{"pk":{"S":"item-001"},"sk":{"N":"1"}}' \
    --update-expression "SET #v = :v" \
    --expression-attribute-names '{"#v":"value"}' \
    --expression-attribute-values '{":v":{"S":"updated"}}'

# Delete to generate a REMOVE event.
run "DeleteItem (REMOVE event)" \
  $AWS delete-item \
    --table-name "$STREAM_TABLE" \
    --key '{"pk":{"S":"item-002"},"sk":{"N":"2"}}'

# ListStreams — capture stream ARN for subsequent calls.
STREAM_LIST=$($DDBSTREAMS list-streams --table-name "$STREAM_TABLE" 2>/dev/null || true)
STREAM_ARN=$(echo "$STREAM_LIST" | jq -r '.Streams[0].StreamArn // empty' 2>/dev/null || true)
if [[ -n "$STREAM_ARN" ]]; then
  ok "ListStreams (stream ARN present)"
else
  fail "ListStreams (no stream ARN returned)"
fi

run "ListStreams (no table filter)" $DDBSTREAMS list-streams

# DescribeStream — capture shard ID and verify metadata.
SHARD_ID=""
if [[ -n "$STREAM_ARN" ]]; then
  STREAM_DESC=$($DDBSTREAMS describe-stream --stream-arn "$STREAM_ARN" 2>/dev/null || true)
  SHARD_ID=$(echo "$STREAM_DESC" | jq -r '.StreamDescription.Shards[0].ShardId // empty' 2>/dev/null || true)
  STREAM_STATUS=$(echo "$STREAM_DESC" | jq -r '.StreamDescription.StreamStatus // empty' 2>/dev/null || true)
  STREAM_VIEW_TYPE=$(echo "$STREAM_DESC" | jq -r '.StreamDescription.StreamViewType // empty' 2>/dev/null || true)

  if [[ -n "$SHARD_ID" ]]; then
    ok "DescribeStream (ShardId present)"
  else
    fail "DescribeStream (no ShardId)"
  fi
  if [[ "$STREAM_STATUS" == "ENABLED" ]]; then
    ok "DescribeStream (StreamStatus=ENABLED)"
  else
    fail "DescribeStream (StreamStatus expected ENABLED, got '$STREAM_STATUS')"
  fi
  if [[ "$STREAM_VIEW_TYPE" == "NEW_AND_OLD_IMAGES" ]]; then
    ok "DescribeStream (StreamViewType=NEW_AND_OLD_IMAGES)"
  else
    fail "DescribeStream (StreamViewType expected NEW_AND_OLD_IMAGES, got '$STREAM_VIEW_TYPE')"
  fi
else
  fail "DescribeStream (skipped: no stream ARN)"
fi

# GetShardIterator (TRIM_HORIZON) — read from the beginning of the shard.
SHARD_ITER=""
if [[ -n "$STREAM_ARN" && -n "$SHARD_ID" ]]; then
  ITER_RESP=$($DDBSTREAMS get-shard-iterator \
    --stream-arn "$STREAM_ARN" \
    --shard-id "$SHARD_ID" \
    --shard-iterator-type TRIM_HORIZON 2>/dev/null || true)
  SHARD_ITER=$(echo "$ITER_RESP" | jq -r '.ShardIterator // empty' 2>/dev/null || true)
  if [[ -n "$SHARD_ITER" ]]; then
    ok "GetShardIterator (TRIM_HORIZON)"
  else
    fail "GetShardIterator (TRIM_HORIZON returned no iterator)"
  fi
else
  fail "GetShardIterator (skipped: no stream ARN or shard ID)"
fi

# GetRecords — verify event names and image fields.
if [[ -n "$SHARD_ITER" ]]; then
  RECORDS_RESP=""
  RECORD_COUNT=0
  for _ in {1..10}; do
    RECORDS_RESP=$($DDBSTREAMS get-records --shard-iterator "$SHARD_ITER" 2>/dev/null || true)
    RECORD_COUNT=$(echo "$RECORDS_RESP" | jq '.Records | length' 2>/dev/null || echo 0)
    [[ "$RECORD_COUNT" -ge 4 ]] && break
    sleep 1
  done

  if [[ "$RECORD_COUNT" -ge 4 ]]; then
    ok "GetRecords (record count: $RECORD_COUNT)"
  else
    fail "GetRecords (expected >=4 records, got $RECORD_COUNT)"
  fi

  INSERT_COUNT=$(echo "$RECORDS_RESP" | jq '[.Records[] | select(.eventName=="INSERT")] | length' 2>/dev/null || echo 0)
  MODIFY_COUNT=$(echo "$RECORDS_RESP" | jq '[.Records[] | select(.eventName=="MODIFY")] | length' 2>/dev/null || echo 0)
  REMOVE_COUNT=$(echo "$RECORDS_RESP" | jq '[.Records[] | select(.eventName=="REMOVE")] | length' 2>/dev/null || echo 0)

  if [[ "$INSERT_COUNT" -ge 2 ]]; then
    ok "GetRecords (INSERT events: $INSERT_COUNT)"
  else
    fail "GetRecords (expected >=2 INSERT events, got $INSERT_COUNT)"
  fi
  if [[ "$MODIFY_COUNT" -ge 1 ]]; then
    ok "GetRecords (MODIFY events: $MODIFY_COUNT)"
  else
    fail "GetRecords (expected >=1 MODIFY event, got $MODIFY_COUNT)"
  fi
  if [[ "$REMOVE_COUNT" -ge 1 ]]; then
    ok "GetRecords (REMOVE events: $REMOVE_COUNT)"
  else
    fail "GetRecords (expected >=1 REMOVE event, got $REMOVE_COUNT)"
  fi

  # Verify NewImage / OldImage per event type (NEW_AND_OLD_IMAGES view type).
  HAS_INSERT_NEW=$(echo "$RECORDS_RESP" | jq '[.Records[] | select(.eventName=="INSERT" and .dynamodb.NewImage!=null)] | length' 2>/dev/null || echo 0)
  HAS_MODIFY_NEW=$(echo "$RECORDS_RESP" | jq '[.Records[] | select(.eventName=="MODIFY" and .dynamodb.NewImage!=null)] | length' 2>/dev/null || echo 0)
  HAS_MODIFY_OLD=$(echo "$RECORDS_RESP" | jq '[.Records[] | select(.eventName=="MODIFY" and .dynamodb.OldImage!=null)] | length' 2>/dev/null || echo 0)
  HAS_REMOVE_OLD=$(echo "$RECORDS_RESP" | jq '[.Records[] | select(.eventName=="REMOVE" and .dynamodb.OldImage!=null)] | length' 2>/dev/null || echo 0)
  if [[ "$HAS_INSERT_NEW" -ge 1 ]]; then
    ok "GetRecords (INSERT record has NewImage)"
  else
    fail "GetRecords (INSERT record missing NewImage)"
  fi
  if [[ "$HAS_MODIFY_NEW" -ge 1 ]]; then
    ok "GetRecords (MODIFY record has NewImage)"
  else
    fail "GetRecords (MODIFY record missing NewImage)"
  fi
  if [[ "$HAS_MODIFY_OLD" -ge 1 ]]; then
    ok "GetRecords (MODIFY record has OldImage)"
  else
    fail "GetRecords (MODIFY record missing OldImage)"
  fi
  if [[ "$HAS_REMOVE_OLD" -ge 1 ]]; then
    ok "GetRecords (REMOVE record has OldImage)"
  else
    fail "GetRecords (REMOVE record missing OldImage)"
  fi
else
  fail "GetRecords (skipped: no shard iterator)"
fi

# GetShardIterator (AT_SEQUENCE_NUMBER / AFTER_SEQUENCE_NUMBER) — requires a known SeqNum.
if [[ -n "$STREAM_ARN" && -n "$SHARD_ID" && -n "$SHARD_ITER" ]]; then
  FIRST_SEQ=$(echo "${RECORDS_RESP:-}" | jq -r '.Records[0].dynamodb.SequenceNumber // empty' 2>/dev/null || true)
  if [[ -n "$FIRST_SEQ" ]]; then
    ITER_AT_RESP=$($DDBSTREAMS get-shard-iterator \
      --stream-arn "$STREAM_ARN" \
      --shard-id "$SHARD_ID" \
      --shard-iterator-type AT_SEQUENCE_NUMBER \
      --sequence-number "$FIRST_SEQ" 2>/dev/null || true)
    SHARD_ITER_AT=$(echo "$ITER_AT_RESP" | jq -r '.ShardIterator // empty' 2>/dev/null || true)
    if [[ -n "$SHARD_ITER_AT" ]]; then
      ok "GetShardIterator (AT_SEQUENCE_NUMBER)"
    else
      fail "GetShardIterator (AT_SEQUENCE_NUMBER returned no iterator)"
    fi

    ITER_AFTER_RESP=$($DDBSTREAMS get-shard-iterator \
      --stream-arn "$STREAM_ARN" \
      --shard-id "$SHARD_ID" \
      --shard-iterator-type AFTER_SEQUENCE_NUMBER \
      --sequence-number "$FIRST_SEQ" 2>/dev/null || true)
    SHARD_ITER_AFTER=$(echo "$ITER_AFTER_RESP" | jq -r '.ShardIterator // empty' 2>/dev/null || true)
    if [[ -n "$SHARD_ITER_AFTER" ]]; then
      ok "GetShardIterator (AFTER_SEQUENCE_NUMBER)"
    else
      fail "GetShardIterator (AFTER_SEQUENCE_NUMBER returned no iterator)"
    fi

    # AT_SEQUENCE_NUMBER should include the first record; AFTER_SEQUENCE_NUMBER should skip it.
    if [[ -n "$SHARD_ITER_AT" ]]; then
      AT_COUNT=$($DDBSTREAMS get-records --shard-iterator "$SHARD_ITER_AT" 2>/dev/null \
        | jq '.Records | length' 2>/dev/null || echo -1)
      if [[ "$AT_COUNT" -ge 1 ]]; then
        ok "GetRecords (AT_SEQUENCE_NUMBER includes target record)"
      else
        fail "GetRecords (AT_SEQUENCE_NUMBER expected >=1 record, got $AT_COUNT)"
      fi
    fi
    if [[ -n "$SHARD_ITER_AFTER" && -n "$RECORDS_RESP" ]]; then
      AFTER_COUNT=$($DDBSTREAMS get-records --shard-iterator "$SHARD_ITER_AFTER" 2>/dev/null \
        | jq '.Records | length' 2>/dev/null || echo -1)
      TOTAL_COUNT=$(echo "$RECORDS_RESP" | jq '.Records | length' 2>/dev/null || echo 0)
      EXPECTED_AFTER=$((TOTAL_COUNT - 1))
      if [[ "$AFTER_COUNT" -eq "$EXPECTED_AFTER" ]]; then
        ok "GetRecords (AFTER_SEQUENCE_NUMBER skips target record)"
      else
        fail "GetRecords (AFTER_SEQUENCE_NUMBER expected $EXPECTED_AFTER records, got $AFTER_COUNT)"
      fi
    fi
  else
    fail "GetShardIterator (AT/AFTER_SEQUENCE_NUMBER skipped: no SequenceNumber in records)"
  fi
fi

# GetShardIterator (LATEST) — should yield an empty page.
if [[ -n "$STREAM_ARN" && -n "$SHARD_ID" ]]; then
  ITER_LATEST_RESP=$($DDBSTREAMS get-shard-iterator \
    --stream-arn "$STREAM_ARN" \
    --shard-id "$SHARD_ID" \
    --shard-iterator-type LATEST 2>/dev/null || true)
  SHARD_ITER_LATEST=$(echo "$ITER_LATEST_RESP" | jq -r '.ShardIterator // empty' 2>/dev/null || true)
  if [[ -n "$SHARD_ITER_LATEST" ]]; then
    ok "GetShardIterator (LATEST)"
  else
    fail "GetShardIterator (LATEST returned no iterator)"
  fi

  if [[ -n "$SHARD_ITER_LATEST" ]]; then
    LATEST_COUNT=$($DDBSTREAMS get-records --shard-iterator "$SHARD_ITER_LATEST" 2>/dev/null \
      | jq '.Records | length' 2>/dev/null || echo -1)
    if [[ "$LATEST_COUNT" -eq 0 ]]; then
      ok "GetRecords (LATEST returns empty page)"
    else
      fail "GetRecords (LATEST expected 0 records, got $LATEST_COUNT)"
    fi
  fi
fi

# Cleanup stream table.
$AWS delete-table --table-name "$STREAM_TABLE" > /dev/null 2>&1 || true

# ---------------------------------------------------------------------------
echo ""
echo "DynamoDB results: ${PASS} passed, ${FAIL} failed"
[[ $FAIL -eq 0 ]]
