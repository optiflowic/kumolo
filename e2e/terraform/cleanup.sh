#!/usr/bin/env bash
# Pre-flight cleanup for make e2e-terraform.
# Removes kumolo resources that Terraform may have left behind on a failed run.
set -euo pipefail

ENDPOINT="${KUMOLO_ENDPOINT:-http://localhost:5566}"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_DEFAULT_REGION=us-east-1
AWS="aws --endpoint-url $ENDPOINT"

# Delete all versions and delete markers, then remove the bucket.
if $AWS s3api head-bucket --bucket kumolo-tf-verify > /dev/null 2>&1; then
  for query in 'Versions[].[Key,VersionId]' 'DeleteMarkers[].[Key,VersionId]'; do
    $AWS s3api list-object-versions --bucket kumolo-tf-verify \
        --output text --query "$query" 2>/dev/null | \
      while IFS=$'\t' read -r key vid; do
        [[ -z "$key" || "$key" == "None" ]] && continue
        $AWS s3api delete-object \
          --bucket kumolo-tf-verify --key "$key" --version-id "$vid" > /dev/null 2>&1 || true
      done
  done
  $AWS s3api delete-bucket --bucket kumolo-tf-verify > /dev/null 2>&1 || true
fi

# Remove DynamoDB tables.
$AWS dynamodb delete-table --table-name kumolo-tf-users        > /dev/null 2>&1 || true
$AWS dynamodb delete-table --table-name kumolo-tf-streams-test > /dev/null 2>&1 || true
