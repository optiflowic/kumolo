package integration_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSTSIntegration(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()

	t.Run("GetCallerIdentity", func(t *testing.T) {
		out, err := clients.sts.GetCallerIdentity(ctx, &awssts.GetCallerIdentityInput{})
		require.NoError(t, err)
		assert.NotEmpty(t, aws.ToString(out.Account))
		assert.NotEmpty(t, aws.ToString(out.Arn))
		assert.NotEmpty(t, aws.ToString(out.UserId))
	})

	t.Run("AssumeRole", func(t *testing.T) {
		out, err := clients.sts.AssumeRole(ctx, &awssts.AssumeRoleInput{
			RoleArn:         aws.String("arn:aws:iam::000000000000:role/test-role"),
			RoleSessionName: aws.String("test-session"),
		})
		require.NoError(t, err)
		require.NotNil(t, out.Credentials)
		assert.NotEmpty(t, aws.ToString(out.Credentials.AccessKeyId))
		assert.NotEmpty(t, aws.ToString(out.Credentials.SecretAccessKey))
		assert.NotEmpty(t, aws.ToString(out.Credentials.SessionToken))
	})

	t.Run("GetSessionToken", func(t *testing.T) {
		out, err := clients.sts.GetSessionToken(ctx, &awssts.GetSessionTokenInput{})
		require.NoError(t, err)
		require.NotNil(t, out.Credentials)
		assert.NotEmpty(t, aws.ToString(out.Credentials.AccessKeyId))
		assert.NotEmpty(t, aws.ToString(out.Credentials.SecretAccessKey))
		assert.NotEmpty(t, aws.ToString(out.Credentials.SessionToken))
	})
}
