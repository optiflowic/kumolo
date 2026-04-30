package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/optiflowic/kumolo/internal/dynamodb"
	"github.com/optiflowic/kumolo/internal/logging"
	"github.com/optiflowic/kumolo/internal/s3"
)

func NewMux(
	ctx context.Context,
	dataDir string,
	lifecycleInterval time.Duration,
) (http.Handler, func(), error) {
	s3Storage, err := s3.NewStorage(dataDir)
	if err != nil {
		return nil, nil, err
	}
	dynamoStorage, err := dynamodb.NewStorage(dataDir)
	if err != nil {
		_ = s3Storage.Close()
		return nil, nil, err
	}

	s3Router := s3.NewRouter(s3Storage)
	dynamoRouter := dynamodb.NewRouter(dynamoStorage)

	s3.NewLifecycleEnforcer(s3Storage, lifecycleInterval).Start(ctx)

	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("X-Amz-Target"), "DynamoDB_") {
			dynamoRouter.ServeHTTP(w, r)
			return
		}
		s3Router.ServeHTTP(w, r)
	}))

	cleanup := func() {
		_ = s3Storage.Close()
		_ = dynamoStorage.Close()
	}
	return logging.Middleware(mux), cleanup, nil
}
