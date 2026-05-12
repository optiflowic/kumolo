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
$AWS delete-table --table-name "$TABLE" > /dev/null 2>&1

# ---------------------------------------------------------------------------
echo ""
echo "DynamoDB results: ${PASS} passed, ${FAIL} failed"
[[ $FAIL -eq 0 ]]
