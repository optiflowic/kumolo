#!/usr/bin/env bash
set -euo pipefail

ENDPOINT="${KUMOLO_ENDPOINT:-http://localhost:5566}"

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

AWS="aws --endpoint-url $ENDPOINT kms"
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

echo "=== KMS ==="

# ---------------------------------------------------------------------------
# CreateKey — capture KeyId for subsequent calls.
# ---------------------------------------------------------------------------
CREATE_RESP=$($AWS create-key --description "kumolo-e2e-test-key" 2>/dev/null || true)
KEY_ID=$(echo "$CREATE_RESP" | jq -r '.KeyMetadata.KeyId // empty' 2>/dev/null || true)
KEY_ARN=$(echo "$CREATE_RESP" | jq -r '.KeyMetadata.Arn // empty' 2>/dev/null || true)
if [[ -n "$KEY_ID" ]]; then
  ok "CreateKey (KeyId present)"
else
  fail "CreateKey (no KeyId returned)"
fi

# ---------------------------------------------------------------------------
# DescribeKey
# ---------------------------------------------------------------------------
if [[ -n "$KEY_ID" ]]; then
  run "DescribeKey (by KeyId)" \
    $AWS describe-key --key-id "$KEY_ID"
  run "DescribeKey (by KeyArn)" \
    $AWS describe-key --key-id "$KEY_ARN"
else
  fail "DescribeKey (skipped: no KeyId)"
  fail "DescribeKey (by KeyArn) (skipped: no KeyArn)"
fi

# ---------------------------------------------------------------------------
# ListKeys
# ---------------------------------------------------------------------------
LIST_RESP=$($AWS list-keys 2>/dev/null || true)
LIST_COUNT=$(echo "$LIST_RESP" | jq '.Keys | length' 2>/dev/null || echo 0)
if [[ "$LIST_COUNT" -ge 1 ]]; then
  ok "ListKeys (at least one key)"
else
  fail "ListKeys (expected >=1 key, got $LIST_COUNT)"
fi

# ---------------------------------------------------------------------------
# GetKeyPolicy / PutKeyPolicy
# ---------------------------------------------------------------------------
if [[ -n "$KEY_ID" ]]; then
  run "GetKeyPolicy" \
    $AWS get-key-policy --key-id "$KEY_ID" --policy-name default

  NEW_POLICY='{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"kms:*","Resource":"*"}]}'
  run "PutKeyPolicy" \
    $AWS put-key-policy --key-id "$KEY_ID" --policy-name default --policy "$NEW_POLICY"

  # Verify the policy was updated.
  POLICY_RESP=$($AWS get-key-policy --key-id "$KEY_ID" --policy-name default 2>/dev/null || true)
  POLICY_CONTENT=$(echo "$POLICY_RESP" | jq -r '.Policy // empty' 2>/dev/null || true)
  if [[ -n "$POLICY_CONTENT" ]]; then
    ok "GetKeyPolicy (policy content present after PutKeyPolicy)"
  else
    fail "GetKeyPolicy (no policy content after PutKeyPolicy)"
  fi
else
  fail "GetKeyPolicy (skipped: no KeyId)"
  fail "PutKeyPolicy (skipped: no KeyId)"
  fail "GetKeyPolicy (policy content present after PutKeyPolicy) (skipped)"
fi

# ---------------------------------------------------------------------------
# Encrypt / Decrypt round-trip
# ---------------------------------------------------------------------------
PLAINTEXT_INPUT="SGVsbG8ga3Vtb2xvIQ=="  # base64("Hello kumolo!")
CIPHERTEXT_BLOB=""
if [[ -n "$KEY_ID" ]]; then
  ENC_RESP=$($AWS encrypt \
    --key-id "$KEY_ID" \
    --plaintext "$PLAINTEXT_INPUT" \
    2>/dev/null || true)
  CIPHERTEXT_BLOB=$(echo "$ENC_RESP" | jq -r '.CiphertextBlob // empty' 2>/dev/null || true)
  ENC_KEY_ID=$(echo "$ENC_RESP" | jq -r '.KeyId // empty' 2>/dev/null || true)
  if [[ -n "$CIPHERTEXT_BLOB" && -n "$ENC_KEY_ID" ]]; then
    ok "Encrypt (CiphertextBlob and KeyId present)"
  else
    fail "Encrypt (missing CiphertextBlob or KeyId)"
  fi

  if [[ -n "$CIPHERTEXT_BLOB" ]]; then
    DEC_RESP=$($AWS decrypt \
      --ciphertext-blob "$CIPHERTEXT_BLOB" \
      2>/dev/null || true)
    DEC_PLAINTEXT=$(echo "$DEC_RESP" | jq -r '.Plaintext // empty' 2>/dev/null || true)
    DEC_KEY_ID=$(echo "$DEC_RESP" | jq -r '.KeyId // empty' 2>/dev/null || true)
    if [[ "$DEC_PLAINTEXT" == "$PLAINTEXT_INPUT" && -n "$DEC_KEY_ID" ]]; then
      ok "Decrypt (plaintext matches original, KeyId present)"
    else
      fail "Decrypt (plaintext mismatch or missing KeyId; got '$DEC_PLAINTEXT', keyId='$DEC_KEY_ID')"
    fi
  else
    fail "Decrypt (skipped: no CiphertextBlob from Encrypt)"
  fi
else
  fail "Encrypt (skipped: no KeyId)"
  fail "Decrypt (skipped: no KeyId)"
fi

# ---------------------------------------------------------------------------
# GenerateDataKey
# ---------------------------------------------------------------------------
if [[ -n "$KEY_ID" ]]; then
  GDK_RESP=$($AWS generate-data-key \
    --key-id "$KEY_ID" \
    --key-spec AES_256 \
    2>/dev/null || true)
  GDK_PLAINTEXT=$(echo "$GDK_RESP" | jq -r '.Plaintext // empty' 2>/dev/null || true)
  GDK_CIPHERTEXT=$(echo "$GDK_RESP" | jq -r '.CiphertextBlob // empty' 2>/dev/null || true)
  GDK_KEY_ID=$(echo "$GDK_RESP" | jq -r '.KeyId // empty' 2>/dev/null || true)
  if [[ -n "$GDK_PLAINTEXT" && -n "$GDK_CIPHERTEXT" && -n "$GDK_KEY_ID" ]]; then
    ok "GenerateDataKey (Plaintext, CiphertextBlob, KeyId present)"
  else
    fail "GenerateDataKey (missing Plaintext, CiphertextBlob, or KeyId)"
  fi
else
  fail "GenerateDataKey (skipped: no KeyId)"
fi

# ---------------------------------------------------------------------------
# GenerateDataKeyWithoutPlaintext
# ---------------------------------------------------------------------------
if [[ -n "$KEY_ID" ]]; then
  GDKWP_RESP=$($AWS generate-data-key-without-plaintext \
    --key-id "$KEY_ID" \
    --key-spec AES_256 \
    2>/dev/null || true)
  GDKWP_CIPHERTEXT=$(echo "$GDKWP_RESP" | jq -r '.CiphertextBlob // empty' 2>/dev/null || true)
  GDKWP_KEY_ID=$(echo "$GDKWP_RESP" | jq -r '.KeyId // empty' 2>/dev/null || true)
  GDKWP_PLAINTEXT=$(echo "$GDKWP_RESP" | jq -r '.Plaintext // empty' 2>/dev/null || true)
  if [[ -n "$GDKWP_CIPHERTEXT" && -n "$GDKWP_KEY_ID" && -z "$GDKWP_PLAINTEXT" ]]; then
    ok "GenerateDataKeyWithoutPlaintext (CiphertextBlob and KeyId present, no Plaintext)"
  else
    fail "GenerateDataKeyWithoutPlaintext (unexpected response: ciphertext='$GDKWP_CIPHERTEXT', keyId='$GDKWP_KEY_ID', plaintext='$GDKWP_PLAINTEXT')"
  fi
else
  fail "GenerateDataKeyWithoutPlaintext (skipped: no KeyId)"
fi

# ---------------------------------------------------------------------------
# Aliases: CreateAlias / ListAliases / UpdateAlias / DeleteAlias
# ---------------------------------------------------------------------------
ALIAS_NAME="alias/kumolo-e2e-test"
ALIAS_NAME2="alias/kumolo-e2e-test-2"

# Cleanup from a previous run.
$AWS delete-alias --alias-name "$ALIAS_NAME"  > /dev/null 2>&1 || true
$AWS delete-alias --alias-name "$ALIAS_NAME2" > /dev/null 2>&1 || true

if [[ -n "$KEY_ID" ]]; then
  run "CreateAlias" \
    $AWS create-alias --alias-name "$ALIAS_NAME" --target-key-id "$KEY_ID"

  # Create a second key for UpdateAlias.
  CREATE_RESP2=$($AWS create-key --description "kumolo-e2e-test-key-2" 2>/dev/null || true)
  KEY_ID2=$(echo "$CREATE_RESP2" | jq -r '.KeyMetadata.KeyId // empty' 2>/dev/null || true)

  # ListAliases (all)
  LIST_ALIASES_RESP=$($AWS list-aliases 2>/dev/null || true)
  ALIAS_COUNT=$(echo "$LIST_ALIASES_RESP" | jq '[.Aliases[] | select(.AliasName == "'"$ALIAS_NAME"'")] | length' 2>/dev/null || echo 0)
  if [[ "$ALIAS_COUNT" -ge 1 ]]; then
    ok "ListAliases (created alias present)"
  else
    fail "ListAliases (created alias not found)"
  fi

  # ListAliases (filter by KeyId)
  if [[ -n "$KEY_ID" ]]; then
    run "ListAliases (filter by KeyId)" \
      $AWS list-aliases --key-id "$KEY_ID"
  fi

  # UpdateAlias — point the alias at the second key.
  if [[ -n "$KEY_ID2" ]]; then
    run "UpdateAlias" \
      $AWS update-alias --alias-name "$ALIAS_NAME" --target-key-id "$KEY_ID2"

    # Verify the alias now resolves to the second key.
    ALIAS_AFTER=$($AWS list-aliases --key-id "$KEY_ID2" 2>/dev/null || true)
    ALIAS_MATCH=$(echo "$ALIAS_AFTER" | jq '[.Aliases[] | select(.AliasName == "'"$ALIAS_NAME"'")] | length' 2>/dev/null || echo 0)
    if [[ "$ALIAS_MATCH" -ge 1 ]]; then
      ok "UpdateAlias (alias points to new key)"
    else
      fail "UpdateAlias (alias not pointing to new key after update)"
    fi
  else
    fail "UpdateAlias (skipped: second key creation failed)"
  fi

  run "DeleteAlias" \
    $AWS delete-alias --alias-name "$ALIAS_NAME"

  # Verify alias is gone.
  LIST_AFTER_DELETE=$($AWS list-aliases 2>/dev/null || true)
  ALIAS_AFTER_DELETE=$(echo "$LIST_AFTER_DELETE" | jq '[.Aliases[] | select(.AliasName == "'"$ALIAS_NAME"'")] | length' 2>/dev/null || echo 0)
  if [[ "$ALIAS_AFTER_DELETE" -eq 0 ]]; then
    ok "DeleteAlias (alias removed from ListAliases)"
  else
    fail "DeleteAlias (alias still present in ListAliases)"
  fi
else
  fail "CreateAlias (skipped: no KeyId)"
  fail "ListAliases (created alias present) (skipped)"
  fail "ListAliases (filter by KeyId) (skipped)"
  fail "UpdateAlias (skipped)"
  fail "UpdateAlias (alias points to new key) (skipped)"
  fail "DeleteAlias (skipped)"
  fail "DeleteAlias (alias removed from ListAliases) (skipped)"
fi

# ---------------------------------------------------------------------------
# EnableKey / DisableKey
# ---------------------------------------------------------------------------
if [[ -n "$KEY_ID" ]]; then
  run "DisableKey" \
    $AWS disable-key --key-id "$KEY_ID"

  DISABLED_RESP=$($AWS describe-key --key-id "$KEY_ID" 2>/dev/null || true)
  KEY_STATE=$(echo "$DISABLED_RESP" | jq -r '.KeyMetadata.KeyState // empty' 2>/dev/null || true)
  if [[ "$KEY_STATE" == "Disabled" ]]; then
    ok "DisableKey (KeyState=Disabled)"
  else
    fail "DisableKey (expected KeyState=Disabled, got '$KEY_STATE')"
  fi

  run "EnableKey" \
    $AWS enable-key --key-id "$KEY_ID"

  ENABLED_RESP=$($AWS describe-key --key-id "$KEY_ID" 2>/dev/null || true)
  KEY_STATE=$(echo "$ENABLED_RESP" | jq -r '.KeyMetadata.KeyState // empty' 2>/dev/null || true)
  if [[ "$KEY_STATE" == "Enabled" ]]; then
    ok "EnableKey (KeyState=Enabled)"
  else
    fail "EnableKey (expected KeyState=Enabled, got '$KEY_STATE')"
  fi
else
  fail "DisableKey (skipped: no KeyId)"
  fail "DisableKey (KeyState=Disabled) (skipped)"
  fail "EnableKey (skipped: no KeyId)"
  fail "EnableKey (KeyState=Enabled) (skipped)"
fi

# ---------------------------------------------------------------------------
# Key rotation: EnableKeyRotation / DisableKeyRotation / GetKeyRotationStatus
# ---------------------------------------------------------------------------
if [[ -n "$KEY_ID" ]]; then
  run "EnableKeyRotation" \
    $AWS enable-key-rotation --key-id "$KEY_ID"

  ROT_RESP=$($AWS get-key-rotation-status --key-id "$KEY_ID" 2>/dev/null || true)
  ROT_ENABLED=$(echo "$ROT_RESP" | jq -r '.KeyRotationEnabled // empty' 2>/dev/null || true)
  if [[ "$ROT_ENABLED" == "true" ]]; then
    ok "GetKeyRotationStatus (KeyRotationEnabled=true after enable)"
  else
    fail "GetKeyRotationStatus (expected KeyRotationEnabled=true, got '$ROT_ENABLED')"
  fi

  run "DisableKeyRotation" \
    $AWS disable-key-rotation --key-id "$KEY_ID"

  ROT_RESP2=$($AWS get-key-rotation-status --key-id "$KEY_ID" 2>/dev/null || true)
  ROT_DISABLED=$(echo "$ROT_RESP2" | jq '.KeyRotationEnabled' 2>/dev/null || true)
  if [[ "$ROT_DISABLED" == "false" ]]; then
    ok "GetKeyRotationStatus (KeyRotationEnabled=false after disable)"
  else
    fail "GetKeyRotationStatus (expected KeyRotationEnabled=false, got '$ROT_DISABLED')"
  fi
else
  fail "EnableKeyRotation (skipped: no KeyId)"
  fail "GetKeyRotationStatus (KeyRotationEnabled=true after enable) (skipped)"
  fail "DisableKeyRotation (skipped: no KeyId)"
  fail "GetKeyRotationStatus (KeyRotationEnabled=false after disable) (skipped)"
fi

# ---------------------------------------------------------------------------
# ScheduleKeyDeletion / CancelKeyDeletion
# ---------------------------------------------------------------------------
if [[ -n "$KEY_ID" ]]; then
  SCHED_RESP=$($AWS schedule-key-deletion \
    --key-id "$KEY_ID" \
    --pending-window-in-days 7 \
    2>/dev/null || true)
  DELETION_DATE=$(echo "$SCHED_RESP" | jq -r '.DeletionDate // empty' 2>/dev/null || true)
  PENDING_STATE=$(echo "$SCHED_RESP" | jq -r '.KeyState // empty' 2>/dev/null || true)
  if [[ -n "$DELETION_DATE" && "$PENDING_STATE" == "PendingDeletion" ]]; then
    ok "ScheduleKeyDeletion (DeletionDate present, KeyState=PendingDeletion)"
  else
    fail "ScheduleKeyDeletion (expected DeletionDate and PendingDeletion, got date='$DELETION_DATE' state='$PENDING_STATE')"
  fi

  $AWS cancel-key-deletion --key-id "$KEY_ID" > /dev/null 2>&1 \
    && ok "CancelKeyDeletion" \
    || fail "CancelKeyDeletion"

  CANCEL_DESC=$($AWS describe-key --key-id "$KEY_ID" 2>/dev/null || true)
  CANCEL_STATE=$(echo "$CANCEL_DESC" | jq -r '.KeyMetadata.KeyState // empty' 2>/dev/null || true)
  if [[ "$CANCEL_STATE" == "Disabled" ]]; then
    ok "CancelKeyDeletion (KeyState=Disabled after describe-key)"
  else
    fail "CancelKeyDeletion (expected KeyState=Disabled, got '$CANCEL_STATE')"
  fi
else
  fail "ScheduleKeyDeletion (skipped: no KeyId)"
  fail "ScheduleKeyDeletion (DeletionDate present, KeyState=PendingDeletion) (skipped)"
  fail "CancelKeyDeletion (skipped: no KeyId)"
  fail "CancelKeyDeletion (KeyState=Disabled) (skipped)"
fi

# ---------------------------------------------------------------------------
echo ""
echo "KMS results: ${PASS} passed, ${FAIL} failed"
[[ $FAIL -eq 0 ]]
