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
# CreateKey: ECC_SECG_P256K1 must be rejected (#341)
# ---------------------------------------------------------------------------
SECP256K1_ERR=$($AWS create-key \
  --key-spec ECC_SECG_P256K1 \
  --key-usage SIGN_VERIFY \
  --description "kumolo-e2e-secp256k1-should-fail" 2>&1 || true)
if echo "$SECP256K1_ERR" | grep -q 'UnsupportedOperationException'; then
  ok "CreateKey (ECC_SECG_P256K1 → UnsupportedOperationException)"
else
  fail "CreateKey (ECC_SECG_P256K1: expected UnsupportedOperationException, got: $(echo "$SECP256K1_ERR" | head -1))"
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
# TagResource / UntagResource / ListResourceTags
# ---------------------------------------------------------------------------
if [[ -n "$KEY_ID" ]]; then
  run "TagResource (add two tags)" \
    $AWS tag-resource --key-id "$KEY_ID" \
      --tags TagKey=Env,TagValue=test TagKey=Team,TagValue=platform

  LRT_RESP=$($AWS list-resource-tags --key-id "$KEY_ID" 2>/dev/null || true)
  LRT_TAGS=$(echo "$LRT_RESP" | jq '.Tags | length' 2>/dev/null || echo -1)
  if [[ "$LRT_TAGS" -eq 2 ]]; then
    ok "ListResourceTags (2 tags after TagResource)"
  else
    fail "ListResourceTags (expected 2 tags, got $LRT_TAGS)"
  fi

  run "TagResource (overwrite tag value)" \
    $AWS tag-resource --key-id "$KEY_ID" \
      --tags TagKey=Env,TagValue=prod

  LRT_RESP2=$($AWS list-resource-tags --key-id "$KEY_ID" 2>/dev/null || true)
  ENV_VALUE=$(echo "$LRT_RESP2" | jq -r '.Tags[] | select(.TagKey=="Env") | .TagValue' 2>/dev/null || true)
  if [[ "$ENV_VALUE" == "prod" ]]; then
    ok "TagResource (tag value overwritten)"
  else
    fail "TagResource (expected Env=prod, got '$ENV_VALUE')"
  fi

  run "UntagResource" \
    $AWS untag-resource --key-id "$KEY_ID" --tag-keys Env

  LRT_RESP3=$($AWS list-resource-tags --key-id "$KEY_ID" 2>/dev/null || true)
  LRT_TAGS3=$(echo "$LRT_RESP3" | jq '.Tags | length' 2>/dev/null || echo -1)
  LRT_TRUNCATED3=$(echo "$LRT_RESP3" | jq '.Truncated // false' 2>/dev/null || true)
  if [[ "$LRT_TAGS3" -eq 1 && "$LRT_TRUNCATED3" == "false" ]]; then
    ok "ListResourceTags (1 tag remains after UntagResource, Truncated=false)"
  else
    fail "ListResourceTags (expected 1 tag and Truncated=false, got tags=$LRT_TAGS3 truncated=$LRT_TRUNCATED3)"
  fi
else
  fail "TagResource (skipped: no KeyId)"
  fail "ListResourceTags (2 tags after TagResource) (skipped)"
  fail "TagResource (overwrite tag value) (skipped)"
  fail "TagResource (tag value overwritten) (skipped)"
  fail "UntagResource (skipped)"
  fail "ListResourceTags (1 tag remains after UntagResource) (skipped)"
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
# ReEncrypt — re-encrypt ciphertext from one symmetric key to another.
# ---------------------------------------------------------------------------
if [[ -n "$KEY_ID" && -n "$CIPHERTEXT_BLOB" ]]; then
  REENC_KEY_RESP=$($AWS create-key --description "kumolo-e2e-reenc-key" 2>/dev/null || true)
  REENC_KEY_ID=$(echo "$REENC_KEY_RESP" | jq -r '.KeyMetadata.KeyId // empty' 2>/dev/null || true)

  if [[ -n "$REENC_KEY_ID" ]]; then
    REENC_RESP=$($AWS re-encrypt \
      --ciphertext-blob "$CIPHERTEXT_BLOB" \
      --destination-key-id "$REENC_KEY_ID" \
      2>/dev/null || true)
    REENC_BLOB=$(echo "$REENC_RESP" | jq -r '.CiphertextBlob // empty' 2>/dev/null || true)
    REENC_SRC_KEY=$(echo "$REENC_RESP" | jq -r '.SourceKeyId // empty' 2>/dev/null || true)
    REENC_DST_KEY=$(echo "$REENC_RESP" | jq -r '.KeyId // empty' 2>/dev/null || true)
    if [[ -n "$REENC_BLOB" && -n "$REENC_SRC_KEY" && -n "$REENC_DST_KEY" ]]; then
      ok "ReEncrypt (CiphertextBlob, SourceKeyId, and KeyId present)"
    else
      fail "ReEncrypt (missing fields: blob='$REENC_BLOB' src='$REENC_SRC_KEY' dst='$REENC_DST_KEY')"
    fi

    if [[ -n "$REENC_BLOB" ]]; then
      DEC_REENC_RESP=$($AWS decrypt \
        --ciphertext-blob "$REENC_BLOB" \
        --key-id "$REENC_KEY_ID" \
        2>/dev/null || true)
      DEC_REENC_PLAINTEXT=$(echo "$DEC_REENC_RESP" | jq -r '.Plaintext // empty' 2>/dev/null || true)
      if [[ "$DEC_REENC_PLAINTEXT" == "$PLAINTEXT_INPUT" ]]; then
        ok "ReEncrypt (re-encrypted ciphertext decrypts to original plaintext)"
      else
        fail "ReEncrypt (decrypted plaintext mismatch: got '$DEC_REENC_PLAINTEXT')"
      fi
    else
      fail "ReEncrypt (re-encrypted ciphertext decrypts to original plaintext) (skipped: ReEncrypt failed)"
    fi
  else
    fail "ReEncrypt (skipped: second key creation failed)"
    fail "ReEncrypt (re-encrypted ciphertext decrypts to original plaintext) (skipped)"
  fi
else
  fail "ReEncrypt (skipped: no KeyId or no CiphertextBlob)"
  fail "ReEncrypt (re-encrypted ciphertext decrypts to original plaintext) (skipped)"
fi

# ---------------------------------------------------------------------------
# RotateKeyOnDemand / ListKeyRotations
# ---------------------------------------------------------------------------
ROD_KEY_RESP=$($AWS create-key --description "kumolo-e2e-rotate-key" 2>/dev/null || true)
ROD_KEY_ID=$(echo "$ROD_KEY_RESP" | jq -r '.KeyMetadata.KeyId // empty' 2>/dev/null || true)
if [[ -n "$ROD_KEY_ID" ]]; then
  ROD_RESP=$($AWS rotate-key-on-demand --key-id "$ROD_KEY_ID" 2>/dev/null || true)
  ROD_KEY_ID_RESP=$(echo "$ROD_RESP" | jq -r '.KeyId // empty' 2>/dev/null || true)
  if [[ -n "$ROD_KEY_ID_RESP" ]]; then
    ok "RotateKeyOnDemand (KeyId present)"
  else
    fail "RotateKeyOnDemand (no KeyId in response)"
  fi

  LKR_RESP=$($AWS list-key-rotations --key-id "$ROD_KEY_ID" 2>/dev/null || true)
  ROT_COUNT=$(echo "$LKR_RESP" | jq '.Rotations | length' 2>/dev/null || echo 0)
  if [[ "$ROT_COUNT" -ge 1 ]]; then
    ok "ListKeyRotations (at least 1 rotation after RotateKeyOnDemand)"
  else
    fail "ListKeyRotations (expected >=1 rotation, got $ROT_COUNT)"
  fi
else
  fail "RotateKeyOnDemand (skipped: key creation failed)"
  fail "ListKeyRotations (skipped: key creation failed)"
fi

# ---------------------------------------------------------------------------
# Sign / Verify — RSA (RSASSA_PKCS1_V1_5_SHA_256) and ECDSA (ECDSA_SHA_256)
# ---------------------------------------------------------------------------
RSA_SIGN_KEY_RESP=$($AWS create-key \
  --key-spec RSA_2048 \
  --key-usage SIGN_VERIFY \
  --description "kumolo-e2e-rsa-sign" \
  2>/dev/null || true)
RSA_SIGN_KEY_ID=$(echo "$RSA_SIGN_KEY_RESP" | jq -r '.KeyMetadata.KeyId // empty' 2>/dev/null || true)

if [[ -n "$RSA_SIGN_KEY_ID" ]]; then
  RSA_SIGN_RESP=$($AWS sign \
    --key-id "$RSA_SIGN_KEY_ID" \
    --message "$PLAINTEXT_INPUT" \
    --signing-algorithm RSASSA_PKCS1_V1_5_SHA_256 \
    2>/dev/null || true)
  RSA_SIG=$(echo "$RSA_SIGN_RESP" | jq -r '.Signature // empty' 2>/dev/null || true)
  RSA_SIGN_ALGO=$(echo "$RSA_SIGN_RESP" | jq -r '.SigningAlgorithm // empty' 2>/dev/null || true)
  if [[ -n "$RSA_SIG" && "$RSA_SIGN_ALGO" == "RSASSA_PKCS1_V1_5_SHA_256" ]]; then
    ok "Sign RSA (Signature and SigningAlgorithm=RSASSA_PKCS1_V1_5_SHA_256 present)"
  else
    fail "Sign RSA (missing fields: sig='$RSA_SIG' algo='$RSA_SIGN_ALGO')"
  fi

  if [[ -n "$RSA_SIG" ]]; then
    RSA_VERIFY_RESP=$($AWS verify \
      --key-id "$RSA_SIGN_KEY_ID" \
      --message "$PLAINTEXT_INPUT" \
      --signing-algorithm RSASSA_PKCS1_V1_5_SHA_256 \
      --signature "$RSA_SIG" \
      2>/dev/null || true)
    RSA_VERIFY_RESULT=$(echo "$RSA_VERIFY_RESP" | jq -r '.SignatureValid // empty' 2>/dev/null || true)
    if [[ "$RSA_VERIFY_RESULT" == "true" ]]; then
      ok "Verify RSA (SignatureValid=true)"
    else
      fail "Verify RSA (expected SignatureValid=true, got '$RSA_VERIFY_RESULT')"
    fi
  else
    fail "Verify RSA (skipped: no Signature from Sign)"
  fi
else
  fail "Sign RSA (skipped: RSA key creation failed)"
  fail "Verify RSA (skipped: RSA key creation failed)"
fi

ECC_KEY_RESP=$($AWS create-key \
  --key-spec ECC_NIST_P256 \
  --key-usage SIGN_VERIFY \
  --description "kumolo-e2e-ecc-sign" \
  2>/dev/null || true)
ECC_KEY_ID=$(echo "$ECC_KEY_RESP" | jq -r '.KeyMetadata.KeyId // empty' 2>/dev/null || true)

if [[ -n "$ECC_KEY_ID" ]]; then
  ECC_SIGN_RESP=$($AWS sign \
    --key-id "$ECC_KEY_ID" \
    --message "$PLAINTEXT_INPUT" \
    --signing-algorithm ECDSA_SHA_256 \
    2>/dev/null || true)
  ECC_SIG=$(echo "$ECC_SIGN_RESP" | jq -r '.Signature // empty' 2>/dev/null || true)
  ECC_SIGN_ALGO=$(echo "$ECC_SIGN_RESP" | jq -r '.SigningAlgorithm // empty' 2>/dev/null || true)
  if [[ -n "$ECC_SIG" && "$ECC_SIGN_ALGO" == "ECDSA_SHA_256" ]]; then
    ok "Sign ECDSA (Signature and SigningAlgorithm=ECDSA_SHA_256 present)"
  else
    fail "Sign ECDSA (missing fields: sig='$ECC_SIG' algo='$ECC_SIGN_ALGO')"
  fi

  if [[ -n "$ECC_SIG" ]]; then
    ECC_VERIFY_RESP=$($AWS verify \
      --key-id "$ECC_KEY_ID" \
      --message "$PLAINTEXT_INPUT" \
      --signing-algorithm ECDSA_SHA_256 \
      --signature "$ECC_SIG" \
      2>/dev/null || true)
    ECC_VERIFY_RESULT=$(echo "$ECC_VERIFY_RESP" | jq -r '.SignatureValid // empty' 2>/dev/null || true)
    if [[ "$ECC_VERIFY_RESULT" == "true" ]]; then
      ok "Verify ECDSA (SignatureValid=true)"
    else
      fail "Verify ECDSA (expected SignatureValid=true, got '$ECC_VERIFY_RESULT')"
    fi
  else
    fail "Verify ECDSA (skipped: no Signature from Sign)"
  fi
else
  fail "Sign ECDSA (skipped: ECC key creation failed)"
  fail "Verify ECDSA (skipped: ECC key creation failed)"
fi

# ---------------------------------------------------------------------------
# GetPublicKey — RSA asymmetric key
# ---------------------------------------------------------------------------
if [[ -n "$RSA_SIGN_KEY_ID" ]]; then
  GPK_RESP=$($AWS get-public-key --key-id "$RSA_SIGN_KEY_ID" 2>/dev/null || true)
  GPK_PUB=$(echo "$GPK_RESP" | jq -r '.PublicKey // empty' 2>/dev/null || true)
  GPK_KEY_ID=$(echo "$GPK_RESP" | jq -r '.KeyId // empty' 2>/dev/null || true)
  GPK_SPEC=$(echo "$GPK_RESP" | jq -r '.KeySpec // empty' 2>/dev/null || true)
  GPK_USAGE=$(echo "$GPK_RESP" | jq -r '.KeyUsage // empty' 2>/dev/null || true)
  if [[ -n "$GPK_PUB" && -n "$GPK_KEY_ID" && "$GPK_SPEC" == "RSA_2048" && "$GPK_USAGE" == "SIGN_VERIFY" ]]; then
    ok "GetPublicKey (PublicKey, KeyId, KeySpec=RSA_2048, KeyUsage=SIGN_VERIFY present)"
  else
    fail "GetPublicKey (unexpected response: pub='$GPK_PUB' id='$GPK_KEY_ID' spec='$GPK_SPEC' usage='$GPK_USAGE')"
  fi
else
  fail "GetPublicKey (skipped: no RSA SIGN_VERIFY key)"
fi

# ---------------------------------------------------------------------------
# GenerateMac / VerifyMac — HMAC_256 key
# ---------------------------------------------------------------------------
HMAC_KEY_RESP=$($AWS create-key \
  --key-spec HMAC_256 \
  --key-usage GENERATE_VERIFY_MAC \
  --description "kumolo-e2e-hmac" \
  2>/dev/null || true)
HMAC_KEY_ID=$(echo "$HMAC_KEY_RESP" | jq -r '.KeyMetadata.KeyId // empty' 2>/dev/null || true)

if [[ -n "$HMAC_KEY_ID" ]]; then
  GMAC_RESP=$($AWS generate-mac \
    --key-id "$HMAC_KEY_ID" \
    --message "$PLAINTEXT_INPUT" \
    --mac-algorithm HMAC_SHA_256 \
    2>/dev/null || true)
  HMAC_MAC=$(echo "$GMAC_RESP" | jq -r '.Mac // empty' 2>/dev/null || true)
  GMAC_KEY_ID=$(echo "$GMAC_RESP" | jq -r '.KeyId // empty' 2>/dev/null || true)
  GMAC_ALGO=$(echo "$GMAC_RESP" | jq -r '.MacAlgorithm // empty' 2>/dev/null || true)
  if [[ -n "$HMAC_MAC" && -n "$GMAC_KEY_ID" && "$GMAC_ALGO" == "HMAC_SHA_256" ]]; then
    ok "GenerateMac (Mac, KeyId, MacAlgorithm=HMAC_SHA_256 present)"
  else
    fail "GenerateMac (missing fields: mac='$HMAC_MAC' key='$GMAC_KEY_ID' algo='$GMAC_ALGO')"
  fi

  if [[ -n "$HMAC_MAC" ]]; then
    VMAC_RESP=$($AWS verify-mac \
      --key-id "$HMAC_KEY_ID" \
      --message "$PLAINTEXT_INPUT" \
      --mac-algorithm HMAC_SHA_256 \
      --mac "$HMAC_MAC" \
      2>/dev/null || true)
    VMAC_RESULT=$(echo "$VMAC_RESP" | jq -r '.MacValid // empty' 2>/dev/null || true)
    if [[ "$VMAC_RESULT" == "true" ]]; then
      ok "VerifyMac (MacValid=true)"
    else
      fail "VerifyMac (expected MacValid=true, got '$VMAC_RESULT')"
    fi
  else
    fail "VerifyMac (skipped: no Mac from GenerateMac)"
  fi
else
  fail "GenerateMac (skipped: HMAC key creation failed)"
  fail "VerifyMac (skipped: HMAC key creation failed)"
fi

# ---------------------------------------------------------------------------
# GenerateDataKeyPair / GenerateDataKeyPairWithoutPlaintext
# ---------------------------------------------------------------------------
if [[ -n "$KEY_ID" ]]; then
  GDKP_RESP=$($AWS generate-data-key-pair \
    --key-id "$KEY_ID" \
    --key-pair-spec RSA_2048 \
    2>/dev/null || true)
  GDKP_PRIV_PLAIN=$(echo "$GDKP_RESP" | jq -r '.PrivateKeyPlaintext // empty' 2>/dev/null || true)
  GDKP_PRIV_CIPHER=$(echo "$GDKP_RESP" | jq -r '.PrivateKeyCiphertextBlob // empty' 2>/dev/null || true)
  GDKP_PUB=$(echo "$GDKP_RESP" | jq -r '.PublicKey // empty' 2>/dev/null || true)
  GDKP_SPEC=$(echo "$GDKP_RESP" | jq -r '.KeyPairSpec // empty' 2>/dev/null || true)
  if [[ -n "$GDKP_PRIV_PLAIN" && -n "$GDKP_PRIV_CIPHER" && -n "$GDKP_PUB" && "$GDKP_SPEC" == "RSA_2048" ]]; then
    ok "GenerateDataKeyPair (PrivateKeyPlaintext, PrivateKeyCiphertextBlob, PublicKey, KeyPairSpec=RSA_2048 present)"
  else
    fail "GenerateDataKeyPair (missing fields: plain='$GDKP_PRIV_PLAIN' cipher='$GDKP_PRIV_CIPHER' pub='$GDKP_PUB' spec='$GDKP_SPEC')"
  fi

  GDKPWP_RESP=$($AWS generate-data-key-pair-without-plaintext \
    --key-id "$KEY_ID" \
    --key-pair-spec RSA_2048 \
    2>/dev/null || true)
  GDKPWP_PRIV_PLAIN=$(echo "$GDKPWP_RESP" | jq -r '.PrivateKeyPlaintext // empty' 2>/dev/null || true)
  GDKPWP_PRIV_CIPHER=$(echo "$GDKPWP_RESP" | jq -r '.PrivateKeyCiphertextBlob // empty' 2>/dev/null || true)
  GDKPWP_PUB=$(echo "$GDKPWP_RESP" | jq -r '.PublicKey // empty' 2>/dev/null || true)
  GDKPWP_SPEC=$(echo "$GDKPWP_RESP" | jq -r '.KeyPairSpec // empty' 2>/dev/null || true)
  if [[ -n "$GDKPWP_PRIV_CIPHER" && -n "$GDKPWP_PUB" && "$GDKPWP_SPEC" == "RSA_2048" && -z "$GDKPWP_PRIV_PLAIN" ]]; then
    ok "GenerateDataKeyPairWithoutPlaintext (PrivateKeyCiphertextBlob and PublicKey present, no PrivateKeyPlaintext)"
  else
    fail "GenerateDataKeyPairWithoutPlaintext (unexpected fields: cipher='$GDKPWP_PRIV_CIPHER' pub='$GDKPWP_PUB' spec='$GDKPWP_SPEC' plain='$GDKPWP_PRIV_PLAIN')"
  fi
else
  fail "GenerateDataKeyPair (skipped: no KeyId)"
  fail "GenerateDataKeyPairWithoutPlaintext (skipped: no KeyId)"
fi

# ---------------------------------------------------------------------------
# GenerateRandom
# ---------------------------------------------------------------------------
GR_RESP=$($AWS generate-random --number-of-bytes 32 2>/dev/null || true)
GR_PLAINTEXT=$(echo "$GR_RESP" | jq -r '.Plaintext // empty' 2>/dev/null || true)
if [[ -n "$GR_PLAINTEXT" ]]; then
  ok "GenerateRandom (Plaintext present for 32 bytes)"
else
  fail "GenerateRandom (no Plaintext returned)"
fi

# ---------------------------------------------------------------------------
# Grants: CreateGrant / ListGrants / RevokeGrant / RetireGrant / ListRetirableGrants
# ---------------------------------------------------------------------------
if [[ -n "$KEY_ID" ]]; then
  GRANT_PRINCIPAL="arn:aws:iam::000000000000:user/kumolo-e2e"
  RETIRING_PRINCIPAL="arn:aws:iam::000000000000:user/kumolo-e2e-retire"

  GRANT_RESP=$($AWS create-grant \
    --key-id "$KEY_ID" \
    --grantee-principal "$GRANT_PRINCIPAL" \
    --operations Encrypt Decrypt \
    --name "kumolo-e2e-grant" \
    2>/dev/null || true)
  GRANT_ID=$(echo "$GRANT_RESP" | jq -r '.GrantId // empty' 2>/dev/null || true)
  GRANT_TOKEN=$(echo "$GRANT_RESP" | jq -r '.GrantToken // empty' 2>/dev/null || true)
  if [[ -n "$GRANT_ID" && -n "$GRANT_TOKEN" ]]; then
    ok "CreateGrant (GrantId and GrantToken present)"
  else
    fail "CreateGrant (missing GrantId or GrantToken)"
  fi

  LG_RESP=$($AWS list-grants --key-id "$KEY_ID" 2>/dev/null || true)
  LG_COUNT=$(echo "$LG_RESP" | jq '[.Grants[] | select(.GrantId == "'"$GRANT_ID"'")] | length' 2>/dev/null || echo 0)
  if [[ "$LG_COUNT" -ge 1 ]]; then
    ok "ListGrants (created grant present)"
  else
    fail "ListGrants (created grant not found)"
  fi

  # Create a second grant with a RetiringPrincipal for RetireGrant / ListRetirableGrants.
  GRANT2_RESP=$($AWS create-grant \
    --key-id "$KEY_ID" \
    --grantee-principal "$GRANT_PRINCIPAL" \
    --retiring-principal "$RETIRING_PRINCIPAL" \
    --operations Decrypt \
    --name "kumolo-e2e-grant-retire" \
    2>/dev/null || true)
  GRANT2_ID=$(echo "$GRANT2_RESP" | jq -r '.GrantId // empty' 2>/dev/null || true)

  LRG_RESP=$($AWS list-retirable-grants --retiring-principal "$RETIRING_PRINCIPAL" 2>/dev/null || true)
  LRG_COUNT=$(echo "$LRG_RESP" | jq '[.Grants[] | select(.GrantId == "'"$GRANT2_ID"'")] | length' 2>/dev/null || echo 0)
  if [[ "$LRG_COUNT" -ge 1 ]]; then
    ok "ListRetirableGrants (grant with RetiringPrincipal present)"
  else
    fail "ListRetirableGrants (grant not found for retiring principal)"
  fi

  if [[ -n "$GRANT2_ID" ]]; then
    run "RetireGrant (by KeyId and GrantId)" \
      $AWS retire-grant --key-id "$KEY_ID" --grant-id "$GRANT2_ID"
  else
    fail "RetireGrant (skipped: second grant creation failed)"
  fi

  if [[ -n "$GRANT_ID" ]]; then
    run "RevokeGrant" \
      $AWS revoke-grant --key-id "$KEY_ID" --grant-id "$GRANT_ID"

    LG_AFTER_RESP=$($AWS list-grants --key-id "$KEY_ID" 2>/dev/null || true)
    LG_AFTER_COUNT=$(echo "$LG_AFTER_RESP" | jq '[.Grants[] | select(.GrantId == "'"$GRANT_ID"'")] | length' 2>/dev/null || echo 0)
    if [[ "$LG_AFTER_COUNT" -eq 0 ]]; then
      ok "RevokeGrant (grant removed from ListGrants)"
    else
      fail "RevokeGrant (grant still present in ListGrants after revoke)"
    fi
  else
    fail "RevokeGrant (skipped: no GrantId)"
    fail "RevokeGrant (grant removed from ListGrants) (skipped)"
  fi
else
  fail "CreateGrant (skipped: no KeyId)"
  fail "ListGrants (skipped: no KeyId)"
  fail "ListRetirableGrants (skipped: no KeyId)"
  fail "RetireGrant (skipped: no KeyId)"
  fail "RevokeGrant (skipped: no KeyId)"
  fail "RevokeGrant (grant removed from ListGrants) (skipped)"
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

  if $AWS cancel-key-deletion --key-id "$KEY_ID" > /dev/null 2>&1; then
    ok "CancelKeyDeletion"
  else
    fail "CancelKeyDeletion"
  fi

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
