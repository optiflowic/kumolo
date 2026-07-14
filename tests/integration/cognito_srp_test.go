package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscognito "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/optiflowic/kumolo/internal/cognitotest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// srpChallenge holds the fields returned by InitiateAuth's USER_SRP_AUTH
// response, needed to compute the PASSWORD_VERIFIER challenge response.
type srpChallenge struct {
	salt        string
	srpB        string
	secretBlock string
	session     string
}

// initiateSRPAuth drives InitiateAuth with AuthFlow=USER_SRP_AUTH, computing
// SRP_A via a fresh cognitotest.SRPClient (aws-sdk-go-v2 has no built-in SRP
// support — real Amplify apps compute these fields client-side, so this
// mirrors that).
func initiateSRPAuth(
	t *testing.T, ctx context.Context, c *awscognito.Client, clientID, username string,
) (*cognitotest.SRPClient, srpChallenge) {
	t.Helper()
	client, err := cognitotest.NewSRPClient()
	require.NoError(t, err)

	out, err := c.InitiateAuth(ctx, &awscognito.InitiateAuthInput{
		ClientId: aws.String(clientID),
		AuthFlow: types.AuthFlowTypeUserSrpAuth,
		AuthParameters: map[string]string{
			"USERNAME": username,
			"SRP_A":    client.AHex(),
		},
	})
	require.NoError(t, err)
	require.Equal(t, types.ChallengeNameTypePasswordVerifier, out.ChallengeName)

	return client, srpChallenge{
		salt:        out.ChallengeParameters["SALT"],
		srpB:        out.ChallengeParameters["SRP_B"],
		secretBlock: out.ChallengeParameters["SECRET_BLOCK"],
		session:     aws.ToString(out.Session),
	}
}

// respondPasswordVerifierErr computes and submits the PASSWORD_VERIFIER
// challenge response for the given username/password, returning any error
// from RespondToAuthChallenge instead of asserting success.
func respondPasswordVerifierErr(
	t *testing.T, ctx context.Context, c *awscognito.Client,
	clientID, poolID, username, password string,
	client *cognitotest.SRPClient, ch srpChallenge,
) (*awscognito.RespondToAuthChallengeOutput, error) {
	t.Helper()
	poolName := strings.SplitN(poolID, "_", 2)[1]
	timestamp := cognitotest.NowString()
	sig, err := client.ComputeSignature(
		poolName, username, password, ch.salt, ch.srpB, ch.secretBlock, timestamp,
	)
	require.NoError(t, err)

	return c.RespondToAuthChallenge(ctx, &awscognito.RespondToAuthChallengeInput{
		ClientId:      aws.String(clientID),
		ChallengeName: types.ChallengeNameTypePasswordVerifier,
		Session:       aws.String(ch.session),
		ChallengeResponses: map[string]string{
			"USERNAME":                    username,
			"PASSWORD_CLAIM_SECRET_BLOCK": ch.secretBlock,
			"TIMESTAMP":                   timestamp,
			"PASSWORD_CLAIM_SIGNATURE":    sig,
		},
	})
}

// respondPasswordVerifier is the asserting wrapper for tests that expect
// RespondToAuthChallenge to succeed.
func respondPasswordVerifier(
	t *testing.T, ctx context.Context, c *awscognito.Client,
	clientID, poolID, username, password string,
	client *cognitotest.SRPClient, ch srpChallenge,
) *awscognito.RespondToAuthChallengeOutput {
	t.Helper()
	out, err := respondPasswordVerifierErr(
		t,
		ctx,
		c,
		clientID,
		poolID,
		username,
		password,
		client,
		ch,
	)
	require.NoError(t, err)
	return out
}

func TestCognitoIntegration_UserSrpAuth(t *testing.T) {
	cap := withCodeCapture(t)
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito

	pool, err := c.CreateUserPool(ctx, &awscognito.CreateUserPoolInput{
		PoolName: aws.String("srp-test-pool"),
	})
	require.NoError(t, err)
	poolID := aws.ToString(pool.UserPool.Id)

	appClient, err := c.CreateUserPoolClient(ctx, &awscognito.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientName: aws.String("srp-test-client"),
	})
	require.NoError(t, err)
	clientID := aws.ToString(appClient.UserPoolClient.ClientId)

	const (
		username = "srp-user"
		password = "Password1!"
	)

	_, err = c.SignUp(ctx, &awscognito.SignUpInput{
		ClientId: aws.String(clientID),
		Username: aws.String(username),
		Password: aws.String(password),
		UserAttributes: []types.AttributeType{
			{Name: aws.String("email"), Value: aws.String("srp-user@example.com")},
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

	t.Run("Success", func(t *testing.T) {
		client, ch := initiateSRPAuth(t, ctx, c, clientID, username)
		out := respondPasswordVerifier(t, ctx, c, clientID, poolID, username, password, client, ch)

		require.NotNil(t, out.AuthenticationResult)
		assert.NotEmpty(t, aws.ToString(out.AuthenticationResult.AccessToken))
		assert.NotEmpty(t, aws.ToString(out.AuthenticationResult.IdToken))
		assert.NotEmpty(t, aws.ToString(out.AuthenticationResult.RefreshToken))
		assert.Equal(t, "Bearer", aws.ToString(out.AuthenticationResult.TokenType))
	})

	t.Run("WrongPassword", func(t *testing.T) {
		client, ch := initiateSRPAuth(t, ctx, c, clientID, username)
		_, err := respondPasswordVerifierErr(
			t,
			ctx,
			c,
			clientID,
			poolID,
			username,
			"WrongPassword1!",
			client,
			ch,
		)
		require.Error(t, err)
		assert.Equal(t, "NotAuthorizedException", apiErrorCode(err))
	})
}

func TestCognitoIntegration_UserSrpAuth_ForceChangePasswordChaining(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	c := clients.cognito
	env := newAdminTestEnv(t, c, "srp-fcp-pool")

	const (
		username = "srp-fcp-user"
		tempPass = "TempPass1!"
		newPass  = "NewPass1!"
	)

	_, err := c.AdminCreateUser(ctx, &awscognito.AdminCreateUserInput{
		UserPoolId:        aws.String(env.poolID),
		Username:          aws.String(username),
		TemporaryPassword: aws.String(tempPass),
	})
	require.NoError(t, err)

	client, ch := initiateSRPAuth(t, ctx, c, env.clientID, username)
	out := respondPasswordVerifier(
		t,
		ctx,
		c,
		env.clientID,
		env.poolID,
		username,
		tempPass,
		client,
		ch,
	)

	// FORCE_CHANGE_PASSWORD users get a chained NEW_PASSWORD_REQUIRED
	// challenge instead of tokens, reusing the existing handler.
	require.Equal(t, types.ChallengeNameTypeNewPasswordRequired, out.ChallengeName)
	require.NotNil(t, out.Session)

	final, err := c.RespondToAuthChallenge(ctx, &awscognito.RespondToAuthChallengeInput{
		ClientId:      aws.String(env.clientID),
		ChallengeName: types.ChallengeNameTypeNewPasswordRequired,
		Session:       out.Session,
		ChallengeResponses: map[string]string{
			"USERNAME":     username,
			"NEW_PASSWORD": newPass,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, final.AuthenticationResult)
	assert.NotEmpty(t, aws.ToString(final.AuthenticationResult.AccessToken))
}
