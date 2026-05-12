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
      $AWS s3api delete-object --bucket "$b" --key "$key" --version-id "$vid" > /dev/null 2>&1 || true
    done
  $AWS s3api list-object-versions --bucket "$b" \
      --output text --query 'DeleteMarkers[].[Key,VersionId]' 2>/dev/null | \
    while IFS=$'\t' read -r key vid _rest; do
      [[ -z "$key" || "$key" == "None" ]] && continue
      $AWS s3api delete-object --bucket "$b" --key "$key" --version-id "$vid" > /dev/null 2>&1 || true
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
run "GetObject (If-None-Match 304)" \
  bash -c "$AWS s3api get-object \
    --bucket $BUCKET --key hello.txt \
    --if-none-match '\"$ETAG\"' /dev/null 2>&1 | grep -q '304\|NotModified'" \
  || true  # 304 causes non-zero exit; check via message instead

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

# Cleanup
cleanup_bucket "$BUCKET"

# ---------------------------------------------------------------------------
echo ""
echo "S3 results: ${PASS} passed, ${FAIL} failed"
[[ $FAIL -eq 0 ]]
