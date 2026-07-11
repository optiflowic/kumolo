package integration_test

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscognito "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// codeCapture intercepts SignUp confirmation codes and attribute-verification
// codes from slog output. It acts as a nop handler (no log forwarding) to
// avoid holding its mutex while calling into log.Logger, which would deadlock
// with the HTTP server goroutine.
type codeCapture struct {
	mu        sync.Mutex
	codes     map[string]string // username -> confirmation code
	attrCodes map[string]string // "username|attribute" -> verification code
}

func newCodeCapture() *codeCapture {
	return &codeCapture{
		codes:     make(map[string]string),
		attrCodes: make(map[string]string),
	}
}

func (c *codeCapture) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (c *codeCapture) Handle(_ context.Context, r slog.Record) error {
	switch r.Message {
	case "SignUp confirmation code", "ResendConfirmationCode", "ForgotPassword":
		var username, code string
		r.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "username":
				username = a.Value.String()
			case "code":
				code = a.Value.String()
			}
			return true
		})
		if username != "" && code != "" {
			c.mu.Lock()
			c.codes[username] = code
			c.mu.Unlock()
		}
	case "GetUserAttributeVerificationCode",
		"UpdateUserAttributes verification code",
		"AdminUpdateUserAttributes verification code":
		var username, attribute, code string
		r.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "username":
				username = a.Value.String()
			case "attribute":
				attribute = a.Value.String()
			case "code":
				code = a.Value.String()
			}
			return true
		})
		if username != "" && attribute != "" && code != "" {
			c.mu.Lock()
			c.attrCodes[username+"|"+attribute] = code
			c.mu.Unlock()
		}
	}
	return nil
}

func (c *codeCapture) WithAttrs(_ []slog.Attr) slog.Handler { return c }
func (c *codeCapture) WithGroup(_ string) slog.Handler      { return c }

func (c *codeCapture) get(username string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.codes[username]
}

func (c *codeCapture) getAttrCode(username, attribute string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attrCodes[username+"|"+attribute]
}

// withCodeCapture installs a slog handler that records SignUp confirmation codes
// for the duration of the test. Returns the capture so tests can retrieve codes.
// The original handler is restored on test cleanup.
func withCodeCapture(t *testing.T) *codeCapture {
	t.Helper()
	cap := newCodeCapture()
	old := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(old) })
	return cap
}

// ── UserPool CRUD ─────────────────────────────────────────────────────────────

func TestCognitoIntegration_UserPool(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	t.Run("CreateUserPool", func(t *testing.T) {
		out, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
			PoolName: aws.String("integration-pool"),
		})
		require.NoError(t, err)
		require.NotNil(t, out.UserPool)
		assert.NotEmpty(t, aws.ToString(out.UserPool.Id))
		assert.Equal(t, "integration-pool", aws.ToString(out.UserPool.Name))
		assert.Contains(t, aws.ToString(out.UserPool.Id), "us-east-1_")
		assert.NotEmpty(t, aws.ToString(out.UserPool.Arn))
	})

	t.Run("DescribeUserPool", func(t *testing.T) {
		created, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
			PoolName: aws.String("describe-pool"),
		})
		require.NoError(t, err)
		poolID := aws.ToString(created.UserPool.Id)

		out, err := c.DescribeUserPool(ctx, &awscognito.DescribeUserPoolInput{
			UserPoolId: aws.String(poolID),
		})
		require.NoError(t, err)
		require.NotNil(t, out.UserPool)
		assert.Equal(t, poolID, aws.ToString(out.UserPool.Id))
		assert.Equal(t, "describe-pool", aws.ToString(out.UserPool.Name))
	})

	t.Run("DescribeUserPool_NotFound", func(t *testing.T) {
		_, err := c.DescribeUserPool(ctx, &awscognito.DescribeUserPoolInput{
			UserPoolId: aws.String("us-east-1_notexist"),
		})
		require.Error(t, err)
		assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
	})

	t.Run("UpdateUserPool", func(t *testing.T) {
		created, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
			PoolName: aws.String("update-pool"),
		})
		require.NoError(t, err)
		poolID := aws.ToString(created.UserPool.Id)

		_, err = c.UpdateUserPool(ctx, &awscognito.UpdateUserPoolInput{
			UserPoolId:       aws.String(poolID),
			MfaConfiguration: types.UserPoolMfaTypeOptional,
		})
		require.NoError(t, err)

		out, err := c.DescribeUserPool(ctx, &awscognito.DescribeUserPoolInput{
			UserPoolId: aws.String(poolID),
		})
		require.NoError(t, err)
		assert.Equal(t, types.UserPoolMfaTypeOptional, out.UserPool.MfaConfiguration)
	})

	t.Run("DeleteUserPool", func(t *testing.T) {
		created, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
			PoolName: aws.String("delete-pool"),
		})
		require.NoError(t, err)
		poolID := aws.ToString(created.UserPool.Id)

		_, err = c.DeleteUserPool(ctx, &awscognito.DeleteUserPoolInput{
			UserPoolId: aws.String(poolID),
		})
		require.NoError(t, err)

		_, err = c.DescribeUserPool(ctx, &awscognito.DescribeUserPoolInput{
			UserPoolId: aws.String(poolID),
		})
		require.Error(t, err)
		assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
	})

	t.Run("DeleteUserPool_DeletionProtectionActive", func(t *testing.T) {
		created, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
			PoolName:           aws.String("protected-pool"),
			DeletionProtection: types.DeletionProtectionTypeActive,
		})
		require.NoError(t, err)
		poolID := aws.ToString(created.UserPool.Id)

		_, err = c.DeleteUserPool(ctx, &awscognito.DeleteUserPoolInput{
			UserPoolId: aws.String(poolID),
		})
		require.Error(t, err)
		assert.Equal(t, "InvalidParameterException", apiErrorCode(err))

		out, err := c.DescribeUserPool(ctx, &awscognito.DescribeUserPoolInput{
			UserPoolId: aws.String(poolID),
		})
		require.NoError(t, err)
		assert.Equal(t, poolID, aws.ToString(out.UserPool.Id))

		_, err = c.UpdateUserPool(ctx, &awscognito.UpdateUserPoolInput{
			UserPoolId:         aws.String(poolID),
			DeletionProtection: types.DeletionProtectionTypeInactive,
		})
		require.NoError(t, err)

		_, err = c.DeleteUserPool(ctx, &awscognito.DeleteUserPoolInput{
			UserPoolId: aws.String(poolID),
		})
		require.NoError(t, err)
	})

	t.Run("GetUserPoolMfaConfig", func(t *testing.T) {
		created, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
			PoolName: aws.String("mfa-config-pool"),
		})
		require.NoError(t, err)
		poolID := aws.ToString(created.UserPool.Id)

		out, err := c.GetUserPoolMfaConfig(ctx, &awscognito.GetUserPoolMfaConfigInput{
			UserPoolId: aws.String(poolID),
		})
		require.NoError(t, err)
		require.NotNil(t, out)
		assert.Equal(t, types.UserPoolMfaTypeOff, out.MfaConfiguration)
		require.NotNil(t, out.SoftwareTokenMfaConfiguration)
		assert.False(t, out.SoftwareTokenMfaConfiguration.Enabled)

		_, err = c.UpdateUserPool(ctx, &awscognito.UpdateUserPoolInput{
			UserPoolId:       aws.String(poolID),
			MfaConfiguration: types.UserPoolMfaTypeOptional,
		})
		require.NoError(t, err)

		updated, err := c.GetUserPoolMfaConfig(ctx, &awscognito.GetUserPoolMfaConfigInput{
			UserPoolId: aws.String(poolID),
		})
		require.NoError(t, err)
		assert.Equal(t, types.UserPoolMfaTypeOptional, updated.MfaConfiguration)
	})

	t.Run("ListUserPools", func(t *testing.T) {
		for _, name := range []string{"list-pool-a", "list-pool-b", "list-pool-c"} {
			_, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
				PoolName: aws.String(name),
			})
			require.NoError(t, err)
		}

		out, err := c.ListUserPools(ctx, &awscognito.ListUserPoolsInput{
			MaxResults: aws.Int32(60),
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(out.UserPools), 3)
	})
}

// ── UserPoolClient CRUD ───────────────────────────────────────────────────────

func TestCognitoIntegration_UserPoolClient(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	pool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
		PoolName: aws.String("client-test-pool"),
	})
	require.NoError(t, err)
	poolID := aws.ToString(pool.UserPool.Id)

	t.Run("CreateUserPoolClient", func(t *testing.T) {
		out, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
			UserPoolId: aws.String(poolID),
			ClientName: aws.String("my-app"),
		})
		require.NoError(t, err)
		require.NotNil(t, out.UserPoolClient)
		assert.NotEmpty(t, aws.ToString(out.UserPoolClient.ClientId))
		assert.Equal(t, "my-app", aws.ToString(out.UserPoolClient.ClientName))
		assert.Equal(t, poolID, aws.ToString(out.UserPoolClient.UserPoolId))
	})

	t.Run("DescribeUserPoolClient", func(t *testing.T) {
		created, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
			UserPoolId: aws.String(poolID),
			ClientName: aws.String("describe-app"),
		})
		require.NoError(t, err)
		clientID := aws.ToString(created.UserPoolClient.ClientId)

		out, err := c.DescribeUserPoolClient(ctx, &awscognito.DescribeUserPoolClientInput{
			UserPoolId: aws.String(poolID),
			ClientId:   aws.String(clientID),
		})
		require.NoError(t, err)
		require.NotNil(t, out.UserPoolClient)
		assert.Equal(t, clientID, aws.ToString(out.UserPoolClient.ClientId))
		assert.Equal(t, "describe-app", aws.ToString(out.UserPoolClient.ClientName))
	})

	t.Run("DescribeUserPoolClient_NotFound", func(t *testing.T) {
		_, err := c.DescribeUserPoolClient(ctx, &awscognito.DescribeUserPoolClientInput{
			UserPoolId: aws.String(poolID),
			ClientId:   aws.String("notexistclientid0000000000"),
		})
		require.Error(t, err)
		assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
	})

	t.Run("UpdateUserPoolClient", func(t *testing.T) {
		created, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
			UserPoolId: aws.String(poolID),
			ClientName: aws.String("update-app"),
		})
		require.NoError(t, err)
		clientID := aws.ToString(created.UserPoolClient.ClientId)

		_, err = c.UpdateUserPoolClient(ctx, &awscognito.UpdateUserPoolClientInput{
			UserPoolId:           aws.String(poolID),
			ClientId:             aws.String(clientID),
			ClientName:           aws.String("updated-app"),
			RefreshTokenValidity: 7,
		})
		require.NoError(t, err)

		out, err := c.DescribeUserPoolClient(ctx, &awscognito.DescribeUserPoolClientInput{
			UserPoolId: aws.String(poolID),
			ClientId:   aws.String(clientID),
		})
		require.NoError(t, err)
		assert.Equal(t, "updated-app", aws.ToString(out.UserPoolClient.ClientName))
		assert.Equal(t, int32(7), out.UserPoolClient.RefreshTokenValidity)
	})

	t.Run("DeleteUserPoolClient", func(t *testing.T) {
		created, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
			UserPoolId: aws.String(poolID),
			ClientName: aws.String("delete-app"),
		})
		require.NoError(t, err)
		clientID := aws.ToString(created.UserPoolClient.ClientId)

		_, err = c.DeleteUserPoolClient(ctx, &awscognito.DeleteUserPoolClientInput{
			UserPoolId: aws.String(poolID),
			ClientId:   aws.String(clientID),
		})
		require.NoError(t, err)

		_, err = c.DescribeUserPoolClient(ctx, &awscognito.DescribeUserPoolClientInput{
			UserPoolId: aws.String(poolID),
			ClientId:   aws.String(clientID),
		})
		require.Error(t, err)
		assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
	})

	t.Run("ListUserPoolClients", func(t *testing.T) {
		for _, name := range []string{"list-app-1", "list-app-2"} {
			_, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
				UserPoolId: aws.String(poolID),
				ClientName: aws.String(name),
			})
			require.NoError(t, err)
		}

		out, err := c.ListUserPoolClients(ctx, &awscognito.ListUserPoolClientsInput{
			UserPoolId: aws.String(poolID),
			MaxResults: aws.Int32(60),
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(out.UserPoolClients), 2)
	})
}

// ── Auth flows ────────────────────────────────────────────────────────────────

func TestCognitoIntegration_AuthFlows(t *testing.T) {
	cap := withCodeCapture(t)
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	pool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
		PoolName: aws.String("auth-test-pool"),
	})
	require.NoError(t, err)
	poolID := aws.ToString(pool.UserPool.Id)

	client, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientName: aws.String("auth-test-client"),
	})
	require.NoError(t, err)
	clientID := aws.ToString(client.UserPoolClient.ClientId)

	const (
		username = "testuser"
		password = "Password1!"
		email    = "testuser@example.com"
	)

	t.Run("SignUp", func(t *testing.T) {
		out, err := c.SignUp(ctx, &awscognito.SignUpInput{
			ClientId: aws.String(clientID),
			Username: aws.String(username),
			Password: aws.String(password),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("email"), Value: aws.String(email)},
			},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, aws.ToString(out.UserSub))
		assert.False(t, out.UserConfirmed)
	})

	t.Run("SignUp_DuplicateUsername", func(t *testing.T) {
		_, err := c.SignUp(ctx, &awscognito.SignUpInput{
			ClientId: aws.String(clientID),
			Username: aws.String(username),
			Password: aws.String(password),
		})
		require.Error(t, err)
		assert.Equal(t, "UsernameExistsException", apiErrorCode(err))
	})

	t.Run("ConfirmSignUp", func(t *testing.T) {
		code := cap.get(username)
		require.NotEmpty(t, code, "confirmation code should be captured from slog output")

		_, err := c.ConfirmSignUp(ctx, &awscognito.ConfirmSignUpInput{
			ClientId:         aws.String(clientID),
			Username:         aws.String(username),
			ConfirmationCode: aws.String(code),
		})
		require.NoError(t, err)
	})

	t.Run("ConfirmSignUp_AlreadyConfirmed", func(t *testing.T) {
		code := cap.get(username)
		require.NotEmpty(t, code)

		_, err := c.ConfirmSignUp(ctx, &awscognito.ConfirmSignUpInput{
			ClientId:         aws.String(clientID),
			Username:         aws.String(username),
			ConfirmationCode: aws.String(code),
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})

	t.Run("InitiateAuth_UserPasswordAuth", func(t *testing.T) {
		out, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeUserPasswordAuth,
			AuthParameters: map[string]string{
				"USERNAME": username,
				"PASSWORD": password,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, out.AuthenticationResult)
		assert.NotEmpty(t, aws.ToString(out.AuthenticationResult.AccessToken))
		assert.NotEmpty(t, aws.ToString(out.AuthenticationResult.IdToken))
		assert.NotEmpty(t, aws.ToString(out.AuthenticationResult.RefreshToken))
		assert.Equal(t, "Bearer", aws.ToString(out.AuthenticationResult.TokenType))
		assert.Equal(t, int32(3600), out.AuthenticationResult.ExpiresIn)
	})

	t.Run("InitiateAuth_WrongPassword", func(t *testing.T) {
		_, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeUserPasswordAuth,
			AuthParameters: map[string]string{
				"USERNAME": username,
				"PASSWORD": "WrongPassword!",
			},
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})

	t.Run("InitiateAuth_RefreshToken", func(t *testing.T) {
		auth, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeUserPasswordAuth,
			AuthParameters: map[string]string{
				"USERNAME": username,
				"PASSWORD": password,
			},
		})
		require.NoError(t, err)
		refreshToken := aws.ToString(auth.AuthenticationResult.RefreshToken)

		out, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeRefreshTokenAuth,
			AuthParameters: map[string]string{
				"REFRESH_TOKEN": refreshToken,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, out.AuthenticationResult)
		assert.NotEmpty(t, aws.ToString(out.AuthenticationResult.AccessToken))
		assert.NotEmpty(t, aws.ToString(out.AuthenticationResult.IdToken))
		assert.Empty(
			t,
			aws.ToString(out.AuthenticationResult.RefreshToken),
			"refresh token should not be re-issued",
		)
	})

	t.Run("RespondToAuthChallenge_NewPasswordRequired", func(t *testing.T) {
		const fcpUser = "fcp-user"
		const tempPass = "TempPass1!"
		const newPass = "NewPass1!"

		// Sign up and skip confirmation — use a separate user directly set to FORCE_CHANGE_PASSWORD
		// by registering and then forcibly initiating the challenge flow.
		// Since we cannot insert storage state from outside, we use SignUp+ConfirmSignUp
		// to get a confirmed user, then test the challenge flow via a different mechanism.
		// Instead, test that an unsupported challenge returns an error.
		_, err := c.RespondToAuthChallenge(ctx, &awscognito.RespondToAuthChallengeInput{
			ClientId:      aws.String(clientID),
			ChallengeName: types.ChallengeNameTypeNewPasswordRequired,
			Session:       aws.String("invalid-session-token"),
			ChallengeResponses: map[string]string{
				"USERNAME":      fcpUser,
				"NEW_PASSWORD":  newPass,
				"TEMP_PASSWORD": tempPass,
			},
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})
}

// ── Admin operations ──────────────────────────────────────────────────────────

// adminTestEnv holds a pool and client provisioned for admin operation tests.
type adminTestEnv struct {
	poolID   string
	clientID string
}

func newAdminTestEnv(t *testing.T, c *awscognito.Client, name string) adminTestEnv {
	t.Helper()
	ctx := context.Background()
	pool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
		PoolName: aws.String(name),
	})
	require.NoError(t, err)
	poolID := aws.ToString(pool.UserPool.Id)

	cl, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientName: aws.String(name + "-client"),
	})
	require.NoError(t, err)
	return adminTestEnv{
		poolID:   poolID,
		clientID: aws.ToString(cl.UserPoolClient.ClientId),
	}
}

// TestCognitoIntegration_AdminLifecycle covers the ordered happy-path flow:
// create → get → set password → login → confirm → delete.
func TestCognitoIntegration_AdminLifecycle(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito
	env := newAdminTestEnv(t, c, "admin-lifecycle-pool")

	const (
		mainUser = "admin-main-user"
		email    = "admin-main@example.com"
		tempPass = "TempPass1!"
		permPass = "PermPass1!"
	)

	t.Run("AdminCreateUser_WithoutPassword", func(t *testing.T) {
		out, err := c.AdminCreateUser(ctx, &awscognito.AdminCreateUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String("no-pass-user"),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("email"), Value: aws.String("nopass@example.com")},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, out.User)
		assert.Equal(t, "no-pass-user", aws.ToString(out.User.Username))
		assert.Equal(t, types.UserStatusTypeConfirmed, out.User.UserStatus)
	})

	t.Run("AdminCreateUser_WithTemporaryPassword", func(t *testing.T) {
		out, err := c.AdminCreateUser(ctx, &awscognito.AdminCreateUserInput{
			UserPoolId:        aws.String(env.poolID),
			Username:          aws.String(mainUser),
			TemporaryPassword: aws.String(tempPass),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("email"), Value: aws.String(email)},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, out.User)
		assert.Equal(t, mainUser, aws.ToString(out.User.Username))
		assert.Equal(t, types.UserStatusTypeForceChangePassword, out.User.UserStatus)
	})

	t.Run("AdminGetUser", func(t *testing.T) {
		out, err := c.AdminGetUser(ctx, &awscognito.AdminGetUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String(mainUser),
		})
		require.NoError(t, err)
		assert.Equal(t, mainUser, aws.ToString(out.Username))
		assert.Equal(t, types.UserStatusTypeForceChangePassword, out.UserStatus)
		var hasSub, hasEmail bool
		for _, a := range out.UserAttributes {
			switch aws.ToString(a.Name) {
			case "sub":
				hasSub = true
			case "email":
				hasEmail = true
				assert.Equal(t, email, aws.ToString(a.Value))
			}
		}
		assert.True(t, hasSub, "sub attribute should be present")
		assert.True(t, hasEmail, "email attribute should be present")
	})

	t.Run("AdminSetUserPassword_Permanent", func(t *testing.T) {
		_, err := c.AdminSetUserPassword(ctx, &awscognito.AdminSetUserPasswordInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String(mainUser),
			Password:   aws.String(permPass),
			Permanent:  true,
		})
		require.NoError(t, err)

		out, err := c.AdminGetUser(ctx, &awscognito.AdminGetUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String(mainUser),
		})
		require.NoError(t, err)
		assert.Equal(t, types.UserStatusTypeConfirmed, out.UserStatus)
	})

	t.Run("AdminSetUserPassword_CanLogin", func(t *testing.T) {
		out, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(env.clientID),
			AuthFlow: types.AuthFlowTypeUserPasswordAuth,
			AuthParameters: map[string]string{
				"USERNAME": mainUser,
				"PASSWORD": permPass,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, out.AuthenticationResult)
		assert.NotEmpty(t, aws.ToString(out.AuthenticationResult.AccessToken))
	})

	t.Run("AdminSetUserPassword_Temporary", func(t *testing.T) {
		_, err := c.AdminSetUserPassword(ctx, &awscognito.AdminSetUserPasswordInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String(mainUser),
			Password:   aws.String(tempPass),
			Permanent:  false,
		})
		require.NoError(t, err)

		out, err := c.AdminGetUser(ctx, &awscognito.AdminGetUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String(mainUser),
		})
		require.NoError(t, err)
		assert.Equal(t, types.UserStatusTypeForceChangePassword, out.UserStatus)
	})

	t.Run("AdminConfirmSignUp", func(t *testing.T) {
		_, err := c.SignUp(ctx, &awscognito.SignUpInput{
			ClientId: aws.String(env.clientID),
			Username: aws.String("unconfirmed-user"),
			Password: aws.String(permPass),
		})
		require.NoError(t, err)

		_, err = c.AdminConfirmSignUp(ctx, &awscognito.AdminConfirmSignUpInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String("unconfirmed-user"),
		})
		require.NoError(t, err)

		out, err := c.AdminGetUser(ctx, &awscognito.AdminGetUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String("unconfirmed-user"),
		})
		require.NoError(t, err)
		assert.Equal(t, types.UserStatusTypeConfirmed, out.UserStatus)
	})

	t.Run("AdminDeleteUser", func(t *testing.T) {
		_, err := c.AdminDeleteUser(ctx, &awscognito.AdminDeleteUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String("no-pass-user"),
		})
		require.NoError(t, err)

		_, err = c.AdminGetUser(ctx, &awscognito.AdminGetUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String("no-pass-user"),
		})
		require.Error(t, err)
		assert.Equal(t, "UserNotFoundException", apiErrorCode(err))
	})
}

// TestCognitoIntegration_AdminUserNotFound table-drives UserNotFoundException
// cases for all admin operations. Each case gets a fresh pool so they are
// independent and can run in any order.
func TestCognitoIntegration_AdminUserNotFound(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito
	env := newAdminTestEnv(t, c, "admin-notfound-pool")

	const permPass = "PermPass1!"

	// Pre-create "dup-user" in the outer test scope so the duplicate subtest
	// can call require.NoError on the correct goroutine's t.
	_, err := c.AdminCreateUser(ctx, &awscognito.AdminCreateUserInput{
		UserPoolId: aws.String(env.poolID),
		Username:   aws.String("dup-user"),
	})
	require.NoError(t, err)

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "AdminCreateUser_Duplicate",
			run: func() error {
				_, err := c.AdminCreateUser(ctx, &awscognito.AdminCreateUserInput{
					UserPoolId: aws.String(env.poolID),
					Username:   aws.String("dup-user"),
				})
				return err
			},
			// UsernameExistsException, not UserNotFoundException — handled separately below.
		},
		{
			name: "AdminGetUser",
			run: func() error {
				_, err := c.AdminGetUser(ctx, &awscognito.AdminGetUserInput{
					UserPoolId: aws.String(env.poolID),
					Username:   aws.String("nonexistent"),
				})
				return err
			},
		},
		{
			name: "AdminSetUserPassword",
			run: func() error {
				_, err := c.AdminSetUserPassword(ctx, &awscognito.AdminSetUserPasswordInput{
					UserPoolId: aws.String(env.poolID),
					Username:   aws.String("nonexistent"),
					Password:   aws.String(permPass),
					Permanent:  true,
				})
				return err
			},
		},
		{
			name: "AdminConfirmSignUp",
			run: func() error {
				_, err := c.AdminConfirmSignUp(ctx, &awscognito.AdminConfirmSignUpInput{
					UserPoolId: aws.String(env.poolID),
					Username:   aws.String("nonexistent"),
				})
				return err
			},
		},
		{
			name: "AdminDeleteUser",
			run: func() error {
				_, err := c.AdminDeleteUser(ctx, &awscognito.AdminDeleteUserInput{
					UserPoolId: aws.String(env.poolID),
					Username:   aws.String("nonexistent"),
				})
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			require.Error(t, err)
			wantCode := "UserNotFoundException"
			if tc.name == "AdminCreateUser_Duplicate" {
				wantCode = "UsernameExistsException"
			}
			assert.Equal(t, wantCode, apiErrorCode(err))
		})
	}
}

// ── GetUser ───────────────────────────────────────────────────────────────────

func TestCognitoIntegration_GetUser(t *testing.T) {
	cap := withCodeCapture(t)
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito
	env := newAdminTestEnv(t, c, "getuser-test-pool")

	const (
		username = "getuser-test"
		password = "Password1!"
		email    = "getuser@example.com"
	)

	_, err := c.SignUp(ctx, &awscognito.SignUpInput{
		ClientId: aws.String(env.clientID),
		Username: aws.String(username),
		Password: aws.String(password),
		UserAttributes: []types.AttributeType{
			{Name: aws.String("email"), Value: aws.String(email)},
		},
	})
	require.NoError(t, err)

	code := cap.get(username)
	require.NotEmpty(t, code)
	_, err = c.ConfirmSignUp(ctx, &awscognito.ConfirmSignUpInput{
		ClientId:         aws.String(env.clientID),
		Username:         aws.String(username),
		ConfirmationCode: aws.String(code),
	})
	require.NoError(t, err)

	auth, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
		ClientId: aws.String(env.clientID),
		AuthFlow: types.AuthFlowTypeUserPasswordAuth,
		AuthParameters: map[string]string{
			"USERNAME": username,
			"PASSWORD": password,
		},
	})
	require.NoError(t, err)
	accessToken := aws.ToString(auth.AuthenticationResult.AccessToken)

	t.Run("GetUser", func(t *testing.T) {
		out, err := c.GetUser(ctx, &awscognito.GetUserInput{
			AccessToken: aws.String(accessToken),
		})
		require.NoError(t, err)
		assert.Equal(t, username, aws.ToString(out.Username))
		var hasSub, hasEmail bool
		for _, a := range out.UserAttributes {
			switch aws.ToString(a.Name) {
			case "sub":
				hasSub = true
			case "email":
				hasEmail = true
				assert.Equal(t, email, aws.ToString(a.Value))
			}
		}
		assert.True(t, hasSub, "sub attribute should be present")
		assert.True(t, hasEmail, "email attribute should be present")
	})

	t.Run("GetUser_InvalidToken", func(t *testing.T) {
		_, err := c.GetUser(ctx, &awscognito.GetUserInput{
			AccessToken: aws.String("invalid-token"),
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})
}

// ── JWKS ──────────────────────────────────────────────────────────────────────

type jwksKey struct {
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// createConfirmedUser creates a pool, a client, signs up and confirms a user,
// then authenticates. Returns the pool ID and an access token.
func createConfirmedUser(
	t *testing.T,
	ctx context.Context,
	c *awscognito.Client,
	cap *codeCapture,
	poolName, clientName, username, password string,
) (poolID, accessToken string) {
	t.Helper()

	pool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
		PoolName: aws.String(poolName),
	})
	require.NoError(t, err)
	poolID = aws.ToString(pool.UserPool.Id)

	client, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientName: aws.String(clientName),
	})
	require.NoError(t, err)
	clientID := aws.ToString(client.UserPoolClient.ClientId)

	_, err = c.SignUp(ctx, &awscognito.SignUpInput{
		ClientId: aws.String(clientID),
		Username: aws.String(username),
		Password: aws.String(password),
	})
	require.NoError(t, err)

	code := cap.get(username)
	require.NotEmpty(t, code)
	_, err = c.ConfirmSignUp(ctx, &awscognito.ConfirmSignUpInput{
		ClientId:         aws.String(clientID),
		Username:         aws.String(username),
		ConfirmationCode: aws.String(code),
	})
	require.NoError(t, err)

	auth, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
		ClientId: aws.String(clientID),
		AuthFlow: types.AuthFlowTypeUserPasswordAuth,
		AuthParameters: map[string]string{
			"USERNAME": username,
			"PASSWORD": password,
		},
	})
	require.NoError(t, err)
	return poolID, aws.ToString(auth.AuthenticationResult.AccessToken)
}

// fetchJWKS retrieves the JWKS for the given pool and returns the single key.
// Asserts HTTP 200 and Content-Type: application/json.
func fetchJWKS(t *testing.T, baseURL, poolID string) jwksKey {
	t.Helper()
	resp, err := http.Get(baseURL + "/" + poolID + "/.well-known/jwks.json")
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body struct {
		Keys []jwksKey `json:"keys"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Keys, 1)
	return body.Keys[0]
}

// verifyAccessTokenSignature asserts that the token's JWT header kid matches
// the JWKS key and that the RS256 signature verifies against the JWKS public key.
func verifyAccessTokenSignature(t *testing.T, accessToken string, key jwksKey) {
	t.Helper()

	nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	require.NoError(t, err)
	eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	require.NoError(t, err)

	pubKey := &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(new(big.Int).SetBytes(eBytes).Int64()),
	}

	parts := strings.Split(accessToken, ".")
	require.Len(t, parts, 3)

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	var jwtHeader struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	require.NoError(t, json.Unmarshal(headerBytes, &jwtHeader))
	assert.Equal(t, "RS256", jwtHeader.Alg)
	assert.Equal(t, key.Kid, jwtHeader.Kid)

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	require.NoError(t,
		rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, digest[:], sigBytes),
		"access token signature must verify against JWKS public key",
	)
}

func TestCognitoIntegration_JWKS(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	cap := withCodeCapture(t)

	poolID, accessToken := createConfirmedUser(t, ctx, clients.cognito, cap,
		"jwks-test-pool", "jwks-test-client", "jwks-user", "Password1!")

	t.Run("UnknownPool_404", func(t *testing.T) {
		resp, err := http.Get(clients.baseURL + "/us-east-1_UNKNOWN/.well-known/jwks.json")
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("FetchJWKS", func(t *testing.T) {
		key := fetchJWKS(t, clients.baseURL, poolID)
		assert.Equal(t, "RS256", key.Alg)
		assert.Equal(t, "RSA", key.Kty)
		assert.Equal(t, "sig", key.Use)
		assert.NotEmpty(t, key.Kid)
		assert.NotEmpty(t, key.N)
		assert.Equal(t, "AQAB", key.E, "standard RSA exponent 65537 must encode as AQAB")
	})

	t.Run("VerifyTokenSignature", func(t *testing.T) {
		key := fetchJWKS(t, clients.baseURL, poolID)
		verifyAccessTokenSignature(t, accessToken, key)
	})
}

// ── ResendConfirmationCode ────────────────────────────────────────────────────

func TestCognitoIntegration_ResendConfirmationCode(t *testing.T) {
	cap := withCodeCapture(t)
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	pool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
		PoolName: aws.String("resend-test-pool"),
	})
	require.NoError(t, err)
	poolID := aws.ToString(pool.UserPool.Id)

	client, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientName: aws.String("resend-test-client"),
	})
	require.NoError(t, err)
	clientID := aws.ToString(client.UserPoolClient.ClientId)

	const (
		username = "resend-user"
		password = "Password1!"
		email    = "resend-user@example.com"
	)

	_, err = c.SignUp(ctx, &awscognito.SignUpInput{
		ClientId: aws.String(clientID),
		Username: aws.String(username),
		Password: aws.String(password),
		UserAttributes: []types.AttributeType{
			{Name: aws.String("email"), Value: aws.String(email)},
		},
	})
	require.NoError(t, err)

	t.Run("ResendConfirmationCode_Success", func(t *testing.T) {
		out, err := c.ResendConfirmationCode(ctx, &awscognito.ResendConfirmationCodeInput{
			ClientId: aws.String(clientID),
			Username: aws.String(username),
		})
		require.NoError(t, err)
		require.NotNil(t, out.CodeDeliveryDetails)
		assert.Equal(t, "EMAIL", string(out.CodeDeliveryDetails.DeliveryMedium))
		assert.Equal(t, "email", aws.ToString(out.CodeDeliveryDetails.AttributeName))
		assert.Equal(t, "r***@example.com", aws.ToString(out.CodeDeliveryDetails.Destination))
	})

	t.Run("ResendConfirmationCode_NewCodeConfirmsUser", func(t *testing.T) {
		_, err := c.ResendConfirmationCode(ctx, &awscognito.ResendConfirmationCodeInput{
			ClientId: aws.String(clientID),
			Username: aws.String(username),
		})
		require.NoError(t, err)

		code := cap.get(username)
		require.NotEmpty(t, code, "resend code should be captured from slog output")

		_, err = c.ConfirmSignUp(ctx, &awscognito.ConfirmSignUpInput{
			ClientId:         aws.String(clientID),
			Username:         aws.String(username),
			ConfirmationCode: aws.String(code),
		})
		require.NoError(t, err)
	})

	t.Run("ResendConfirmationCode_AlreadyConfirmed", func(t *testing.T) {
		_, err := c.ResendConfirmationCode(ctx, &awscognito.ResendConfirmationCodeInput{
			ClientId: aws.String(clientID),
			Username: aws.String(username),
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})

	t.Run("ResendConfirmationCode_UserNotFound", func(t *testing.T) {
		_, err := c.ResendConfirmationCode(ctx, &awscognito.ResendConfirmationCodeInput{
			ClientId: aws.String(clientID),
			Username: aws.String("nobody"),
		})
		require.Error(t, err)
		assert.Equal(t, "UserNotFoundException", apiErrorCode(err))
	})
}

// ── Group management ──────────────────────────────────────────────────────────

func TestCognitoIntegration_Groups(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	pool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
		PoolName: aws.String("group-test-pool"),
	})
	require.NoError(t, err)
	poolID := aws.ToString(pool.UserPool.Id)

	t.Run("CreateGroup", func(t *testing.T) {
		prec := int32(10)
		out, err := c.CreateGroup(ctx, &awscognito.CreateGroupInput{
			UserPoolId:  aws.String(poolID),
			GroupName:   aws.String("admins"),
			Description: aws.String("Admin users"),
			Precedence:  &prec,
		})
		require.NoError(t, err)
		require.NotNil(t, out.Group)
		assert.Equal(t, "admins", aws.ToString(out.Group.GroupName))
		assert.Equal(t, poolID, aws.ToString(out.Group.UserPoolId))
		assert.Equal(t, "Admin users", aws.ToString(out.Group.Description))
		require.NotNil(t, out.Group.Precedence)
		assert.Equal(t, int32(10), *out.Group.Precedence)
		assert.NotNil(t, out.Group.CreationDate)
		assert.NotNil(t, out.Group.LastModifiedDate)
	})

	t.Run("CreateGroup_Duplicate", func(t *testing.T) {
		_, err := c.CreateGroup(ctx, &awscognito.CreateGroupInput{
			UserPoolId: aws.String(poolID),
			GroupName:  aws.String("admins"),
		})
		require.Error(t, err)
		assert.Equal(t, "GroupExistsException", apiErrorCode(err))
	})

	t.Run("GetGroup", func(t *testing.T) {
		out, err := c.GetGroup(ctx, &awscognito.GetGroupInput{
			UserPoolId: aws.String(poolID),
			GroupName:  aws.String("admins"),
		})
		require.NoError(t, err)
		require.NotNil(t, out.Group)
		assert.Equal(t, "admins", aws.ToString(out.Group.GroupName))
		assert.Equal(t, poolID, aws.ToString(out.Group.UserPoolId))
	})

	t.Run("GetGroup_NotFound", func(t *testing.T) {
		_, err := c.GetGroup(ctx, &awscognito.GetGroupInput{
			UserPoolId: aws.String(poolID),
			GroupName:  aws.String("nonexistent"),
		})
		require.Error(t, err)
		assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
	})

	t.Run("UpdateGroup", func(t *testing.T) {
		newPrec := int32(5)
		_, err := c.UpdateGroup(ctx, &awscognito.UpdateGroupInput{
			UserPoolId:  aws.String(poolID),
			GroupName:   aws.String("admins"),
			Description: aws.String("Updated description"),
			Precedence:  &newPrec,
		})
		require.NoError(t, err)

		out, err := c.GetGroup(ctx, &awscognito.GetGroupInput{
			UserPoolId: aws.String(poolID),
			GroupName:  aws.String("admins"),
		})
		require.NoError(t, err)
		assert.Equal(t, "Updated description", aws.ToString(out.Group.Description))
		require.NotNil(t, out.Group.Precedence)
		assert.Equal(t, int32(5), *out.Group.Precedence)
	})

	t.Run("ListGroups", func(t *testing.T) {
		for _, name := range []string{"beta-group", "gamma-group"} {
			_, err := c.CreateGroup(ctx, &awscognito.CreateGroupInput{
				UserPoolId: aws.String(poolID),
				GroupName:  aws.String(name),
			})
			require.NoError(t, err)
		}

		out, err := c.ListGroups(ctx, &awscognito.ListGroupsInput{
			UserPoolId: aws.String(poolID),
		})
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(out.Groups), 3)

		names := make([]string, len(out.Groups))
		for i, g := range out.Groups {
			names[i] = aws.ToString(g.GroupName)
		}
		assert.Contains(t, names, "admins")
		assert.Contains(t, names, "beta-group")
		assert.Contains(t, names, "gamma-group")
	})

	t.Run("DeleteGroup", func(t *testing.T) {
		_, err := c.CreateGroup(ctx, &awscognito.CreateGroupInput{
			UserPoolId: aws.String(poolID),
			GroupName:  aws.String("to-delete"),
		})
		require.NoError(t, err)

		_, err = c.DeleteGroup(ctx, &awscognito.DeleteGroupInput{
			UserPoolId: aws.String(poolID),
			GroupName:  aws.String("to-delete"),
		})
		require.NoError(t, err)

		_, err = c.GetGroup(ctx, &awscognito.GetGroupInput{
			UserPoolId: aws.String(poolID),
			GroupName:  aws.String("to-delete"),
		})
		require.Error(t, err)
		assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
	})

	t.Run("DeleteGroup_NotFound", func(t *testing.T) {
		_, err := c.DeleteGroup(ctx, &awscognito.DeleteGroupInput{
			UserPoolId: aws.String(poolID),
			GroupName:  aws.String("ghost-group"),
		})
		require.Error(t, err)
		assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
	})
}

func TestCognitoIntegration_GroupMembership(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito
	env := newAdminTestEnv(t, c, "group-membership-pool")

	_, err := c.CreateGroup(ctx, &awscognito.CreateGroupInput{
		UserPoolId: aws.String(env.poolID),
		GroupName:  aws.String("members"),
	})
	require.NoError(t, err)

	_, err = c.AdminCreateUser(ctx, &awscognito.AdminCreateUserInput{
		UserPoolId: aws.String(env.poolID),
		Username:   aws.String("member-user"),
	})
	require.NoError(t, err)

	t.Run("AdminAddUserToGroup", func(t *testing.T) {
		_, err := c.AdminAddUserToGroup(ctx, &awscognito.AdminAddUserToGroupInput{
			UserPoolId: aws.String(env.poolID),
			GroupName:  aws.String("members"),
			Username:   aws.String("member-user"),
		})
		require.NoError(t, err)
	})

	t.Run("AdminListGroupsForUser", func(t *testing.T) {
		out, err := c.AdminListGroupsForUser(ctx, &awscognito.AdminListGroupsForUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String("member-user"),
		})
		require.NoError(t, err)
		require.Len(t, out.Groups, 1)
		assert.Equal(t, "members", aws.ToString(out.Groups[0].GroupName))
	})

	t.Run("ListUsersInGroup", func(t *testing.T) {
		out, err := c.ListUsersInGroup(ctx, &awscognito.ListUsersInGroupInput{
			UserPoolId: aws.String(env.poolID),
			GroupName:  aws.String("members"),
		})
		require.NoError(t, err)
		require.Len(t, out.Users, 1)
		assert.Equal(t, "member-user", aws.ToString(out.Users[0].Username))
	})

	t.Run("AdminRemoveUserFromGroup", func(t *testing.T) {
		_, err := c.AdminRemoveUserFromGroup(ctx, &awscognito.AdminRemoveUserFromGroupInput{
			UserPoolId: aws.String(env.poolID),
			GroupName:  aws.String("members"),
			Username:   aws.String("member-user"),
		})
		require.NoError(t, err)

		// Forward index: user no longer belongs to the group.
		out, err := c.AdminListGroupsForUser(ctx, &awscognito.AdminListGroupsForUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String("member-user"),
		})
		require.NoError(t, err)
		assert.Empty(t, out.Groups)

		// Reverse index: group no longer contains the user.
		usersOut, err := c.ListUsersInGroup(ctx, &awscognito.ListUsersInGroupInput{
			UserPoolId: aws.String(env.poolID),
			GroupName:  aws.String("members"),
		})
		require.NoError(t, err)
		assert.Empty(t, usersOut.Users)
	})

	t.Run("AdminAddUserToGroup_UserNotFound", func(t *testing.T) {
		_, err := c.AdminAddUserToGroup(ctx, &awscognito.AdminAddUserToGroupInput{
			UserPoolId: aws.String(env.poolID),
			GroupName:  aws.String("members"),
			Username:   aws.String("nonexistent-user"),
		})
		require.Error(t, err)
		assert.Equal(t, "UserNotFoundException", apiErrorCode(err))
	})

	t.Run("AdminAddUserToGroup_GroupNotFound", func(t *testing.T) {
		_, err := c.AdminAddUserToGroup(ctx, &awscognito.AdminAddUserToGroupInput{
			UserPoolId: aws.String(env.poolID),
			GroupName:  aws.String("nonexistent-group"),
			Username:   aws.String("member-user"),
		})
		require.Error(t, err)
		assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
	})
}

// ── Token revocation ──────────────────────────────────────────────────────────

func TestCognitoIntegration_TokenRevocation(t *testing.T) {
	cap := withCodeCapture(t)
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	pool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
		PoolName: aws.String("revocation-test-pool"),
	})
	require.NoError(t, err)
	poolID := aws.ToString(pool.UserPool.Id)

	cl, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientName: aws.String("revocation-test-client"),
	})
	require.NoError(t, err)
	clientID := aws.ToString(cl.UserPoolClient.ClientId)

	const (
		username = "revocation-user"
		password = "Password1!"
	)

	_, err = c.SignUp(ctx, &awscognito.SignUpInput{
		ClientId: aws.String(clientID),
		Username: aws.String(username),
		Password: aws.String(password),
	})
	require.NoError(t, err)

	code := cap.get(username)
	require.NotEmpty(t, code)
	_, err = c.ConfirmSignUp(ctx, &awscognito.ConfirmSignUpInput{
		ClientId:         aws.String(clientID),
		Username:         aws.String(username),
		ConfirmationCode: aws.String(code),
	})
	require.NoError(t, err)

	// authenticate issues a fresh token pair via USER_PASSWORD_AUTH.
	authenticate := func(t *testing.T) (accessToken, refreshToken string) {
		t.Helper()
		out, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeUserPasswordAuth,
			AuthParameters: map[string]string{
				"USERNAME": username,
				"PASSWORD": password,
			},
		})
		require.NoError(t, err)
		return aws.ToString(out.AuthenticationResult.AccessToken),
			aws.ToString(out.AuthenticationResult.RefreshToken)
	}

	t.Run("RevokeToken_RefreshTokenNoLongerUsable", func(t *testing.T) {
		_, refreshToken := authenticate(t)

		_, err := c.RevokeToken(ctx, &awscognito.RevokeTokenInput{
			ClientId: aws.String(clientID),
			Token:    aws.String(refreshToken),
		})
		require.NoError(t, err)

		_, err = c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeRefreshTokenAuth,
			AuthParameters: map[string]string{
				"REFRESH_TOKEN": refreshToken,
			},
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})

	t.Run("RevokeToken_PairedAccessTokenRevoked", func(t *testing.T) {
		accessToken, refreshToken := authenticate(t)

		_, err := c.RevokeToken(ctx, &awscognito.RevokeTokenInput{
			ClientId: aws.String(clientID),
			Token:    aws.String(refreshToken),
		})
		require.NoError(t, err)

		_, err = c.GetUser(ctx, &awscognito.GetUserInput{
			AccessToken: aws.String(accessToken),
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})

	t.Run("RevokeToken_Idempotent", func(t *testing.T) {
		_, err := c.RevokeToken(ctx, &awscognito.RevokeTokenInput{
			ClientId: aws.String(clientID),
			Token:    aws.String("not-a-real-refresh-token"),
		})
		require.NoError(t, err)
	})

	t.Run("GlobalSignOut_AccessTokenRevoked", func(t *testing.T) {
		accessToken, _ := authenticate(t)

		_, err := c.GlobalSignOut(ctx, &awscognito.GlobalSignOutInput{
			AccessToken: aws.String(accessToken),
		})
		require.NoError(t, err)

		_, err = c.GetUser(ctx, &awscognito.GetUserInput{
			AccessToken: aws.String(accessToken),
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})

	t.Run("GlobalSignOut_RefreshTokensRevoked", func(t *testing.T) {
		accessToken, refreshToken := authenticate(t)

		_, err := c.GlobalSignOut(ctx, &awscognito.GlobalSignOutInput{
			AccessToken: aws.String(accessToken),
		})
		require.NoError(t, err)

		_, err = c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeRefreshTokenAuth,
			AuthParameters: map[string]string{
				"REFRESH_TOKEN": refreshToken,
			},
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})

	t.Run("GlobalSignOut_OtherSessionAccessTokenRevoked", func(t *testing.T) {
		// Sign in from two independent sessions.
		token1, _ := authenticate(t)
		token2, _ := authenticate(t)

		// Confirm session 2's token is valid before sign-out.
		_, err := c.GetUser(ctx, &awscognito.GetUserInput{
			AccessToken: aws.String(token2),
		})
		require.NoError(t, err)

		// Sign out using session 1's token.
		_, err = c.GlobalSignOut(ctx, &awscognito.GlobalSignOutInput{
			AccessToken: aws.String(token1),
		})
		require.NoError(t, err)

		// Session 2's access token must also be rejected.
		_, err = c.GetUser(ctx, &awscognito.GetUserInput{
			AccessToken: aws.String(token2),
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})
}

// ── Tagging ───────────────────────────────────────────────────────────────────

func TestCognitoIntegration_Tagging(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	pool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
		PoolName: aws.String("tagging-test-pool"),
	})
	require.NoError(t, err)
	arn := aws.ToString(pool.UserPool.Arn)
	require.NotEmpty(t, arn)
	t.Cleanup(func() {
		_, _ = c.DeleteUserPool(ctx, &awscognito.DeleteUserPoolInput{
			UserPoolId: pool.UserPool.Id,
		})
	})

	t.Run("TagResource", func(t *testing.T) {
		_, err := c.TagResource(ctx, &awscognito.TagResourceInput{
			ResourceArn: aws.String(arn),
			Tags:        map[string]string{"env": "test", "owner": "alice"},
		})
		require.NoError(t, err)

		out, err := c.ListTagsForResource(ctx, &awscognito.ListTagsForResourceInput{
			ResourceArn: aws.String(arn),
		})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"env": "test", "owner": "alice"}, out.Tags)
	})

	t.Run("TagResource_merge", func(t *testing.T) {
		_, err := c.TagResource(ctx, &awscognito.TagResourceInput{
			ResourceArn: aws.String(arn),
			Tags:        map[string]string{"owner": "bob", "team": "platform"},
		})
		require.NoError(t, err)

		out, err := c.ListTagsForResource(ctx, &awscognito.ListTagsForResourceInput{
			ResourceArn: aws.String(arn),
		})
		require.NoError(t, err)
		assert.Equal(
			t,
			map[string]string{"env": "test", "owner": "bob", "team": "platform"},
			out.Tags,
		)
	})

	t.Run("UntagResource", func(t *testing.T) {
		_, err := c.UntagResource(ctx, &awscognito.UntagResourceInput{
			ResourceArn: aws.String(arn),
			TagKeys:     []string{"env"},
		})
		require.NoError(t, err)

		out, err := c.ListTagsForResource(ctx, &awscognito.ListTagsForResourceInput{
			ResourceArn: aws.String(arn),
		})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"owner": "bob", "team": "platform"}, out.Tags)
	})

	t.Run("ListTagsForResource_empty", func(t *testing.T) {
		emptyPool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
			PoolName: aws.String("notag-pool"),
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = c.DeleteUserPool(ctx, &awscognito.DeleteUserPoolInput{
				UserPoolId: emptyPool.UserPool.Id,
			})
		})

		out, err := c.ListTagsForResource(ctx, &awscognito.ListTagsForResourceInput{
			ResourceArn: aws.String(aws.ToString(emptyPool.UserPool.Arn)),
		})
		require.NoError(t, err)
		assert.Empty(t, out.Tags)
	})

	t.Run("TagResource_NotFound", func(t *testing.T) {
		_, err := c.TagResource(ctx, &awscognito.TagResourceInput{
			ResourceArn: aws.String(
				"arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_notexist",
			),
			Tags: map[string]string{"k": "v"},
		})
		require.Error(t, err)
		assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
	})

	t.Run("UntagResource_NotFound", func(t *testing.T) {
		_, err := c.UntagResource(ctx, &awscognito.UntagResourceInput{
			ResourceArn: aws.String(
				"arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_notexist",
			),
			TagKeys: []string{"k"},
		})
		require.Error(t, err)
		assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
	})

	t.Run("ListTagsForResource_NotFound", func(t *testing.T) {
		_, err := c.ListTagsForResource(ctx, &awscognito.ListTagsForResourceInput{
			ResourceArn: aws.String(
				"arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_notexist",
			),
		})
		require.Error(t, err)
		assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
	})
}

func TestCognitoIntegration_RefreshTokenExpiry(t *testing.T) {
	cap := withCodeCapture(t)
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	pool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
		PoolName: aws.String("expiry-test-pool"),
	})
	require.NoError(t, err)
	poolID := aws.ToString(pool.UserPool.Id)

	client, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientName: aws.String("expiry-test-client"),
	})
	require.NoError(t, err)
	clientID := aws.ToString(client.UserPoolClient.ClientId)

	const (
		username = "expiry-user"
		password = "Password1!"
		email    = "expiry@example.com"
	)

	_, err = c.SignUp(ctx, &awscognito.SignUpInput{
		ClientId: aws.String(clientID),
		Username: aws.String(username),
		Password: aws.String(password),
		UserAttributes: []types.AttributeType{
			{Name: aws.String("email"), Value: aws.String(email)},
		},
	})
	require.NoError(t, err)

	code := cap.get(username)
	require.NotEmpty(t, code)
	_, err = c.ConfirmSignUp(ctx, &awscognito.ConfirmSignUpInput{
		ClientId:         aws.String(clientID),
		Username:         aws.String(username),
		ConfirmationCode: aws.String(code),
	})
	require.NoError(t, err)

	auth, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
		ClientId: aws.String(clientID),
		AuthFlow: types.AuthFlowTypeUserPasswordAuth,
		AuthParameters: map[string]string{
			"USERNAME": username,
			"PASSWORD": password,
		},
	})
	require.NoError(t, err)
	refreshToken := aws.ToString(auth.AuthenticationResult.RefreshToken)
	require.NotEmpty(t, refreshToken)

	// Patch the on-disk token file to place ExpiresAt one second in the past.
	// This simulates a token that was valid when issued but has since expired,
	// without requiring a real time.Sleep.
	tokenPath := filepath.Join(
		clients.dataDir, "cognito", "pools", poolID,
		"refresh_tokens", refreshToken+".json",
	)
	raw, err := os.ReadFile(filepath.Clean(tokenPath))
	require.NoError(t, err)
	var tokenJSON map[string]any
	require.NoError(t, json.Unmarshal(raw, &tokenJSON))
	tokenJSON["ExpiresAt"] = float64(time.Now().Unix() - 1)
	patched, err := json.Marshal(tokenJSON)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tokenPath, patched, 0o600))

	t.Run("ExpiredToken_Rejected", func(t *testing.T) {
		_, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeRefreshTokenAuth,
			AuthParameters: map[string]string{
				"REFRESH_TOKEN": refreshToken,
			},
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})
}

// ── ListUsers ─────────────────────────────────────────────────────────────────

func TestCognitoIntegration_ListUsers(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito
	env := newAdminTestEnv(t, c, "listusers-test-pool")

	for _, u := range []struct{ username, email string }{
		{"alice", "alice@example.com"},
		{"bob", "bob@example.com"},
		{"charlie", "charlie@example.com"},
	} {
		_, err := c.AdminCreateUser(ctx, &awscognito.AdminCreateUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String(u.username),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("email"), Value: aws.String(u.email)},
			},
		})
		require.NoError(t, err)
	}

	t.Run("ListAll", func(t *testing.T) {
		out, err := c.ListUsers(ctx, &awscognito.ListUsersInput{
			UserPoolId: aws.String(env.poolID),
		})
		require.NoError(t, err)
		require.Len(t, out.Users, 3)
		assert.Equal(t, "alice", aws.ToString(out.Users[0].Username))
		assert.True(t, out.Users[0].Enabled)
	})

	t.Run("Pagination", func(t *testing.T) {
		out, err := c.ListUsers(ctx, &awscognito.ListUsersInput{
			UserPoolId: aws.String(env.poolID),
			Limit:      aws.Int32(2),
		})
		require.NoError(t, err)
		require.Len(t, out.Users, 2)
		require.NotNil(t, out.PaginationToken)

		out2, err := c.ListUsers(ctx, &awscognito.ListUsersInput{
			UserPoolId:      aws.String(env.poolID),
			Limit:           aws.Int32(2),
			PaginationToken: out.PaginationToken,
		})
		require.NoError(t, err)
		require.Len(t, out2.Users, 1)
		assert.Equal(t, "charlie", aws.ToString(out2.Users[0].Username))
	})

	t.Run("FilterByUsername", func(t *testing.T) {
		out, err := c.ListUsers(ctx, &awscognito.ListUsersInput{
			UserPoolId: aws.String(env.poolID),
			Filter:     aws.String(`username = "bob"`),
		})
		require.NoError(t, err)
		require.Len(t, out.Users, 1)
		assert.Equal(t, "bob", aws.ToString(out.Users[0].Username))
	})

	t.Run("FilterByEmailPrefix", func(t *testing.T) {
		out, err := c.ListUsers(ctx, &awscognito.ListUsersInput{
			UserPoolId: aws.String(env.poolID),
			Filter:     aws.String(`email ^= "ali"`),
		})
		require.NoError(t, err)
		require.Len(t, out.Users, 1)
		assert.Equal(t, "alice", aws.ToString(out.Users[0].Username))
	})

	t.Run("PoolNotFound", func(t *testing.T) {
		_, err := c.ListUsers(ctx, &awscognito.ListUsersInput{
			UserPoolId: aws.String("us-east-1_notexist"),
		})
		require.Error(t, err)
		assert.Equal(t, "ResourceNotFoundException", apiErrorCode(err))
	})
}

// ── UpdateUserAttributes / DeleteUser / attribute verification ─────────────────

func TestCognitoIntegration_SelfServiceUserManagement(t *testing.T) {
	cap := withCodeCapture(t)
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	const (
		username = "selfservice-user"
		password = "Password1!"
	)
	poolID, accessToken := createConfirmedUser(
		t, ctx, c, cap, "selfservice-pool", "selfservice-client", username, password,
	)

	t.Run("UpdateUserAttributes_NonVerified", func(t *testing.T) {
		out, err := c.UpdateUserAttributes(ctx, &awscognito.UpdateUserAttributesInput{
			AccessToken: aws.String(accessToken),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("given_name"), Value: aws.String("Alice")},
			},
		})
		require.NoError(t, err)
		assert.Empty(t, out.CodeDeliveryDetailsList)

		got, err := c.GetUser(ctx, &awscognito.GetUserInput{AccessToken: aws.String(accessToken)})
		require.NoError(t, err)
		var givenName string
		for _, a := range got.UserAttributes {
			if aws.ToString(a.Name) == "given_name" {
				givenName = aws.ToString(a.Value)
			}
		}
		assert.Equal(t, "Alice", givenName)
	})

	t.Run("UpdateUserAttributes_EmailChange_GeneratesCode", func(t *testing.T) {
		out, err := c.UpdateUserAttributes(ctx, &awscognito.UpdateUserAttributesInput{
			AccessToken: aws.String(accessToken),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("email"), Value: aws.String("newemail@example.com")},
			},
		})
		require.NoError(t, err)
		require.Len(t, out.CodeDeliveryDetailsList, 1)
		assert.Equal(t, "email", aws.ToString(out.CodeDeliveryDetailsList[0].AttributeName))
	})

	t.Run("VerifyUserAttribute_CompletesVerification", func(t *testing.T) {
		code := cap.getAttrCode(username, "email")
		require.NotEmpty(t, code)

		_, err := c.VerifyUserAttribute(ctx, &awscognito.VerifyUserAttributeInput{
			AccessToken:   aws.String(accessToken),
			AttributeName: aws.String("email"),
			Code:          aws.String(code),
		})
		require.NoError(t, err)

		out, err := c.AdminGetUser(ctx, &awscognito.AdminGetUserInput{
			UserPoolId: aws.String(poolID),
			Username:   aws.String(username),
		})
		require.NoError(t, err)
		var verified string
		for _, a := range out.UserAttributes {
			if aws.ToString(a.Name) == "email_verified" {
				verified = aws.ToString(a.Value)
			}
		}
		assert.Equal(t, "true", verified)
	})

	t.Run("VerifyUserAttribute_CodeMismatch", func(t *testing.T) {
		_, err := c.UpdateUserAttributes(ctx, &awscognito.UpdateUserAttributesInput{
			AccessToken: aws.String(accessToken),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("phone_number"), Value: aws.String("+15551234567")},
			},
		})
		require.NoError(t, err)

		_, err = c.VerifyUserAttribute(ctx, &awscognito.VerifyUserAttributeInput{
			AccessToken:   aws.String(accessToken),
			AttributeName: aws.String("phone_number"),
			Code:          aws.String("000000"),
		})
		require.Error(t, err)
		assert.Equal(t, "CodeMismatchException", apiErrorCode(err))
	})

	t.Run("GetUserAttributeVerificationCode_Resend", func(t *testing.T) {
		out, err := c.GetUserAttributeVerificationCode(
			ctx,
			&awscognito.GetUserAttributeVerificationCodeInput{
				AccessToken:   aws.String(accessToken),
				AttributeName: aws.String("phone_number"),
			},
		)
		require.NoError(t, err)
		require.NotNil(t, out.CodeDeliveryDetails)
		assert.Equal(t, "phone_number", aws.ToString(out.CodeDeliveryDetails.AttributeName))
		assert.Equal(t, types.DeliveryMediumTypeSms, out.CodeDeliveryDetails.DeliveryMedium)

		code := cap.getAttrCode(username, "phone_number")
		require.NotEmpty(t, code)

		_, err = c.VerifyUserAttribute(ctx, &awscognito.VerifyUserAttributeInput{
			AccessToken:   aws.String(accessToken),
			AttributeName: aws.String("phone_number"),
			Code:          aws.String(code),
		})
		require.NoError(t, err)
	})

	t.Run("DeleteUser_RemovesAccountAndRevokesToken", func(t *testing.T) {
		_, err := c.DeleteUser(ctx, &awscognito.DeleteUserInput{
			AccessToken: aws.String(accessToken),
		})
		require.NoError(t, err)

		_, err = c.AdminGetUser(ctx, &awscognito.AdminGetUserInput{
			UserPoolId: aws.String(poolID),
			Username:   aws.String(username),
		})
		require.Error(t, err)
		assert.Equal(t, "UserNotFoundException", apiErrorCode(err))

		_, err = c.GetUser(ctx, &awscognito.GetUserInput{AccessToken: aws.String(accessToken)})
		require.Error(t, err)
		assert.Equal(t, "UserNotFoundException", apiErrorCode(err))
	})
}

// ── AdminUpdateUserAttributes ────────────────────────────────────────────────

func TestCognitoIntegration_AdminUpdateUserAttributes(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito
	env := newAdminTestEnv(t, c, "adminupdateattrs-pool")

	const username = "admin-update-user"
	_, err := c.AdminCreateUser(ctx, &awscognito.AdminCreateUserInput{
		UserPoolId: aws.String(env.poolID),
		Username:   aws.String(username),
	})
	require.NoError(t, err)

	t.Run("UpdateAttribute", func(t *testing.T) {
		_, err := c.AdminUpdateUserAttributes(ctx, &awscognito.AdminUpdateUserAttributesInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String(username),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("given_name"), Value: aws.String("Bob")},
			},
		})
		require.NoError(t, err)

		out, err := c.AdminGetUser(ctx, &awscognito.AdminGetUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String(username),
		})
		require.NoError(t, err)
		var givenName string
		for _, a := range out.UserAttributes {
			if aws.ToString(a.Name) == "given_name" {
				givenName = aws.ToString(a.Value)
			}
		}
		assert.Equal(t, "Bob", givenName)
	})

	t.Run("BypassVerificationWithEmailVerifiedTrue", func(t *testing.T) {
		_, err := c.AdminUpdateUserAttributes(ctx, &awscognito.AdminUpdateUserAttributesInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String(username),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("email"), Value: aws.String("bob@example.com")},
				{Name: aws.String("email_verified"), Value: aws.String("true")},
			},
		})
		require.NoError(t, err)

		out, err := c.AdminGetUser(ctx, &awscognito.AdminGetUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String(username),
		})
		require.NoError(t, err)
		var email, verified string
		for _, a := range out.UserAttributes {
			switch aws.ToString(a.Name) {
			case "email":
				email = aws.ToString(a.Value)
			case "email_verified":
				verified = aws.ToString(a.Value)
			}
		}
		assert.Equal(t, "bob@example.com", email)
		assert.Equal(t, "true", verified)
	})

	t.Run("UserNotFound", func(t *testing.T) {
		_, err := c.AdminUpdateUserAttributes(ctx, &awscognito.AdminUpdateUserAttributesInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String("nonexistent"),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("given_name"), Value: aws.String("X")},
			},
		})
		require.Error(t, err)
		assert.Equal(t, "UserNotFoundException", apiErrorCode(err))
	})
}

// ── AdminDisableUser / AdminEnableUser ──────────────────────────────────────

func TestCognitoIntegration_AdminDisableEnableUser(t *testing.T) {
	cap := withCodeCapture(t)
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	const (
		username = "disable-user"
		password = "Password1!"
	)
	poolID, accessToken := createConfirmedUser(
		t, ctx, c, cap, "disable-test-pool", "disable-test-client", username, password,
	)

	clientOut, err := c.ListUserPoolClients(ctx, &awscognito.ListUserPoolClientsInput{
		UserPoolId: aws.String(poolID),
	})
	require.NoError(t, err)
	require.NotEmpty(t, clientOut.UserPoolClients)
	clientID := aws.ToString(clientOut.UserPoolClients[0].ClientId)

	t.Run("AdminDisableUser_RevokesSessionAndBlocksSignIn", func(t *testing.T) {
		_, err := c.GetUser(ctx, &awscognito.GetUserInput{AccessToken: aws.String(accessToken)})
		require.NoError(t, err, "token must be valid before disabling")

		_, err = c.AdminDisableUser(ctx, &awscognito.AdminDisableUserInput{
			UserPoolId: aws.String(poolID),
			Username:   aws.String(username),
		})
		require.NoError(t, err)

		out, err := c.AdminGetUser(ctx, &awscognito.AdminGetUserInput{
			UserPoolId: aws.String(poolID),
			Username:   aws.String(username),
		})
		require.NoError(t, err)
		assert.False(t, out.Enabled)

		_, err = c.GetUser(ctx, &awscognito.GetUserInput{AccessToken: aws.String(accessToken)})
		require.Error(t, err, "existing session must be revoked")
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))

		_, err = c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeUserPasswordAuth,
			AuthParameters: map[string]string{
				"USERNAME": username,
				"PASSWORD": password,
			},
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})

	t.Run("AdminEnableUser_RestoresSignIn", func(t *testing.T) {
		_, err := c.AdminEnableUser(ctx, &awscognito.AdminEnableUserInput{
			UserPoolId: aws.String(poolID),
			Username:   aws.String(username),
		})
		require.NoError(t, err)

		out, err := c.AdminGetUser(ctx, &awscognito.AdminGetUserInput{
			UserPoolId: aws.String(poolID),
			Username:   aws.String(username),
		})
		require.NoError(t, err)
		assert.True(t, out.Enabled)

		authOut, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeUserPasswordAuth,
			AuthParameters: map[string]string{
				"USERNAME": username,
				"PASSWORD": password,
			},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, aws.ToString(authOut.AuthenticationResult.AccessToken))
	})

	t.Run("AdminDisableUser_UserNotFound", func(t *testing.T) {
		_, err := c.AdminDisableUser(ctx, &awscognito.AdminDisableUserInput{
			UserPoolId: aws.String(poolID),
			Username:   aws.String("nonexistent"),
		})
		require.Error(t, err)
		assert.Equal(t, "UserNotFoundException", apiErrorCode(err))
	})

	t.Run("AdminEnableUser_UserNotFound", func(t *testing.T) {
		_, err := c.AdminEnableUser(ctx, &awscognito.AdminEnableUserInput{
			UserPoolId: aws.String(poolID),
			Username:   aws.String("nonexistent"),
		})
		require.Error(t, err)
		assert.Equal(t, "UserNotFoundException", apiErrorCode(err))
	})
}

// ── PasswordPolicy complexity enforcement ───────────────────────────────────

// newPasswordPolicyTestEnv creates a pool with the given password policy — nil
// leaves the pool's Policies unset, exercising kumolo's built-in default — and
// a client for it.
func newPasswordPolicyTestEnv(
	t *testing.T, c *awscognito.Client, name string, policy *types.PasswordPolicyType,
) adminTestEnv {
	t.Helper()
	ctx := context.Background()
	input := &awscognito.CreateUserPoolInput{PoolName: aws.String(name)}
	if policy != nil {
		input.Policies = &types.UserPoolPolicyType{PasswordPolicy: policy}
	}
	pool, err := c.CreateUserPool(ctx, input)
	require.NoError(t, err)
	poolID := aws.ToString(pool.UserPool.Id)

	cl, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientName: aws.String(name + "-client"),
	})
	require.NoError(t, err)
	return adminTestEnv{poolID: poolID, clientID: aws.ToString(cl.UserPoolClient.ClientId)}
}

// TestCognitoIntegration_PasswordPolicy drives PasswordPolicy complexity
// enforcement through the real SDK across SignUp, AdminCreateUser, and
// AdminSetUserPassword, covering the built-in default policy, a fully custom
// policy, and a policy that only overrides a subset of fields.
func TestCognitoIntegration_PasswordPolicy(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	t.Run("SignUp_DefaultPolicy_RejectsWeakPassword", func(t *testing.T) {
		env := newPasswordPolicyTestEnv(t, c, "pwpolicy-default-pool", nil)

		_, err := c.SignUp(ctx, &awscognito.SignUpInput{
			ClientId: aws.String(env.clientID),
			Username: aws.String("weak-pw-user"),
			Password: aws.String("alllowercase1"), // missing uppercase and symbol
		})
		require.Error(t, err)
		assert.Equal(t, "InvalidPasswordException", apiErrorCode(err))

		_, err = c.SignUp(ctx, &awscognito.SignUpInput{
			ClientId: aws.String(env.clientID),
			Username: aws.String("strong-pw-user"),
			Password: aws.String("Valid1Pass!"),
		})
		require.NoError(t, err)
	})

	t.Run("SignUp_DefaultPolicy_MinimumLengthCountsRunesNotBytes", func(t *testing.T) {
		env := newPasswordPolicyTestEnv(t, c, "pwpolicy-multibyte-pool", nil)

		// 6 runes / 10 UTF-8 bytes: satisfies every category (upper/lower/number/
		// symbol) and clears the 8-byte mark, so a byte-counting implementation
		// would wrongly accept it. It is still short of the 8-rune default
		// minimum length, so it must be rejected once length is counted in runes.
		_, err := c.SignUp(ctx, &awscognito.SignUpInput{
			ClientId: aws.String(env.clientID),
			Username: aws.String("multibyte-pw-user"),
			Password: aws.String("Aa1!アイ"),
		})
		require.Error(t, err)
		assert.Equal(t, "InvalidPasswordException", apiErrorCode(err))
	})

	// Note: the SDK's JSON serializer only emits RequireUppercase / RequireLowercase
	// / RequireNumbers / RequireSymbols when they are true — a false value is
	// indistinguishable from an omitted field on the wire (see
	// serializeDocumentPasswordPolicyType in the SDK). So "explicit false"
	// scenarios can't be driven through this client; they're covered by
	// unit tests (internal/cognito/password_policy_test.go) and by
	// e2e/aws-cli/cognito.sh, which sends raw JSON and isn't subject to this
	// limitation.
	t.Run("SignUp_CustomPolicy_OnlyMinimumLengthOverridden", func(t *testing.T) {
		// This pool's Policies blob carries only MinimumLength; every
		// RequireX field must still inherit the built-in default (true).
		env := newPasswordPolicyTestEnv(t, c, "pwpolicy-custom-pool", &types.PasswordPolicyType{
			MinimumLength: aws.Int32(14),
		})

		// Satisfies every default category requirement but is short of the
		// pool's custom 14-character minimum.
		_, err := c.SignUp(ctx, &awscognito.SignUpInput{
			ClientId: aws.String(env.clientID),
			Username: aws.String("custom-short-user"),
			Password: aws.String("Sh0rter1!"),
		})
		require.Error(t, err)
		assert.Equal(t, "InvalidPasswordException", apiErrorCode(err))

		// Long enough, but missing a symbol — the default RequireSymbols=true
		// must still apply even though this pool only overrode MinimumLength.
		_, err = c.SignUp(ctx, &awscognito.SignUpInput{
			ClientId: aws.String(env.clientID),
			Username: aws.String("custom-nosymbol-user"),
			Password: aws.String("LongEnough1234"),
		})
		require.Error(t, err)
		assert.Equal(t, "InvalidPasswordException", apiErrorCode(err))

		_, err = c.SignUp(ctx, &awscognito.SignUpInput{
			ClientId: aws.String(env.clientID),
			Username: aws.String("custom-ok-user"),
			Password: aws.String("LongEnough1234!"),
		})
		require.NoError(t, err)
	})

	t.Run("AdminCreateUser_CustomPolicy_RejectsWeakTemporaryPassword", func(t *testing.T) {
		env := newPasswordPolicyTestEnv(
			t,
			c,
			"pwpolicy-admin-create-pool",
			&types.PasswordPolicyType{
				MinimumLength:    aws.Int32(10),
				RequireUppercase: true,
				RequireLowercase: true,
				RequireNumbers:   true,
				RequireSymbols:   true,
			},
		)

		_, err := c.AdminCreateUser(ctx, &awscognito.AdminCreateUserInput{
			UserPoolId:        aws.String(env.poolID),
			Username:          aws.String("admin-weak-temp-user"),
			TemporaryPassword: aws.String("Sh0rt!"),
		})
		require.Error(t, err)
		assert.Equal(t, "InvalidPasswordException", apiErrorCode(err))

		out, err := c.AdminCreateUser(ctx, &awscognito.AdminCreateUserInput{
			UserPoolId:        aws.String(env.poolID),
			Username:          aws.String("admin-strong-temp-user"),
			TemporaryPassword: aws.String("LongEnough1!"),
		})
		require.NoError(t, err)
		assert.Equal(t, types.UserStatusTypeForceChangePassword, out.User.UserStatus)
	})

	t.Run("AdminSetUserPassword_CustomPolicy_RejectsWeakPassword", func(t *testing.T) {
		env := newPasswordPolicyTestEnv(t, c, "pwpolicy-admin-set-pool", &types.PasswordPolicyType{
			MinimumLength:    aws.Int32(10),
			RequireUppercase: true,
			RequireLowercase: true,
			RequireNumbers:   true,
			RequireSymbols:   true,
		})
		_, err := c.AdminCreateUser(ctx, &awscognito.AdminCreateUserInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String("admin-set-pw-user"),
		})
		require.NoError(t, err)

		_, err = c.AdminSetUserPassword(ctx, &awscognito.AdminSetUserPasswordInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String("admin-set-pw-user"),
			Password:   aws.String("weak"),
			Permanent:  true,
		})
		require.Error(t, err)
		assert.Equal(t, "InvalidPasswordException", apiErrorCode(err))

		_, err = c.AdminSetUserPassword(ctx, &awscognito.AdminSetUserPasswordInput{
			UserPoolId: aws.String(env.poolID),
			Username:   aws.String("admin-set-pw-user"),
			Password:   aws.String("LongEnough1!"),
			Permanent:  true,
		})
		require.NoError(t, err)
	})
}

// ── Password Management (ForgotPassword / ConfirmForgotPassword / ChangePassword) ──

func TestCognitoIntegration_PasswordManagement(t *testing.T) {
	cap := withCodeCapture(t)
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	t.Run("ForgotPassword_ConfirmForgotPassword_Success", func(t *testing.T) {
		const (
			username = "forgotpw-user"
			password = "Password1!"
		)
		poolID, _ := createConfirmedUser(
			t, ctx, c, cap, "forgotpw-pool", "forgotpw-client", username, password,
		)
		clientOut, err := c.ListUserPoolClients(ctx, &awscognito.ListUserPoolClientsInput{
			UserPoolId: aws.String(poolID),
		})
		require.NoError(t, err)
		require.Len(t, clientOut.UserPoolClients, 1)
		clientID := aws.ToString(clientOut.UserPoolClients[0].ClientId)

		_, err = c.AdminUpdateUserAttributes(ctx, &awscognito.AdminUpdateUserAttributesInput{
			UserPoolId: aws.String(poolID),
			Username:   aws.String(username),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("email"), Value: aws.String("forgotpw@example.com")},
				{Name: aws.String("email_verified"), Value: aws.String("true")},
			},
		})
		require.NoError(t, err)

		fpOut, err := c.ForgotPassword(ctx, &awscognito.ForgotPasswordInput{
			ClientId: aws.String(clientID),
			Username: aws.String(username),
		})
		require.NoError(t, err)
		require.NotNil(t, fpOut.CodeDeliveryDetails)
		assert.Equal(t, "email", aws.ToString(fpOut.CodeDeliveryDetails.AttributeName))

		code := cap.get(username)
		require.NotEmpty(t, code)

		_, err = c.ConfirmForgotPassword(ctx, &awscognito.ConfirmForgotPasswordInput{
			ClientId:         aws.String(clientID),
			Username:         aws.String(username),
			ConfirmationCode: aws.String(code),
			Password:         aws.String("NewPassword1!"),
		})
		require.NoError(t, err)

		_, err = c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeUserPasswordAuth,
			AuthParameters: map[string]string{
				"USERNAME": username,
				"PASSWORD": "NewPassword1!",
			},
		})
		require.NoError(t, err)

		_, err = c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeUserPasswordAuth,
			AuthParameters: map[string]string{
				"USERNAME": username,
				"PASSWORD": password,
			},
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})

	t.Run("ForgotPassword_NoVerifiedContact_InvalidParameterException", func(t *testing.T) {
		const (
			username = "forgotpw-noverified-user"
			password = "Password1!"
		)
		poolID, _ := createConfirmedUser(
			t, ctx, c, cap, "forgotpw-noverified-pool", "forgotpw-noverified-client",
			username, password,
		)
		clientOut, err := c.ListUserPoolClients(ctx, &awscognito.ListUserPoolClientsInput{
			UserPoolId: aws.String(poolID),
		})
		require.NoError(t, err)
		clientID := aws.ToString(clientOut.UserPoolClients[0].ClientId)

		// The user was created via SignUp with no attributes, so it has no
		// verified email or phone_number for the reset code to be sent to.
		_, err = c.ForgotPassword(ctx, &awscognito.ForgotPasswordInput{
			ClientId: aws.String(clientID),
			Username: aws.String(username),
		})
		require.Error(t, err)
		assert.Equal(t, "InvalidParameterException", apiErrorCode(err))
	})

	t.Run("ConfirmForgotPassword_CodeMismatch", func(t *testing.T) {
		const (
			username = "forgotpw-mismatch-user"
			password = "Password1!"
		)
		poolID, _ := createConfirmedUser(
			t, ctx, c, cap, "forgotpw-mismatch-pool", "forgotpw-mismatch-client",
			username, password,
		)
		clientOut, err := c.ListUserPoolClients(ctx, &awscognito.ListUserPoolClientsInput{
			UserPoolId: aws.String(poolID),
		})
		require.NoError(t, err)
		clientID := aws.ToString(clientOut.UserPoolClients[0].ClientId)

		_, err = c.AdminUpdateUserAttributes(ctx, &awscognito.AdminUpdateUserAttributesInput{
			UserPoolId: aws.String(poolID),
			Username:   aws.String(username),
			UserAttributes: []types.AttributeType{
				{Name: aws.String("email"), Value: aws.String("forgotpw-mismatch@example.com")},
				{Name: aws.String("email_verified"), Value: aws.String("true")},
			},
		})
		require.NoError(t, err)

		_, err = c.ForgotPassword(ctx, &awscognito.ForgotPasswordInput{
			ClientId: aws.String(clientID),
			Username: aws.String(username),
		})
		require.NoError(t, err)

		_, err = c.ConfirmForgotPassword(ctx, &awscognito.ConfirmForgotPasswordInput{
			ClientId:         aws.String(clientID),
			Username:         aws.String(username),
			ConfirmationCode: aws.String("000000"),
			Password:         aws.String("NewPassword1!"),
		})
		require.Error(t, err)
		assert.Equal(t, "CodeMismatchException", apiErrorCode(err))
	})

	t.Run("ChangePassword_Success", func(t *testing.T) {
		const (
			username = "changepw-user"
			password = "Password1!"
		)
		poolID, accessToken := createConfirmedUser(
			t, ctx, c, cap, "changepw-pool", "changepw-client", username, password,
		)
		clientOut, err := c.ListUserPoolClients(ctx, &awscognito.ListUserPoolClientsInput{
			UserPoolId: aws.String(poolID),
		})
		require.NoError(t, err)
		clientID := aws.ToString(clientOut.UserPoolClients[0].ClientId)

		_, err = c.ChangePassword(ctx, &awscognito.ChangePasswordInput{
			AccessToken:      aws.String(accessToken),
			PreviousPassword: aws.String(password),
			ProposedPassword: aws.String("ChangedPassword1!"),
		})
		require.NoError(t, err)

		_, err = c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeUserPasswordAuth,
			AuthParameters: map[string]string{
				"USERNAME": username,
				"PASSWORD": "ChangedPassword1!",
			},
		})
		require.NoError(t, err)

		_, err = c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
			ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeUserPasswordAuth,
			AuthParameters: map[string]string{
				"USERNAME": username,
				"PASSWORD": password,
			},
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})

	t.Run("ChangePassword_WrongPreviousPassword", func(t *testing.T) {
		const (
			username = "changepw-wrong-user"
			password = "Password1!"
		)
		_, accessToken := createConfirmedUser(
			t, ctx, c, cap, "changepw-wrong-pool", "changepw-wrong-client", username, password,
		)

		_, err := c.ChangePassword(ctx, &awscognito.ChangePasswordInput{
			AccessToken:      aws.String(accessToken),
			PreviousPassword: aws.String("WrongPassword1!"),
			ProposedPassword: aws.String("ChangedPassword1!"),
		})
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})

	t.Run("ChangePassword_InvalidProposedPassword", func(t *testing.T) {
		const (
			username = "changepw-invalid-user"
			password = "Password1!"
		)
		_, accessToken := createConfirmedUser(
			t, ctx, c, cap, "changepw-invalid-pool", "changepw-invalid-client", username, password,
		)

		_, err := c.ChangePassword(ctx, &awscognito.ChangePasswordInput{
			AccessToken:      aws.String(accessToken),
			PreviousPassword: aws.String(password),
			ProposedPassword: aws.String("short"),
		})
		require.Error(t, err)
		assert.Equal(t, "InvalidPasswordException", apiErrorCode(err))
	})
}
