package integration_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP mandates HMAC-SHA1
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscognito "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// totpCode independently reimplements RFC 6238 (client-side) to generate a code from a
// base32 SecretCode returned by AssociateSoftwareToken, simulating a real authenticator app.
func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	require.NoError(t, err)

	// at.Unix() is always >= 0, so the uint64 conversion cannot wrap.
	step := uint64(at.Unix()) / 30 //nolint:gosec

	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], step)

	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", code%1_000_000)
}

// mfaTestEnv provisions a pool, client, and a confirmed+authenticated user for MFA tests.
type mfaTestEnv struct {
	c           *awscognito.Client
	clientID    string
	username    string
	password    string
	accessToken string
}

func newMFATestEnv(t *testing.T, name string) mfaTestEnv {
	t.Helper()
	cap := withCodeCapture(t)
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

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
	clientID := aws.ToString(cl.UserPoolClient.ClientId)

	const username = "mfa-user"
	const password = "Password1!"
	_, err = c.SignUp(ctx, &awscognito.SignUpInput{
		ClientId: aws.String(clientID),
		Username: aws.String(username),
		Password: aws.String(password),
		UserAttributes: []types.AttributeType{
			{Name: aws.String("email"), Value: aws.String("mfa-user@example.com")},
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

	return mfaTestEnv{
		c:           c,
		clientID:    clientID,
		username:    username,
		password:    password,
		accessToken: aws.ToString(auth.AuthenticationResult.AccessToken),
	}
}

// enrollTOTP drives AssociateSoftwareToken + VerifySoftwareToken to completion and returns
// the verified secret.
func enrollTOTP(t *testing.T, env mfaTestEnv) string {
	t.Helper()
	ctx := context.Background()

	assoc, err := env.c.AssociateSoftwareToken(ctx, &awscognito.AssociateSoftwareTokenInput{
		AccessToken: aws.String(env.accessToken),
	})
	require.NoError(t, err)
	secret := aws.ToString(assoc.SecretCode)
	require.NotEmpty(t, secret)

	verify, err := env.c.VerifySoftwareToken(ctx, &awscognito.VerifySoftwareTokenInput{
		AccessToken: aws.String(env.accessToken),
		UserCode:    aws.String(totpCode(t, secret, time.Now())),
	})
	require.NoError(t, err)
	assert.Equal(t, types.VerifySoftwareTokenResponseTypeSuccess, verify.Status)
	return secret
}

func TestCognitoIntegration_MFA_EnrollmentFlow(t *testing.T) {
	env := newMFATestEnv(t, "mfa-enroll-pool")
	ctx := context.Background()

	secret := enrollTOTP(t, env)

	_, err := env.c.SetUserMFAPreference(ctx, &awscognito.SetUserMFAPreferenceInput{
		AccessToken: aws.String(env.accessToken),
		SoftwareTokenMfaSettings: &types.SoftwareTokenMfaSettingsType{
			Enabled:      true,
			PreferredMfa: true,
		},
	})
	require.NoError(t, err)

	// Signing in again must now issue a SOFTWARE_TOKEN_MFA challenge instead of tokens.
	initAuth, err := env.c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
		ClientId: aws.String(env.clientID),
		AuthFlow: types.AuthFlowTypeUserPasswordAuth,
		AuthParameters: map[string]string{
			"USERNAME": env.username,
			"PASSWORD": env.password,
		},
	})
	require.NoError(t, err)
	require.Nil(t, initAuth.AuthenticationResult)
	assert.Equal(t, types.ChallengeNameTypeSoftwareTokenMfa, initAuth.ChallengeName)
	require.NotNil(t, initAuth.Session)

	resp, err := env.c.RespondToAuthChallenge(ctx, &awscognito.RespondToAuthChallengeInput{
		ClientId:      aws.String(env.clientID),
		ChallengeName: types.ChallengeNameTypeSoftwareTokenMfa,
		Session:       initAuth.Session,
		ChallengeResponses: map[string]string{
			"USERNAME":                env.username,
			"SOFTWARE_TOKEN_MFA_CODE": totpCode(t, secret, time.Now()),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.AuthenticationResult)
	assert.NotEmpty(t, aws.ToString(resp.AuthenticationResult.AccessToken))
	assert.NotEmpty(t, aws.ToString(resp.AuthenticationResult.RefreshToken))
}

func TestCognitoIntegration_MFA_VerifySoftwareToken_WrongCode(t *testing.T) {
	env := newMFATestEnv(t, "mfa-wrongcode-pool")
	ctx := context.Background()

	_, err := env.c.AssociateSoftwareToken(ctx, &awscognito.AssociateSoftwareTokenInput{
		AccessToken: aws.String(env.accessToken),
	})
	require.NoError(t, err)

	_, err = env.c.VerifySoftwareToken(ctx, &awscognito.VerifySoftwareTokenInput{
		AccessToken: aws.String(env.accessToken),
		UserCode:    aws.String("000000"),
	})
	require.Error(t, err)
	assert.Equal(t, "CodeMismatchException", apiErrorCode(err))
}

func TestCognitoIntegration_MFA_SetPreference_WithoutVerifiedTOTP(t *testing.T) {
	env := newMFATestEnv(t, "mfa-unverified-pool")
	ctx := context.Background()

	_, err := env.c.SetUserMFAPreference(ctx, &awscognito.SetUserMFAPreferenceInput{
		AccessToken: aws.String(env.accessToken),
		SoftwareTokenMfaSettings: &types.SoftwareTokenMfaSettingsType{
			Enabled: true,
		},
	})
	require.Error(t, err)
	assert.Equal(t, "InvalidParameterException", apiErrorCode(err))
}

// newForcedMFATestEnv provisions a pool with MfaConfiguration "ON" (forced MFA_SETUP) and a
// confirmed, unauthenticated user. Unlike newMFATestEnv, it does not call InitiateAuth up front:
// tests need to observe the first sign-in's MFA_SETUP challenge themselves.
func newForcedMFATestEnv(t *testing.T, name string) mfaTestEnv {
	t.Helper()
	cap := withCodeCapture(t)
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	pool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
		PoolName:         aws.String(name),
		MfaConfiguration: types.UserPoolMfaTypeOn,
	})
	require.NoError(t, err)
	poolID := aws.ToString(pool.UserPool.Id)

	cl, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientName: aws.String(name + "-client"),
		ExplicitAuthFlows: []types.ExplicitAuthFlowsType{
			types.ExplicitAuthFlowsTypeAllowUserPasswordAuth,
			types.ExplicitAuthFlowsTypeAllowRefreshTokenAuth,
		},
	})
	require.NoError(t, err)
	clientID := aws.ToString(cl.UserPoolClient.ClientId)

	const username = "forced-mfa-user"
	const password = "Password1!" //nolint:gosec // test fixture, not a real credential
	_, err = c.SignUp(ctx, &awscognito.SignUpInput{
		ClientId: aws.String(clientID),
		Username: aws.String(username),
		Password: aws.String(password),
		UserAttributes: []types.AttributeType{
			{Name: aws.String("email"), Value: aws.String("forced-mfa-user@example.com")},
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

	return mfaTestEnv{c: c, clientID: clientID, username: username, password: password}
}

// mustInitiateForcedMfaSetup runs the password-auth InitiateAuth call against a forced-MFA
// pool and asserts it issues an MFA_SETUP challenge (not tokens), returning the output so
// callers can continue the flow with their own distinguishing steps.
func mustInitiateForcedMfaSetup(
	ctx context.Context,
	t *testing.T,
	env mfaTestEnv,
) *awscognito.InitiateAuthOutput {
	t.Helper()
	initAuth, err := env.c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
		ClientId: aws.String(env.clientID),
		AuthFlow: types.AuthFlowTypeUserPasswordAuth,
		AuthParameters: map[string]string{
			"USERNAME": env.username,
			"PASSWORD": env.password,
		},
	})
	require.NoError(t, err)
	require.Nil(t, initAuth.AuthenticationResult)
	assert.Equal(t, types.ChallengeNameTypeMfaSetup, initAuth.ChallengeName)
	require.NotNil(t, initAuth.Session)
	return initAuth
}

func TestCognitoIntegration_MFA_ForcedSetup_SignInFlow(t *testing.T) {
	env := newForcedMFATestEnv(t, "mfa-forced-setup-pool")
	ctx := context.Background()

	// First sign-in for a pool with MfaConfiguration "ON" must issue MFA_SETUP, not tokens.
	initAuth := mustInitiateForcedMfaSetup(ctx, t, env)

	assoc, err := env.c.AssociateSoftwareToken(ctx, &awscognito.AssociateSoftwareTokenInput{
		Session: initAuth.Session,
	})
	require.NoError(t, err)
	secret := aws.ToString(assoc.SecretCode)
	require.NotEmpty(t, secret)
	require.NotNil(t, assoc.Session)

	verify, err := env.c.VerifySoftwareToken(ctx, &awscognito.VerifySoftwareTokenInput{
		Session:  assoc.Session,
		UserCode: aws.String(totpCode(t, secret, time.Now())),
	})
	require.NoError(t, err)
	assert.Equal(t, types.VerifySoftwareTokenResponseTypeSuccess, verify.Status)
	require.NotNil(t, verify.Session)

	complete, err := env.c.RespondToAuthChallenge(ctx, &awscognito.RespondToAuthChallengeInput{
		ClientId:      aws.String(env.clientID),
		ChallengeName: types.ChallengeNameTypeMfaSetup,
		Session:       verify.Session,
		ChallengeResponses: map[string]string{
			"USERNAME": env.username,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, complete.AuthenticationResult)
	assert.NotEmpty(t, aws.ToString(complete.AuthenticationResult.AccessToken))
	assert.NotEmpty(t, aws.ToString(complete.AuthenticationResult.RefreshToken))

	// A later sign-in must now get SOFTWARE_TOKEN_MFA, not MFA_SETUP again.
	reAuth, err := env.c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
		ClientId: aws.String(env.clientID),
		AuthFlow: types.AuthFlowTypeUserPasswordAuth,
		AuthParameters: map[string]string{
			"USERNAME": env.username,
			"PASSWORD": env.password,
		},
	})
	require.NoError(t, err)
	require.Nil(t, reAuth.AuthenticationResult)
	assert.Equal(t, types.ChallengeNameTypeSoftwareTokenMfa, reAuth.ChallengeName)

	reResp, err := env.c.RespondToAuthChallenge(ctx, &awscognito.RespondToAuthChallengeInput{
		ClientId:      aws.String(env.clientID),
		ChallengeName: types.ChallengeNameTypeSoftwareTokenMfa,
		Session:       reAuth.Session,
		ChallengeResponses: map[string]string{
			"USERNAME":                env.username,
			"SOFTWARE_TOKEN_MFA_CODE": totpCode(t, secret, time.Now()),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, reResp.AuthenticationResult)
	assert.NotEmpty(t, aws.ToString(reResp.AuthenticationResult.AccessToken))
}

func TestCognitoIntegration_MFA_ForcedSetup_VerifySoftwareToken_WrongCode(t *testing.T) {
	env := newForcedMFATestEnv(t, "mfa-forced-wrongcode-pool")
	ctx := context.Background()

	initAuth := mustInitiateForcedMfaSetup(ctx, t, env)

	assoc, err := env.c.AssociateSoftwareToken(ctx, &awscognito.AssociateSoftwareTokenInput{
		Session: initAuth.Session,
	})
	require.NoError(t, err)

	_, err = env.c.VerifySoftwareToken(ctx, &awscognito.VerifySoftwareTokenInput{
		Session:  assoc.Session,
		UserCode: aws.String("000000"),
	})
	require.Error(t, err)
	assert.Equal(t, "CodeMismatchException", apiErrorCode(err))
}

func TestCognitoIntegration_MFA_ForcedSetup_RespondWithoutVerify(t *testing.T) {
	env := newForcedMFATestEnv(t, "mfa-forced-unverified-pool")
	ctx := context.Background()

	initAuth := mustInitiateForcedMfaSetup(ctx, t, env)

	// Responding directly with the InitiateAuth session (no AssociateSoftwareToken/
	// VerifySoftwareToken in between) carries no verified secret and must be rejected.
	_, err := env.c.RespondToAuthChallenge(ctx, &awscognito.RespondToAuthChallengeInput{
		ClientId:      aws.String(env.clientID),
		ChallengeName: types.ChallengeNameTypeMfaSetup,
		Session:       initAuth.Session,
		ChallengeResponses: map[string]string{
			"USERNAME": env.username,
		},
	})
	require.Error(t, err)
	assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
}

func TestCognitoIntegration_MFA_DisablePreference_RestoresDirectSignIn(t *testing.T) {
	env := newMFATestEnv(t, "mfa-disable-pool")
	ctx := context.Background()
	enrollTOTP(t, env)

	_, err := env.c.SetUserMFAPreference(ctx, &awscognito.SetUserMFAPreferenceInput{
		AccessToken: aws.String(env.accessToken),
		SoftwareTokenMfaSettings: &types.SoftwareTokenMfaSettingsType{
			Enabled:      true,
			PreferredMfa: true,
		},
	})
	require.NoError(t, err)

	_, err = env.c.SetUserMFAPreference(ctx, &awscognito.SetUserMFAPreferenceInput{
		AccessToken: aws.String(env.accessToken),
		SoftwareTokenMfaSettings: &types.SoftwareTokenMfaSettingsType{
			Enabled: false,
		},
	})
	require.NoError(t, err)

	auth, err := env.c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
		ClientId: aws.String(env.clientID),
		AuthFlow: types.AuthFlowTypeUserPasswordAuth,
		AuthParameters: map[string]string{
			"USERNAME": env.username,
			"PASSWORD": env.password,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, auth.AuthenticationResult)
	assert.NotEmpty(t, aws.ToString(auth.AuthenticationResult.AccessToken))
}
