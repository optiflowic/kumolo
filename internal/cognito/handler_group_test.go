package cognito

import (
	"encoding/json"
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

	addBody, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": "admins", "Username": "alice"})
	require.Equal(t, http.StatusOK, doOp(t, ro, "AdminAddUserToGroup", string(addBody)).Code)

	removeBody, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": "admins", "Username": "alice"})
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
		addBody, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": name, "Username": "alice"})
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
		addBody, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": "admins", "Username": u})
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
	signUpAndConfirmUser(t, ro, poolID, "alice", "Pass1234!")
	for _, name := range []string{"beta", "alpha"} {
		addBody, _ := json.Marshal(map[string]any{"UserPoolId": poolID, "GroupName": name, "Username": "alice"})
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

func signUpAndConfirmUser(t *testing.T, ro *Router, poolID, username, password string) {
	t.Helper()
	clientID := createGroupTestClient(t, ro, poolID)

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
