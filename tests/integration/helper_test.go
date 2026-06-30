package integration_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscognito "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	awsstreams "github.com/aws/aws-sdk-go-v2/service/dynamodbstreams"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	"github.com/optiflowic/kumolo/internal/server"
	"github.com/stretchr/testify/require"
)

type testClients struct {
	s3      *awss3.Client
	ddb     *awsdynamodb.Client
	streams *awsstreams.Client
	sts     *awssts.Client
	kms     *awskms.Client
	cognito *awscognito.Client
	baseURL string
	dataDir string
}

// apiErrorCode extracts the AWS error code from an SDK error.
func apiErrorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return ""
}

// newServerAt starts a kumolo server rooted at dataDir and returns clients plus
// an explicit stop function. The stop function is idempotent and is also
// registered as a t.Cleanup safety net. Callers that need to simulate a
// process restart should call stop() explicitly before creating a second server.
func newServerAt(t *testing.T, dataDir string) (testClients, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	mux, cleanup, err := server.NewMux(ctx, dataDir, time.Minute)
	require.NoError(t, err)
	srv := httptest.NewServer(mux)

	var once sync.Once
	stop := func() {
		once.Do(func() {
			srv.Close()
			cleanup()
			cancel()
		})
	}
	t.Cleanup(stop)

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
		ddb:     awsdynamodb.NewFromConfig(cfg),
		streams: awsstreams.NewFromConfig(cfg),
		sts:     awssts.NewFromConfig(cfg),
		kms:     awskms.NewFromConfig(cfg),
		cognito: awscognito.NewFromConfig(cfg),
		baseURL: srv.URL,
		dataDir: dataDir,
	}, stop
}

func newTestClients(t *testing.T) testClients {
	t.Helper()
	clients, _ := newServerAt(t, t.TempDir())
	return clients
}
