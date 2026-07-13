#!/usr/bin/env bash
set -euo pipefail

ENDPOINT="${KUMOLO_ENDPOINT:-http://localhost:5566}"

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

AWS="aws --endpoint-url $ENDPOINT cognito-idp"
PASS=0
FAIL=0

ok()   { echo "  PASS: $*"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $*"; FAIL=$((FAIL + 1)); }
skip() { echo "  SKIP: $*"; }

run() {
  local label="$1"; shift
  if "$@" > /dev/null 2>&1; then
    ok "$label"
  else
    fail "$label"
  fi
}

echo "=== Cognito ==="

POOL_ID=""
CLIENT_ID=""
DP_POOL_ID=""

cleanup() {
  if [[ -n "$CLIENT_ID" && "$CLIENT_ID" != "UNKNOWN" ]]; then
    $AWS delete-user-pool-client \
      --user-pool-id "$POOL_ID" \
      --client-id "$CLIENT_ID" >/dev/null 2>&1 || true
  fi
  if [[ -n "$POOL_ID" && "$POOL_ID" != "us-east-1_UNKNOWN" ]]; then
    $AWS delete-user-pool --user-pool-id "$POOL_ID" >/dev/null 2>&1 || true
  fi
  if [[ -n "$DP_POOL_ID" ]]; then
    $AWS update-user-pool --user-pool-id "$DP_POOL_ID" --deletion-protection "INACTIVE" >/dev/null 2>&1 || true
    $AWS delete-user-pool --user-pool-id "$DP_POOL_ID" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# UserPool CRUD
# ---------------------------------------------------------------------------
echo ""
echo "--- UserPool CRUD ---"

POOL_JSON=$($AWS create-user-pool --pool-name "e2e-pool" 2>&1)
if echo "$POOL_JSON" | grep -q '"Id"'; then
  ok "CreateUserPool"
  POOL_ID=$(echo "$POOL_JSON" | jq -r '.UserPool.Id // empty' 2>/dev/null || true)
  POOL_ARN=$(echo "$POOL_JSON" | jq -r '.UserPool.Arn // empty' 2>/dev/null || true)
else
  fail "CreateUserPool"
  POOL_ID="us-east-1_UNKNOWN"
  POOL_ARN=""
fi

run "DescribeUserPool" \
  $AWS describe-user-pool --user-pool-id "$POOL_ID"

DESCRIBE_JSON=$($AWS describe-user-pool --user-pool-id "$POOL_ID" 2>&1)
if echo "$DESCRIBE_JSON" | jq -e '
    .UserPool.AccountRecoverySetting.RecoveryMechanisms == [
      {"Name":"verified_phone_number","Priority":1},
      {"Name":"verified_email","Priority":2}
    ]' >/dev/null 2>&1; then
  ok "DescribeUserPool — AccountRecoverySetting defaults to phone-then-email"
else
  fail "DescribeUserPool — expected default AccountRecoverySetting (phone-then-email)"
fi

run "UpdateUserPool" \
  $AWS update-user-pool --user-pool-id "$POOL_ID" --mfa-configuration "OFF"

# ListUserPools
LIST_JSON=$($AWS list-user-pools --max-results 10 2>&1)
if echo "$LIST_JSON" | grep -q '"UserPools"'; then
  ok "ListUserPools"
else
  fail "ListUserPools"
fi

# DeleteUserPool must be rejected while DeletionProtection is ACTIVE, then
# succeed once it's switched back to INACTIVE. Uses an isolated pool so the
# script's shared $POOL_ID (deleted by the cleanup trap) is unaffected.
DP_POOL_JSON=$($AWS create-user-pool --pool-name "e2e-deletion-protection-pool" \
  --deletion-protection "ACTIVE" 2>&1)
DP_POOL_ID=$(echo "$DP_POOL_JSON" | jq -r '.UserPool.Id // empty' 2>/dev/null || true)
if [[ -n "$DP_POOL_ID" ]]; then
  ok "CreateUserPool (DeletionProtection=ACTIVE)"

  DP_DELETE_JSON=$($AWS delete-user-pool --user-pool-id "$DP_POOL_ID" 2>&1) || true
  if echo "$DP_DELETE_JSON" | grep -qi 'InvalidParameterException'; then
    ok "DeleteUserPool — rejected while DeletionProtection is ACTIVE"
  else
    fail "DeleteUserPool — expected InvalidParameterException while DeletionProtection is ACTIVE"
  fi

  run "UpdateUserPool (DeletionProtection=INACTIVE)" \
    $AWS update-user-pool --user-pool-id "$DP_POOL_ID" --deletion-protection "INACTIVE"

  run "DeleteUserPool — succeeds after DeletionProtection is deactivated" \
    $AWS delete-user-pool --user-pool-id "$DP_POOL_ID"
else
  fail "CreateUserPool (DeletionProtection=ACTIVE)"
fi

# ---------------------------------------------------------------------------
# UserPoolClient CRUD
# ---------------------------------------------------------------------------
echo ""
echo "--- UserPoolClient CRUD ---"

CLIENT_JSON=$($AWS create-user-pool-client \
  --user-pool-id "$POOL_ID" \
  --client-name "e2e-client" 2>&1)
if echo "$CLIENT_JSON" | grep -q '"ClientId"'; then
  ok "CreateUserPoolClient"
  CLIENT_ID=$(echo "$CLIENT_JSON" | jq -r '.UserPoolClient.ClientId // empty' 2>/dev/null || true)
else
  fail "CreateUserPoolClient"
  CLIENT_ID="UNKNOWN"
fi

run "DescribeUserPoolClient" \
  $AWS describe-user-pool-client \
    --user-pool-id "$POOL_ID" \
    --client-id "$CLIENT_ID"

run "UpdateUserPoolClient" \
  $AWS update-user-pool-client \
    --user-pool-id "$POOL_ID" \
    --client-id "$CLIENT_ID" \
    --client-name "e2e-client-updated"

LIST_CLIENTS_JSON=$($AWS list-user-pool-clients \
  --user-pool-id "$POOL_ID" --max-results 10 2>&1)
if echo "$LIST_CLIENTS_JSON" | grep -q '"UserPoolClients"'; then
  ok "ListUserPoolClients"
else
  fail "ListUserPoolClients"
fi

# ---------------------------------------------------------------------------
# Auth flows
# ---------------------------------------------------------------------------
echo ""
echo "--- Auth flows ---"

USERNAME="e2e-user@example.com"
PASSWORD="Password1!"

SIGNUP_JSON=$($AWS sign-up \
  --client-id "$CLIENT_ID" \
  --username "$USERNAME" \
  --password "$PASSWORD" \
  --user-attributes "Name=email,Value=$USERNAME" 2>&1)
if echo "$SIGNUP_JSON" | grep -q '"UserSub"'; then
  ok "SignUp"
else
  fail "SignUp"
fi

# Obtain the confirmation code.
# kumolo logs the code at INFO level: "SignUp confirmation code ... code=XXXXXX"
# Try docker compose logs first; fall back to E2E_COGNITO_CODE env var.
CONFIRM_CODE="${E2E_COGNITO_CODE:-}"
if [[ -z "$CONFIRM_CODE" ]]; then
  if command -v docker &>/dev/null && docker compose ps --services 2>/dev/null | grep -q .; then
    CONFIRM_CODE=$(docker compose logs 2>/dev/null \
      | grep 'SignUp confirmation code' \
      | grep "$USERNAME" \
      | tail -1 \
      | grep -oE 'code=[0-9]+' \
      | cut -d= -f2 || true)
  fi
fi

if [[ -n "$CONFIRM_CODE" ]]; then
  run "ConfirmSignUp" \
    $AWS confirm-sign-up \
      --client-id "$CLIENT_ID" \
      --username "$USERNAME" \
      --confirmation-code "$CONFIRM_CODE"

  AUTH_JSON=$($AWS initiate-auth \
    --client-id "$CLIENT_ID" \
    --auth-flow "USER_PASSWORD_AUTH" \
    --auth-parameters "USERNAME=$USERNAME,PASSWORD=$PASSWORD" 2>&1)
  if echo "$AUTH_JSON" | grep -q '"AccessToken"'; then
    ok "InitiateAuth (USER_PASSWORD_AUTH)"
  else
    fail "InitiateAuth (USER_PASSWORD_AUTH)"
  fi

  # Refresh token
  REFRESH_TOKEN=$(echo "$AUTH_JSON" | jq -r '.AuthenticationResult.RefreshToken // empty' 2>/dev/null || true)
  if [[ -n "$REFRESH_TOKEN" ]]; then
    run "InitiateAuth (REFRESH_TOKEN_AUTH)" \
      $AWS initiate-auth \
        --client-id "$CLIENT_ID" \
        --auth-flow "REFRESH_TOKEN_AUTH" \
        --auth-parameters "REFRESH_TOKEN=$REFRESH_TOKEN"
  else
    skip "InitiateAuth (REFRESH_TOKEN_AUTH) — could not extract refresh token"
  fi

  # GetUser (requires a valid access token)
  ACCESS_TOKEN=$(echo "$AUTH_JSON" | jq -r '.AuthenticationResult.AccessToken // empty' 2>/dev/null || true)
  if [[ -n "$ACCESS_TOKEN" ]]; then
    run "GetUser" \
      $AWS get-user --access-token "$ACCESS_TOKEN"
  else
    skip "GetUser — could not extract access token"
  fi
else
  skip "ConfirmSignUp — no confirmation code available"
  skip "InitiateAuth  — skipped (user not confirmed)"
  echo "  Hint: set E2E_COGNITO_CODE=<code> from kumolo logs, or use Docker Compose"
fi

# ---------------------------------------------------------------------------
# Refresh token expiry
# ---------------------------------------------------------------------------
echo ""
echo "--- Refresh token expiry ---"

# Create a client with explicit refresh_token_validity to verify the value is persisted.
# ALLOW_USER_PASSWORD_AUTH is required so the happy-path re-auth step below can use
# USER_PASSWORD_AUTH on this client (real AWS rejects the flow without this flag).
RT_CLIENT_JSON=$($AWS create-user-pool-client \
  --user-pool-id "$POOL_ID" \
  --client-name "e2e-rt-validity-client" \
  --refresh-token-validity 7 \
  --explicit-auth-flows ALLOW_USER_PASSWORD_AUTH ALLOW_REFRESH_TOKEN_AUTH 2>&1)
if echo "$RT_CLIENT_JSON" | grep -q '"ClientId"'; then
  RT_VALIDITY=$(echo "$RT_CLIENT_JSON" | jq -r '.UserPoolClient.RefreshTokenValidity // empty' 2>/dev/null || true)
  if [[ "$RT_VALIDITY" == "7" ]]; then
    ok "CreateUserPoolClient (refresh_token_validity=7)"
  else
    fail "CreateUserPoolClient (refresh_token_validity=7) — expected 7, got ${RT_VALIDITY:-<empty>}"
  fi
  RT_CLIENT_ID=$(echo "$RT_CLIENT_JSON" | jq -r '.UserPoolClient.ClientId // empty' 2>/dev/null || true)
else
  fail "CreateUserPoolClient (refresh_token_validity=7)"
  RT_CLIENT_ID="UNKNOWN"
fi

# Invalid refresh token must be rejected with NotAuthorizedException
INVALID_RT_JSON=$($AWS initiate-auth \
  --client-id "$RT_CLIENT_ID" \
  --auth-flow "REFRESH_TOKEN_AUTH" \
  --auth-parameters "REFRESH_TOKEN=not-a-real-token" 2>&1) || true
if echo "$INVALID_RT_JSON" | grep -qi 'NotAuthorizedException'; then
  ok "InitiateAuth (REFRESH_TOKEN_AUTH) — NotAuthorizedException for invalid token"
else
  fail "InitiateAuth (REFRESH_TOKEN_AUTH) — expected NotAuthorizedException for invalid token"
fi

# Happy path: re-auth with the client that has explicit refresh_token_validity
# REFRESH_TOKEN is set earlier in the "Auth flows" section when CONFIRM_CODE is available
if [[ -n "${REFRESH_TOKEN:-}" ]]; then
  RT_AUTH_JSON=$($AWS initiate-auth \
    --client-id "$RT_CLIENT_ID" \
    --auth-flow "USER_PASSWORD_AUTH" \
    --auth-parameters "USERNAME=$USERNAME,PASSWORD=$PASSWORD" 2>&1)
  RT_REFRESH_TOKEN=$(echo "$RT_AUTH_JSON" | jq -r '.AuthenticationResult.RefreshToken // empty' 2>/dev/null || true)
  if [[ -n "$RT_REFRESH_TOKEN" ]]; then
    RT_REFRESH_RESP=$($AWS initiate-auth \
      --client-id "$RT_CLIENT_ID" \
      --auth-flow "REFRESH_TOKEN_AUTH" \
      --auth-parameters "REFRESH_TOKEN=$RT_REFRESH_TOKEN" 2>&1)
    if echo "$RT_REFRESH_RESP" | grep -q '"AccessToken"'; then
      ok "InitiateAuth (REFRESH_TOKEN_AUTH) — new AccessToken issued for client with refresh_token_validity=7"
    else
      fail "InitiateAuth (REFRESH_TOKEN_AUTH) — expected AccessToken for client with explicit validity"
    fi
    # AWS does not return a new refresh token on REFRESH_TOKEN_AUTH
    NEW_RT=$(echo "$RT_REFRESH_RESP" | jq -r '.AuthenticationResult.RefreshToken // empty' 2>/dev/null || true)
    if [[ -z "$NEW_RT" ]]; then
      ok "InitiateAuth (REFRESH_TOKEN_AUTH) — no new refresh token in response (matches AWS behavior)"
    else
      fail "InitiateAuth (REFRESH_TOKEN_AUTH) — unexpected new refresh token returned"
    fi
  else
    skip "InitiateAuth (REFRESH_TOKEN_AUTH) explicit validity — could not obtain refresh token"
  fi
else
  skip "InitiateAuth (REFRESH_TOKEN_AUTH) explicit validity — no confirmed user"
  echo "  Hint: set E2E_COGNITO_CODE=<code> from kumolo logs, or use Docker Compose"
fi

$AWS delete-user-pool-client \
  --user-pool-id "$POOL_ID" \
  --client-id "$RT_CLIENT_ID" >/dev/null 2>&1 || true

# ---------------------------------------------------------------------------
# TokenValidityUnits (AccessTokenValidity/IdTokenValidity honored per-client)
# ---------------------------------------------------------------------------
echo ""
echo "--- TokenValidityUnits ---"

TVU_CLIENT_JSON=$($AWS create-user-pool-client \
  --user-pool-id "$POOL_ID" \
  --client-name "e2e-tvu-client" \
  --access-token-validity 10 \
  --id-token-validity 30 \
  --token-validity-units AccessToken=minutes,IdToken=minutes \
  --explicit-auth-flows ALLOW_USER_PASSWORD_AUTH ALLOW_REFRESH_TOKEN_AUTH 2>&1)
if echo "$TVU_CLIENT_JSON" | grep -q '"ClientId"'; then
  TVU_ACCESS_UNIT=$(echo "$TVU_CLIENT_JSON" | jq -r '.UserPoolClient.TokenValidityUnits.AccessToken // empty' 2>/dev/null || true)
  if [[ "$TVU_ACCESS_UNIT" == "minutes" ]]; then
    ok "CreateUserPoolClient (token_validity_units=minutes)"
  else
    fail "CreateUserPoolClient (token_validity_units=minutes) — expected minutes, got ${TVU_ACCESS_UNIT:-<empty>}"
  fi
  TVU_CLIENT_ID=$(echo "$TVU_CLIENT_JSON" | jq -r '.UserPoolClient.ClientId // empty' 2>/dev/null || true)
else
  fail "CreateUserPoolClient (token_validity_units=minutes)"
  TVU_CLIENT_ID="UNKNOWN"
fi

TVU_USERNAME="e2e-tvu-user@example.com"

TVU_SIGNUP_JSON=$($AWS sign-up \
  --client-id "$TVU_CLIENT_ID" \
  --username "$TVU_USERNAME" \
  --password "$PASSWORD" \
  --user-attributes "Name=email,Value=$TVU_USERNAME" 2>&1)
if echo "$TVU_SIGNUP_JSON" | grep -q '"UserSub"'; then
  ok "SignUp (TokenValidityUnits client)"
else
  fail "SignUp (TokenValidityUnits client)"
fi

TVU_CONFIRM_CODE="${E2E_COGNITO_CODE:-}"
if [[ -z "$TVU_CONFIRM_CODE" ]]; then
  if command -v docker &>/dev/null && docker compose ps --services 2>/dev/null | grep -q .; then
    TVU_CONFIRM_CODE=$(docker compose logs 2>/dev/null \
      | grep 'SignUp confirmation code' \
      | grep "$TVU_USERNAME" \
      | tail -1 \
      | grep -oE 'code=[0-9]+' \
      | cut -d= -f2 || true)
  fi
fi

if [[ -n "$TVU_CONFIRM_CODE" ]]; then
  run "ConfirmSignUp (TokenValidityUnits client)" \
    $AWS confirm-sign-up \
      --client-id "$TVU_CLIENT_ID" \
      --username "$TVU_USERNAME" \
      --confirmation-code "$TVU_CONFIRM_CODE"

  TVU_AUTH_JSON=$($AWS initiate-auth \
    --client-id "$TVU_CLIENT_ID" \
    --auth-flow "USER_PASSWORD_AUTH" \
    --auth-parameters "USERNAME=$TVU_USERNAME,PASSWORD=$PASSWORD" 2>&1)
  TVU_EXPIRES_IN=$(echo "$TVU_AUTH_JSON" | jq -r '.AuthenticationResult.ExpiresIn // empty' 2>/dev/null || true)
  if [[ "$TVU_EXPIRES_IN" == "600" ]]; then
    ok "InitiateAuth — ExpiresIn=600 for AccessTokenValidity=10 with unit=minutes"
  else
    fail "InitiateAuth — expected ExpiresIn=600, got ${TVU_EXPIRES_IN:-<empty>}"
  fi
else
  skip "ConfirmSignUp (TokenValidityUnits client) — no confirmation code available"
  skip "InitiateAuth (TokenValidityUnits client) — skipped (user not confirmed)"
  echo "  Hint: set E2E_COGNITO_CODE=<code> from kumolo logs, or use Docker Compose"
fi

$AWS delete-user-pool-client \
  --user-pool-id "$POOL_ID" \
  --client-id "$TVU_CLIENT_ID" >/dev/null 2>&1 || true

# ---------------------------------------------------------------------------
# ResendConfirmationCode
# ---------------------------------------------------------------------------
echo ""
echo "--- ResendConfirmationCode ---"

RESEND_USER="resend-e2e@example.com"
RESEND_PASS="Password1!"

RESEND_SIGNUP_JSON=$($AWS sign-up \
  --client-id "$CLIENT_ID" \
  --username "$RESEND_USER" \
  --password "$RESEND_PASS" \
  --user-attributes "Name=email,Value=$RESEND_USER" 2>&1)
if echo "$RESEND_SIGNUP_JSON" | grep -q '"UserSub"'; then
  ok "SignUp (for ResendConfirmationCode)"
else
  fail "SignUp (for ResendConfirmationCode)"
fi

RESEND_JSON=$($AWS resend-confirmation-code \
  --client-id "$CLIENT_ID" \
  --username "$RESEND_USER" 2>&1)
if echo "$RESEND_JSON" | grep -q '"CodeDeliveryDetails"'; then
  ok "ResendConfirmationCode"
else
  fail "ResendConfirmationCode"
fi

# Error: user not found
RESEND_NF_JSON=$($AWS resend-confirmation-code \
  --client-id "$CLIENT_ID" \
  --username "no-such-user-resend@example.com" 2>&1) || true
if echo "$RESEND_NF_JSON" | grep -qi 'UserNotFoundException\|does not exist'; then
  ok "ResendConfirmationCode — UserNotFoundException for unknown user"
else
  fail "ResendConfirmationCode — expected UserNotFoundException"
fi

# Confirm the user via resent code and verify already-confirmed error
RESEND_CODE="${E2E_COGNITO_RESEND_CODE:-}"
if [[ -z "$RESEND_CODE" ]]; then
  if command -v docker &>/dev/null && docker compose ps --services 2>/dev/null | grep -q .; then
    RESEND_CODE=$(docker compose logs 2>/dev/null \
      | grep 'ResendConfirmationCode' \
      | grep "$RESEND_USER" \
      | tail -1 \
      | grep -oE 'code=[0-9]+' \
      | cut -d= -f2 || true)
  fi
fi

if [[ -n "$RESEND_CODE" ]]; then
  run "ConfirmSignUp (with resent code)" \
    $AWS confirm-sign-up \
      --client-id "$CLIENT_ID" \
      --username "$RESEND_USER" \
      --confirmation-code "$RESEND_CODE"

  RESEND_CONFIRMED_JSON=$($AWS resend-confirmation-code \
    --client-id "$CLIENT_ID" \
    --username "$RESEND_USER" 2>&1) || true
  if echo "$RESEND_CONFIRMED_JSON" | grep -qi 'NotAuthorizedException'; then
    ok "ResendConfirmationCode — NotAuthorizedException for already confirmed user"
  else
    fail "ResendConfirmationCode — expected NotAuthorizedException for already confirmed user"
  fi
else
  skip "ConfirmSignUp (with resent code) — no code available"
  skip "ResendConfirmationCode already-confirmed check — skipped (user not confirmed)"
  echo "  Hint: set E2E_COGNITO_RESEND_CODE=<code> from kumolo logs, or use Docker Compose"
fi

# ---------------------------------------------------------------------------
# JWKS endpoint
# ---------------------------------------------------------------------------
echo ""
echo "--- JWKS ---"

JWKS_RESP=$(mktemp)
JWKS_JSON=$(curl -sfD "$JWKS_RESP" "$ENDPOINT/$POOL_ID/.well-known/jwks.json") || true
JWKS_CT=$(grep -i "^content-type:" "$JWKS_RESP" | tr -d '\r' | sed 's/[^:]*: //')
JWKS_KID=$(echo "$JWKS_JSON" | jq -r '.keys[0].kid // empty' 2>/dev/null)
JWKS_N=$(echo "$JWKS_JSON" | jq -r '.keys[0].n // empty' 2>/dev/null)
JWKS_E=$(echo "$JWKS_JSON" | jq -r '.keys[0].e // empty' 2>/dev/null)
if [ -n "$JWKS_KID" ] && [ -n "$JWKS_N" ] && [ -n "$JWKS_E" ]; then
  ok "JWKS endpoint — returns keys array with kid/n/e"
else
  fail "JWKS endpoint — unexpected response: $JWKS_JSON"
fi
if echo "$JWKS_CT" | grep -q "application/json"; then
  ok "JWKS endpoint — Content-Type is application/json"
else
  fail "JWKS endpoint — expected application/json Content-Type, got: $JWKS_CT"
fi

JWKS_HTTP=$(curl -s -o /dev/null -w "%{http_code}" "$ENDPOINT/us-east-1_UNKNOWN/.well-known/jwks.json")
if [[ "$JWKS_HTTP" == "404" ]]; then
  ok "JWKS unknown pool — 404"
else
  fail "JWKS unknown pool — expected 404, got $JWKS_HTTP"
fi

# ---------------------------------------------------------------------------
# Admin user operations
# ---------------------------------------------------------------------------
echo ""
echo "--- Admin user operations ---"

ADMIN_USER="admin-e2e@example.com"
ADMIN_USER_UC="admin-e2e-unconfirmed@example.com"

# AdminCreateUser with temporary password → FORCE_CHANGE_PASSWORD
ADMIN_CREATE_JSON=$($AWS admin-create-user \
  --user-pool-id "$POOL_ID" \
  --username "$ADMIN_USER" \
  --temporary-password "TempPass1!" \
  --user-attributes "Name=email,Value=$ADMIN_USER" 2>&1)
if echo "$ADMIN_CREATE_JSON" | grep -q '"Username"'; then
  ok "AdminCreateUser (with temporary password)"
else
  fail "AdminCreateUser (with temporary password)"
fi

# AdminGetUser
ADMIN_GET_JSON=$($AWS admin-get-user \
  --user-pool-id "$POOL_ID" \
  --username "$ADMIN_USER" 2>&1)
if echo "$ADMIN_GET_JSON" | grep -q '"Username"'; then
  ok "AdminGetUser"
else
  fail "AdminGetUser"
fi
if echo "$ADMIN_GET_JSON" | grep -q 'FORCE_CHANGE_PASSWORD'; then
  ok "AdminGetUser — UserStatus is FORCE_CHANGE_PASSWORD"
else
  fail "AdminGetUser — expected FORCE_CHANGE_PASSWORD"
fi

# AdminSetUserPassword (permanent=true → CONFIRMED)
run "AdminSetUserPassword (permanent)" \
  $AWS admin-set-user-password \
    --user-pool-id "$POOL_ID" \
    --username "$ADMIN_USER" \
    --password "PermanentPass1!" \
    --permanent
ADMIN_GET2_JSON=$($AWS admin-get-user \
  --user-pool-id "$POOL_ID" \
  --username "$ADMIN_USER" 2>&1)
if echo "$ADMIN_GET2_JSON" | grep -q '"CONFIRMED"'; then
  ok "AdminSetUserPassword — UserStatus changed to CONFIRMED"
else
  fail "AdminSetUserPassword — expected CONFIRMED"
fi

# AdminConfirmSignUp: sign up a new user then confirm via admin API (no code needed)
SIGNUP_UC_JSON=$($AWS sign-up \
  --client-id "$CLIENT_ID" \
  --username "$ADMIN_USER_UC" \
  --password "Password1!" \
  --user-attributes "Name=email,Value=$ADMIN_USER_UC" 2>&1)
if echo "$SIGNUP_UC_JSON" | grep -q '"UserSub"'; then
  ok "SignUp (for AdminConfirmSignUp)"
else
  fail "SignUp (for AdminConfirmSignUp)"
fi
run "AdminConfirmSignUp" \
  $AWS admin-confirm-sign-up \
    --user-pool-id "$POOL_ID" \
    --username "$ADMIN_USER_UC"
ADMIN_GET3_JSON=$($AWS admin-get-user \
  --user-pool-id "$POOL_ID" \
  --username "$ADMIN_USER_UC" 2>&1)
if echo "$ADMIN_GET3_JSON" | grep -q '"CONFIRMED"'; then
  ok "AdminConfirmSignUp — UserStatus is CONFIRMED"
else
  fail "AdminConfirmSignUp — expected CONFIRMED"
fi

# AdminDeleteUser
run "AdminDeleteUser" \
  $AWS admin-delete-user \
    --user-pool-id "$POOL_ID" \
    --username "$ADMIN_USER"
run "AdminDeleteUser (confirmed user)" \
  $AWS admin-delete-user \
    --user-pool-id "$POOL_ID" \
    --username "$ADMIN_USER_UC"

# Verify AdminGetUser returns UserNotFoundException after delete
DELETED_JSON=$($AWS admin-get-user \
  --user-pool-id "$POOL_ID" \
  --username "$ADMIN_USER" 2>&1) || true
if echo "$DELETED_JSON" | grep -qi 'UserNotFoundException\|does not exist'; then
  ok "AdminGetUser — UserNotFoundException after AdminDeleteUser"
else
  fail "AdminGetUser — expected UserNotFoundException after AdminDeleteUser"
fi

# ---------------------------------------------------------------------------
# PasswordPolicy complexity enforcement
# ---------------------------------------------------------------------------
echo ""
echo "--- PasswordPolicy complexity enforcement ---"

# Uses a dedicated pool/client so custom Policies don't affect the shared
# $POOL_ID used by the rest of this script. Unlike the Go SDK (which never
# serializes RequireX=false — see tests/integration/cognito_test.go), the CLI
# sends whatever JSON is given verbatim, so this is the only e2e path that can
# exercise an explicit "false" override alongside omitted fields.
PW_POOL_JSON=$($AWS create-user-pool --pool-name "e2e-pwpolicy-pool" \
  --policies '{"PasswordPolicy":{"MinimumLength":10,"RequireSymbols":false}}' 2>&1)
PW_POOL_ID=$(echo "$PW_POOL_JSON" | jq -r '.UserPool.Id // empty' 2>/dev/null || true)
if [[ -n "$PW_POOL_ID" ]]; then
  ok "CreateUserPool (custom PasswordPolicy: MinimumLength=10, RequireSymbols=false)"
else
  fail "CreateUserPool (custom PasswordPolicy)"
fi

PW_CLIENT_JSON=$($AWS create-user-pool-client \
  --user-pool-id "$PW_POOL_ID" \
  --client-name "e2e-pwpolicy-client" 2>&1)
PW_CLIENT_ID=$(echo "$PW_CLIENT_JSON" | jq -r '.UserPoolClient.ClientId // empty' 2>/dev/null || true)

if [[ -n "$PW_POOL_ID" && -n "$PW_CLIENT_ID" ]]; then
  # RequireSymbols is explicitly disabled, but RequireUppercase/Lowercase/Numbers
  # were omitted from the policy and must still fall back to the built-in
  # default (true) rather than being treated as disabled too.
  PW_NOUPPER_JSON=$($AWS sign-up \
    --client-id "$PW_CLIENT_ID" \
    --username "pwpolicy-noupper-user" \
    --password "lowercase123" 2>&1) || true
  if echo "$PW_NOUPPER_JSON" | grep -qi 'InvalidPasswordException'; then
    ok "SignUp — omitted RequireUppercase still enforced (defaults to true)"
  else
    fail "SignUp — expected InvalidPasswordException for missing uppercase"
  fi

  # Satisfies uppercase/lowercase/number but has no symbol — must be accepted
  # since RequireSymbols=false was explicitly set on this pool.
  PW_NOSYMBOL_JSON=$($AWS sign-up \
    --client-id "$PW_CLIENT_ID" \
    --username "pwpolicy-nosymbol-user" \
    --password "Lowercase123" 2>&1)
  if echo "$PW_NOSYMBOL_JSON" | grep -q '"UserSub"'; then
    ok "SignUp — explicit RequireSymbols=false honored"
  else
    fail "SignUp — expected explicit RequireSymbols=false to allow a symbol-less password"
  fi

  # Satisfies every category but is under the custom MinimumLength=10.
  PW_SHORT_JSON=$($AWS sign-up \
    --client-id "$PW_CLIENT_ID" \
    --username "pwpolicy-short-user" \
    --password "Short1" 2>&1) || true
  if echo "$PW_SHORT_JSON" | grep -qi 'InvalidPasswordException'; then
    ok "SignUp — custom MinimumLength=10 enforced"
  else
    fail "SignUp — expected InvalidPasswordException for password shorter than 10"
  fi

  $AWS delete-user-pool-client \
    --user-pool-id "$PW_POOL_ID" \
    --client-id "$PW_CLIENT_ID" >/dev/null 2>&1 || true
else
  skip "PasswordPolicy enforcement checks — pool/client not available"
fi

$AWS delete-user-pool --user-pool-id "$PW_POOL_ID" >/dev/null 2>&1 || true

# ---------------------------------------------------------------------------
# Group management
# ---------------------------------------------------------------------------
echo ""
echo "--- Group management ---"

GROUP_NAME="e2e-admins"
GROUP_USER="group-member@example.com"

GROUP_JSON=$($AWS create-group \
  --user-pool-id "$POOL_ID" \
  --group-name "$GROUP_NAME" \
  --description "E2E admin group" \
  --precedence 10 2>&1)
if echo "$GROUP_JSON" | grep -q '"GroupName"'; then
  ok "CreateGroup"
else
  fail "CreateGroup"
fi

# CreateGroup duplicate → GroupExistsException
DUP_GROUP_JSON=$($AWS create-group \
  --user-pool-id "$POOL_ID" \
  --group-name "$GROUP_NAME" 2>&1) || true
if echo "$DUP_GROUP_JSON" | grep -qi 'GroupExistsException'; then
  ok "CreateGroup — GroupExistsException for duplicate name"
else
  fail "CreateGroup — expected GroupExistsException for duplicate name"
fi

# GetGroup
GET_GROUP_JSON=$($AWS get-group \
  --user-pool-id "$POOL_ID" \
  --group-name "$GROUP_NAME" 2>&1)
if echo "$GET_GROUP_JSON" | grep -q '"GroupName"'; then
  ok "GetGroup"
else
  fail "GetGroup"
fi
if echo "$GET_GROUP_JSON" | grep -q 'E2E admin group'; then
  ok "GetGroup — description matches"
else
  fail "GetGroup — expected description"
fi

# UpdateGroup
run "UpdateGroup" \
  $AWS update-group \
    --user-pool-id "$POOL_ID" \
    --group-name "$GROUP_NAME" \
    --description "Updated description" \
    --precedence 5

UPDATED_GROUP_JSON=$($AWS get-group \
  --user-pool-id "$POOL_ID" \
  --group-name "$GROUP_NAME" 2>&1)
if echo "$UPDATED_GROUP_JSON" | grep -q 'Updated description'; then
  ok "UpdateGroup — description updated"
else
  fail "UpdateGroup — expected updated description"
fi

# ListGroups
LIST_GROUPS_JSON=$($AWS list-groups \
  --user-pool-id "$POOL_ID" 2>&1)
if echo "$LIST_GROUPS_JSON" | grep -q '"Groups"'; then
  ok "ListGroups"
else
  fail "ListGroups"
fi
if echo "$LIST_GROUPS_JSON" | grep -q "$GROUP_NAME"; then
  ok "ListGroups — created group appears in list"
else
  fail "ListGroups — expected group in list"
fi

# GetGroup not found
GET_NF_JSON=$($AWS get-group \
  --user-pool-id "$POOL_ID" \
  --group-name "no-such-group-e2e" 2>&1) || true
if echo "$GET_NF_JSON" | grep -qi 'ResourceNotFoundException'; then
  ok "GetGroup — ResourceNotFoundException for unknown group"
else
  fail "GetGroup — expected ResourceNotFoundException"
fi

# ---------------------------------------------------------------------------
# Group membership
# ---------------------------------------------------------------------------
echo ""
echo "--- Group membership ---"

# Create a user to add to the group
GROUP_USER_CREATE_JSON=$($AWS admin-create-user \
  --user-pool-id "$POOL_ID" \
  --username "$GROUP_USER" \
  --user-attributes "Name=email,Value=$GROUP_USER" 2>&1)
if echo "$GROUP_USER_CREATE_JSON" | grep -q '"Username"'; then
  ok "AdminCreateUser (for group membership)"
else
  fail "AdminCreateUser (for group membership)"
fi

# AdminAddUserToGroup
run "AdminAddUserToGroup" \
  $AWS admin-add-user-to-group \
    --user-pool-id "$POOL_ID" \
    --group-name "$GROUP_NAME" \
    --username "$GROUP_USER"

# AdminListGroupsForUser
GROUPS_FOR_USER_JSON=$($AWS admin-list-groups-for-user \
  --user-pool-id "$POOL_ID" \
  --username "$GROUP_USER" 2>&1)
if echo "$GROUPS_FOR_USER_JSON" | grep -q '"Groups"'; then
  ok "AdminListGroupsForUser"
else
  fail "AdminListGroupsForUser"
fi
if echo "$GROUPS_FOR_USER_JSON" | grep -q "$GROUP_NAME"; then
  ok "AdminListGroupsForUser — group appears in user's groups"
else
  fail "AdminListGroupsForUser — expected group in user's groups"
fi

# ListUsersInGroup
USERS_IN_GROUP_JSON=$($AWS list-users-in-group \
  --user-pool-id "$POOL_ID" \
  --group-name "$GROUP_NAME" 2>&1)
if echo "$USERS_IN_GROUP_JSON" | grep -q '"Users"'; then
  ok "ListUsersInGroup"
else
  fail "ListUsersInGroup"
fi
if echo "$USERS_IN_GROUP_JSON" | grep -q "$GROUP_USER"; then
  ok "ListUsersInGroup — added user appears in group"
else
  fail "ListUsersInGroup — expected user in group"
fi

# AdminAddUserToGroup — user not found
ADD_NF_JSON=$($AWS admin-add-user-to-group \
  --user-pool-id "$POOL_ID" \
  --group-name "$GROUP_NAME" \
  --username "no-such-user-e2e@example.com" 2>&1) || true
if echo "$ADD_NF_JSON" | grep -qi 'UserNotFoundException'; then
  ok "AdminAddUserToGroup — UserNotFoundException for unknown user"
else
  fail "AdminAddUserToGroup — expected UserNotFoundException"
fi

# AdminRemoveUserFromGroup
run "AdminRemoveUserFromGroup" \
  $AWS admin-remove-user-from-group \
    --user-pool-id "$POOL_ID" \
    --group-name "$GROUP_NAME" \
    --username "$GROUP_USER"

GROUPS_AFTER_REMOVE_JSON=$($AWS admin-list-groups-for-user \
  --user-pool-id "$POOL_ID" \
  --username "$GROUP_USER" 2>&1)
if echo "$GROUPS_AFTER_REMOVE_JSON" | grep -q '"Groups"'; then
  GROUPS_COUNT=$(echo "$GROUPS_AFTER_REMOVE_JSON" | jq -r '.Groups | length')
  if [[ "$GROUPS_COUNT" == "0" ]]; then
    ok "AdminRemoveUserFromGroup — user no longer in group"
  else
    fail "AdminRemoveUserFromGroup — expected empty groups after removal"
  fi
else
  fail "AdminRemoveUserFromGroup — admin-list-groups-for-user failed after removal"
fi

# Delete the group (before pool/client cleanup)
run "DeleteGroup" \
  $AWS delete-group \
    --user-pool-id "$POOL_ID" \
    --group-name "$GROUP_NAME"

# DeleteGroup not found
DEL_NF_JSON=$($AWS delete-group \
  --user-pool-id "$POOL_ID" \
  --group-name "$GROUP_NAME" 2>&1) || true
if echo "$DEL_NF_JSON" | grep -qi 'ResourceNotFoundException'; then
  ok "DeleteGroup — ResourceNotFoundException after deletion"
else
  fail "DeleteGroup — expected ResourceNotFoundException after deletion"
fi

# ---------------------------------------------------------------------------
# Token revocation
# ---------------------------------------------------------------------------
echo ""
echo "--- Token revocation ---"

if [[ -n "${ACCESS_TOKEN:-}" && -n "${REFRESH_TOKEN:-}" ]]; then
  # Obtain a fresh token pair so revocation tests do not interfere with earlier steps.
  REVOKE_AUTH_JSON=$($AWS initiate-auth \
    --client-id "$CLIENT_ID" \
    --auth-flow "USER_PASSWORD_AUTH" \
    --auth-parameters "USERNAME=$USERNAME,PASSWORD=$PASSWORD" 2>&1)
  REVOKE_ACCESS_TOKEN=$(echo "$REVOKE_AUTH_JSON" | jq -r '.AuthenticationResult.AccessToken // empty' 2>/dev/null || true)
  REVOKE_REFRESH_TOKEN=$(echo "$REVOKE_AUTH_JSON" | jq -r '.AuthenticationResult.RefreshToken // empty' 2>/dev/null || true)

  if [[ -n "$REVOKE_REFRESH_TOKEN" ]]; then
    run "RevokeToken" \
      $AWS revoke-token \
        --client-id "$CLIENT_ID" \
        --token "$REVOKE_REFRESH_TOKEN"

    # Revoked refresh token must be rejected on re-use.
    REVOKE_REUSE_JSON=$($AWS initiate-auth \
      --client-id "$CLIENT_ID" \
      --auth-flow "REFRESH_TOKEN_AUTH" \
      --auth-parameters "REFRESH_TOKEN=$REVOKE_REFRESH_TOKEN" 2>&1) || true
    if echo "$REVOKE_REUSE_JSON" | grep -qi 'NotAuthorizedException'; then
      ok "RevokeToken — revoked refresh token rejected"
    else
      fail "RevokeToken — expected NotAuthorizedException for revoked refresh token"
    fi

    # Access token paired with the revoked refresh token must also be rejected.
    REVOKE_AT_JSON=$($AWS get-user --access-token "$REVOKE_ACCESS_TOKEN" 2>&1) || true
    if echo "$REVOKE_AT_JSON" | grep -qi 'NotAuthorizedException'; then
      ok "RevokeToken — paired access token rejected"
    else
      fail "RevokeToken — expected NotAuthorizedException for paired access token"
    fi
  else
    skip "RevokeToken — could not obtain fresh token pair"
  fi

  # GlobalSignOut: obtain another fresh pair.
  GSO_AUTH_JSON=$($AWS initiate-auth \
    --client-id "$CLIENT_ID" \
    --auth-flow "USER_PASSWORD_AUTH" \
    --auth-parameters "USERNAME=$USERNAME,PASSWORD=$PASSWORD" 2>&1)
  GSO_ACCESS_TOKEN=$(echo "$GSO_AUTH_JSON" | jq -r '.AuthenticationResult.AccessToken // empty' 2>/dev/null || true)
  GSO_REFRESH_TOKEN=$(echo "$GSO_AUTH_JSON" | jq -r '.AuthenticationResult.RefreshToken // empty' 2>/dev/null || true)

  if [[ -n "$GSO_ACCESS_TOKEN" ]]; then
    run "GlobalSignOut" \
      $AWS global-sign-out --access-token "$GSO_ACCESS_TOKEN"

    # Access token must be rejected after GlobalSignOut.
    GSO_AT_JSON=$($AWS get-user --access-token "$GSO_ACCESS_TOKEN" 2>&1) || true
    if echo "$GSO_AT_JSON" | grep -qi 'NotAuthorizedException'; then
      ok "GlobalSignOut — access token rejected"
    else
      fail "GlobalSignOut — expected NotAuthorizedException for access token"
    fi

    # All refresh tokens for the user must be rejected after GlobalSignOut.
    GSO_RT_JSON=$($AWS initiate-auth \
      --client-id "$CLIENT_ID" \
      --auth-flow "REFRESH_TOKEN_AUTH" \
      --auth-parameters "REFRESH_TOKEN=$GSO_REFRESH_TOKEN" 2>&1) || true
    if echo "$GSO_RT_JSON" | grep -qi 'NotAuthorizedException'; then
      ok "GlobalSignOut — refresh token rejected"
    else
      fail "GlobalSignOut — expected NotAuthorizedException for refresh token"
    fi

    # A second session's access token must also be rejected after GlobalSignOut.
    GSO2_AUTH_JSON=$($AWS initiate-auth \
      --client-id "$CLIENT_ID" \
      --auth-flow "USER_PASSWORD_AUTH" \
      --auth-parameters "USERNAME=$USERNAME,PASSWORD=$PASSWORD" 2>&1)
    GSO2_ACCESS_TOKEN=$(echo "$GSO2_AUTH_JSON" | jq -r '.AuthenticationResult.AccessToken // empty' 2>/dev/null || true)
    # Obtain a third session so we can sign out from session 2 while session 3 is open.
    GSO3_AUTH_JSON=$($AWS initiate-auth \
      --client-id "$CLIENT_ID" \
      --auth-flow "USER_PASSWORD_AUTH" \
      --auth-parameters "USERNAME=$USERNAME,PASSWORD=$PASSWORD" 2>&1)
    GSO3_ACCESS_TOKEN=$(echo "$GSO3_AUTH_JSON" | jq -r '.AuthenticationResult.AccessToken // empty' 2>/dev/null || true)
    GSO3_REFRESH_TOKEN=$(echo "$GSO3_AUTH_JSON" | jq -r '.AuthenticationResult.RefreshToken // empty' 2>/dev/null || true)
    if [[ -n "$GSO2_ACCESS_TOKEN" && -n "$GSO3_ACCESS_TOKEN" && -n "$GSO3_REFRESH_TOKEN" ]]; then
      run "GlobalSignOut (second session)" \
        $AWS global-sign-out --access-token "$GSO2_ACCESS_TOKEN"
      GSO3_AT_JSON=$($AWS get-user --access-token "$GSO3_ACCESS_TOKEN" 2>&1) || true
      if echo "$GSO3_AT_JSON" | grep -qi 'NotAuthorizedException'; then
        ok "GlobalSignOut — concurrent session access token also rejected"
      else
        fail "GlobalSignOut — expected concurrent session access token to be rejected"
      fi
      GSO3_RT_JSON=$($AWS initiate-auth \
        --client-id "$CLIENT_ID" \
        --auth-flow "REFRESH_TOKEN_AUTH" \
        --auth-parameters "REFRESH_TOKEN=$GSO3_REFRESH_TOKEN" 2>&1) || true
      if echo "$GSO3_RT_JSON" | grep -qi 'NotAuthorizedException'; then
        ok "GlobalSignOut — concurrent session refresh token also rejected"
      else
        fail "GlobalSignOut — expected concurrent session refresh token to be rejected"
      fi
    else
      skip "GlobalSignOut — could not obtain second session token pair"
    fi
  else
    skip "GlobalSignOut — could not obtain fresh access token"
  fi
else
  skip "Token revocation — no confirmed user available"
  echo "  Hint: set E2E_COGNITO_CODE=<code> from kumolo logs, or use Docker Compose"
fi

# ---------------------------------------------------------------------------
# Tagging
# ---------------------------------------------------------------------------
echo ""
echo "--- Tagging ---"

if [[ -n "${POOL_ARN:-}" ]]; then
  # TagResource
  if TAG_JSON=$($AWS tag-resource \
    --resource-arn "$POOL_ARN" \
    --tags "env=e2e,owner=kumolo" 2>&1); then
    ok "tag-resource"
  else
    fail "tag-resource: $TAG_JSON"
  fi

  # ListTagsForResource — verify both tags are present
  LIST_TAGS_JSON=$($AWS list-tags-for-resource --resource-arn "$POOL_ARN" 2>&1)
  if echo "$LIST_TAGS_JSON" | grep -q '"Tags"'; then
    ok "list-tags-for-resource"
  else
    fail "list-tags-for-resource: $LIST_TAGS_JSON"
  fi
  if echo "$LIST_TAGS_JSON" | grep -q '"e2e"'; then
    ok "list-tags-for-resource — env=e2e tag present"
  else
    fail "list-tags-for-resource — expected env=e2e tag"
  fi
  if echo "$LIST_TAGS_JSON" | grep -q '"kumolo"'; then
    ok "list-tags-for-resource — owner=kumolo tag present"
  else
    fail "list-tags-for-resource — expected owner=kumolo tag"
  fi

  # UntagResource — remove the env key
  if UNTAG_JSON=$($AWS untag-resource \
    --resource-arn "$POOL_ARN" \
    --tag-keys "env" 2>&1); then
    ok "untag-resource"
  else
    fail "untag-resource: $UNTAG_JSON"
  fi

  # Verify env is gone, owner is preserved
  LIST_TAGS_JSON2=$($AWS list-tags-for-resource --resource-arn "$POOL_ARN" 2>&1)
  if echo "$LIST_TAGS_JSON2" | grep -q '"e2e"'; then
    fail "untag-resource — env tag should have been removed"
  else
    ok "untag-resource — env tag removed"
  fi
  if echo "$LIST_TAGS_JSON2" | grep -q '"kumolo"'; then
    ok "untag-resource — owner tag preserved"
  else
    fail "untag-resource — expected owner tag to be preserved"
  fi
else
  skip "Tagging — pool ARN not available"
fi

# ---------------------------------------------------------------------------
# ListUsers
# ---------------------------------------------------------------------------
echo ""
echo "--- ListUsers ---"

LU_USER1="listusers-alice@example.com"
LU_USER2="listusers-bob@example.com"

$AWS admin-create-user \
  --user-pool-id "$POOL_ID" \
  --username "$LU_USER1" \
  --user-attributes "Name=email,Value=$LU_USER1" >/dev/null 2>&1 || true
$AWS admin-create-user \
  --user-pool-id "$POOL_ID" \
  --username "$LU_USER2" \
  --user-attributes "Name=email,Value=$LU_USER2" >/dev/null 2>&1 || true

LIST_USERS_JSON=$($AWS list-users --user-pool-id "$POOL_ID" 2>&1)
if echo "$LIST_USERS_JSON" | grep -q '"Users"'; then
  ok "ListUsers"
else
  fail "ListUsers"
fi
if echo "$LIST_USERS_JSON" | grep -q "$LU_USER1"; then
  ok "ListUsers — created user appears in list"
else
  fail "ListUsers — expected user in list"
fi

LIST_USERS_FILTER_JSON=$($AWS list-users \
  --user-pool-id "$POOL_ID" \
  --filter "username = \"$LU_USER1\"" 2>&1)
if echo "$LIST_USERS_FILTER_JSON" | grep -q "$LU_USER1" \
    && ! echo "$LIST_USERS_FILTER_JSON" | grep -q "$LU_USER2"; then
  ok "ListUsers — Filter narrows to matching username"
else
  fail "ListUsers — Filter did not narrow results as expected"
fi

LIST_USERS_LIMIT_JSON=$($AWS list-users --user-pool-id "$POOL_ID" --limit 1 2>&1)
LIST_USERS_LIMIT_COUNT=$(echo "$LIST_USERS_LIMIT_JSON" | jq -r '.Users | length' 2>/dev/null || echo "")
if [[ "$LIST_USERS_LIMIT_COUNT" == "1" ]]; then
  ok "ListUsers — Limit=1 returns exactly one user"
else
  fail "ListUsers — expected exactly one user with Limit=1, got ${LIST_USERS_LIMIT_COUNT:-<empty>}"
fi

# ---------------------------------------------------------------------------
# Self-service: UpdateUserAttributes, attribute verification, DeleteUser
# ---------------------------------------------------------------------------
echo ""
echo "--- Self-service user management ---"

SS_USER="selfservice-e2e@example.com"
SS_PASS="Password1!"

SS_SIGNUP_JSON=$($AWS sign-up \
  --client-id "$CLIENT_ID" \
  --username "$SS_USER" \
  --password "$SS_PASS" \
  --user-attributes "Name=email,Value=$SS_USER" 2>&1)
if echo "$SS_SIGNUP_JSON" | grep -q '"UserSub"'; then
  ok "SignUp (for self-service flow)"
else
  fail "SignUp (for self-service flow)"
fi

SS_CODE="${E2E_COGNITO_SS_CODE:-}"
if [[ -z "$SS_CODE" ]]; then
  if command -v docker &>/dev/null && docker compose ps --services 2>/dev/null | grep -q .; then
    SS_CODE=$(docker compose logs 2>/dev/null \
      | grep 'SignUp confirmation code' \
      | grep "$SS_USER" \
      | tail -1 \
      | grep -oE 'code=[0-9]+' \
      | cut -d= -f2 || true)
  fi
fi

if [[ -n "$SS_CODE" ]]; then
  run "ConfirmSignUp (self-service)" \
    $AWS confirm-sign-up \
      --client-id "$CLIENT_ID" \
      --username "$SS_USER" \
      --confirmation-code "$SS_CODE"

  SS_AUTH_JSON=$($AWS initiate-auth \
    --client-id "$CLIENT_ID" \
    --auth-flow "USER_PASSWORD_AUTH" \
    --auth-parameters "USERNAME=$SS_USER,PASSWORD=$SS_PASS" 2>&1)
  SS_ACCESS_TOKEN=$(echo "$SS_AUTH_JSON" | jq -r '.AuthenticationResult.AccessToken // empty' 2>/dev/null || true)

  if [[ -n "$SS_ACCESS_TOKEN" ]]; then
    # UpdateUserAttributes: non-verified attribute applies immediately, no code.
    UUA_JSON=$($AWS update-user-attributes \
      --access-token "$SS_ACCESS_TOKEN" \
      --user-attributes "Name=given_name,Value=Alice" 2>&1)
    if echo "$UUA_JSON" | grep -q '"CodeDeliveryDetailsList": \[\]'; then
      ok "UpdateUserAttributes — non-verified attribute, empty CodeDeliveryDetailsList"
    else
      fail "UpdateUserAttributes — expected empty CodeDeliveryDetailsList: $UUA_JSON"
    fi

    # UpdateUserAttributes: email change generates a verification code.
    UUA_EMAIL_JSON=$($AWS update-user-attributes \
      --access-token "$SS_ACCESS_TOKEN" \
      --user-attributes "Name=email,Value=newemail-e2e@example.com" 2>&1)
    if echo "$UUA_EMAIL_JSON" | grep -q '"AttributeName": "email"'; then
      ok "UpdateUserAttributes — email change returns CodeDeliveryDetails"
    else
      fail "UpdateUserAttributes — expected CodeDeliveryDetails for email change"
    fi

    SS_ATTR_CODE="${E2E_COGNITO_SS_ATTR_CODE:-}"
    if [[ -z "$SS_ATTR_CODE" ]]; then
      if command -v docker &>/dev/null && docker compose ps --services 2>/dev/null | grep -q .; then
        SS_ATTR_CODE=$(docker compose logs 2>/dev/null \
          | grep 'UpdateUserAttributes verification code' \
          | grep "$SS_USER" \
          | tail -1 \
          | grep -oE 'code=[0-9]+' \
          | cut -d= -f2 || true)
      fi
    fi

    if [[ -n "$SS_ATTR_CODE" ]]; then
      run "VerifyUserAttribute" \
        $AWS verify-user-attribute \
          --access-token "$SS_ACCESS_TOKEN" \
          --attribute-name email \
          --code "$SS_ATTR_CODE"

      VUA_MISMATCH_JSON=$($AWS verify-user-attribute \
        --access-token "$SS_ACCESS_TOKEN" \
        --attribute-name email \
        --code "000000" 2>&1) || true
      if echo "$VUA_MISMATCH_JSON" | grep -qi 'CodeMismatchException'; then
        ok "VerifyUserAttribute — CodeMismatchException for stale code after verification"
      else
        fail "VerifyUserAttribute — expected CodeMismatchException for stale code"
      fi
    else
      skip "VerifyUserAttribute — no verification code available"
      echo "  Hint: set E2E_COGNITO_SS_ATTR_CODE=<code> from kumolo logs, or use Docker Compose"
    fi

    # GetUserAttributeVerificationCode: request a fresh code independently.
    GUAVC_JSON=$($AWS get-user-attribute-verification-code \
      --access-token "$SS_ACCESS_TOKEN" \
      --attribute-name given_name 2>&1) || true
    if echo "$GUAVC_JSON" | grep -qi 'InvalidParameterException'; then
      ok "GetUserAttributeVerificationCode — InvalidParameterException for unsupported attribute"
    else
      fail "GetUserAttributeVerificationCode — expected InvalidParameterException for given_name"
    fi

    # DeleteUser: self-service account deletion.
    run "DeleteUser" \
      $AWS delete-user --access-token "$SS_ACCESS_TOKEN"

    DELETED_SS_JSON=$($AWS admin-get-user \
      --user-pool-id "$POOL_ID" \
      --username "$SS_USER" 2>&1) || true
    if echo "$DELETED_SS_JSON" | grep -qi 'UserNotFoundException'; then
      ok "AdminGetUser — UserNotFoundException after DeleteUser"
    else
      fail "AdminGetUser — expected UserNotFoundException after DeleteUser"
    fi

    DELETED_SS_TOKEN_JSON=$($AWS get-user --access-token "$SS_ACCESS_TOKEN" 2>&1) || true
    if echo "$DELETED_SS_TOKEN_JSON" | grep -qi 'UserNotFoundException'; then
      ok "GetUser — UserNotFoundException for deleted user's access token"
    else
      fail "GetUser — expected UserNotFoundException for deleted user's access token"
    fi
  else
    skip "Self-service attribute/delete tests — could not obtain access token"
  fi
else
  skip "Self-service attribute/delete tests — no confirmation code available"
  echo "  Hint: set E2E_COGNITO_SS_CODE=<code> from kumolo logs, or use Docker Compose"
fi

# ---------------------------------------------------------------------------
# AdminUpdateUserAttributes
# ---------------------------------------------------------------------------
echo ""
echo "--- AdminUpdateUserAttributes ---"

AUUA_USER="admin-update-attrs-e2e@example.com"
$AWS admin-create-user \
  --user-pool-id "$POOL_ID" \
  --username "$AUUA_USER" >/dev/null 2>&1 || true

run "AdminUpdateUserAttributes" \
  $AWS admin-update-user-attributes \
    --user-pool-id "$POOL_ID" \
    --username "$AUUA_USER" \
    --user-attributes "Name=given_name,Value=Bob"

AUUA_GET_JSON=$($AWS admin-get-user \
  --user-pool-id "$POOL_ID" \
  --username "$AUUA_USER" 2>&1)
if echo "$AUUA_GET_JSON" | grep -q '"Name": "given_name"' \
    && echo "$AUUA_GET_JSON" | grep -q '"Value": "Bob"'; then
  ok "AdminUpdateUserAttributes — given_name updated"
else
  fail "AdminUpdateUserAttributes — expected given_name=Bob"
fi

# Admin bypass: email_verified=true skips the verification-code step.
run "AdminUpdateUserAttributes (email + bypass)" \
  $AWS admin-update-user-attributes \
    --user-pool-id "$POOL_ID" \
    --username "$AUUA_USER" \
    --user-attributes "Name=email,Value=bob-e2e@example.com" "Name=email_verified,Value=true"

AUUA_GET2_JSON=$($AWS admin-get-user \
  --user-pool-id "$POOL_ID" \
  --username "$AUUA_USER" 2>&1)
if echo "$AUUA_GET2_JSON" | grep -A1 '"Name": "email_verified"' | grep -q '"Value": "true"'; then
  ok "AdminUpdateUserAttributes — email_verified bypass set to true"
else
  fail "AdminUpdateUserAttributes — expected email_verified=true after bypass"
fi

AUUA_NF_JSON=$($AWS admin-update-user-attributes \
  --user-pool-id "$POOL_ID" \
  --username "no-such-user-e2e@example.com" \
  --user-attributes "Name=given_name,Value=X" 2>&1) || true
if echo "$AUUA_NF_JSON" | grep -qi 'UserNotFoundException'; then
  ok "AdminUpdateUserAttributes — UserNotFoundException for unknown user"
else
  fail "AdminUpdateUserAttributes — expected UserNotFoundException"
fi

# ---------------------------------------------------------------------------
# AdminDisableUser / AdminEnableUser
# ---------------------------------------------------------------------------
echo ""
echo "--- AdminDisableUser / AdminEnableUser ---"

DIS_USER="disable-e2e@example.com"
DIS_PASS="Password1!"

DIS_SIGNUP_JSON=$($AWS sign-up \
  --client-id "$CLIENT_ID" \
  --username "$DIS_USER" \
  --password "$DIS_PASS" \
  --user-attributes "Name=email,Value=$DIS_USER" 2>&1)
if echo "$DIS_SIGNUP_JSON" | grep -q '"UserSub"'; then
  ok "SignUp (for AdminDisableUser flow)"
else
  fail "SignUp (for AdminDisableUser flow)"
fi

DIS_CODE="${E2E_COGNITO_DIS_CODE:-}"
if [[ -z "$DIS_CODE" ]]; then
  if command -v docker &>/dev/null && docker compose ps --services 2>/dev/null | grep -q .; then
    DIS_CODE=$(docker compose logs 2>/dev/null \
      | grep 'SignUp confirmation code' \
      | grep "$DIS_USER" \
      | tail -1 \
      | grep -oE 'code=[0-9]+' \
      | cut -d= -f2 || true)
  fi
fi

if [[ -n "$DIS_CODE" ]]; then
  run "ConfirmSignUp (for AdminDisableUser flow)" \
    $AWS confirm-sign-up \
      --client-id "$CLIENT_ID" \
      --username "$DIS_USER" \
      --confirmation-code "$DIS_CODE"

  DIS_AUTH_JSON=$($AWS initiate-auth \
    --client-id "$CLIENT_ID" \
    --auth-flow "USER_PASSWORD_AUTH" \
    --auth-parameters "USERNAME=$DIS_USER,PASSWORD=$DIS_PASS" 2>&1)
  DIS_ACCESS_TOKEN=$(echo "$DIS_AUTH_JSON" | jq -r '.AuthenticationResult.AccessToken // empty' 2>/dev/null || true)

  if [[ -n "$DIS_ACCESS_TOKEN" ]]; then
    run "AdminDisableUser" \
      $AWS admin-disable-user \
        --user-pool-id "$POOL_ID" \
        --username "$DIS_USER"

    DIS_GET_JSON=$($AWS admin-get-user \
      --user-pool-id "$POOL_ID" \
      --username "$DIS_USER" 2>&1)
    if echo "$DIS_GET_JSON" | grep -q '"Enabled": false'; then
      ok "AdminGetUser — Enabled is false after AdminDisableUser"
    else
      fail "AdminGetUser — expected Enabled=false after AdminDisableUser"
    fi

    DIS_TOKEN_JSON=$($AWS get-user --access-token "$DIS_ACCESS_TOKEN" 2>&1) || true
    if echo "$DIS_TOKEN_JSON" | grep -qi 'NotAuthorizedException'; then
      ok "AdminDisableUser — existing access token revoked"
    else
      fail "AdminDisableUser — expected existing access token to be revoked"
    fi

    DIS_SIGNIN_JSON=$($AWS initiate-auth \
      --client-id "$CLIENT_ID" \
      --auth-flow "USER_PASSWORD_AUTH" \
      --auth-parameters "USERNAME=$DIS_USER,PASSWORD=$DIS_PASS" 2>&1) || true
    if echo "$DIS_SIGNIN_JSON" | grep -qi 'NotAuthorizedException'; then
      ok "AdminDisableUser — sign-in blocked"
    else
      fail "AdminDisableUser — expected sign-in to be blocked"
    fi

    run "AdminEnableUser" \
      $AWS admin-enable-user \
        --user-pool-id "$POOL_ID" \
        --username "$DIS_USER"

    EN_GET_JSON=$($AWS admin-get-user \
      --user-pool-id "$POOL_ID" \
      --username "$DIS_USER" 2>&1)
    if echo "$EN_GET_JSON" | grep -q '"Enabled": true'; then
      ok "AdminGetUser — Enabled is true after AdminEnableUser"
    else
      fail "AdminGetUser — expected Enabled=true after AdminEnableUser"
    fi

    EN_SIGNIN_JSON=$($AWS initiate-auth \
      --client-id "$CLIENT_ID" \
      --auth-flow "USER_PASSWORD_AUTH" \
      --auth-parameters "USERNAME=$DIS_USER,PASSWORD=$DIS_PASS" 2>&1)
    if echo "$EN_SIGNIN_JSON" | grep -q '"AccessToken"'; then
      ok "AdminEnableUser — sign-in restored"
    else
      fail "AdminEnableUser — expected sign-in to succeed after re-enabling"
    fi
  else
    skip "AdminDisableUser / AdminEnableUser — could not obtain access token"
  fi
else
  skip "AdminDisableUser / AdminEnableUser — no confirmation code available"
  echo "  Hint: set E2E_COGNITO_DIS_CODE=<code> from kumolo logs, or use Docker Compose"
fi

# ---------------------------------------------------------------------------
# Password Management: ForgotPassword, ConfirmForgotPassword, ChangePassword
# ---------------------------------------------------------------------------
echo ""
echo "--- Password Management ---"

PW_USER="pwmgmt-e2e@example.com"
PW_PASS="Password1!"

PW_SIGNUP_JSON=$($AWS sign-up \
  --client-id "$CLIENT_ID" \
  --username "$PW_USER" \
  --password "$PW_PASS" \
  --user-attributes "Name=email,Value=$PW_USER" 2>&1)
if echo "$PW_SIGNUP_JSON" | grep -q '"UserSub"'; then
  ok "SignUp (for password management)"
else
  fail "SignUp (for password management)"
fi

PW_CODE="${E2E_COGNITO_PW_CODE:-}"
if [[ -z "$PW_CODE" ]]; then
  if command -v docker &>/dev/null && docker compose ps --services 2>/dev/null | grep -q .; then
    PW_CODE=$(docker compose logs 2>/dev/null \
      | grep 'SignUp confirmation code' \
      | grep "$PW_USER" \
      | tail -1 \
      | grep -oE 'code=[0-9]+' \
      | cut -d= -f2 || true)
  fi
fi

if [[ -n "$PW_CODE" ]]; then
  run "ConfirmSignUp (for password management)" \
    $AWS confirm-sign-up \
      --client-id "$CLIENT_ID" \
      --username "$PW_USER" \
      --confirmation-code "$PW_CODE"

  # ForgotPassword requires a verified contact attribute; AdminUpdateUserAttributes
  # with email_verified=true bypasses the normal VerifyUserAttribute code flow.
  run "AdminUpdateUserAttributes (mark email verified)" \
    $AWS admin-update-user-attributes \
      --user-pool-id "$POOL_ID" \
      --username "$PW_USER" \
      --user-attributes "Name=email,Value=$PW_USER" "Name=email_verified,Value=true"

  # Error: no verified contact attribute.
  NOVERIFIED_JSON=$($AWS forgot-password \
    --client-id "$CLIENT_ID" \
    --username "no-such-user-forgot@example.com" 2>&1) || true
  if echo "$NOVERIFIED_JSON" | grep -qi 'UserNotFoundException'; then
    ok "ForgotPassword — UserNotFoundException for unknown user"
  else
    fail "ForgotPassword — expected UserNotFoundException for unknown user"
  fi

  FP_JSON=$($AWS forgot-password \
    --client-id "$CLIENT_ID" \
    --username "$PW_USER" 2>&1)
  if echo "$FP_JSON" | grep -q '"AttributeName": "email"'; then
    ok "ForgotPassword"
  else
    fail "ForgotPassword"
  fi

  FP_CODE="${E2E_COGNITO_FP_CODE:-}"
  if [[ -z "$FP_CODE" ]]; then
    if command -v docker &>/dev/null && docker compose ps --services 2>/dev/null | grep -q .; then
      FP_CODE=$(docker compose logs 2>/dev/null \
        | grep 'ForgotPassword' \
        | grep "$PW_USER" \
        | tail -1 \
        | grep -oE 'code=[0-9]+' \
        | cut -d= -f2 || true)
    fi
  fi

  if [[ -n "$FP_CODE" ]]; then
    CFP_MISMATCH_JSON=$($AWS confirm-forgot-password \
      --client-id "$CLIENT_ID" \
      --username "$PW_USER" \
      --confirmation-code "000000" \
      --password "NewPassword1!" 2>&1) || true
    if echo "$CFP_MISMATCH_JSON" | grep -qi 'CodeMismatchException'; then
      ok "ConfirmForgotPassword — CodeMismatchException for wrong code"
    else
      fail "ConfirmForgotPassword — expected CodeMismatchException for wrong code"
    fi

    run "ConfirmForgotPassword" \
      $AWS confirm-forgot-password \
        --client-id "$CLIENT_ID" \
        --username "$PW_USER" \
        --confirmation-code "$FP_CODE" \
        --password "NewPassword1!"

    NEWPW_AUTH_JSON=$($AWS initiate-auth \
      --client-id "$CLIENT_ID" \
      --auth-flow "USER_PASSWORD_AUTH" \
      --auth-parameters "USERNAME=$PW_USER,PASSWORD=NewPassword1!" 2>&1)
    if echo "$NEWPW_AUTH_JSON" | grep -q '"AccessToken"'; then
      ok "InitiateAuth — sign-in with new password after ConfirmForgotPassword"
    else
      fail "InitiateAuth — expected sign-in with new password after ConfirmForgotPassword"
    fi

    OLDPW_AUTH_JSON=$($AWS initiate-auth \
      --client-id "$CLIENT_ID" \
      --auth-flow "USER_PASSWORD_AUTH" \
      --auth-parameters "USERNAME=$PW_USER,PASSWORD=$PW_PASS" 2>&1) || true
    if echo "$OLDPW_AUTH_JSON" | grep -qi 'NotAuthorizedException'; then
      ok "InitiateAuth — old password rejected after ConfirmForgotPassword"
    else
      fail "InitiateAuth — expected old password to be rejected after ConfirmForgotPassword"
    fi

    # ChangePassword: authenticated password change.
    CP_ACCESS_TOKEN=$(echo "$NEWPW_AUTH_JSON" | jq -r '.AuthenticationResult.AccessToken // empty' 2>/dev/null || true)
    if [[ -n "$CP_ACCESS_TOKEN" ]]; then
      CP_WRONG_JSON=$($AWS change-password \
        --access-token "$CP_ACCESS_TOKEN" \
        --previous-password "WrongPassword1!" \
        --proposed-password "ChangedPassword1!" 2>&1) || true
      if echo "$CP_WRONG_JSON" | grep -qi 'NotAuthorizedException'; then
        ok "ChangePassword — NotAuthorizedException for wrong previous password"
      else
        fail "ChangePassword — expected NotAuthorizedException for wrong previous password"
      fi

      run "ChangePassword" \
        $AWS change-password \
          --access-token "$CP_ACCESS_TOKEN" \
          --previous-password "NewPassword1!" \
          --proposed-password "ChangedPassword1!"

      CHANGED_AUTH_JSON=$($AWS initiate-auth \
        --client-id "$CLIENT_ID" \
        --auth-flow "USER_PASSWORD_AUTH" \
        --auth-parameters "USERNAME=$PW_USER,PASSWORD=ChangedPassword1!" 2>&1)
      if echo "$CHANGED_AUTH_JSON" | grep -q '"AccessToken"'; then
        ok "InitiateAuth — sign-in with changed password after ChangePassword"
      else
        fail "InitiateAuth — expected sign-in with changed password after ChangePassword"
      fi
    else
      skip "ChangePassword — could not obtain access token"
    fi
  else
    skip "ConfirmForgotPassword / ChangePassword — no reset code available"
    echo "  Hint: set E2E_COGNITO_FP_CODE=<code> from kumolo logs, or use Docker Compose"
  fi
else
  skip "Password management tests — no confirmation code available"
  echo "  Hint: set E2E_COGNITO_PW_CODE=<code> from kumolo logs, or use Docker Compose"
fi

# admin_only AccountRecoverySetting: self-service ForgotPassword must be refused
# even for a user with a verified contact attribute.
run "UpdateUserPool (admin_only recovery)" \
  $AWS update-user-pool \
    --user-pool-id "$POOL_ID" \
    --account-recovery-setting '{"RecoveryMechanisms":[{"Name":"admin_only","Priority":1}]}'

ADMINONLY_JSON=$($AWS forgot-password \
  --client-id "$CLIENT_ID" \
  --username "$PW_USER" 2>&1) || true
if echo "$ADMINONLY_JSON" | grep -qi 'InvalidParameterException'; then
  ok "ForgotPassword — InvalidParameterException when pool is admin_only"
else
  fail "ForgotPassword — expected InvalidParameterException when pool is admin_only"
fi

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------
# Only clear CLIENT_ID / POOL_ID after a successful delete so the EXIT-trap
# cleanup() can retry if either command fails here.
if $AWS delete-user-pool-client \
    --user-pool-id "$POOL_ID" \
    --client-id "$CLIENT_ID" > /dev/null 2>&1; then
  ok "DeleteUserPoolClient"
  CLIENT_ID=""
else
  fail "DeleteUserPoolClient"
fi

if $AWS delete-user-pool --user-pool-id "$POOL_ID" > /dev/null 2>&1; then
  ok "DeleteUserPool"
  POOL_ID=""
else
  fail "DeleteUserPool"
fi

# ---------------------------------------------------------------------------
echo ""
echo "Cognito results: ${PASS} passed, ${FAIL} failed"
[[ $FAIL -eq 0 ]]
