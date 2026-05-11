package integration_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/optiflowic/kumolo/internal/server"
	"github.com/stretchr/testify/require"
)

type testClients struct {
	s3  *awss3.Client
	ddb *awsdynamodb.Client
}

func newTestClients(t *testing.T) testClients {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mux, cleanup, err := server.NewMux(ctx, t.TempDir(), time.Minute)
	require.NoError(t, err)
	t.Cleanup(cleanup)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg, err := config.LoadDefaultConfig(
		context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
		config.WithBaseEndpoint(srv.URL),
	)
	require.NoError(t, err)

	return testClients{
		s3: awss3.NewFromConfig(cfg, func(o *awss3.Options) {
			o.UsePathStyle = true
		}),
		ddb: awsdynamodb.NewFromConfig(cfg),
	}
}
