#!/usr/bin/env bash
set -euo pipefail

ENDPOINT="${KUMOLO_ENDPOINT:-http://localhost:5566}"
BUCKET="kumolo-cli-s3-verify"
TMPFILE="$(mktemp)"
SSEC_TMPKEY=""
trap 'rm -f "$TMPFILE" "${SSEC_TMPKEY:-}"' EXIT

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
run "GetBucketLocation" \
  $AWS s3api get-bucket-location --bucket "$BUCKET"

# Object operations
echo "hello, kumolo" > "$TMPFILE"
run "PutObject"        $AWS s3api put-object --bucket "$BUCKET" --key "hello.txt" --body "$TMPFILE"
run "HeadObject"       $AWS s3api head-object --bucket "$BUCKET" --key "hello.txt"
run "GetObject"        $AWS s3api get-object  --bucket "$BUCKET" --key "hello.txt" /dev/null
run "ListObjectsV2"    $AWS s3api list-objects-v2 --bucket "$BUCKET"
run "ListObjects (v1)" $AWS s3api list-objects    --bucket "$BUCKET"

# ---------------------------------------------------------------------------
# encoding-type=url — keys with spaces must be percent-encoded in response.
# ---------------------------------------------------------------------------
SPECIAL_KEY="folder/hello world.txt"
echo "encoding test" > "$TMPFILE"
$AWS s3api put-object --bucket "$BUCKET" --key "$SPECIAL_KEY" --body "$TMPFILE" > /dev/null 2>&1 || true

LIST_V1_OUT=$($AWS s3api list-objects --bucket "$BUCKET" \
  --prefix "folder/" --encoding-type url 2>/dev/null || true)
LIST_V1_KEY=$(echo "$LIST_V1_OUT" | jq -r '.Contents[0].Key // empty' 2>/dev/null || true)
if echo "$LIST_V1_KEY" | grep -q '%20'; then
  ok "ListObjects (encoding-type=url: space encoded as %20)"
else
  fail "ListObjects (encoding-type=url: expected %20 in key, got '$LIST_V1_KEY')"
fi

LIST_V2_OUT=$($AWS s3api list-objects-v2 --bucket "$BUCKET" \
  --prefix "folder/" --encoding-type url 2>/dev/null || true)
LIST_V2_KEY=$(echo "$LIST_V2_OUT" | jq -r '.Contents[0].Key // empty' 2>/dev/null || true)
if echo "$LIST_V2_KEY" | grep -q '%20'; then
  ok "ListObjectsV2 (encoding-type=url: space encoded as %20)"
else
  fail "ListObjectsV2 (encoding-type=url: expected %20 in key, got '$LIST_V2_KEY')"
fi

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
run "DeleteObjectTagging" \
  $AWS s3api delete-object-tagging --bucket "$BUCKET" --key "meta.txt"

# Copy
run "CopyObject" \
  $AWS s3api copy-object \
    --bucket "$BUCKET" \
    --key "hello-copy.txt" \
    --copy-source "$BUCKET/hello.txt"

# ---------------------------------------------------------------------------
# CopyObject: x-amz-tagging-directive
# ---------------------------------------------------------------------------
$AWS s3api put-object-tagging \
  --bucket "$BUCKET" --key "hello.txt" \
  --tagging '{"TagSet":[{"Key":"src","Value":"original"}]}' > /dev/null 2>&1 || true

run "CopyObject (tagging-directive=COPY)" \
  $AWS s3api copy-object \
    --bucket "$BUCKET" \
    --key "copy-tagging-copy.txt" \
    --copy-source "$BUCKET/hello.txt" \
    --tagging-directive COPY

COPY_TAGS=$($AWS s3api get-object-tagging \
  --bucket "$BUCKET" --key "copy-tagging-copy.txt" 2>/dev/null || true)
COPY_TAG_VAL=$(echo "$COPY_TAGS" | jq -r '.TagSet[] | select(.Key=="src") | .Value' 2>/dev/null || true)
if [[ "$COPY_TAG_VAL" == "original" ]]; then
  ok "CopyObject (tagging-directive=COPY: source tag preserved)"
else
  fail "CopyObject (tagging-directive=COPY: expected src=original, got '$COPY_TAG_VAL')"
fi

run "CopyObject (tagging-directive=REPLACE)" \
  $AWS s3api copy-object \
    --bucket "$BUCKET" \
    --key "copy-tagging-replace.txt" \
    --copy-source "$BUCKET/hello.txt" \
    --tagging-directive REPLACE \
    --tagging "purpose=replaced"

REPLACE_TAGS=$($AWS s3api get-object-tagging \
  --bucket "$BUCKET" --key "copy-tagging-replace.txt" 2>/dev/null || true)
REPLACE_TAG_VAL=$(echo "$REPLACE_TAGS" | jq -r '.TagSet[] | select(.Key=="purpose") | .Value' 2>/dev/null || true)
SRC_TAG_GONE=$(echo "$REPLACE_TAGS" | jq -r '.TagSet[] | select(.Key=="src") | .Value' 2>/dev/null || true)
if [[ "$REPLACE_TAG_VAL" == "replaced" && -z "$SRC_TAG_GONE" ]]; then
  ok "CopyObject (tagging-directive=REPLACE: new tag set, old tag absent)"
else
  fail "CopyObject (tagging-directive=REPLACE: purpose='$REPLACE_TAG_VAL' src='$SRC_TAG_GONE')"
fi

# ---------------------------------------------------------------------------
# CopyObject: x-amz-copy-source-if-* conditional headers
# ---------------------------------------------------------------------------
SRC_ETAG=$($AWS s3api head-object --bucket "$BUCKET" --key "hello.txt" \
  --query ETag --output text 2>/dev/null | tr -d '"')

run "CopyObject (copy-source-if-match: matching ETag)" \
  $AWS s3api copy-object \
    --bucket "$BUCKET" \
    --key "copy-if-match.txt" \
    --copy-source "$BUCKET/hello.txt" \
    --copy-source-if-match "\"$SRC_ETAG\""

IFNM_ERR=$($AWS s3api copy-object \
  --bucket "$BUCKET" \
  --key "copy-if-none-match.txt" \
  --copy-source "$BUCKET/hello.txt" \
  --copy-source-if-none-match "\"$SRC_ETAG\"" 2>&1 || true)
if echo "$IFNM_ERR" | grep -q '412\|PreconditionFailed'; then
  ok "CopyObject (copy-source-if-none-match: matching ETag → 412)"
else
  fail "CopyObject (copy-source-if-none-match: expected 412, got: $(echo "$IFNM_ERR" | head -1))"
fi

# Multipart upload (with x-amz-tagging to verify tag propagation to final object)
UPLOAD_ID=$($AWS s3api create-multipart-upload --bucket "$BUCKET" --key "multipart.bin" \
  --tagging "purpose=e2e&env=local" \
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
    MPU_TAGS=$($AWS s3api get-object-tagging \
      --bucket "$BUCKET" --key "multipart.bin" 2>/dev/null || true)
    MPU_TAG_PURPOSE=$(echo "$MPU_TAGS" | jq -r '.TagSet[] | select(.Key=="purpose") | .Value' 2>/dev/null || true)
    MPU_TAG_ENV=$(echo "$MPU_TAGS" | jq -r '.TagSet[] | select(.Key=="env") | .Value' 2>/dev/null || true)
    if [[ "$MPU_TAG_PURPOSE" == "e2e" && "$MPU_TAG_ENV" == "local" ]]; then
      ok "CompleteMultipartUpload (x-amz-tagging propagated to final object)"
    else
      fail "CompleteMultipartUpload (x-amz-tagging: expected purpose=e2e env=local, got purpose='$MPU_TAG_PURPOSE' env='$MPU_TAG_ENV')"
    fi
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

# DeleteObject — single object deletion
echo "delete-me" > "$TMPFILE"
$AWS s3api put-object --bucket "$BUCKET" --key "to-delete.txt" --body "$TMPFILE" > /dev/null 2>&1 || true
run "DeleteObject (single)" \
  $AWS s3api delete-object --bucket "$BUCKET" --key "to-delete.txt"

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

# ---------------------------------------------------------------------------
# BucketLifecycle — NoncurrentVersionExpiration and NoncurrentVersionTransition.
# Verifies rules are accepted and noncurrent versions are created; enforcement
# is time-based (NoncurrentDays >= 1) and covered by unit tests.
# ---------------------------------------------------------------------------
NONCUR_BUCKET="kumolo-cli-s3-noncurrent-lc"
cleanup_bucket "$NONCUR_BUCKET"
run "CreateBucket (noncurrent lifecycle)" \
  $AWS s3api create-bucket --bucket "$NONCUR_BUCKET"
run "PutBucketVersioning (noncurrent lifecycle)" \
  $AWS s3api put-bucket-versioning \
    --bucket "$NONCUR_BUCKET" \
    --versioning-configuration Status=Enabled

run "PutBucketLifecycleConfiguration (NoncurrentVersionExpiration + Transition)" \
  $AWS s3api put-bucket-lifecycle-configuration \
    --bucket "$NONCUR_BUCKET" \
    --lifecycle-configuration '{"Rules":[{"ID":"expire-nc","Status":"Enabled","Filter":{},"NoncurrentVersionExpiration":{"NoncurrentDays":1}},{"ID":"transition-nc","Status":"Enabled","Filter":{},"NoncurrentVersionTransitions":[{"NoncurrentDays":1,"StorageClass":"GLACIER"}]}]}'

NONCUR_LC_RESP=$($AWS s3api get-bucket-lifecycle-configuration --bucket "$NONCUR_BUCKET" 2>/dev/null || true)
NONCUR_RULE_COUNT=$(echo "$NONCUR_LC_RESP" | jq '.Rules | length' 2>/dev/null || echo 0)
if [[ "$NONCUR_RULE_COUNT" -eq 2 ]]; then
  ok "GetBucketLifecycleConfiguration (NoncurrentVersion rules: 2 rules stored)"
else
  fail "GetBucketLifecycleConfiguration (NoncurrentVersion: expected 2 rules, got $NONCUR_RULE_COUNT)"
fi

echo "v1-content" > "$TMPFILE"
$AWS s3api put-object \
  --bucket "$NONCUR_BUCKET" --key "noncurrent.txt" --body "$TMPFILE" > /dev/null 2>&1 || true
echo "v2-content" > "$TMPFILE"
$AWS s3api put-object \
  --bucket "$NONCUR_BUCKET" --key "noncurrent.txt" --body "$TMPFILE" > /dev/null 2>&1 || true

NONCUR_VERS=$($AWS s3api list-object-versions --bucket "$NONCUR_BUCKET" --prefix "noncurrent.txt" 2>/dev/null || true)
NONCUR_COUNT=$(echo "$NONCUR_VERS" | jq '[.Versions[]? | select(.IsLatest == false)] | length' 2>/dev/null || echo 0)
if [[ "$NONCUR_COUNT" -ge 1 ]]; then
  ok "BucketLifecycle NoncurrentVersionExpiration (noncurrent version present)"
else
  fail "BucketLifecycle NoncurrentVersionExpiration (expected >=1 noncurrent version, got $NONCUR_COUNT)"
fi

cleanup_bucket "$NONCUR_BUCKET"

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
# BucketPolicy — store and retrieve an IAM-principal policy.
# ---------------------------------------------------------------------------
BUCKET_POLICY=$(cat <<'JSON'
{
  "Version": "2012-10-17",
  "Statement": [{
    "Sid": "AllowOwnerGet",
    "Effect": "Allow",
    "Principal": {"AWS": "arn:aws:iam::000000000000:root"},
    "Action": "s3:GetObject",
    "Resource": "arn:aws:s3:::kumolo-cli-s3-verify/*"
  }]
}
JSON
)
run "PutBucketPolicy" \
  $AWS s3api put-bucket-policy --bucket "$BUCKET" --policy "$BUCKET_POLICY"
run "GetBucketPolicy" \
  $AWS s3api get-bucket-policy --bucket "$BUCKET"
run "DeleteBucketPolicy" \
  $AWS s3api delete-bucket-policy --bucket "$BUCKET"

# ---------------------------------------------------------------------------
# PublicAccessBlock — store, retrieve, and remove block configuration.
# ---------------------------------------------------------------------------
run "PutPublicAccessBlock" \
  $AWS s3api put-public-access-block \
    --bucket "$BUCKET" \
    --public-access-block-configuration \
      '{"BlockPublicAcls":true,"IgnorePublicAcls":true,"BlockPublicPolicy":true,"RestrictPublicBuckets":true}'
run "GetPublicAccessBlock" \
  $AWS s3api get-public-access-block --bucket "$BUCKET"
run "DeletePublicAccessBlock" \
  $AWS s3api delete-public-access-block --bucket "$BUCKET"

# ---------------------------------------------------------------------------
# OwnershipControls — store, retrieve, and remove ownership setting.
# ---------------------------------------------------------------------------
run "PutBucketOwnershipControls" \
  $AWS s3api put-bucket-ownership-controls \
    --bucket "$BUCKET" \
    --ownership-controls '{"Rules":[{"ObjectOwnership":"BucketOwnerEnforced"}]}'
run "GetBucketOwnershipControls" \
  $AWS s3api get-bucket-ownership-controls --bucket "$BUCKET"
run "DeleteBucketOwnershipControls" \
  $AWS s3api delete-bucket-ownership-controls --bucket "$BUCKET"

# ---------------------------------------------------------------------------
# BucketLogging — configure log delivery and verify a log object is written.
# ---------------------------------------------------------------------------
LOG_BUCKET="kumolo-cli-s3-logging"
cleanup_bucket "$LOG_BUCKET"
run "CreateBucket (log target)" $AWS s3api create-bucket --bucket "$LOG_BUCKET"

LOG_CONFIG=$(printf '{"LoggingEnabled":{"TargetBucket":"%s","TargetPrefix":"logs/"}}' "$LOG_BUCKET")
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

run "DeleteBucketReplication" \
  $AWS s3api delete-bucket-replication --bucket "$BUCKET"

cleanup_bucket "$DEST_BUCKET"

# ---------------------------------------------------------------------------
# BucketReplication — DeleteMarkerReplication: delete marker propagates to dest.
# ---------------------------------------------------------------------------
DM_SRC="kumolo-cli-s3-dm-src"
DM_DEST="kumolo-cli-s3-dm-dest"
cleanup_bucket "$DM_SRC"
cleanup_bucket "$DM_DEST"

run "CreateBucket (delete-marker src)" $AWS s3api create-bucket --bucket "$DM_SRC"
run "PutBucketVersioning (delete-marker src)" \
  $AWS s3api put-bucket-versioning \
    --bucket "$DM_SRC" \
    --versioning-configuration Status=Enabled
run "CreateBucket (delete-marker dest)" $AWS s3api create-bucket --bucket "$DM_DEST"
run "PutBucketVersioning (delete-marker dest)" \
  $AWS s3api put-bucket-versioning \
    --bucket "$DM_DEST" \
    --versioning-configuration Status=Enabled

DM_REPL_CONFIG=$(cat <<'JSON'
{
  "Role": "arn:aws:iam::000000000000:role/replication-role",
  "Rules": [{
    "ID": "replicate-with-delete-markers",
    "Status": "Enabled",
    "Filter": {},
    "Destination": {"Bucket": "arn:aws:s3:::kumolo-cli-s3-dm-dest"},
    "DeleteMarkerReplication": {"Status": "Enabled"}
  }]
}
JSON
)
run "PutBucketReplication (DeleteMarkerReplication enabled)" \
  $AWS s3api put-bucket-replication \
    --bucket "$DM_SRC" \
    --replication-configuration "$DM_REPL_CONFIG"

# Upload an object (replicates to dest), then delete it (creates a delete marker).
echo "dm-repl-content" > "$TMPFILE"
$AWS s3api put-object \
  --bucket "$DM_SRC" --key "dm-obj.txt" --body "$TMPFILE" > /dev/null 2>&1 || true
$AWS s3api delete-object \
  --bucket "$DM_SRC" --key "dm-obj.txt" > /dev/null 2>&1 || true

DM_DEST_VERS=$($AWS s3api list-object-versions --bucket "$DM_DEST" 2>/dev/null || true)
DM_MARKER_COUNT=$(echo "$DM_DEST_VERS" | jq '[.DeleteMarkers[]? | select(.Key=="dm-obj.txt")] | length' 2>/dev/null || echo 0)
if [[ "$DM_MARKER_COUNT" -ge 1 ]]; then
  ok "BucketReplication DeleteMarkerReplication (delete marker replicated to destination)"
else
  fail "BucketReplication DeleteMarkerReplication (expected delete marker in dest, got $DM_MARKER_COUNT)"
fi

cleanup_bucket "$DM_SRC"
cleanup_bucket "$DM_DEST"

# ---------------------------------------------------------------------------
# ObjectLock — DefaultRetention applied automatically to new objects.
# Creates an object-lock-enabled bucket, sets DefaultRetention (GOVERNANCE,
# 1 day), uploads an object, and verifies GetObjectRetention reflects the mode.
# Cleanup bypasses GOVERNANCE retention.
# ---------------------------------------------------------------------------
OL_BUCKET="kumolo-cli-s3-objectlock-default"
# Pre-cleanup: bypass retention from any previous run (guard against missing bucket).
if $AWS s3api head-bucket --bucket "$OL_BUCKET" > /dev/null 2>&1; then
  # Turn off any active legal holds before deletion (legal hold blocks delete-object
  # even with --bypass-governance-retention).
  $AWS s3api list-object-versions --bucket "$OL_BUCKET" \
      --output text --query 'Versions[].[Key,VersionId]' 2>/dev/null | \
    while IFS=$'\t' read -r key vid _rest; do
      [[ -z "$key" || "$key" == "None" ]] && continue
      $AWS s3api put-object-legal-hold \
        --bucket "$OL_BUCKET" --key "$key" --version-id "$vid" \
        --legal-hold '{"Status":"OFF"}' > /dev/null 2>&1 || true
    done || true
  $AWS s3api list-object-versions --bucket "$OL_BUCKET" \
      --output text --query 'Versions[].[Key,VersionId]' 2>/dev/null | \
    while IFS=$'\t' read -r key vid _rest; do
      [[ -z "$key" || "$key" == "None" ]] && continue
      $AWS s3api delete-object \
        --bucket "$OL_BUCKET" --key "$key" --version-id "$vid" \
        --bypass-governance-retention > /dev/null 2>&1 || true
    done || true
  $AWS s3api delete-bucket --bucket "$OL_BUCKET" > /dev/null 2>&1 || true
fi

run "CreateBucket (ObjectLock enabled)" \
  $AWS s3api create-bucket --bucket "$OL_BUCKET" --object-lock-enabled-for-bucket

run "PutObjectLockConfiguration (DefaultRetention GOVERNANCE 1 day)" \
  $AWS s3api put-object-lock-configuration \
    --bucket "$OL_BUCKET" \
    --object-lock-configuration '{"ObjectLockEnabled":"Enabled","Rule":{"DefaultRetention":{"Mode":"GOVERNANCE","Days":1}}}'

OL_CONFIG=$($AWS s3api get-object-lock-configuration --bucket "$OL_BUCKET" 2>/dev/null || true)
OL_CFG_MODE=$(echo "$OL_CONFIG" | jq -r '.ObjectLockConfiguration.Rule.DefaultRetention.Mode // empty' 2>/dev/null || true)
if [[ "$OL_CFG_MODE" == "GOVERNANCE" ]]; then
  ok "GetObjectLockConfiguration (DefaultRetention Mode=GOVERNANCE stored)"
else
  fail "GetObjectLockConfiguration (expected GOVERNANCE, got '$OL_CFG_MODE')"
fi

echo "locked-content" > "$TMPFILE"
$AWS s3api put-object \
  --bucket "$OL_BUCKET" --key "retained.txt" --body "$TMPFILE" > /dev/null 2>&1 || true

OL_RET=$($AWS s3api get-object-retention --bucket "$OL_BUCKET" --key "retained.txt" 2>/dev/null || true)
OL_RET_MODE=$(echo "$OL_RET" | jq -r '.Retention.Mode // empty' 2>/dev/null || true)
if [[ "$OL_RET_MODE" == "GOVERNANCE" ]]; then
  ok "ObjectLock DefaultRetention (new object inherits GOVERNANCE mode)"
else
  fail "ObjectLock DefaultRetention (expected GOVERNANCE retention on new object, got '$OL_RET_MODE')"
fi

# PutObjectRetention — explicitly set retention on a new object.
echo "explicit-retention" > "$TMPFILE"
$AWS s3api put-object \
  --bucket "$OL_BUCKET" --key "explicit-retained.txt" --body "$TMPFILE" > /dev/null 2>&1 || true
run "PutObjectRetention (explicit GOVERNANCE)" \
  $AWS s3api put-object-retention \
    --bucket "$OL_BUCKET" \
    --key "explicit-retained.txt" \
    --retention '{"Mode":"GOVERNANCE","RetainUntilDate":"2030-01-01T00:00:00Z"}'
OL_EXPLICIT_RET=$($AWS s3api get-object-retention \
  --bucket "$OL_BUCKET" --key "explicit-retained.txt" 2>/dev/null || true)
OL_EXPLICIT_MODE=$(echo "$OL_EXPLICIT_RET" | jq -r '.Retention.Mode // empty' 2>/dev/null || true)
if [[ "$OL_EXPLICIT_MODE" == "GOVERNANCE" ]]; then
  ok "GetObjectRetention (explicit: Mode=GOVERNANCE)"
else
  fail "GetObjectRetention (explicit: expected GOVERNANCE, got '$OL_EXPLICIT_MODE')"
fi

# ObjectLegalHold — enable and verify legal hold; disable before cleanup.
echo "hold-content" > "$TMPFILE"
$AWS s3api put-object \
  --bucket "$OL_BUCKET" --key "held.txt" --body "$TMPFILE" > /dev/null 2>&1 || true
run "PutObjectLegalHold (ON)" \
  $AWS s3api put-object-legal-hold \
    --bucket "$OL_BUCKET" \
    --key "held.txt" \
    --legal-hold '{"Status":"ON"}'
OL_HOLD=$($AWS s3api get-object-legal-hold --bucket "$OL_BUCKET" --key "held.txt" 2>/dev/null || true)
OL_HOLD_STATUS=$(echo "$OL_HOLD" | jq -r '.LegalHold.Status // empty' 2>/dev/null || true)
if [[ "$OL_HOLD_STATUS" == "ON" ]]; then
  ok "GetObjectLegalHold (Status=ON)"
else
  fail "GetObjectLegalHold (expected ON, got '$OL_HOLD_STATUS')"
fi
# Turn off legal hold before cleanup.
$AWS s3api put-object-legal-hold \
  --bucket "$OL_BUCKET" --key "held.txt" \
  --legal-hold '{"Status":"OFF"}' > /dev/null 2>&1 || true

# Cleanup: bypass GOVERNANCE retention before deleting the bucket.
$AWS s3api list-object-versions --bucket "$OL_BUCKET" \
    --output text --query 'Versions[].[Key,VersionId]' 2>/dev/null | \
  while IFS=$'\t' read -r key vid _rest; do
    [[ -z "$key" || "$key" == "None" ]] && continue
    $AWS s3api delete-object \
      --bucket "$OL_BUCKET" --key "$key" --version-id "$vid" \
      --bypass-governance-retention > /dev/null 2>&1 || true
  done || true
$AWS s3api delete-bucket --bucket "$OL_BUCKET" > /dev/null 2>&1 || true

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
SSEC_TMPKEY="$(mktemp)"  # registered in EXIT trap above
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
# UploadPartCopy, ListParts, AbortMultipartUpload — multipart copy flow.
# ---------------------------------------------------------------------------
UPC_UPLOAD_ID=$($AWS s3api create-multipart-upload \
  --bucket "$BUCKET" --key "upc-result.bin" \
  --query UploadId --output text 2>/dev/null || true)
if [[ -n "$UPC_UPLOAD_ID" ]]; then
  UPC_COPY_RESP=$($AWS s3api upload-part-copy \
    --bucket "$BUCKET" \
    --key "upc-result.bin" \
    --upload-id "$UPC_UPLOAD_ID" \
    --part-number 1 \
    --copy-source "$BUCKET/hello.txt" 2>/dev/null || true)
  UPC_ETAG=$(echo "$UPC_COPY_RESP" | jq -r '.CopyPartResult.ETag // empty' 2>/dev/null || true)
  if [[ -n "$UPC_ETAG" ]]; then
    ok "UploadPartCopy"
    UPC_PARTS_RESP=$($AWS s3api list-parts \
      --bucket "$BUCKET" --key "upc-result.bin" \
      --upload-id "$UPC_UPLOAD_ID" 2>/dev/null || true)
    UPC_PART_COUNT=$(echo "$UPC_PARTS_RESP" | jq '.Parts | length' 2>/dev/null || echo 0)
    if [[ "$UPC_PART_COUNT" -eq 1 ]]; then
      ok "ListParts (1 part listed)"
    else
      fail "ListParts (expected 1 part, got $UPC_PART_COUNT)"
    fi
  else
    fail "UploadPartCopy"
  fi
  run "AbortMultipartUpload" \
    $AWS s3api abort-multipart-upload \
      --bucket "$BUCKET" --key "upc-result.bin" \
      --upload-id "$UPC_UPLOAD_ID"
else
  fail "UploadPartCopy (CreateMultipartUpload failed)"
fi

# ---------------------------------------------------------------------------
# PresignedPost — browser-based HTML form upload via POST policy (curl).
# AWS CLI has no presigned-post command; tests use curl multipart/form-data.
# Policy and SigV4 fields are accepted and ignored by kumolo.
# ---------------------------------------------------------------------------
echo "presigned-post-content" > "$TMPFILE"

# Basic upload: default response is 204 No Content.
POST_STATUS=$(curl -s -m 10 -o /dev/null -w "%{http_code}" \
  -F "key=presigned/basic.txt" \
  -F "Content-Type=text/plain" \
  -F "file=@${TMPFILE};type=text/plain" \
  "${ENDPOINT}/${BUCKET}")
if [[ "$POST_STATUS" == "204" ]]; then
  ok "PresignedPost (default 204 No Content)"
else
  fail "PresignedPost (default: expected 204, got $POST_STATUS)"
fi

run "HeadObject (PresignedPost basic upload)" \
  $AWS s3api head-object --bucket "$BUCKET" --key "presigned/basic.txt"

# success_action_status=201 returns 201 with PostResponse XML body.
POST_201_BODY=$(curl -s -m 10 \
  -F "key=presigned/status201.txt" \
  -F "success_action_status=201" \
  -F "file=@${TMPFILE};type=text/plain" \
  "${ENDPOINT}/${BUCKET}")
if echo "$POST_201_BODY" | grep -q "<PostResponse>"; then
  ok "PresignedPost (success_action_status=201 returns PostResponse XML)"
else
  fail "PresignedPost (success_action_status=201: expected PostResponse XML, got: $(echo "$POST_201_BODY" | head -3))"
fi

# \${filename} substitution: key field contains \${filename}, resolved from the
# filename parameter in the file part's Content-Disposition header.
POST_FN_STATUS=$(curl -s -m 10 -o /dev/null -w "%{http_code}" \
  -F 'key=presigned/${filename}' \
  -F "file=@${TMPFILE};filename=uploaded.txt;type=text/plain" \
  "${ENDPOINT}/${BUCKET}")
if [[ "$POST_FN_STATUS" == "204" ]]; then
  ok "PresignedPost (\${filename} substitution: 204)"
else
  fail "PresignedPost (\${filename} substitution: expected 204, got $POST_FN_STATUS)"
fi
run "HeadObject (PresignedPost \${filename} → presigned/uploaded.txt)" \
  $AWS s3api head-object --bucket "$BUCKET" --key "presigned/uploaded.txt"

# ---------------------------------------------------------------------------
# ACL enforcement — verify anonymous access is controlled by object ACL.
# ---------------------------------------------------------------------------
echo "public-content" > "$TMPFILE"
$AWS s3api put-object \
  --bucket "$BUCKET" --key "acl-test.txt" --body "$TMPFILE" > /dev/null 2>&1 || true

run "PutObjectAcl (public-read)" \
  $AWS s3api put-object-acl --bucket "$BUCKET" --key "acl-test.txt" --acl public-read

# Anonymous GET via plain HTTP (no auth headers) should succeed.
ACL_PUBLIC_STATUS=$(curl -s -m 10 -o /dev/null -w "%{http_code}" \
  "${ENDPOINT}/${BUCKET}/acl-test.txt")
if [[ "$ACL_PUBLIC_STATUS" == "200" ]]; then
  ok "ACL enforcement (public-read: anonymous GET returns 200)"
else
  fail "ACL enforcement (public-read: expected 200, got $ACL_PUBLIC_STATUS)"
fi

run "PutObjectAcl (private)" \
  $AWS s3api put-object-acl --bucket "$BUCKET" --key "acl-test.txt" --acl private

ACL_RESP=$($AWS s3api get-object-acl --bucket "$BUCKET" --key "acl-test.txt" 2>/dev/null || true)
ACL_OWNER=$(echo "$ACL_RESP" | jq -r '.Owner.DisplayName // empty' 2>/dev/null || true)
if [[ -n "$ACL_OWNER" ]]; then
  ok "GetObjectACL (Owner present)"
else
  fail "GetObjectACL (expected Owner in response)"
fi

# Anonymous GET should now be denied.
ACL_PRIVATE_STATUS=$(curl -s -m 10 -o /dev/null -w "%{http_code}" \
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
