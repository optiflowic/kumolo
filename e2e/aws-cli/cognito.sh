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
# Refresh token expiry
# ---------------------------------------------------------------------------
echo ""
echo "--- Refresh token expiry ---"

# Create a client with explicit refresh_token_validity to verify the value is persisted
RT_CLIENT_JSON=$($AWS create-user-pool-client \
  --user-pool-id "$POOL_ID" \
  --client-name "e2e-rt-validity-client" \
  --refresh-token-validity 7 2>&1)
if echo "$RT_CLIENT_JSON" | grep -q '"ClientId"'; then
  ok "CreateUserPoolClient (refresh_token_validity=7)"
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
