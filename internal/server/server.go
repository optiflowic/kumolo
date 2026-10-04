package server

import (
	"context"
	"errors"
	"fmt"
	"net"
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

// WithCognitoOptions passes through options to cognito.NewRouter. Used by
// tests to override internals such as bcrypt cost, and by production code
// (cmd/kumolo/main.go) to supply cognito.WithAWSRegion.
func WithCognitoOptions(opts ...cognito.Option) Option {
	return func(o *options) {
		o.cognitoOpts = append(o.cognitoOpts, opts...)
	}
}

// WithCORSAllowOrigin enables CORS support for the X-Amz-Target-routed
// services (DynamoDB, DynamoDB Streams, KMS, Cognito) and STS, which have no
// CORS handling of their own. It has no effect on S3, whose CORS behavior
// remains driven exclusively by PutBucketCors, matching real AWS fidelity.
// An empty origin leaves DynamoDB/DynamoDB Streams/KMS/STS behavior fully
// unchanged; Cognito gets a default CORS origin regardless (see
// defaultCognitoCORSAllowOrigin), and always wins over that default when
// set — including for a preflight identified via a convention Host (#567,
// see hostService).
func WithCORSAllowOrigin(origin string) Option {
	return func(o *options) {
		o.corsAllowOrigin = origin
	}
}

// defaultCognitoCORSAllowOrigin is the Access-Control-Allow-Origin value
// applied to Cognito-routed requests when KUMOLO_CORS_ALLOW_ORIGIN is unset.
// Real cognito-idp sends "access-control-allow-origin: *" unconditionally and
// browser SDKs depend on it (#553).
const defaultCognitoCORSAllowOrigin = "*"

// Convention hostnames that let a client name its target service via the Host
// header (#567). See docs/service-dispatch.md.
const (
	hostServiceCognito         = "cognito-idp"
	hostServiceDynamoDB        = "dynamodb"
	hostServiceDynamoDBStreams = "streams.dynamodb"
	hostServiceKMS             = "kms"
	hostServiceSTS             = "sts"

	hostSuffix = ".localhost"
)

// hostService identifies which X-Amz-Target-routed service (if any) a
// request's Host header names. Matching is exact, never by prefix or suffix,
// so "dynamodb.localhost.evil.example" does not match.
func hostService(host string) (service string, ok bool) {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host // no ":port" suffix present
	}
	h = strings.ToLower(h)
	switch h {
	case hostServiceCognito + hostSuffix:
		return hostServiceCognito, true
	case hostServiceDynamoDBStreams + hostSuffix:
		return hostServiceDynamoDBStreams, true
	case hostServiceDynamoDB + hostSuffix:
		return hostServiceDynamoDB, true
	case hostServiceKMS + hostSuffix:
		return hostServiceKMS, true
	case hostServiceSTS + hostSuffix:
		return hostServiceSTS, true
	default:
		return "", false
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
		// A preflight never carries X-Amz-Target, so the dispatch below can't
		// route it. Path "/" is never an S3 path, so S3's own preflight
		// handling is untouched.
		if r.Method == http.MethodOptions && r.URL.Path == "/" {
			if svc, ok := hostService(r.Host); ok {
				origin := o.corsAllowOrigin
				if origin == "" && svc == hostServiceCognito {
					origin = defaultCognitoCORSAllowOrigin
				}
				writeCORSHeaders(w, r, origin)
				w.WriteHeader(http.StatusOK)
				return
			}
			// Opt-in only: the target is unknown here, and approving the
			// preflight would let a browser send a side-effecting request
			// to an unauthenticated service.
			if o.corsAllowOrigin != "" {
				writeCORSHeaders(w, r, o.corsAllowOrigin)
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		var target http.Handler
		isCognito := false
		if svc, ok := hostService(r.Host); ok {
			// Ignore X-Amz-Target here: otherwise the Cognito host's open
			// CORS policy could be used to reach another service (#566).
			switch svc {
			case hostServiceCognito:
				target = cognitoRouter
				isCognito = true
			case hostServiceDynamoDBStreams:
				target = dynamoStreamsRouter
			case hostServiceDynamoDB:
				target = dynamoRouter
			case hostServiceKMS:
				target = kmsRouter
			case hostServiceSTS:
				target = stsRouter
			default:
				// unreachable: hostService only returns the services handled
				// above. Fail closed rather than fall back to s3Router.
				panic(fmt.Sprintf("server: hostService returned unhandled service %q", svc))
			}
		} else {
			switch {
			case r.Method == http.MethodPost &&
				strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded"):
				target = stsRouter
			case strings.HasPrefix(r.Header.Get("X-Amz-Target"), "DynamoDBStreams_"):
				target = dynamoStreamsRouter
			case strings.HasPrefix(r.Header.Get("X-Amz-Target"), "DynamoDB_"):
				target = dynamoRouter
			case strings.HasPrefix(r.Header.Get("X-Amz-Target"), "TrentService."):
				target = kmsRouter
			case strings.HasPrefix(r.Header.Get("X-Amz-Target"), "AWSCognitoIdentityProviderService.") ||
				strings.HasSuffix(r.URL.Path, "/.well-known/jwks.json"):
				target = cognitoRouter
				isCognito = true
			default:
				s3Router.ServeHTTP(w, r)
				return
			}
		}
		origin := o.corsAllowOrigin
		if origin == "" && isCognito {
			origin = defaultCognitoCORSAllowOrigin
		}
		writeCORSHeaders(w, r, origin)
		target.ServeHTTP(w, r)
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
