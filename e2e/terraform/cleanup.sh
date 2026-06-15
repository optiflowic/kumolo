#!/usr/bin/env bash
# Pre-flight cleanup for make e2e-terraform.
# Removes kumolo resources that Terraform may have left behind on a failed run.
set -euo pipefail

ENDPOINT="${KUMOLO_ENDPOINT:-http://localhost:5566}"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_DEFAULT_REGION=us-east-1
AWS="aws --endpoint-url $ENDPOINT"

# Helper: delete all objects/versions/delete-markers then the bucket itself.
# Handles both versioned and non-versioned buckets.
cleanup_s3_bucket() {
  local b="$1"
  $AWS s3api head-bucket --bucket "$b" > /dev/null 2>&1 || return 0
  # Delete plain objects (non-versioned buckets).
  $AWS s3api list-objects-v2 --bucket "$b" \
      --output text --query 'Contents[].[Key]' 2>/dev/null | \
    while IFS=$'\t' read -r key; do
      [[ -z "$key" || "$key" == "None" ]] && continue
      $AWS s3api delete-object \
        --bucket "$b" --key "$key" > /dev/null 2>&1 || true
    done || true
  # Delete versioned objects and delete markers.
  for query in 'Versions[].[Key,VersionId]' 'DeleteMarkers[].[Key,VersionId]'; do
    $AWS s3api list-object-versions --bucket "$b" \
        --output text --query "$query" 2>/dev/null | \
      while IFS=$'\t' read -r key vid; do
        [[ -z "$key" || "$key" == "None" ]] && continue
        $AWS s3api delete-object \
          --bucket "$b" --key "$key" --version-id "$vid" \
          --bypass-governance-retention > /dev/null 2>&1 || true
      done || true
  done
  $AWS s3api delete-bucket --bucket "$b" > /dev/null 2>&1 || true
}

cleanup_s3_bucket kumolo-tf-verify
cleanup_s3_bucket kumolo-tf-verify-replica
cleanup_s3_bucket kumolo-tf-verify-kms
cleanup_s3_bucket kumolo-tf-verify-objectlock

# Remove DynamoDB tables.
$AWS dynamodb delete-table --table-name kumolo-tf-users        > /dev/null 2>&1 || true
$AWS dynamodb delete-table --table-name kumolo-tf-streams-test > /dev/null 2>&1 || true

# Remove KMS alias (key itself does not need cleanup; Terraform manages it via state).
$AWS kms delete-alias --alias-name "alias/kumolo-tf-verify" > /dev/null 2>&1 || true
