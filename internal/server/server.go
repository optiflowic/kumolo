package server

import (
	"context"
	"errors"
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
// Real cognito-idp returns "access-control-allow-origin: *" on every
// response with no configuration required — browser SDKs
// (amazon-cognito-identity-js, Amplify) call it directly and depend on
// this. Unlike DynamoDB/KMS/STS, which are not designed for direct browser
// use, leaving Cognito's CORS support opt-in broke the "works on kumolo ⇒
// works on AWS" guarantee for the browser-SPA case (#553). It applies to
// the actual Cognito response below unconditionally, and — since #567 — to
// the OPTIONS preflight too, but only when the Host header names the
// Cognito convention hostname (see hostService); a preflight to any other
// Host can't be scoped to Cognito alone (the eventual target isn't known
// yet) and stays opt-in via KUMOLO_CORS_ALLOW_ORIGIN only.
const defaultCognitoCORSAllowOrigin = "*"

// Convention hostnames for Host-header virtual hosting (#567). These let an
// SDK client identify its target service before the X-Amz-Target body
// arrives, by pointing a per-service BaseEndpoint at one of these hosts
// instead of the shared http://localhost:5566 endpoint. *.localhost
// resolves to 127.0.0.1 with no DNS/hosts-file setup on every major OS and
// browser (RFC 6761). See docs/service-dispatch.md for the full design.
const (
	hostServiceCognito         = "cognito-idp"
	hostServiceDynamoDB        = "dynamodb"
	hostServiceDynamoDBStreams = "streams.dynamodb"
	hostServiceKMS             = "kms"
	hostServiceSTS             = "sts"

	hostSuffix = ".localhost"
)

// hostService identifies which X-Amz-Target-routed service (if any) a
// request's Host header names. NewMux consults it twice: to pick the CORS
// policy for an OPTIONS preflight, and to pin the actual request's dispatch
// to that same service regardless of X-Amz-Target. Matching is exact and
// case-insensitive against the fixed convention hostnames above — no
// wildcard/prefix matching, so an unrelated domain that merely contains one
// of these labels (e.g. "dynamodb.localhost.evil.example") does not match.
// Any Host that isn't one of these, including the default single-endpoint
// usage (http://localhost:5566 for everything), reports ok=false and falls
// back to the pre-#567 behavior.
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
		// Browser CORS preflight (OPTIONS) requests never carry X-Amz-Target
		// (it's a custom header, only declared via Access-Control-Request-Headers),
		// so they can't be matched by the dispatch chain below. Path "/" is never
		// a valid S3 bucket/object path (parsePath treats it as bucket==""), so
		// this only intercepts requests bound for the services dispatched below,
		// leaving S3's own PutBucketCors-driven preflight handling untouched.
		if r.Method == http.MethodOptions && r.URL.Path == "/" {
			// The Host header (#567), unlike X-Amz-Target, is always present
			// on a preflight — a client using one of the convention
			// hostnames has already told us which service it's calling, so
			// it's safe to resolve the request here with that service's own
			// CORS policy and always answer 200. Omitting
			// Access-Control-Allow-Origin (services with no configured or
			// default origin) still returns 200, but a browser that doesn't
			// see the header won't send the follow-up request. And even if
			// it did, the actual dispatch below pins Host-identified
			// requests to that same service regardless of X-Amz-Target, so
			// this can't be used to reach a different, unauthenticated
			// service the way answering unconditionally for an
			// unidentified Host would.
			if svc, ok := hostService(r.Host); ok {
				origin := o.corsAllowOrigin
				if origin == "" && svc == hostServiceCognito {
					origin = defaultCognitoCORSAllowOrigin
				}
				writeCORSHeaders(w, r, origin)
				w.WriteHeader(http.StatusOK)
				return
			}
			// Host didn't match a known service — the default
			// single-endpoint usage (http://localhost:5566 for everything).
			// This stays strictly opt-in (o.corsAllowOrigin != ""): the
			// eventual target isn't known yet, and answering unconditionally
			// would let a browser send the unauthenticated DynamoDB/KMS/STS
			// request that follows — the response itself would lack CORS
			// headers and be unreadable by the page, but the side effect
			// (PutItem, CreateKey, AssumeRole, ...) would already have
			// happened server-side. defaultCognitoCORSAllowOrigin only ever
			// applies to the actual Cognito response below (or the
			// Host-identified branch above), where the target service is
			// known.
			if o.corsAllowOrigin != "" {
				writeCORSHeaders(w, r, o.corsAllowOrigin)
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		var target http.Handler
		isCognito := false
		if svc, ok := hostService(r.Host); ok {
			// Host names the target service explicitly (#567). Pin the
			// actual dispatch to it regardless of X-Amz-Target: without
			// this, a request whose Host names one service (e.g. the
			// Cognito convention host, whose CORS policy defaults open) but
			// whose X-Amz-Target names another (e.g.
			// "DynamoDB_20120810.PutItem") would still reach that other,
			// unauthenticated service via the X-Amz-Target-only switch
			// below — reopening the exact preflight/dispatch mismatch #566
			// closed, just via a Host the browser was allowed to identify
			// itself with. A mismatched target now hits that service's own
			// unknown-operation error instead.
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
				// unreachable: hostService only returns ok=true for one of
				// the five service constants handled above.
				s3Router.ServeHTTP(w, r)
				return
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
