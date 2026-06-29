package cognito

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createGroup is a test helper that creates a group via the router and returns the group name.
func createGroup(t *testing.T, ro *Router, poolID, groupName string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"GroupName":  groupName,
	})
	w := doOp(t, ro, "CreateGroup", string(body))
	require.Equal(t, http.StatusOK, w.Code, "createGroup failed: %s", w.Body.String())
}

// createAdminUser creates a user via AdminCreateUser and returns the username.
func createAdminUser(t *testing.T, ro *Router, poolID, username string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"Username":   username,
	})
	w := doOp(t, ro, "AdminCreateUser", string(body))
	require.Equal(t, http.StatusOK, w.Code, "createAdminUser failed: %s", w.Body.String())
}

// ──── CreateGroup ─────────────────────────────────────────────────────────────

func TestCreateGroup_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	prec := 1
	body, _ := json.Marshal(map[string]any{
		"UserPoolId":  poolID,
		"GroupName":   "admins",
		"Description": "Admin group",
		"Precedence":  prec,
	})
	w := doOp(t, ro, "CreateGroup", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Group struct {
			GroupName        string  `json:"GroupName"`
			UserPoolId       string  `json:"UserPoolId"`
			Description      string  `json:"Description"`
			Precedence       *int    `json:"Precedence"`
			CreationDate     float64 `json:"CreationDate"`
			LastModifiedDate float64 `json:"LastModifiedDate"`
		} `json:"Group"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "admins", resp.Group.GroupName)
	assert.Equal(t, poolID, resp.Group.UserPoolId)
	assert.Equal(t, "Admin group", resp.Group.Description)
	require.NotNil(t, resp.Group.Precedence)
	assert.Equal(t, 1, *resp.Group.Precedence)
	assert.Greater(t, resp.Group.CreationDate, float64(0))
}

func TestCreateGroup_Duplicate(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createGroup(t, ro, poolID, "admins")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": "admins"})
	w := doOp(t, ro, "CreateGroup", string(body))

	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeGroupExistsException)
}

func TestCreateGroup_MissingPoolID(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "CreateGroup", `{"GroupName":"admins"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeInvalidParameterException)
}

func TestCreateGroup_MissingGroupName(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID})
	w := doOp(t, ro, "CreateGroup", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeInvalidParameterException)
}

func TestCreateGroup_PoolNotFound(t *testing.T) {
	ro := newTestRouter(t)
	body, _ := json.Marshal(map[string]any{"UserPoolId": "us-east-1_NoPool", "GroupName": "admins"})
	w := doOp(t, ro, "CreateGroup", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeResourceNotFoundException)
}

// ──── GetGroup ────────────────────────────────────────────────────────────────

func TestGetGroup_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createGroup(t, ro, poolID, "admins")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": "admins"})
	w := doOp(t, ro, "GetGroup", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Group struct{ GroupName string } `json:"Group"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "admins", resp.Group.GroupName)
}

func TestGetGroup_NotFound(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": "nonexistent"})
	w := doOp(t, ro, "GetGroup", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeResourceNotFoundException)
}

// ──── UpdateGroup ─────────────────────────────────────────────────────────────

func TestUpdateGroup_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createGroup(t, ro, poolID, "admins")

	prec := 3
	body, _ := json.Marshal(map[string]any{
		"UserPoolId":  poolID,
		"GroupName":   "admins",
		"Description": "Updated description",
		"Precedence":  prec,
	})
	w := doOp(t, ro, "UpdateGroup", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Group struct {
			Description string `json:"Description"`
			Precedence  *int   `json:"Precedence"`
		} `json:"Group"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "Updated description", resp.Group.Description)
	require.NotNil(t, resp.Group.Precedence)
	assert.Equal(t, 3, *resp.Group.Precedence)
}

func TestUpdateGroup_NotFound(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": "nonexistent"})
	w := doOp(t, ro, "UpdateGroup", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeResourceNotFoundException)
}

// ──── DeleteGroup ─────────────────────────────────────────────────────────────

func TestDeleteGroup_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createGroup(t, ro, poolID, "admins")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": "admins"})
	w := doOp(t, ro, "DeleteGroup", string(body))

	require.Equal(t, http.StatusOK, w.Code)

	// Group no longer found.
	w2 := doOp(t, ro, "GetGroup", string(body))
	require.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrorType(t, w2, ErrTypeResourceNotFoundException)
}

func TestDeleteGroup_NotFound(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": "nonexistent"})
	w := doOp(t, ro, "DeleteGroup", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeResourceNotFoundException)
}

// ──── ListGroups ──────────────────────────────────────────────────────────────

func TestListGroups_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	for _, name := range []string{"beta", "alpha", "gamma"} {
		createGroup(t, ro, poolID, name)
	}

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID})
	w := doOp(t, ro, "ListGroups", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Groups []struct{ GroupName string } `json:"Groups"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Groups, 3)
	assert.Equal(t, "alpha", resp.Groups[0].GroupName)
	assert.Equal(t, "beta", resp.Groups[1].GroupName)
	assert.Equal(t, "gamma", resp.Groups[2].GroupName)
}

func TestListGroups_Pagination(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	for _, name := range []string{"c", "a", "b"} {
		createGroup(t, ro, poolID, name)
	}

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "Limit": 2})
	w := doOp(t, ro, "ListGroups", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Groups    []struct{ GroupName string } `json:"Groups"`
		NextToken string                       `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Groups, 2)
	assert.NotEmpty(t, resp.NextToken)

	body2, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"Limit":      2,
		"NextToken":  resp.NextToken,
	})
	w2 := doOp(t, ro, "ListGroups", string(body2))
	require.Equal(t, http.StatusOK, w2.Code)
	var resp2 struct {
		Groups []struct{ GroupName string } `json:"Groups"`
	}
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp2))
	require.Len(t, resp2.Groups, 1)
}

// ──── AdminAddUserToGroup ─────────────────────────────────────────────────────

func TestAdminAddUserToGroup_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createGroup(t, ro, poolID, "admins")
	createAdminUser(t, ro, poolID, "alice")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"GroupName":  "admins",
		"Username":   "alice",
	})
	w := doOp(t, ro, "AdminAddUserToGroup", string(body))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestAdminAddUserToGroup_GroupNotFound(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createAdminUser(t, ro, poolID, "alice")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"GroupName":  "nonexistent",
		"Username":   "alice",
	})
	w := doOp(t, ro, "AdminAddUserToGroup", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeResourceNotFoundException)
}

func TestAdminAddUserToGroup_UserNotFound(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createGroup(t, ro, poolID, "admins")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"GroupName":  "admins",
		"Username":   "nonexistent",
	})
	w := doOp(t, ro, "AdminAddUserToGroup", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeUserNotFoundException)
}

// ──── AdminRemoveUserFromGroup ────────────────────────────────────────────────

func TestAdminRemoveUserFromGroup_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createGroup(t, ro, poolID, "admins")
	createAdminUser(t, ro, poolID, "alice")

	addBody, _ := json.Marshal(
		map[string]any{"UserPoolId": poolID, "GroupName": "admins", "Username": "alice"},
	)
	require.Equal(t, http.StatusOK, doOp(t, ro, "AdminAddUserToGroup", string(addBody)).Code)

	removeBody, _ := json.Marshal(
		map[string]any{"UserPoolId": poolID, "GroupName": "admins", "Username": "alice"},
	)
	w := doOp(t, ro, "AdminRemoveUserFromGroup", string(removeBody))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestAdminRemoveUserFromGroup_GroupNotFound(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createAdminUser(t, ro, poolID, "alice")

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"GroupName":  "nonexistent",
		"Username":   "alice",
	})
	w := doOp(t, ro, "AdminRemoveUserFromGroup", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeResourceNotFoundException)
}

// ──── AdminListGroupsForUser ──────────────────────────────────────────────────

func TestAdminListGroupsForUser_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	for _, name := range []string{"beta", "alpha"} {
		createGroup(t, ro, poolID, name)
	}
	createAdminUser(t, ro, poolID, "alice")

	for _, name := range []string{"beta", "alpha"} {
		addBody, _ := json.Marshal(
			map[string]any{"UserPoolId": poolID, "GroupName": name, "Username": "alice"},
		)
		require.Equal(t, http.StatusOK, doOp(t, ro, "AdminAddUserToGroup", string(addBody)).Code)
	}

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "Username": "alice"})
	w := doOp(t, ro, "AdminListGroupsForUser", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Groups []struct{ GroupName string } `json:"Groups"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Groups, 2)
	assert.Equal(t, "alpha", resp.Groups[0].GroupName)
	assert.Equal(t, "beta", resp.Groups[1].GroupName)
}

func TestAdminListGroupsForUser_UserNotFound(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "Username": "nonexistent"})
	w := doOp(t, ro, "AdminListGroupsForUser", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeUserNotFoundException)
}

// ──── ListUsersInGroup ────────────────────────────────────────────────────────

func TestListUsersInGroup_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createGroup(t, ro, poolID, "admins")
	for _, u := range []string{"charlie", "alice", "bob"} {
		createAdminUser(t, ro, poolID, u)
		addBody, _ := json.Marshal(
			map[string]any{"UserPoolId": poolID, "GroupName": "admins", "Username": u},
		)
		require.Equal(t, http.StatusOK, doOp(t, ro, "AdminAddUserToGroup", string(addBody)).Code)
	}

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": "admins"})
	w := doOp(t, ro, "ListUsersInGroup", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Users []struct{ Username string } `json:"Users"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Users, 3)
	assert.Equal(t, "alice", resp.Users[0].Username)
	assert.Equal(t, "bob", resp.Users[1].Username)
	assert.Equal(t, "charlie", resp.Users[2].Username)
}

func TestListUsersInGroup_GroupNotFound(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": "nonexistent"})
	w := doOp(t, ro, "ListUsersInGroup", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeResourceNotFoundException)
}

// ──── cognito:groups in JWT ───────────────────────────────────────────────────

func TestJWT_CognitoGroupsClaim(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	clientID := createGroupTestClient(t, ro, poolID)

	for _, name := range []string{"beta", "alpha"} {
		createGroup(t, ro, poolID, name)
	}
	signUpAndConfirmUser(t, ro, poolID, clientID, "alice", "Pass1234!")
	for _, name := range []string{"beta", "alpha"} {
		addBody, _ := json.Marshal(
			map[string]any{"UserPoolId": poolID, "GroupName": name, "Username": "alice"},
		)
		require.Equal(t, http.StatusOK, doOp(t, ro, "AdminAddUserToGroup", string(addBody)).Code)
	}

	authBody, _ := json.Marshal(map[string]any{
		"AuthFlow": "USER_PASSWORD_AUTH",
		"ClientId": clientID,
		"AuthParameters": map[string]string{
			"USERNAME": "alice",
			"PASSWORD": "Pass1234!",
		},
	})
	authW := doOp(t, ro, "InitiateAuth", string(authBody))
	require.Equal(t, http.StatusOK, authW.Code)

	var authResp struct {
		AuthenticationResult struct {
			AccessToken string `json:"AccessToken"`
			IdToken     string `json:"IdToken"`
		} `json:"AuthenticationResult"`
	}
	require.NoError(t, json.NewDecoder(authW.Body).Decode(&authResp))

	// Parse claims from access token (without verifying — just check the groups claim).
	accessClaims, err := parseRawClaims(authResp.AuthenticationResult.AccessToken)
	require.NoError(t, err)
	groupsRaw, ok := accessClaims["cognito:groups"]
	require.True(t, ok, "cognito:groups must be present in access token")
	groups, ok := groupsRaw.([]any)
	require.True(t, ok)
	assert.Len(t, groups, 2)

	idClaims, err := parseRawClaims(authResp.AuthenticationResult.IdToken)
	require.NoError(t, err)
	_, ok = idClaims["cognito:groups"]
	assert.True(t, ok, "cognito:groups must be present in ID token")
}

// ──── helpers ─────────────────────────────────────────────────────────────────

func assertErrorType(t *testing.T, w *httptest.ResponseRecorder, errType string) {
	t.Helper()
	var resp struct {
		Type string `json:"__type"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, errType, resp.Type)
}

func createGroupTestClient(t *testing.T, ro *Router, poolID string) string {
	t.Helper()
	body := fmt.Sprintf(`{"UserPoolId":%q,"ClientName":"test-client"}`, poolID)
	w := doOp(t, ro, "CreateUserPoolClient", body)
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		UserPoolClient struct{ ClientId string } `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp.UserPoolClient.ClientId
}

// ──── Validation error table (invalid input / not-found across all operations) ─

// TestGroupHandlers_Validation exercises the common error paths for all group
// handler operations using a single table-driven subtest, keeping each case to
// the minimum setup it actually needs.
func TestGroupHandlers_Validation(t *testing.T) {
	// body helpers; all ignore the router when no pool creation is required.
	poolID := func(t *testing.T, ro *Router) string {
		t.Helper()
		return createPool(t, ro, "p")
	}
	withPool := func(extra map[string]any) func(*testing.T, *Router) string {
		return func(t *testing.T, ro *Router) string {
			t.Helper()
			m := map[string]any{"UserPoolId": poolID(t, ro)}
			for k, v := range extra {
				m[k] = v
			}
			b, _ := json.Marshal(m)
			return string(b)
		}
	}
	withPoolAndGroup := func(extra map[string]any) func(*testing.T, *Router) string {
		return func(t *testing.T, ro *Router) string {
			t.Helper()
			pid := poolID(t, ro)
			createGroup(t, ro, pid, "admins")
			m := map[string]any{"UserPoolId": pid, "GroupName": "admins"}
			for k, v := range extra {
				m[k] = v
			}
			b, _ := json.Marshal(m)
			return string(b)
		}
	}
	static := func(fields map[string]any) func(*testing.T, *Router) string {
		b, _ := json.Marshal(fields)
		s := string(b)
		return func(*testing.T, *Router) string { return s }
	}

	tests := []struct {
		name     string
		op       string
		body     func(*testing.T, *Router) string
		wantCode int
		wantType string
	}{
		// ── CreateGroup ────────────────────────────────────────────────────────
		{"CreateGroup/invalid body", "CreateGroup",
			func(*testing.T, *Router) string { return "not-json" },
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"CreateGroup/missing pool ID", "CreateGroup",
			static(map[string]any{"GroupName": "admins"}),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"CreateGroup/missing group name", "CreateGroup",
			withPool(nil),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"CreateGroup/pool not found", "CreateGroup",
			static(map[string]any{"UserPoolId": "us-east-1_NoPool", "GroupName": "admins"}),
			http.StatusBadRequest, ErrTypeResourceNotFoundException},

		// ── DeleteGroup ────────────────────────────────────────────────────────
		{"DeleteGroup/invalid body", "DeleteGroup",
			func(*testing.T, *Router) string { return "not-json" },
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"DeleteGroup/missing pool ID", "DeleteGroup",
			static(map[string]any{"GroupName": "admins"}),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"DeleteGroup/missing group name", "DeleteGroup",
			withPool(nil),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"DeleteGroup/pool not found", "DeleteGroup",
			static(map[string]any{"UserPoolId": "us-east-1_NoPool", "GroupName": "admins"}),
			http.StatusBadRequest, ErrTypeResourceNotFoundException},

		// ── GetGroup ───────────────────────────────────────────────────────────
		{"GetGroup/invalid body", "GetGroup",
			func(*testing.T, *Router) string { return "not-json" },
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"GetGroup/missing pool ID", "GetGroup",
			static(map[string]any{"GroupName": "admins"}),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"GetGroup/missing group name", "GetGroup",
			withPool(nil),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"GetGroup/pool not found", "GetGroup",
			static(map[string]any{"UserPoolId": "us-east-1_NoPool", "GroupName": "admins"}),
			http.StatusBadRequest, ErrTypeResourceNotFoundException},

		// ── UpdateGroup ────────────────────────────────────────────────────────
		{"UpdateGroup/invalid body", "UpdateGroup",
			func(*testing.T, *Router) string { return "not-json" },
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"UpdateGroup/missing pool ID", "UpdateGroup",
			static(map[string]any{"GroupName": "admins"}),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"UpdateGroup/missing group name", "UpdateGroup",
			withPool(nil),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"UpdateGroup/pool not found", "UpdateGroup",
			static(map[string]any{"UserPoolId": "us-east-1_NoPool", "GroupName": "admins"}),
			http.StatusBadRequest, ErrTypeResourceNotFoundException},

		// ── ListGroups ─────────────────────────────────────────────────────────
		{"ListGroups/invalid body", "ListGroups",
			func(*testing.T, *Router) string { return "not-json" },
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"ListGroups/missing pool ID", "ListGroups",
			func(*testing.T, *Router) string { return `{}` },
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"ListGroups/pool not found", "ListGroups",
			static(map[string]any{"UserPoolId": "us-east-1_NoPool"}),
			http.StatusBadRequest, ErrTypeResourceNotFoundException},

		// ── AdminAddUserToGroup ────────────────────────────────────────────────
		{"AdminAddUserToGroup/invalid body", "AdminAddUserToGroup",
			func(*testing.T, *Router) string { return "not-json" },
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"AdminAddUserToGroup/missing pool ID", "AdminAddUserToGroup",
			static(map[string]any{"GroupName": "admins", "Username": "alice"}),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"AdminAddUserToGroup/missing group name", "AdminAddUserToGroup",
			withPool(map[string]any{"Username": "alice"}),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"AdminAddUserToGroup/missing username", "AdminAddUserToGroup",
			withPool(map[string]any{"GroupName": "admins"}),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{
			"AdminAddUserToGroup/pool not found",
			"AdminAddUserToGroup",
			static(
				map[string]any{
					"UserPoolId": "us-east-1_NoPool",
					"GroupName":  "admins",
					"Username":   "alice",
				},
			),
			http.StatusBadRequest,
			ErrTypeResourceNotFoundException,
		},

		// ── AdminRemoveUserFromGroup ───────────────────────────────────────────
		{"AdminRemoveUserFromGroup/invalid body", "AdminRemoveUserFromGroup",
			func(*testing.T, *Router) string { return "not-json" },
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"AdminRemoveUserFromGroup/missing pool ID", "AdminRemoveUserFromGroup",
			static(map[string]any{"GroupName": "admins", "Username": "alice"}),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"AdminRemoveUserFromGroup/missing group name", "AdminRemoveUserFromGroup",
			withPool(map[string]any{"Username": "alice"}),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"AdminRemoveUserFromGroup/missing username", "AdminRemoveUserFromGroup",
			withPool(map[string]any{"GroupName": "admins"}),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{
			"AdminRemoveUserFromGroup/pool not found",
			"AdminRemoveUserFromGroup",
			static(
				map[string]any{
					"UserPoolId": "us-east-1_NoPool",
					"GroupName":  "admins",
					"Username":   "alice",
				},
			),
			http.StatusBadRequest,
			ErrTypeResourceNotFoundException,
		},
		{"AdminRemoveUserFromGroup/user not found", "AdminRemoveUserFromGroup",
			withPoolAndGroup(map[string]any{"Username": "nonexistent"}),
			http.StatusBadRequest, ErrTypeUserNotFoundException},

		// ── AdminListGroupsForUser ─────────────────────────────────────────────
		{"AdminListGroupsForUser/invalid body", "AdminListGroupsForUser",
			func(*testing.T, *Router) string { return "not-json" },
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"AdminListGroupsForUser/missing pool ID", "AdminListGroupsForUser",
			static(map[string]any{"Username": "alice"}),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"AdminListGroupsForUser/missing username", "AdminListGroupsForUser",
			withPool(nil),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"AdminListGroupsForUser/pool not found", "AdminListGroupsForUser",
			static(map[string]any{"UserPoolId": "us-east-1_NoPool", "Username": "alice"}),
			http.StatusBadRequest, ErrTypeResourceNotFoundException},

		// ── ListUsersInGroup ───────────────────────────────────────────────────
		{"ListUsersInGroup/invalid body", "ListUsersInGroup",
			func(*testing.T, *Router) string { return "not-json" },
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"ListUsersInGroup/missing pool ID", "ListUsersInGroup",
			static(map[string]any{"GroupName": "admins"}),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"ListUsersInGroup/missing group name", "ListUsersInGroup",
			withPool(nil),
			http.StatusBadRequest, ErrTypeInvalidParameterException},
		{"ListUsersInGroup/pool not found", "ListUsersInGroup",
			static(map[string]any{"UserPoolId": "us-east-1_NoPool", "GroupName": "admins"}),
			http.StatusBadRequest, ErrTypeResourceNotFoundException},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ro := newTestRouter(t)
			w := doOp(t, ro, tc.op, tc.body(t, ro))
			require.Equal(t, tc.wantCode, w.Code)
			assertErrorType(t, w, tc.wantType)
		})
	}
}

func TestListGroups_InvalidNextToken(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createGroup(t, ro, poolID, "alpha")

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "NextToken": "bad-token"})
	w := doOp(t, ro, "ListGroups", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeInvalidParameterException)
}

func TestAdminListGroupsForUser_InvalidNextToken(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createAdminUser(t, ro, poolID, "alice")
	createGroup(t, ro, poolID, "alpha")
	addBody, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "GroupName": "alpha", "Username": "alice",
	})
	require.Equal(t, http.StatusOK, doOp(t, ro, "AdminAddUserToGroup", string(addBody)).Code)

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "Username": "alice", "NextToken": "bad-token",
	})
	w := doOp(t, ro, "AdminListGroupsForUser", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeInvalidParameterException)
}

func TestAdminListGroupsForUser_Pagination(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	for _, name := range []string{"c", "a", "b"} {
		createGroup(t, ro, poolID, name)
	}
	createAdminUser(t, ro, poolID, "alice")
	for _, name := range []string{"c", "a", "b"} {
		addBody, _ := json.Marshal(map[string]any{
			"UserPoolId": poolID, "GroupName": name, "Username": "alice",
		})
		require.Equal(t, http.StatusOK, doOp(t, ro, "AdminAddUserToGroup", string(addBody)).Code)
	}

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "Username": "alice", "Limit": 2})
	w := doOp(t, ro, "AdminListGroupsForUser", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Groups    []struct{ GroupName string } `json:"Groups"`
		NextToken string                       `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Groups, 2)
	assert.NotEmpty(t, resp.NextToken)
	assert.Equal(t, "a", resp.Groups[0].GroupName)

	body2, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "Username": "alice",
		"Limit": 2, "NextToken": resp.NextToken,
	})
	w2 := doOp(t, ro, "AdminListGroupsForUser", string(body2))
	require.Equal(t, http.StatusOK, w2.Code)
	var resp2 struct {
		Groups    []struct{ GroupName string } `json:"Groups"`
		NextToken string                       `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp2))
	require.Len(t, resp2.Groups, 1)
	assert.Equal(t, "c", resp2.Groups[0].GroupName)
	assert.Empty(t, resp2.NextToken)
}

func TestListUsersInGroup_InvalidNextToken(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createGroup(t, ro, poolID, "admins")
	createAdminUser(t, ro, poolID, "alice")
	addBody, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "GroupName": "admins", "Username": "alice",
	})
	require.Equal(t, http.StatusOK, doOp(t, ro, "AdminAddUserToGroup", string(addBody)).Code)

	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "GroupName": "admins", "NextToken": "bad-token",
	})
	w := doOp(t, ro, "ListUsersInGroup", string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assertErrorType(t, w, ErrTypeInvalidParameterException)
}

func TestListUsersInGroup_Pagination(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createGroup(t, ro, poolID, "admins")
	for _, u := range []string{"charlie", "alice", "bob"} {
		createAdminUser(t, ro, poolID, u)
		addBody, _ := json.Marshal(map[string]any{
			"UserPoolId": poolID, "GroupName": "admins", "Username": u,
		})
		require.Equal(t, http.StatusOK, doOp(t, ro, "AdminAddUserToGroup", string(addBody)).Code)
	}

	body, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": "admins", "Limit": 2})
	w := doOp(t, ro, "ListUsersInGroup", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Users     []struct{ Username string } `json:"Users"`
		NextToken string                      `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Users, 2)
	assert.NotEmpty(t, resp.NextToken)
	assert.Equal(t, "alice", resp.Users[0].Username)

	body2, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID, "GroupName": "admins",
		"Limit": 2, "NextToken": resp.NextToken,
	})
	w2 := doOp(t, ro, "ListUsersInGroup", string(body2))
	require.Equal(t, http.StatusOK, w2.Code)
	var resp2 struct {
		Users     []struct{ Username string } `json:"Users"`
		NextToken string                      `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp2))
	require.Len(t, resp2.Users, 1)
	assert.Equal(t, "charlie", resp2.Users[0].Username)
	assert.Empty(t, resp2.NextToken)
}

// ──── UpdateGroup: RoleArn branch ────────────────────────────────────────────

func TestUpdateGroup_WithRoleArn(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")
	createGroup(t, ro, poolID, "admins")

	roleArn := "arn:aws:iam::123456789012:role/AdminRole"
	body, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"GroupName":  "admins",
		"RoleArn":    roleArn,
	})
	w := doOp(t, ro, "UpdateGroup", string(body))

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Group struct {
			RoleArn string `json:"RoleArn"`
		} `json:"Group"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, roleArn, resp.Group.RoleArn)
}

// ──── Internal error paths (storage returns unexpected error) ─────────────────

func diskErr() error { return errors.New("disk error") }

func groupOK() func(string, string) (*GroupMetadata, error) {
	return func(_, _ string) (*GroupMetadata, error) { return &GroupMetadata{}, nil }
}

func userOK() func(string, string) (*UserMetadata, error) {
	return func(_, _ string) (*UserMetadata, error) { return &UserMetadata{}, nil }
}

func TestCreateGroup_GetUserPoolInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: diskErr()}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "GroupName": "admins"})
	w := doOp(t, ro, "CreateGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestCreateGroup_CreateGroupInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{createGroupErr: diskErr()}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "GroupName": "admins"})
	w := doOp(t, ro, "CreateGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestDeleteGroup_GetUserPoolInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: diskErr()}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "GroupName": "admins"})
	w := doOp(t, ro, "DeleteGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestDeleteGroup_DeleteGroupInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{deleteGroupErr: diskErr()}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "GroupName": "admins"})
	w := doOp(t, ro, "DeleteGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestGetGroup_GetUserPoolInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: diskErr()}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "GroupName": "admins"})
	w := doOp(t, ro, "GetGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestGetGroup_GetGroupInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getGroupFn: func(_, _ string) (*GroupMetadata, error) { return nil, diskErr() },
	}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "GroupName": "admins"})
	w := doOp(t, ro, "GetGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestUpdateGroup_GetUserPoolInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: diskErr()}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "GroupName": "admins"})
	w := doOp(t, ro, "UpdateGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestUpdateGroup_UpdateGroupInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{updateGroupErr: diskErr()}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "GroupName": "admins"})
	w := doOp(t, ro, "UpdateGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestListGroups_GetUserPoolInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: diskErr()}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1"})
	w := doOp(t, ro, "ListGroups", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestListGroups_ListGroupsInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{listGroupsErr: diskErr()}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1"})
	w := doOp(t, ro, "ListGroups", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestAdminAddUserToGroup_GetUserPoolInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: diskErr()}}
	body, _ := json.Marshal(
		map[string]any{"UserPoolId": "pool1", "GroupName": "admins", "Username": "alice"},
	)
	w := doOp(t, ro, "AdminAddUserToGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestAdminAddUserToGroup_GetGroupInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getGroupFn: func(_, _ string) (*GroupMetadata, error) { return nil, diskErr() },
	}}
	body, _ := json.Marshal(
		map[string]any{"UserPoolId": "pool1", "GroupName": "admins", "Username": "alice"},
	)
	w := doOp(t, ro, "AdminAddUserToGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestAdminAddUserToGroup_GetUserInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getGroupFn: groupOK(),
		getUserFn:  func(_, _ string) (*UserMetadata, error) { return nil, diskErr() },
	}}
	body, _ := json.Marshal(
		map[string]any{"UserPoolId": "pool1", "GroupName": "admins", "Username": "alice"},
	)
	w := doOp(t, ro, "AdminAddUserToGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestAdminAddUserToGroup_AddUserToGroupInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getGroupFn:        groupOK(),
		getUserFn:         userOK(),
		addUserToGroupErr: diskErr(),
	}}
	body, _ := json.Marshal(
		map[string]any{"UserPoolId": "pool1", "GroupName": "admins", "Username": "alice"},
	)
	w := doOp(t, ro, "AdminAddUserToGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestAdminRemoveUserFromGroup_GetUserPoolInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: diskErr()}}
	body, _ := json.Marshal(
		map[string]any{"UserPoolId": "pool1", "GroupName": "admins", "Username": "alice"},
	)
	w := doOp(t, ro, "AdminRemoveUserFromGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestAdminRemoveUserFromGroup_GetGroupInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getGroupFn: func(_, _ string) (*GroupMetadata, error) { return nil, diskErr() },
	}}
	body, _ := json.Marshal(
		map[string]any{"UserPoolId": "pool1", "GroupName": "admins", "Username": "alice"},
	)
	w := doOp(t, ro, "AdminRemoveUserFromGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestAdminRemoveUserFromGroup_GetUserInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getGroupFn: groupOK(),
		getUserFn:  func(_, _ string) (*UserMetadata, error) { return nil, diskErr() },
	}}
	body, _ := json.Marshal(
		map[string]any{"UserPoolId": "pool1", "GroupName": "admins", "Username": "alice"},
	)
	w := doOp(t, ro, "AdminRemoveUserFromGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestAdminRemoveUserFromGroup_RemoveUserFromGroupInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getGroupFn:             groupOK(),
		getUserFn:              userOK(),
		removeUserFromGroupErr: diskErr(),
	}}
	body, _ := json.Marshal(
		map[string]any{"UserPoolId": "pool1", "GroupName": "admins", "Username": "alice"},
	)
	w := doOp(t, ro, "AdminRemoveUserFromGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestAdminListGroupsForUser_GetUserPoolInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: diskErr()}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "Username": "alice"})
	w := doOp(t, ro, "AdminListGroupsForUser", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestAdminListGroupsForUser_GetUserInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getUserFn: func(_, _ string) (*UserMetadata, error) { return nil, diskErr() },
	}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "Username": "alice"})
	w := doOp(t, ro, "AdminListGroupsForUser", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestAdminListGroupsForUser_ListGroupsForUserInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getUserFn:            userOK(),
		listGroupsForUserErr: diskErr(),
	}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "Username": "alice"})
	w := doOp(t, ro, "AdminListGroupsForUser", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestListUsersInGroup_GetUserPoolInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{getErr: diskErr()}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "GroupName": "admins"})
	w := doOp(t, ro, "ListUsersInGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestListUsersInGroup_GetGroupInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getGroupFn: func(_, _ string) (*GroupMetadata, error) { return nil, diskErr() },
	}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "GroupName": "admins"})
	w := doOp(t, ro, "ListUsersInGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func TestListUsersInGroup_ListUsersInGroupInternalError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getGroupFn:          groupOK(),
		listUsersInGroupErr: diskErr(),
	}}
	body, _ := json.Marshal(map[string]any{"UserPoolId": "pool1", "GroupName": "admins"})
	w := doOp(t, ro, "ListUsersInGroup", string(body))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrorType(t, w, ErrTypeInternalErrorException)
}

func signUpAndConfirmUser(t *testing.T, ro *Router, poolID, clientID, username, password string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"ClientId": clientID,
		"Username": username,
		"Password": password,
	})
	w := doOp(t, ro, "SignUp", string(body))
	require.Equal(t, http.StatusOK, w.Code)

	confirmBody, _ := json.Marshal(map[string]any{
		"UserPoolId": poolID,
		"Username":   username,
	})
	require.Equal(t, http.StatusOK, doOp(t, ro, "AdminConfirmSignUp", string(confirmBody)).Code)
}
