#!/usr/bin/env bash
set -euo pipefail

ENDPOINT="${KUMOLO_ENDPOINT:-http://localhost:5566}"
BUCKET="kumolo-cli-s3-verify"
TMPFILE="$(mktemp)"
trap 'rm -f "$TMPFILE"' EXIT

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1
# Use path-style URLs (required for kumolo).
export AWS_S3_ADDRESSING_STYLE=path

AWS="aws --endpoint-url $ENDPOINT"
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

# Abort in-progress multipart uploads, delete all object versions and delete
# markers, then remove the bucket.  Works on both versioned and plain buckets.
cleanup_bucket() {
  local b="$1"
  # Nothing to do if the bucket does not exist.
  $AWS s3api head-bucket --bucket "$b" > /dev/null 2>&1 || return 0
  $AWS s3api list-multipart-uploads --bucket "$b" \
      --output text --query 'Uploads[].[Key,UploadId]' 2>/dev/null | \
    while IFS=$'\t' read -r key uid _rest; do
      [[ -z "$key" || "$key" == "None" ]] && continue
      $AWS s3api abort-multipart-upload \
        --bucket "$b" --key "$key" --upload-id "$uid" > /dev/null 2>&1 || true
    done
  $AWS s3api list-object-versions --bucket "$b" \
      --output text --query 'Versions[].[Key,VersionId]' 2>/dev/null | \
    while IFS=$'\t' read -r key vid _rest; do
      [[ -z "$key" || "$key" == "None" ]] && continue
      if [[ -z "$vid" || "$vid" == "null" || "$vid" == "None" ]]; then
        $AWS s3api delete-object --bucket "$b" --key "$key" > /dev/null 2>&1 || true
      else
        $AWS s3api delete-object --bucket "$b" --key "$key" --version-id "$vid" > /dev/null 2>&1 || true
      fi
    done
  $AWS s3api list-object-versions --bucket "$b" \
      --output text --query 'DeleteMarkers[].[Key,VersionId]' 2>/dev/null | \
    while IFS=$'\t' read -r key vid _rest; do
      [[ -z "$key" || "$key" == "None" ]] && continue
      if [[ -z "$vid" || "$vid" == "null" || "$vid" == "None" ]]; then
        $AWS s3api delete-object --bucket "$b" --key "$key" > /dev/null 2>&1 || true
      else
        $AWS s3api delete-object --bucket "$b" --key "$key" --version-id "$vid" > /dev/null 2>&1 || true
      fi
    done
  $AWS s3api delete-bucket --bucket "$b" > /dev/null 2>&1 || true
}

# ---------------------------------------------------------------------------
# Setup: remove bucket if it already exists from a previous run.
# ---------------------------------------------------------------------------
cleanup_bucket "$BUCKET"

echo "=== S3 ==="

# Bucket operations
run "CreateBucket"     $AWS s3api create-bucket --bucket "$BUCKET"
run "HeadBucket"       $AWS s3api head-bucket   --bucket "$BUCKET"
run "ListBuckets"      $AWS s3api list-buckets

# Versioning
run "PutBucketVersioning (Enable)" \
  $AWS s3api put-bucket-versioning \
    --bucket "$BUCKET" \
    --versioning-configuration Status=Enabled
run "GetBucketVersioning" \
  $AWS s3api get-bucket-versioning --bucket "$BUCKET"

# Object operations
echo "hello, kumolo" > "$TMPFILE"
run "PutObject"        $AWS s3api put-object --bucket "$BUCKET" --key "hello.txt" --body "$TMPFILE"
run "HeadObject"       $AWS s3api head-object --bucket "$BUCKET" --key "hello.txt"
run "GetObject"        $AWS s3api get-object  --bucket "$BUCKET" --key "hello.txt" /dev/null
run "ListObjectsV2"    $AWS s3api list-objects-v2 --bucket "$BUCKET"

# Second version
echo "hello again" > "$TMPFILE"
run "PutObject (v2)"   $AWS s3api put-object --bucket "$BUCKET" --key "hello.txt" --body "$TMPFILE"
run "ListObjectVersions" \
  $AWS s3api list-object-versions --bucket "$BUCKET"

# User metadata and tagging
run "PutObject (with metadata)" \
  $AWS s3api put-object \
    --bucket "$BUCKET" \
    --key "meta.txt" \
    --body "$TMPFILE" \
    --metadata '{"author":"kumolo","env":"local"}'
run "PutObjectTagging" \
  $AWS s3api put-object-tagging \
    --bucket "$BUCKET" \
    --key "meta.txt" \
    --tagging '{"TagSet":[{"Key":"purpose","Value":"verify"}]}'
run "GetObjectTagging" \
  $AWS s3api get-object-tagging --bucket "$BUCKET" --key "meta.txt"

# Copy
run "CopyObject" \
  $AWS s3api copy-object \
    --bucket "$BUCKET" \
    --key "hello-copy.txt" \
    --copy-source "$BUCKET/hello.txt"

# Multipart upload
UPLOAD_ID=$($AWS s3api create-multipart-upload --bucket "$BUCKET" --key "multipart.bin" \
  --query UploadId --output text 2>/dev/null)
if [[ -n "$UPLOAD_ID" ]]; then
  ok "CreateMultipartUpload"
  # Each part must be >= 5 MB (except the last). Use a 5 MB part.
  dd if=/dev/urandom bs=1048576 count=5 2>/dev/null > "$TMPFILE"
  ETAG=$($AWS s3api upload-part \
    --bucket "$BUCKET" --key "multipart.bin" \
    --upload-id "$UPLOAD_ID" --part-number 1 \
    --body "$TMPFILE" \
    --query ETag --output text 2>/dev/null)
  if [[ -n "$ETAG" ]]; then
    ok "UploadPart"
    # --output text returns the S3 ETag with surrounding double-quote chars
    # (e.g. "abc123").  Strip them, then re-embed as JSON-escaped \" so the
    # server receives the correct S3 ETag value including the surrounding quotes.
    # In bash printf, \\" in a single-quoted format string → \" in output.
    ETAG_CLEAN=$(echo "$ETAG" | tr -d '"')
    PARTS=$(printf '{"Parts":[{"PartNumber":1,"ETag":"\\"%s\\""}]}' "$ETAG_CLEAN")
    run "CompleteMultipartUpload" \
      $AWS s3api complete-multipart-upload \
        --bucket "$BUCKET" --key "multipart.bin" \
        --upload-id "$UPLOAD_ID" \
        --multipart-upload "$PARTS"
  else
    fail "UploadPart"
    $AWS s3api abort-multipart-upload \
      --bucket "$BUCKET" --key "multipart.bin" \
      --upload-id "$UPLOAD_ID" > /dev/null 2>&1 || true
  fi
else
  fail "CreateMultipartUpload"
fi

# Range GET
run "GetObject (Range)" \
  $AWS s3api get-object \
    --bucket "$BUCKET" \
    --key "hello.txt" \
    --range "bytes=0-4" \
    /dev/null

# Object attributes
run "GetObjectAttributes" \
  $AWS s3api get-object-attributes \
    --bucket "$BUCKET" \
    --key "hello.txt" \
    --object-attributes ETag ObjectSize

# Conditional GET (If-None-Match)
ETAG=$($AWS s3api head-object --bucket "$BUCKET" --key "hello.txt" \
  --query ETag --output text 2>/dev/null | tr -d '"')
IFNM_OUT=$($AWS s3api get-object \
  --bucket "$BUCKET" --key "hello.txt" \
  --if-none-match "\"$ETAG\"" /dev/null 2>&1 || true)
if echo "$IFNM_OUT" | grep -q '304\|NotModified'; then
  ok "GetObject (If-None-Match 304)"
else
  fail "GetObject (If-None-Match 304) — unexpected response: $(echo "$IFNM_OUT" | head -1)"
fi

# DeleteObjects (batch)
run "DeleteObjects" \
  $AWS s3api delete-objects \
    --bucket "$BUCKET" \
    --delete '{"Objects":[{"Key":"hello-copy.txt"},{"Key":"meta.txt"}]}'

# Bucket config operations
run "PutBucketTagging" \
  $AWS s3api put-bucket-tagging \
    --bucket "$BUCKET" \
    --tagging '{"TagSet":[{"Key":"env","Value":"local"}]}'
run "GetBucketTagging"   $AWS s3api get-bucket-tagging   --bucket "$BUCKET"
run "DeleteBucketTagging" $AWS s3api delete-bucket-tagging --bucket "$BUCKET"

run "PutBucketLifecycleConfiguration" \
  $AWS s3api put-bucket-lifecycle-configuration \
    --bucket "$BUCKET" \
    --lifecycle-configuration '{"Rules":[{"ID":"expire","Status":"Enabled","Filter":{},"Expiration":{"Days":90}}]}'
run "GetBucketLifecycleConfiguration" \
  $AWS s3api get-bucket-lifecycle-configuration --bucket "$BUCKET"
run "DeleteBucketLifecycle" \
  $AWS s3api delete-bucket-lifecycle --bucket "$BUCKET"

run "PutBucketEncryption" \
  $AWS s3api put-bucket-encryption \
    --bucket "$BUCKET" \
    --server-side-encryption-configuration \
      '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'
run "GetBucketEncryption"   $AWS s3api get-bucket-encryption   --bucket "$BUCKET"
run "DeleteBucketEncryption" $AWS s3api delete-bucket-encryption --bucket "$BUCKET"

run "PutBucketCors" \
  $AWS s3api put-bucket-cors \
    --bucket "$BUCKET" \
    --cors-configuration \
      '{"CORSRules":[{"AllowedOrigins":["*"],"AllowedMethods":["GET","PUT"]}]}'
run "GetBucketCors"    $AWS s3api get-bucket-cors    --bucket "$BUCKET"
run "DeleteBucketCors" $AWS s3api delete-bucket-cors --bucket "$BUCKET"

# ---------------------------------------------------------------------------
# BucketLogging — configure log delivery and verify a log object is written.
# ---------------------------------------------------------------------------
LOG_BUCKET="kumolo-cli-s3-logging"
cleanup_bucket "$LOG_BUCKET"
run "CreateBucket (log target)" $AWS s3api create-bucket --bucket "$LOG_BUCKET"

LOG_CONFIG='{"LoggingEnabled":{"TargetBucket":"kumolo-cli-s3-logging","TargetPrefix":"logs/"}}'
run "PutBucketLogging" \
  $AWS s3api put-bucket-logging \
    --bucket "$BUCKET" \
    --bucket-logging-status "$LOG_CONFIG"

LOG_RESP=$($AWS s3api get-bucket-logging --bucket "$BUCKET" 2>/dev/null || true)
LOG_TARGET=$(echo "$LOG_RESP" | jq -r '.LoggingEnabled.TargetBucket // empty' 2>/dev/null || true)
if [[ "$LOG_TARGET" == "$LOG_BUCKET" ]]; then
  ok "GetBucketLogging (TargetBucket present)"
else
  fail "GetBucketLogging (expected TargetBucket=$LOG_BUCKET, got '$LOG_TARGET')"
fi

# Trigger a request; logging is synchronous so the log object is present
# in the target bucket before this script proceeds.
echo "log-trigger" > "$TMPFILE"
$AWS s3api put-object \
  --bucket "$BUCKET" --key "log-trigger.txt" --body "$TMPFILE" > /dev/null 2>&1 || true

LOG_OBJ_RESP=$($AWS s3api list-objects-v2 --bucket "$LOG_BUCKET" --prefix "logs/" 2>/dev/null || true)
LOG_OBJ_COUNT=$(echo "$LOG_OBJ_RESP" | jq '.Contents | length' 2>/dev/null || echo 0)
if [[ "$LOG_OBJ_COUNT" -ge 1 ]]; then
  ok "BucketLogging (log object written to target bucket)"
else
  fail "BucketLogging (expected >=1 log object under logs/ in $LOG_BUCKET, got $LOG_OBJ_COUNT)"
fi

cleanup_bucket "$LOG_BUCKET"

# ---------------------------------------------------------------------------
# BucketReplication — configure replication and verify object is copied.
# Source bucket already has versioning enabled; enable it on destination too.
# ---------------------------------------------------------------------------
DEST_BUCKET="kumolo-cli-s3-replication-dest"
cleanup_bucket "$DEST_BUCKET"
run "CreateBucket (replication dest)" $AWS s3api create-bucket --bucket "$DEST_BUCKET"
run "PutBucketVersioning (replication dest)" \
  $AWS s3api put-bucket-versioning \
    --bucket "$DEST_BUCKET" \
    --versioning-configuration Status=Enabled

REPL_CONFIG=$(cat <<'JSON'
{
  "Role": "arn:aws:iam::000000000000:role/replication-role",
  "Rules": [{
    "ID": "replicate-all",
    "Status": "Enabled",
    "Filter": {},
    "Destination": {"Bucket": "arn:aws:s3:::kumolo-cli-s3-replication-dest"}
  }]
}
JSON
)
run "PutBucketReplication" \
  $AWS s3api put-bucket-replication \
    --bucket "$BUCKET" \
    --replication-configuration "$REPL_CONFIG"

REPL_RESP=$($AWS s3api get-bucket-replication --bucket "$BUCKET" 2>/dev/null || true)
REPL_RULE_COUNT=$(echo "$REPL_RESP" | jq '.ReplicationConfiguration.Rules | length' 2>/dev/null || echo 0)
if [[ "$REPL_RULE_COUNT" -ge 1 ]]; then
  ok "GetBucketReplication (rules present)"
else
  fail "GetBucketReplication (expected >=1 rule, got $REPL_RULE_COUNT)"
fi

# Replication is synchronous; the destination object exists immediately.
echo "replicated-content" > "$TMPFILE"
$AWS s3api put-object \
  --bucket "$BUCKET" --key "replicated.txt" --body "$TMPFILE" > /dev/null 2>&1 || true

REPL_HEAD=$($AWS s3api head-object --bucket "$DEST_BUCKET" --key "replicated.txt" 2>/dev/null || true)
REPL_STATUS=$(echo "$REPL_HEAD" | jq -r '.ReplicationStatus // empty' 2>/dev/null || true)
if [[ "$REPL_STATUS" == "REPLICA" ]]; then
  ok "BucketReplication (object replicated; ReplicationStatus=REPLICA in destination)"
else
  fail "BucketReplication (expected ReplicationStatus=REPLICA, got '$REPL_STATUS')"
fi

cleanup_bucket "$DEST_BUCKET"

# ---------------------------------------------------------------------------
# SSE-KMS — PutObject with aws:kms; verify header echoed on HeadObject.
# ---------------------------------------------------------------------------
echo "sse-kms-data" > "$TMPFILE"
run "PutObject (SSE-KMS)" \
  $AWS s3api put-object \
    --bucket "$BUCKET" \
    --key "sse-kms.txt" \
    --body "$TMPFILE" \
    --server-side-encryption aws:kms

HEAD_SSE_KMS=$($AWS s3api head-object --bucket "$BUCKET" --key "sse-kms.txt" 2>/dev/null || true)
SSE_VALUE=$(echo "$HEAD_SSE_KMS" | jq -r '.ServerSideEncryption // empty' 2>/dev/null || true)
if [[ "$SSE_VALUE" == "aws:kms" ]]; then
  ok "HeadObject (SSE-KMS echoed: ServerSideEncryption=aws:kms)"
else
  fail "HeadObject (SSE-KMS: expected ServerSideEncryption=aws:kms, got '$SSE_VALUE')"
fi

run "GetObject (SSE-KMS)" \
  $AWS s3api get-object --bucket "$BUCKET" --key "sse-kms.txt" /dev/null

# ---------------------------------------------------------------------------
# SSE-C — PutObject with customer-provided key; verify round-trip.
# The AWS CLI expects raw key bytes via fileb://; it base64-encodes and
# computes the MD5 internally.
# ---------------------------------------------------------------------------
SSEC_TMPKEY="$(mktemp)"
dd if=/dev/urandom bs=32 count=1 2>/dev/null > "$SSEC_TMPKEY"

echo "sse-c-data" > "$TMPFILE"
run "PutObject (SSE-C)" \
  $AWS s3api put-object \
    --bucket "$BUCKET" \
    --key "sse-c.txt" \
    --body "$TMPFILE" \
    --sse-customer-algorithm AES256 \
    --sse-customer-key "fileb://$SSEC_TMPKEY"

run "GetObject (SSE-C round-trip)" \
  $AWS s3api get-object \
    --bucket "$BUCKET" \
    --key "sse-c.txt" \
    --sse-customer-algorithm AES256 \
    --sse-customer-key "fileb://$SSEC_TMPKEY" \
    /dev/null

run "HeadObject (SSE-C)" \
  $AWS s3api head-object \
    --bucket "$BUCKET" \
    --key "sse-c.txt" \
    --sse-customer-algorithm AES256 \
    --sse-customer-key "fileb://$SSEC_TMPKEY"

rm -f "$SSEC_TMPKEY"

# ---------------------------------------------------------------------------
# SelectObjectContent — CSV and JSON input/output; verify filtered results.
# ---------------------------------------------------------------------------
CSV_KEY="select-test.csv"
printf 'name,age\nAlice,30\nBob,25\nCarol,35\n' > "$TMPFILE"
$AWS s3api put-object \
  --bucket "$BUCKET" --key "$CSV_KEY" --body "$TMPFILE" > /dev/null 2>&1 || true

CSV_SELECT_OUT="$(mktemp)"
if $AWS s3api select-object-content \
  --bucket "$BUCKET" \
  --key "$CSV_KEY" \
  --expression "SELECT * FROM S3Object WHERE CAST(age AS INT) > 27" \
  --expression-type SQL \
  --input-serialization '{"CSV":{"FileHeaderInfo":"USE"}}' \
  --output-serialization '{"CSV":{}}' \
  "$CSV_SELECT_OUT" > /dev/null 2>&1; then
  if grep -q "Alice" "$CSV_SELECT_OUT" || grep -q "Carol" "$CSV_SELECT_OUT"; then
    ok "SelectObjectContent (CSV input/output: filtered rows present)"
  else
    fail "SelectObjectContent (CSV: expected Alice or Carol in output, got: $(head -3 "$CSV_SELECT_OUT"))"
  fi
else
  fail "SelectObjectContent (CSV: command failed)"
fi
rm -f "$CSV_SELECT_OUT"

JSON_KEY="select-test.json"
printf '{"name":"Alice","score":90}\n{"name":"Bob","score":60}\n{"name":"Carol","score":85}\n' > "$TMPFILE"
$AWS s3api put-object \
  --bucket "$BUCKET" --key "$JSON_KEY" --body "$TMPFILE" > /dev/null 2>&1 || true

JSON_SELECT_OUT="$(mktemp)"
if $AWS s3api select-object-content \
  --bucket "$BUCKET" \
  --key "$JSON_KEY" \
  --expression "SELECT * FROM S3Object s WHERE s.score > 70" \
  --expression-type SQL \
  --input-serialization '{"JSON":{"Type":"LINES"}}' \
  --output-serialization '{"JSON":{}}' \
  "$JSON_SELECT_OUT" > /dev/null 2>&1; then
  if grep -q "Alice" "$JSON_SELECT_OUT" || grep -q "Carol" "$JSON_SELECT_OUT"; then
    ok "SelectObjectContent (JSON input/output: filtered rows present)"
  else
    fail "SelectObjectContent (JSON: expected Alice or Carol in output, got: $(head -3 "$JSON_SELECT_OUT"))"
  fi
else
  fail "SelectObjectContent (JSON: command failed)"
fi
rm -f "$JSON_SELECT_OUT"

# ---------------------------------------------------------------------------
# ACL enforcement — verify anonymous access is controlled by object ACL.
# ---------------------------------------------------------------------------
echo "public-content" > "$TMPFILE"
$AWS s3api put-object \
  --bucket "$BUCKET" --key "acl-test.txt" --body "$TMPFILE" > /dev/null 2>&1 || true

run "PutObjectAcl (public-read)" \
  $AWS s3api put-object-acl --bucket "$BUCKET" --key "acl-test.txt" --acl public-read

# Anonymous GET via plain HTTP (no auth headers) should succeed.
ACL_PUBLIC_STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  "${ENDPOINT}/${BUCKET}/acl-test.txt")
if [[ "$ACL_PUBLIC_STATUS" == "200" ]]; then
  ok "ACL enforcement (public-read: anonymous GET returns 200)"
else
  fail "ACL enforcement (public-read: expected 200, got $ACL_PUBLIC_STATUS)"
fi

run "PutObjectAcl (private)" \
  $AWS s3api put-object-acl --bucket "$BUCKET" --key "acl-test.txt" --acl private

# Anonymous GET should now be denied.
ACL_PRIVATE_STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  "${ENDPOINT}/${BUCKET}/acl-test.txt")
if [[ "$ACL_PRIVATE_STATUS" == "403" ]]; then
  ok "ACL enforcement (private: anonymous GET returns 403)"
else
  fail "ACL enforcement (private: expected 403, got $ACL_PRIVATE_STATUS)"
fi

# Cleanup
cleanup_bucket "$BUCKET"

# ---------------------------------------------------------------------------
echo ""
echo "S3 results: ${PASS} passed, ${FAIL} failed"
[[ $FAIL -eq 0 ]]
