package cognito

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func doOp(t *testing.T, ro *Router, op, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService."+op)
	w := httptest.NewRecorder()
	ro.ServeHTTP(w, req)
	return w
}

func createPool(t *testing.T, ro *Router, name string) string {
	t.Helper()
	w := doOp(t, ro, "CreateUserPool", fmt.Sprintf(`{"PoolName":%q}`, name))
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		UserPool struct {
			Id string `json:"Id"`
		} `json:"UserPool"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotEmpty(t, resp.UserPool.Id)
	return resp.UserPool.Id
}

func TestCreateUserPool_Success(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "CreateUserPool", `{
		"PoolName": "my-pool",
		"MfaConfiguration": "OPTIONAL",
		"UserPoolTier": "PLUS"
	}`)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPool struct {
			Id               string          `json:"Id"`
			Name             string          `json:"Name"`
			Arn              string          `json:"Arn"`
			Status           string          `json:"Status"`
			MfaConfiguration string          `json:"MfaConfiguration"`
			UserPoolTier     string          `json:"UserPoolTier"`
			SchemaAttributes json.RawMessage `json:"SchemaAttributes"`
			CreationDate     float64         `json:"CreationDate"`
			LastModifiedDate float64         `json:"LastModifiedDate"`
		} `json:"UserPool"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	pool := resp.UserPool
	assert.NotEmpty(t, pool.Id)
	assert.True(t, strings.HasPrefix(pool.Id, "us-east-1_"), "pool ID must have region prefix")
	assert.Equal(t, "my-pool", pool.Name)
	assert.Equal(t, "Active", pool.Status)
	assert.Equal(t, "OPTIONAL", pool.MfaConfiguration)
	assert.Equal(t, "PLUS", pool.UserPoolTier)
	assert.Contains(t, pool.Arn, pool.Id)
	assert.Contains(t, pool.Arn, "arn:aws:cognito-idp:")
	assert.Greater(t, pool.CreationDate, float64(0))
	assert.Equal(t, pool.CreationDate, pool.LastModifiedDate)

	// SchemaAttributes must include standard OIDC attributes
	var attrs []map[string]any
	require.NoError(t, json.Unmarshal(pool.SchemaAttributes, &attrs))
	names := make([]string, 0, len(attrs))
	for _, a := range attrs {
		names = append(names, a["Name"].(string))
	}
	assert.Contains(t, names, "sub")
	assert.Contains(t, names, "email")
	assert.Contains(t, names, "phone_number")
}

func TestCreateUserPool_Defaults(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "CreateUserPool", `{"PoolName": "defaults-pool"}`)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPool struct {
			MfaConfiguration string `json:"MfaConfiguration"`
			UserPoolTier     string `json:"UserPoolTier"`
		} `json:"UserPool"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "OFF", resp.UserPool.MfaConfiguration)
	assert.Equal(t, "ESSENTIALS", resp.UserPool.UserPoolTier)
}

func TestCreateUserPool_WithCustomSchema(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "CreateUserPool", `{
		"PoolName": "schema-pool",
		"Schema": [{"AttributeDataType":"String","Name":"custom:dept","Mutable":true}]
	}`)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPool struct {
			SchemaAttributes json.RawMessage `json:"SchemaAttributes"`
		} `json:"UserPool"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	var attrs []map[string]any
	require.NoError(t, json.Unmarshal(resp.UserPool.SchemaAttributes, &attrs))
	names := make([]string, 0, len(attrs))
	for _, a := range attrs {
		names = append(names, a["Name"].(string))
	}
	assert.Contains(t, names, "sub")
	assert.Contains(t, names, "custom:dept")
}

func TestCreateUserPool_InvalidSchema(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "CreateUserPool", `{"PoolName":"pool","Schema":42}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestListUserPools_InvalidNextToken(t *testing.T) {
	ro := newTestRouter(t)
	createPool(t, ro, "pool-0")

	w := doOp(t, ro, "ListUserPools", `{"MaxResults":10,"NextToken":"nonexistent-token"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestCreateUserPool_MissingPoolName(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "CreateUserPool", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestCreateUserPool_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "CreateUserPool", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestCreateUserPool_InvalidPoolName(t *testing.T) {
	tests := []struct {
		name     string
		poolName string
	}{
		{name: "invalid chars", poolName: "pool!invalid"},
		{name: "too long", poolName: strings.Repeat("a", 129)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ro := newTestRouter(t)
			w := doOp(t, ro, "CreateUserPool", fmt.Sprintf(`{"PoolName":%q}`, tt.poolName))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp errResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
		})
	}
}

func TestDescribeUserPool_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "describe-pool")

	w := doOp(t, ro, "DescribeUserPool", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPool struct {
			Id   string `json:"Id"`
			Name string `json:"Name"`
		} `json:"UserPool"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, poolID, resp.UserPool.Id)
	assert.Equal(t, "describe-pool", resp.UserPool.Name)
}

func TestDescribeUserPool_NotFound(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "DescribeUserPool", `{"UserPoolId":"us-east-1_NOTEXIST"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeResourceNotFoundException, resp.Type)
}

func TestDescribeUserPool_MissingID(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "DescribeUserPool", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestDescribeUserPool_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "DescribeUserPool", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateUserPool_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "update-pool")

	w := doOp(t, ro, "UpdateUserPool", fmt.Sprintf(`{
		"UserPoolId": %q,
		"PoolName": "renamed-pool",
		"MfaConfiguration": "ON"
	}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)

	// Verify via Describe
	w2 := doOp(t, ro, "DescribeUserPool", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	require.Equal(t, http.StatusOK, w2.Code)

	var resp struct {
		UserPool struct {
			Name             string  `json:"Name"`
			MfaConfiguration string  `json:"MfaConfiguration"`
			LastModifiedDate float64 `json:"LastModifiedDate"`
			CreationDate     float64 `json:"CreationDate"`
		} `json:"UserPool"`
	}
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	assert.Equal(t, "renamed-pool", resp.UserPool.Name)
	assert.Equal(t, "ON", resp.UserPool.MfaConfiguration)
	assert.GreaterOrEqual(t, resp.UserPool.LastModifiedDate, resp.UserPool.CreationDate)
}

func TestUpdateUserPool_NotFound(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "UpdateUserPool", `{"UserPoolId":"us-east-1_NOTEXIST","PoolName":"x"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeResourceNotFoundException, resp.Type)
}

func TestUpdateUserPool_MissingID(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "UpdateUserPool", `{"PoolName":"x"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestUpdateUserPool_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "UpdateUserPool", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeleteUserPool_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "delete-pool")

	w := doOp(t, ro, "DeleteUserPool", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)

	// Verify it's gone
	w2 := doOp(t, ro, "DescribeUserPool", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	assert.Equal(t, ErrTypeResourceNotFoundException, resp.Type)
}

func TestDeleteUserPool_NotFound(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "DeleteUserPool", `{"UserPoolId":"us-east-1_NOTEXIST"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeResourceNotFoundException, resp.Type)
}

func TestDeleteUserPool_MissingID(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "DeleteUserPool", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestDeleteUserPool_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "DeleteUserPool", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListUserPools_Empty(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ListUserPools", `{"MaxResults":10}`)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPools []any  `json:"UserPools"`
		NextToken string `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Empty(t, resp.UserPools)
	assert.Empty(t, resp.NextToken)
}

func TestListUserPools_SinglePage(t *testing.T) {
	ro := newTestRouter(t)
	ids := make([]string, 3)
	for i := range ids {
		ids[i] = createPool(t, ro, fmt.Sprintf("pool-%d", i))
	}

	w := doOp(t, ro, "ListUserPools", `{"MaxResults":10}`)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		UserPools []struct {
			Id   string `json:"Id"`
			Name string `json:"Name"`
		} `json:"UserPools"`
		NextToken string `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp.UserPools, 3)
	assert.Empty(t, resp.NextToken)
	for _, p := range resp.UserPools {
		assert.NotEmpty(t, p.Id)
		assert.NotEmpty(t, p.Name)
	}
}

func TestListUserPools_Pagination(t *testing.T) {
	ro := newTestRouter(t)
	for i := range 5 {
		createPool(t, ro, fmt.Sprintf("page-pool-%d", i))
	}

	// Page 1: MaxResults=2
	w1 := doOp(t, ro, "ListUserPools", `{"MaxResults":2}`)
	require.Equal(t, http.StatusOK, w1.Code)
	var resp1 struct {
		UserPools []struct {
			Id string `json:"Id"`
		} `json:"UserPools"`
		NextToken string `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w1.Body).Decode(&resp1))
	assert.Len(t, resp1.UserPools, 2)
	assert.NotEmpty(t, resp1.NextToken)

	// Page 2
	w2 := doOp(
		t,
		ro,
		"ListUserPools",
		fmt.Sprintf(`{"MaxResults":2,"NextToken":%q}`, resp1.NextToken),
	)
	require.Equal(t, http.StatusOK, w2.Code)
	var resp2 struct {
		UserPools []struct {
			Id string `json:"Id"`
		} `json:"UserPools"`
		NextToken string `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp2))
	assert.Len(t, resp2.UserPools, 2)
	assert.NotEmpty(t, resp2.NextToken)

	// Page 3: last page
	w3 := doOp(
		t,
		ro,
		"ListUserPools",
		fmt.Sprintf(`{"MaxResults":2,"NextToken":%q}`, resp2.NextToken),
	)
	require.Equal(t, http.StatusOK, w3.Code)
	var resp3 struct {
		UserPools []struct {
			Id string `json:"Id"`
		} `json:"UserPools"`
		NextToken string `json:"NextToken"`
	}
	require.NoError(t, json.NewDecoder(w3.Body).Decode(&resp3))
	assert.Len(t, resp3.UserPools, 1)
	assert.Empty(t, resp3.NextToken)

	// All IDs unique across pages
	allIDs := make(map[string]bool)
	for _, p := range resp1.UserPools {
		allIDs[p.Id] = true
	}
	for _, p := range resp2.UserPools {
		allIDs[p.Id] = true
	}
	for _, p := range resp3.UserPools {
		allIDs[p.Id] = true
	}
	assert.Len(t, allIDs, 5)
}

func TestListUserPools_MissingMaxResults(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ListUserPools", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestListUserPools_InvalidMaxResults(t *testing.T) {
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
			w := doOp(t, ro, "ListUserPools", fmt.Sprintf(`{"MaxResults":%d}`, tt.maxResults))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp errResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
		})
	}
}

func TestListUserPools_InvalidBody(t *testing.T) {
	ro := newTestRouter(t)
	w := doOp(t, ro, "ListUserPools", `not-json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateUserPool_AllOptionalFields(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "all-fields-pool")

	w := doOp(t, ro, "UpdateUserPool", fmt.Sprintf(`{
		"UserPoolId": %q,
		"DeletionProtection": "ACTIVE",
		"Policies": {"PasswordPolicy": {"MinimumLength": 8}},
		"AutoVerifiedAttributes": ["email"],
		"LambdaConfig": {"PreSignUp": "arn:aws:lambda:us-east-1:000:function:fn"},
		"EmailConfiguration": {"EmailSendingAccount": "COGNITO_DEFAULT"},
		"SmsConfiguration": {"SnsCallerArn": "arn:aws:iam::000:role/r"},
		"DeviceConfiguration": {"ChallengeRequiredOnNewDevice": true},
		"AdminCreateUserConfig": {"AllowAdminCreateUserOnly": true},
		"AccountRecoverySetting": {"RecoveryMechanisms": [{"Name":"verified_email","Priority":1}]},
		"UserAttributeUpdateSettings": {"AttributesRequireVerificationBeforeUpdate": ["email"]},
		"UserPoolAddOns": {"AdvancedSecurityMode": "ENFORCED"},
		"VerificationMessageTemplate": {"DefaultEmailOption": "CONFIRM_WITH_CODE"},
		"UserPoolTags": {"env": "test"},
		"UserPoolTier": "PLUS",
		"EmailVerificationMessage": "code {####}",
		"EmailVerificationSubject": "Verify",
		"SmsAuthenticationMessage": "code {####}",
		"SmsVerificationMessage": "code {####}"
	}`, poolID))
	require.Equal(t, http.StatusOK, w.Code)

	w2 := doOp(t, ro, "DescribeUserPool", fmt.Sprintf(`{"UserPoolId":%q}`, poolID))
	require.Equal(t, http.StatusOK, w2.Code)
	var resp struct {
		UserPool struct {
			DeletionProtection          string            `json:"DeletionProtection"`
			AutoVerifiedAttributes      []string          `json:"AutoVerifiedAttributes"`
			UserPoolTags                map[string]string `json:"UserPoolTags"`
			UserPoolTier                string            `json:"UserPoolTier"`
			EmailVerificationMessage    string            `json:"EmailVerificationMessage"`
			EmailVerificationSubject    string            `json:"EmailVerificationSubject"`
			SmsAuthenticationMessage    string            `json:"SmsAuthenticationMessage"`
			SmsVerificationMessage      string            `json:"SmsVerificationMessage"`
			Policies                    json.RawMessage   `json:"Policies"`
			LambdaConfig                json.RawMessage   `json:"LambdaConfig"`
			EmailConfiguration          json.RawMessage   `json:"EmailConfiguration"`
			SmsConfiguration            json.RawMessage   `json:"SmsConfiguration"`
			DeviceConfiguration         json.RawMessage   `json:"DeviceConfiguration"`
			AdminCreateUserConfig       json.RawMessage   `json:"AdminCreateUserConfig"`
			AccountRecoverySetting      json.RawMessage   `json:"AccountRecoverySetting"`
			UserAttributeUpdateSettings json.RawMessage   `json:"UserAttributeUpdateSettings"`
			UserPoolAddOns              json.RawMessage   `json:"UserPoolAddOns"`
			VerificationMessageTemplate json.RawMessage   `json:"VerificationMessageTemplate"`
		} `json:"UserPool"`
	}
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	pool := resp.UserPool
	assert.Equal(t, "ACTIVE", pool.DeletionProtection)
	assert.Equal(t, []string{"email"}, pool.AutoVerifiedAttributes)
	assert.Equal(t, map[string]string{"env": "test"}, pool.UserPoolTags)
	assert.Equal(t, "PLUS", pool.UserPoolTier)
	assert.Equal(t, "code {####}", pool.EmailVerificationMessage)
	assert.Equal(t, "Verify", pool.EmailVerificationSubject)
	assert.Equal(t, "code {####}", pool.SmsAuthenticationMessage)
	assert.Equal(t, "code {####}", pool.SmsVerificationMessage)
	assert.JSONEq(t, `{"PasswordPolicy":{"MinimumLength":8}}`, string(pool.Policies))
	assert.JSONEq(
		t,
		`{"PreSignUp":"arn:aws:lambda:us-east-1:000:function:fn"}`,
		string(pool.LambdaConfig),
	)
	assert.JSONEq(t, `{"EmailSendingAccount":"COGNITO_DEFAULT"}`, string(pool.EmailConfiguration))
	assert.JSONEq(t, `{"SnsCallerArn":"arn:aws:iam::000:role/r"}`, string(pool.SmsConfiguration))
	assert.JSONEq(t, `{"ChallengeRequiredOnNewDevice":true}`, string(pool.DeviceConfiguration))
	assert.JSONEq(t, `{"AllowAdminCreateUserOnly":true}`, string(pool.AdminCreateUserConfig))
	assert.JSONEq(
		t,
		`{"RecoveryMechanisms":[{"Name":"verified_email","Priority":1}]}`,
		string(pool.AccountRecoverySetting),
	)
	assert.JSONEq(
		t,
		`{"AttributesRequireVerificationBeforeUpdate":["email"]}`,
		string(pool.UserAttributeUpdateSettings),
	)
	assert.JSONEq(t, `{"AdvancedSecurityMode":"ENFORCED"}`, string(pool.UserPoolAddOns))
	assert.JSONEq(
		t,
		`{"DefaultEmailOption":"CONFIRM_WITH_CODE"}`,
		string(pool.VerificationMessageTemplate),
	)
}

func TestStorageErrors(t *testing.T) {
	storageErr := errors.New("storage error")
	tests := []struct {
		name  string
		store mockStore
		op    string
		body  string
	}{
		{
			name:  "CreateUserPool",
			store: mockStore{createErr: storageErr},
			op:    "CreateUserPool",
			body:  `{"PoolName":"pool"}`,
		},
		{
			name:  "DescribeUserPool",
			store: mockStore{getErr: storageErr},
			op:    "DescribeUserPool",
			body:  `{"UserPoolId":"us-east-1_Test12345"}`,
		},
		{
			name:  "UpdateUserPool",
			store: mockStore{updateErr: storageErr},
			op:    "UpdateUserPool",
			body:  `{"UserPoolId":"us-east-1_Test12345","PoolName":"x"}`,
		},
		{
			name:  "DeleteUserPool",
			store: mockStore{deleteErr: storageErr},
			op:    "DeleteUserPool",
			body:  `{"UserPoolId":"us-east-1_Test12345"}`,
		},
		{
			name:  "ListUserPools",
			store: mockStore{listErr: storageErr},
			op:    "ListUserPools",
			body:  `{"MaxResults":10}`,
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
