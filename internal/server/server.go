package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/optiflowic/kumolo/internal/cognito"
	"github.com/optiflowic/kumolo/internal/dynamodb"
	"github.com/optiflowic/kumolo/internal/kms"
	"github.com/optiflowic/kumolo/internal/s3"
	"github.com/optiflowic/kumolo/internal/sts"
)

// kmsAdapter adapts kms.Storage to the s3.KMSService interface, translating
// kms-package error sentinels into the S3-owned equivalents so that the s3
// package does not need to import internal/kms.
type kmsAdapter struct{ s *kms.Storage }

func (a *kmsAdapter) ResolveKeyForEncryption(keyRef string) (string, error) {
	arn, err := a.s.ResolveKeyForEncryption(keyRef)
	if err != nil {
		switch {
		case errors.Is(err, kms.ErrKeyNotFound):
			return "", s3.ErrKMSKeyNotFound
		case errors.Is(err, kms.ErrKeyDisabled):
			return "", s3.ErrKMSKeyDisabled
		case errors.Is(err, kms.ErrKeyPendingDeletion):
			return "", s3.ErrKMSKeyPendingDeletion
		}
		return "", err
	}
	return arn, nil
}

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
	kmsStorage, err := kms.NewStorage(dataDir)
	if err != nil {
		_ = s3Storage.Close()
		_ = dynamoStorage.Close()
		return nil, nil, err
	}
	cognitoStorage, err := cognito.NewStorage(dataDir)
	if err != nil { // unreachable: cognito.NewStorage always succeeds until storage performs filesystem I/O
		_ = s3Storage.Close()
		_ = dynamoStorage.Close()
		_ = kmsStorage.Close()
		return nil, nil, err
	}

	s3Router := s3.NewRouter(s3Storage, &kmsAdapter{s: kmsStorage})
	dynamoRouter := dynamodb.NewRouter(dynamoStorage)
	dynamoStreamsRouter := dynamodb.NewStreamsRouter(dynamoStorage)
	stsRouter := sts.NewRouter()
	kmsRouter := kms.NewRouter(kmsStorage)
	cognitoRouter := cognito.NewRouter(cognitoStorage)

	s3.NewLifecycleEnforcer(s3Storage, lifecycleInterval).Start(ctx)

	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost &&
			strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			stsRouter.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.Header.Get("X-Amz-Target"), "DynamoDBStreams_") {
			dynamoStreamsRouter.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.Header.Get("X-Amz-Target"), "DynamoDB_") {
			dynamoRouter.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.Header.Get("X-Amz-Target"), "TrentService.") {
			kmsRouter.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.Header.Get("X-Amz-Target"), "AWSCognitoIdentityProviderService.") {
			cognitoRouter.ServeHTTP(w, r)
			return
		}
		s3Router.ServeHTTP(w, r)
	}))

	cleanup := func() {
		_ = s3Storage.Close()
		_ = dynamoStorage.Close()
		_ = kmsStorage.Close()
		_ = cognitoStorage.Close()
	}
	return mux, cleanup, nil
}
