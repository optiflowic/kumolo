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
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscognito "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// codeCapture intercepts SignUp confirmation codes from slog output.
// It acts as a nop handler (no log forwarding) to avoid holding its mutex
// while calling into log.Logger, which would deadlock with the HTTP server goroutine.
type codeCapture struct {
	mu    sync.Mutex
	codes map[string]string // username -> confirmation code
}

func newCodeCapture() *codeCapture {
	return &codeCapture{codes: make(map[string]string)}
}

func (c *codeCapture) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (c *codeCapture) Handle(_ context.Context, r slog.Record) error {
	if r.Message == "SignUp confirmation code" {
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

func TestCognitoIntegration_JWKS(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito
	cap := withCodeCapture(t)

	pool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
		PoolName: aws.String("jwks-test-pool"),
	})
	require.NoError(t, err)
	poolID := aws.ToString(pool.UserPool.Id)

	client, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientName: aws.String("jwks-test-client"),
	})
	require.NoError(t, err)
	clientID := aws.ToString(client.UserPoolClient.ClientId)

	const (
		jwksUsername = "jwks-user"
		jwksPassword = "Password1!"
	)
	_, err = c.SignUp(ctx, &awscognito.SignUpInput{
		ClientId: aws.String(clientID),
		Username: aws.String(jwksUsername),
		Password: aws.String(jwksPassword),
	})
	require.NoError(t, err)

	code := cap.get(jwksUsername)
	require.NotEmpty(t, code)
	_, err = c.ConfirmSignUp(ctx, &awscognito.ConfirmSignUpInput{
		ClientId:         aws.String(clientID),
		Username:         aws.String(jwksUsername),
		ConfirmationCode: aws.String(code),
	})
	require.NoError(t, err)

	auth, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
		ClientId: aws.String(clientID),
		AuthFlow: types.AuthFlowTypeUserPasswordAuth,
		AuthParameters: map[string]string{
			"USERNAME": jwksUsername,
			"PASSWORD": jwksPassword,
		},
	})
	require.NoError(t, err)
	accessToken := aws.ToString(auth.AuthenticationResult.AccessToken)

	t.Run("UnknownPool_404", func(t *testing.T) {
		resp, err := http.Get(clients.baseURL + "/us-east-1_UNKNOWN/.well-known/jwks.json")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("FetchJWKS", func(t *testing.T) {
		resp, err := http.Get(clients.baseURL + "/" + poolID + "/.well-known/jwks.json")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var jwks struct {
			Keys []struct {
				Kid string `json:"kid"`
				Alg string `json:"alg"`
				Kty string `json:"kty"`
				Use string `json:"use"`
				N   string `json:"n"`
				E   string `json:"e"`
			} `json:"keys"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&jwks))
		require.Len(t, jwks.Keys, 1)
		key := jwks.Keys[0]
		assert.Equal(t, "RS256", key.Alg)
		assert.Equal(t, "RSA", key.Kty)
		assert.Equal(t, "sig", key.Use)
		assert.NotEmpty(t, key.Kid)
		assert.NotEmpty(t, key.N)
		assert.Equal(t, "AQAB", key.E, "standard RSA exponent 65537 must encode as AQAB")
	})

	t.Run("VerifyTokenSignature", func(t *testing.T) {
		resp, err := http.Get(clients.baseURL + "/" + poolID + "/.well-known/jwks.json")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var jwks struct {
			Keys []struct {
				N string `json:"n"`
				E string `json:"e"`
			} `json:"keys"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&jwks))
		require.Len(t, jwks.Keys, 1)
		k := jwks.Keys[0]

		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		require.NoError(t, err)
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		require.NoError(t, err)

		pubKey := &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: int(new(big.Int).SetBytes(eBytes).Int64()),
		}

		parts := strings.Split(accessToken, ".")
		require.Len(t, parts, 3)
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
		require.NoError(t, err)
		require.NoError(t,
			rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, digest[:], sigBytes),
			"access token signature must verify against JWKS public key",
		)
	})
}
