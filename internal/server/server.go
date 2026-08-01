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

// Option configures NewMux at construction time.
type Option func(*options)

type options struct {
	cognitoOpts     []cognito.Option
	corsAllowOrigin string
}

// WithCognitoOptions passes through options to cognito.NewRouter. Intended
// for tests that need to override internals such as bcrypt cost; production
// code should not need this.
func WithCognitoOptions(opts ...cognito.Option) Option {
	return func(o *options) {
		o.cognitoOpts = append(o.cognitoOpts, opts...)
	}
}

// WithCORSAllowOrigin enables CORS support for the X-Amz-Target-routed
// services (DynamoDB, DynamoDB Streams, KMS, Cognito) and STS, which have no
// CORS handling of their own. It has no effect on S3, whose CORS behavior
// remains driven exclusively by PutBucketCors, matching real AWS fidelity.
// An empty origin leaves current behavior fully unchanged.
func WithCORSAllowOrigin(origin string) Option {
	return func(o *options) {
		o.corsAllowOrigin = origin
	}
}

func NewMux(
	ctx context.Context,
	dataDir string,
	lifecycleInterval time.Duration,
	opts ...Option,
) (http.Handler, func(), error) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
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
	cognitoRouter := cognito.NewRouter(cognitoStorage, o.cognitoOpts...)

	s3.NewLifecycleEnforcer(s3Storage, lifecycleInterval).Start(ctx)

	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Browser CORS preflight (OPTIONS) requests never carry X-Amz-Target
		// (it's a custom header, only declared via Access-Control-Request-Headers),
		// so they can't be matched by the dispatch chain below. Path "/" is never
		// a valid S3 bucket/object path (parsePath treats it as bucket==""), so
		// this only intercepts requests bound for the services dispatched below,
		// leaving S3's own PutBucketCors-driven preflight handling untouched.
		if o.corsAllowOrigin != "" && r.Method == http.MethodOptions && r.URL.Path == "/" {
			writeCORSHeaders(w, r, o.corsAllowOrigin)
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPost &&
			strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			writeCORSHeaders(w, r, o.corsAllowOrigin)
			stsRouter.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.Header.Get("X-Amz-Target"), "DynamoDBStreams_") {
			writeCORSHeaders(w, r, o.corsAllowOrigin)
			dynamoStreamsRouter.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.Header.Get("X-Amz-Target"), "DynamoDB_") {
			writeCORSHeaders(w, r, o.corsAllowOrigin)
			dynamoRouter.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.Header.Get("X-Amz-Target"), "TrentService.") {
			writeCORSHeaders(w, r, o.corsAllowOrigin)
			kmsRouter.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.Header.Get("X-Amz-Target"), "AWSCognitoIdentityProviderService.") ||
			strings.HasSuffix(r.URL.Path, "/.well-known/jwks.json") {
			writeCORSHeaders(w, r, o.corsAllowOrigin)
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

// writeCORSHeaders adds Access-Control-Allow-Origin to the response so that
// browsers permit reading it cross-origin, and answers preflight-specific
// requirements when the request is itself a preflight. It is a no-op when
// allowOrigin is empty, which is the default and preserves prior behavior.
func writeCORSHeaders(w http.ResponseWriter, r *http.Request, allowOrigin string) {
	if allowOrigin == "" {
		return
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", allowOrigin)
	if allowOrigin != "*" {
		h.Set("Vary", "Origin")
	}
	if r.Method != http.MethodOptions {
		return
	}
	if v := r.Header.Get("Access-Control-Request-Method"); v != "" {
		h.Set("Access-Control-Allow-Methods", v)
	}
	if v := r.Header.Get("Access-Control-Request-Headers"); v != "" {
		h.Set("Access-Control-Allow-Headers", v)
	}
	h.Set("Access-Control-Max-Age", "600")
}
