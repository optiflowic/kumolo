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

cleanup() {
  if [[ -n "$CLIENT_ID" && "$CLIENT_ID" != "UNKNOWN" ]]; then
    $AWS delete-user-pool-client \
      --user-pool-id "$POOL_ID" \
      --client-id "$CLIENT_ID" >/dev/null 2>&1 || true
  fi
  if [[ -n "$POOL_ID" && "$POOL_ID" != "us-east-1_UNKNOWN" ]]; then
    $AWS delete-user-pool --user-pool-id "$POOL_ID" >/dev/null 2>&1 || true
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
else
  fail "CreateUserPool"
  POOL_ID="us-east-1_UNKNOWN"
fi

run "DescribeUserPool" \
  $AWS describe-user-pool --user-pool-id "$POOL_ID"

run "UpdateUserPool" \
  $AWS update-user-pool --user-pool-id "$POOL_ID" --mfa-configuration "OFF"

# ListUserPools
LIST_JSON=$($AWS list-user-pools --max-results 10 2>&1)
if echo "$LIST_JSON" | grep -q '"UserPools"'; then
  ok "ListUserPools"
else
  fail "ListUserPools"
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
# JWKS endpoint
# ---------------------------------------------------------------------------
echo ""
echo "--- JWKS ---"

JWKS_JSON=$(curl -sf "$ENDPOINT/$POOL_ID/.well-known/jwks.json") || true
JWKS_CT=$(curl -sI "$ENDPOINT/$POOL_ID/.well-known/jwks.json" | grep -i "^content-type:" | tr -d '\r' | sed 's/[^:]*: //')
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
