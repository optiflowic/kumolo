package cognito

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createClient creates a user pool client and returns its ClientId.
func createClient(t *testing.T, ro *Router, poolID, name string) string {
	t.Helper()
	w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(
		`{"UserPoolId":%q,"ClientName":%q}`, poolID, name,
	))
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		UserPoolClient struct {
			ClientId string `json:"ClientId"`
		} `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotEmpty(t, resp.UserPoolClient.ClientId)
	return resp.UserPoolClient.ClientId
}

func TestCreateUserPoolClient_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "test-pool")

	w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientName": "my-app",
		"ExplicitAuthFlows": ["ALLOW_USER_SRP_AUTH","ALLOW_REFRESH_TOKEN_AUTH"],
		"RefreshTokenValidity": 30,
		"AccessTokenValidity": 1,
		"EnableTokenRevocation": true
	}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPoolClient struct {
			ClientId              string   `json:"ClientId"`
			ClientName            string   `json:"ClientName"`
			UserPoolId            string   `json:"UserPoolId"`
			ClientSecret          string   `json:"ClientSecret"`
			CreationDate          float64  `json:"CreationDate"`
			LastModifiedDate      float64  `json:"LastModifiedDate"`
			EnableTokenRevocation bool     `json:"EnableTokenRevocation"`
			ExplicitAuthFlows     []string `json:"ExplicitAuthFlows"`
			RefreshTokenValidity  int      `json:"RefreshTokenValidity"`
			AccessTokenValidity   int      `json:"AccessTokenValidity"`
		} `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	c := resp.UserPoolClient
	assert.Len(t, c.ClientId, clientIDLen)
	assert.Equal(t, "my-app", c.ClientName)
	assert.Equal(t, poolID, c.UserPoolId)
	assert.Empty(t, c.ClientSecret)
	assert.Greater(t, c.CreationDate, float64(0))
	assert.Equal(t, c.CreationDate, c.LastModifiedDate)
	assert.True(t, c.EnableTokenRevocation)
	assert.Equal(
		t,
		[]string{"ALLOW_USER_SRP_AUTH", "ALLOW_REFRESH_TOKEN_AUTH"},
		c.ExplicitAuthFlows,
	)
	assert.Equal(t, 30, c.RefreshTokenValidity)
	assert.Equal(t, 1, c.AccessTokenValidity)
}

func TestCreateUserPoolClient_GenerateSecret(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "secret-pool")

	w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientName": "server-app",
		"GenerateSecret": true
	}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPoolClient struct {
			ClientId     string `json:"ClientId"`
			ClientSecret string `json:"ClientSecret"`
		} `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp.UserPoolClient.ClientSecret, clientSecretLen)
}

func TestCreateUserPoolClient_ProvidedSecret(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "custom-secret-pool")

	w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientName": "custom-secret-app",
		"ClientSecret": "mysecret123456789012345678901234567890123456789012"
	}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPoolClient struct {
			ClientSecret string `json:"ClientSecret"`
		} `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(
		t,
		"mysecret123456789012345678901234567890123456789012",
		resp.UserPoolClient.ClientSecret,
	)
}

func TestCreateUserPoolClient_DefaultEnableTokenRevocation(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "revoke-pool")

	// EnableTokenRevocation not set → should default to true
	w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientName": "default-app"
	}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPoolClient struct {
			EnableTokenRevocation bool `json:"EnableTokenRevocation"`
		} `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.UserPoolClient.EnableTokenRevocation)
}

func TestCreateUserPoolClient_BothGenerateSecretAndClientSecret(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "conflict-pool")

	w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientName": "app",
		"GenerateSecret": true,
		"ClientSecret": "somesecret"
	}`, poolID))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestCreateUserPoolClient_PoolNotFound(t *testing.T) {
	ro := newTestRouter(t)

	w := doOp(t, ro, "CreateUserPoolClient", `{
		"UserPoolId": "us-east-1_NOTEXIST",
		"ClientName": "app"
	}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeResourceNotFoundException, resp.Type)
}

func TestCreateUserPoolClient_MissingPoolId(t *testing.T) {
	ro := newTestRouter(t)

	w := doOp(t, ro, "CreateUserPoolClient", `{"ClientName":"app"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestCreateUserPoolClient_MissingClientName(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")

	w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestCreateUserPoolClient_InvalidClientName(t *testing.T) {
	tests := []struct {
		name       string
		clientName string
	}{
		{"invalid chars", "app!@#"},
		{"too long", strings.Repeat("a", 129)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ro := newTestRouter(t)
			poolID := createPool(t, ro, "pool")
			w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(
				`{"UserPoolId":%q,"ClientName":%q}`, poolID, tt.clientName,
			))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp errResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
		})
	}
}

func TestCreateUserPoolClient_InvalidClientSecretLength(t *testing.T) {
	tests := []struct {
		name   string
		secret string
	}{
		{"too short", strings.Repeat("a", 23)},
		{"too long", strings.Repeat("a", 65)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ro := newTestRouter(t)
			poolID := createPool(t, ro, "pool")
			w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(
				`{"UserPoolId":%q,"ClientName":"app","ClientSecret":%q}`, poolID, tt.secret,
			))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp errResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
		})
	}
}

func TestCreateUserPoolClient_InvalidClientSecretPattern(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")
	// secret with invalid characters (spaces are not in [\w+]+)
	secret := "invalid secret with spaces!!!"
	w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(
		`{"UserPoolId":%q,"ClientName":"app","ClientSecret":%q}`, poolID, secret,
	))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestCreateUserPoolClient_ExplicitDisableTokenRevocation(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "revoke-false-pool")

	w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientName": "app",
		"EnableTokenRevocation": false
	}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPoolClient struct {
			EnableTokenRevocation bool `json:"EnableTokenRevocation"`
		} `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.UserPoolClient.EnableTokenRevocation)
}

func TestCreateUserPoolClient_InvalidUserPoolID(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "CreateUserPoolClient", `{
		"UserPoolId": "../../etc/passwd",
		"ClientName": "app"
	}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestCreateUserPoolClient_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "CreateUserPoolClient", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDescribeUserPoolClient_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")
	clientID := createClient(t, ro, poolID, "my-app")

	w := doOp(t, ro, "DescribeUserPoolClient", fmt.Sprintf(
		`{"UserPoolId":%q,"ClientId":%q}`, poolID, clientID,
	))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPoolClient struct {
			ClientId   string `json:"ClientId"`
			ClientName string `json:"ClientName"`
			UserPoolId string `json:"UserPoolId"`
		} `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, clientID, resp.UserPoolClient.ClientId)
	assert.Equal(t, "my-app", resp.UserPoolClient.ClientName)
	assert.Equal(t, poolID, resp.UserPoolClient.UserPoolId)
}

func TestDescribeUserPoolClient_SecretPreserved(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")

	// Create with generated secret
	w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientName": "secret-app",
		"GenerateSecret": true
	}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)
	var createResp struct {
		UserPoolClient struct {
			ClientId     string `json:"ClientId"`
			ClientSecret string `json:"ClientSecret"`
		} `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&createResp))
	clientID := createResp.UserPoolClient.ClientId
	secret := createResp.UserPoolClient.ClientSecret

	// Describe should return the same secret
	w2 := doOp(t, ro, "DescribeUserPoolClient", fmt.Sprintf(
		`{"UserPoolId":%q,"ClientId":%q}`, poolID, clientID,
	))
	require.Equal(t, http.StatusOK, w2.Code)
	var descResp struct {
		UserPoolClient struct {
			ClientSecret string `json:"ClientSecret"`
		} `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&descResp))
	assert.Equal(t, secret, descResp.UserPoolClient.ClientSecret)
}

func TestDescribeUserPoolClient_NotFound(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")

	w := doOp(t, ro, "DescribeUserPoolClient", fmt.Sprintf(
		`{"UserPoolId":%q,"ClientId":"nonexistentclientid0000000"}`, poolID,
	))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeResourceNotFoundException, resp.Type)
}

func TestDescribeUserPoolClient_MissingPoolId(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "DescribeUserPoolClient", `{"ClientId":"abc"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestDescribeUserPoolClient_MissingClientId(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")

	w := doOp(t, ro, "DescribeUserPoolClient", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestDescribeUserPoolClient_InvalidUserPoolID(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "DescribeUserPoolClient", `{
		"UserPoolId": "../../etc/passwd",
		"ClientId": "testclientid00000000000000"
	}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestDescribeUserPoolClient_InvalidClientID(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")
	w := doOp(t, ro, "DescribeUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientId": "../../etc/passwd"
	}`, poolID))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestDescribeUserPoolClient_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "DescribeUserPoolClient", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateUserPoolClient_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")
	clientID := createClient(t, ro, poolID, "original-name")

	w := doOp(t, ro, "UpdateUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientId": %q,
		"ClientName": "updated-name",
		"RefreshTokenValidity": 7,
		"ExplicitAuthFlows": ["ALLOW_USER_PASSWORD_AUTH","ALLOW_REFRESH_TOKEN_AUTH"],
		"EnableTokenRevocation": false
	}`, poolID, clientID))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPoolClient struct {
			ClientId              string   `json:"ClientId"`
			ClientName            string   `json:"ClientName"`
			RefreshTokenValidity  int      `json:"RefreshTokenValidity"`
			ExplicitAuthFlows     []string `json:"ExplicitAuthFlows"`
			EnableTokenRevocation bool     `json:"EnableTokenRevocation"`
			LastModifiedDate      float64  `json:"LastModifiedDate"`
			CreationDate          float64  `json:"CreationDate"`
		} `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	c := resp.UserPoolClient
	assert.Equal(t, clientID, c.ClientId)
	assert.Equal(t, "updated-name", c.ClientName)
	assert.Equal(t, 7, c.RefreshTokenValidity)
	assert.Equal(
		t,
		[]string{"ALLOW_USER_PASSWORD_AUTH", "ALLOW_REFRESH_TOKEN_AUTH"},
		c.ExplicitAuthFlows,
	)
	assert.False(t, c.EnableTokenRevocation)
	assert.GreaterOrEqual(t, c.LastModifiedDate, c.CreationDate)
}

func TestUpdateUserPoolClient_SecretPreservedAfterUpdate(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")

	// Create with secret
	w := doOp(t, ro, "CreateUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientName": "secret-app",
		"GenerateSecret": true
	}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)
	var createResp struct {
		UserPoolClient struct {
			ClientId     string `json:"ClientId"`
			ClientSecret string `json:"ClientSecret"`
		} `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&createResp))
	clientID := createResp.UserPoolClient.ClientId
	originalSecret := createResp.UserPoolClient.ClientSecret

	// Update should preserve the secret
	w2 := doOp(t, ro, "UpdateUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientId": %q,
		"ClientName": "updated-app"
	}`, poolID, clientID))
	require.Equal(t, http.StatusOK, w2.Code)

	var updateResp struct {
		UserPoolClient struct {
			ClientSecret string `json:"ClientSecret"`
		} `json:"UserPoolClient"`
	}
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&updateResp))
	assert.Equal(t, originalSecret, updateResp.UserPoolClient.ClientSecret)
}

func TestUpdateUserPoolClient_NotFound(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")

	w := doOp(t, ro, "UpdateUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientId": "nonexistentclientid0000000"
	}`, poolID))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeResourceNotFoundException, resp.Type)
}

func TestUpdateUserPoolClient_MissingPoolId(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "UpdateUserPoolClient", `{"ClientId":"abc"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestUpdateUserPoolClient_MissingClientId(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")

	w := doOp(t, ro, "UpdateUserPoolClient", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestUpdateUserPoolClient_InvalidUserPoolID(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "UpdateUserPoolClient", `{
		"UserPoolId": "../../etc/passwd",
		"ClientId": "testclientid00000000000000"
	}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestUpdateUserPoolClient_InvalidClientID(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")
	w := doOp(t, ro, "UpdateUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientId": "../../etc/passwd"
	}`, poolID))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestUpdateUserPoolClient_InvalidClientName(t *testing.T) {
	tests := []struct {
		name       string
		clientName string
	}{
		{"invalid chars", "app!@#"},
		{"too long", strings.Repeat("a", 129)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ro := newTestRouter(t)
			poolID := createPool(t, ro, "pool")
			clientID := createClient(t, ro, poolID, "original-name")
			w := doOp(t, ro, "UpdateUserPoolClient", fmt.Sprintf(
				`{"UserPoolId":%q,"ClientId":%q,"ClientName":%q}`, poolID, clientID, tt.clientName,
			))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp errResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
		})
	}
}

func TestUpdateUserPoolClient_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "UpdateUserPoolClient", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeleteUserPoolClient_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")
	clientID := createClient(t, ro, poolID, "app")

	w := doOp(t, ro, "DeleteUserPoolClient", fmt.Sprintf(
		`{"UserPoolId":%q,"ClientId":%q}`, poolID, clientID,
	))
	require.Equal(t, http.StatusOK, w.Code)

	// Verify it's gone
	w2 := doOp(t, ro, "DescribeUserPoolClient", fmt.Sprintf(
		`{"UserPoolId":%q,"ClientId":%q}`, poolID, clientID,
	))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	assert.Equal(t, ErrTypeResourceNotFoundException, resp.Type)
}

func TestDeleteUserPoolClient_NotFound(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")

	w := doOp(t, ro, "DeleteUserPoolClient", fmt.Sprintf(
		`{"UserPoolId":%q,"ClientId":"nonexistentclientid0000000"}`, poolID,
	))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeResourceNotFoundException, resp.Type)
}

func TestDeleteUserPoolClient_MissingPoolId(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "DeleteUserPoolClient", `{"ClientId":"abc"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestDeleteUserPoolClient_MissingClientId(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")

	w := doOp(t, ro, "DeleteUserPoolClient", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestDeleteUserPoolClient_InvalidUserPoolID(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "DeleteUserPoolClient", `{
		"UserPoolId": "../../etc/passwd",
		"ClientId": "testclientid00000000000000"
	}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestDeleteUserPoolClient_InvalidClientID(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")
	w := doOp(t, ro, "DeleteUserPoolClient", fmt.Sprintf(`{
		"UserPoolId": %q,
		"ClientId": "../../etc/passwd"
	}`, poolID))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestDeleteUserPoolClient_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "DeleteUserPoolClient", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListUserPoolClients_Empty(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")

	w := doOp(t, ro, "ListUserPoolClients", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPoolClients []any  `json:"UserPoolClients"`
		NextToken       string `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Empty(t, resp.UserPoolClients)
	assert.Empty(t, resp.NextToken)
}

func TestListUserPoolClients_SinglePage(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")
	for i := range 3 {
		createClient(t, ro, poolID, fmt.Sprintf("app-%d", i))
	}

	w := doOp(t, ro, "ListUserPoolClients", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPoolClients []struct {
			ClientId   string `json:"ClientId"`
			ClientName string `json:"ClientName"`
			UserPoolId string `json:"UserPoolId"`
		} `json:"UserPoolClients"`
		NextToken string `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp.UserPoolClients, 3)
	assert.Empty(t, resp.NextToken)
	for _, c := range resp.UserPoolClients {
		assert.NotEmpty(t, c.ClientId)
		assert.NotEmpty(t, c.ClientName)
		assert.Equal(t, poolID, c.UserPoolId)
	}
}

func TestListUserPoolClients_Pagination(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")
	for i := range 5 {
		createClient(t, ro, poolID, fmt.Sprintf("app-%d", i))
	}

	// Page 1: MaxResults=2
	w1 := doOp(t, ro, "ListUserPoolClients", fmt.Sprintf(
		`{"UserPoolId":%q,"MaxResults":2}`, poolID,
	))
	require.Equal(t, http.StatusOK, w1.Code)
	var resp1 struct {
		UserPoolClients []struct {
			ClientId string `json:"ClientId"`
		} `json:"UserPoolClients"`
		NextToken string `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w1.Body).Decode(&resp1))
	assert.Len(t, resp1.UserPoolClients, 2)
	assert.NotEmpty(t, resp1.NextToken)

	// Page 2
	w2 := doOp(t, ro, "ListUserPoolClients", fmt.Sprintf(
		`{"UserPoolId":%q,"MaxResults":2,"NextToken":%q}`, poolID, resp1.NextToken,
	))
	require.Equal(t, http.StatusOK, w2.Code)
	var resp2 struct {
		UserPoolClients []struct {
			ClientId string `json:"ClientId"`
		} `json:"UserPoolClients"`
		NextToken string `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp2))
	assert.Len(t, resp2.UserPoolClients, 2)
	assert.NotEmpty(t, resp2.NextToken)

	// Page 3: last page
	w3 := doOp(t, ro, "ListUserPoolClients", fmt.Sprintf(
		`{"UserPoolId":%q,"MaxResults":2,"NextToken":%q}`, poolID, resp2.NextToken,
	))
	require.Equal(t, http.StatusOK, w3.Code)
	var resp3 struct {
		UserPoolClients []struct {
			ClientId string `json:"ClientId"`
		} `json:"UserPoolClients"`
		NextToken string `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w3.Body).Decode(&resp3))
	assert.Len(t, resp3.UserPoolClients, 1)
	assert.Empty(t, resp3.NextToken)

	// All IDs unique across pages
	allIDs := make(map[string]bool)
	for _, c := range resp1.UserPoolClients {
		allIDs[c.ClientId] = true
	}
	for _, c := range resp2.UserPoolClients {
		allIDs[c.ClientId] = true
	}
	for _, c := range resp3.UserPoolClients {
		allIDs[c.ClientId] = true
	}
	assert.Len(t, allIDs, 5)
}

func TestListUserPoolClients_InvalidMaxResults(t *testing.T) {
	tests := []struct {
		name       string
		maxResults int
	}{
		{"zero", 0},
		{"too large", 61},
		{"negative", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ro := newTestRouter(t)
			poolID := createPool(t, ro, "pool")
			w := doOp(t, ro, "ListUserPoolClients", fmt.Sprintf(
				`{"UserPoolId":%q,"MaxResults":%d}`, poolID, tt.maxResults,
			))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp errResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
		})
	}
}

func TestListUserPoolClients_InvalidNextToken(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")
	createClient(t, ro, poolID, "app")

	w := doOp(t, ro, "ListUserPoolClients", fmt.Sprintf(
		`{"UserPoolId":%q,"NextToken":"nonexistent-token"}`, poolID,
	))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestListUserPoolClients_PoolNotFound(t *testing.T) {
	ro := newTestRouter(t)

	w := doOp(t, ro, "ListUserPoolClients", `{"UserPoolId":"us-east-1_NOTEXIST"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeResourceNotFoundException, resp.Type)
}

func TestListUserPoolClients_MissingPoolId(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ListUserPoolClients", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestListUserPoolClients_InvalidUserPoolID(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ListUserPoolClients", `{"UserPoolId":"../../etc/passwd"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestListUserPoolClients_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ListUserPoolClients", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeleteUserPool_WithClients(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool-with-clients")
	createClient(t, ro, poolID, "app-1")
	createClient(t, ro, poolID, "app-2")

	// Delete the pool — should succeed even with clients
	w := doOp(t, ro, "DeleteUserPool", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)

	// Pool must be gone
	w2 := doOp(t, ro, "DescribeUserPool", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
}

func TestUserPoolClientStorageErrors(t *testing.T) {
	storageErr := errors.New("storage error")
	tests := []struct {
		name  string
		store mockStore
		op    string
		body  string
	}{
		{
			name:  "CreateUserPoolClient",
			store: mockStore{createClientErr: storageErr},
			op:    "CreateUserPoolClient",
			body:  `{"UserPoolId":"us-east-1_Test12345","ClientName":"app"}`,
		},
		{
			name:  "DescribeUserPoolClient",
			store: mockStore{getClientErr: storageErr},
			op:    "DescribeUserPoolClient",
			body:  `{"UserPoolId":"us-east-1_Test12345","ClientId":"testclientid00000000000000"}`,
		},
		{
			name:  "UpdateUserPoolClient",
			store: mockStore{updateClientErr: storageErr},
			op:    "UpdateUserPoolClient",
			body:  `{"UserPoolId":"us-east-1_Test12345","ClientId":"testclientid00000000000000"}`,
		},
		{
			name:  "DeleteUserPoolClient",
			store: mockStore{deleteClientErr: storageErr},
			op:    "DeleteUserPoolClient",
			body:  `{"UserPoolId":"us-east-1_Test12345","ClientId":"testclientid00000000000000"}`,
		},
		{
			name:  "ListUserPoolClients",
			store: mockStore{listClientErr: storageErr},
			op:    "ListUserPoolClients",
			body:  `{"UserPoolId":"us-east-1_Test12345"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := tt.store
			ro := &Router{storage: &store}
			w := doOp(t, ro, tt.op, tt.body)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
			var resp errResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, ErrTypeInternalErrorException, resp.Type)
		})
	}
}
