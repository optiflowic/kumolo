package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/optiflowic/kumolo/internal/cognito"
	"github.com/optiflowic/kumolo/internal/kms"
	"github.com/optiflowic/kumolo/internal/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func newTestKMSStorage(t *testing.T) (*kms.Storage, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := kms.NewStorage(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func TestKMSAdapter(t *testing.T) {
	t.Run("resolves valid key to ARN", func(t *testing.T) {
		s, _ := newTestKMSStorage(t)
		a := &kmsAdapter{s: s}
		arn, err := a.ResolveKeyForEncryption("")
		require.NoError(t, err)
		assert.Contains(t, arn, ":key/")
	})

	t.Run("maps ErrKeyNotFound to s3.ErrKMSKeyNotFound", func(t *testing.T) {
		s, _ := newTestKMSStorage(t)
		a := &kmsAdapter{s: s}
		_, err := a.ResolveKeyForEncryption("00000000-0000-0000-0000-000000000000")
		assert.ErrorIs(t, err, s3.ErrKMSKeyNotFound)
	})

	t.Run("maps ErrKeyDisabled to s3.ErrKMSKeyDisabled", func(t *testing.T) {
		s, _ := newTestKMSStorage(t)
		meta, err := s.CreateKey(kms.CreateKeyInput{
			KeySpec:  "SYMMETRIC_DEFAULT",
			KeyUsage: "ENCRYPT_DECRYPT",
			Origin:   "AWS_KMS",
		})
		require.NoError(t, err)
		require.NoError(t, s.DisableKey(meta.KeyID))
		a := &kmsAdapter{s: s}
		_, err = a.ResolveKeyForEncryption(meta.KeyID)
		assert.ErrorIs(t, err, s3.ErrKMSKeyDisabled)
	})

	t.Run("maps ErrKeyPendingDeletion to s3.ErrKMSKeyPendingDeletion", func(t *testing.T) {
		s, _ := newTestKMSStorage(t)
		meta, err := s.CreateKey(kms.CreateKeyInput{
			KeySpec:  "SYMMETRIC_DEFAULT",
			KeyUsage: "ENCRYPT_DECRYPT",
			Origin:   "AWS_KMS",
		})
		require.NoError(t, err)
		_, err = s.ScheduleKeyDeletion(meta.KeyID, 7)
		require.NoError(t, err)
		a := &kmsAdapter{s: s}
		_, err = a.ResolveKeyForEncryption(meta.KeyID)
		assert.ErrorIs(t, err, s3.ErrKMSKeyPendingDeletion)
	})

	t.Run("passes through unknown errors unwrapped", func(t *testing.T) {
		s, dir := newTestKMSStorage(t)
		meta, err := s.CreateKey(kms.CreateKeyInput{
			KeySpec:  "SYMMETRIC_DEFAULT",
			KeyUsage: "ENCRYPT_DECRYPT",
			Origin:   "AWS_KMS",
		})
		require.NoError(t, err)
		// Corrupt meta.json to produce a non-sentinel JSON parse error.
		metaPath := filepath.Join(dir, "kms", "keys", meta.KeyID, "meta.json")
		require.NoError(t, os.WriteFile(metaPath, []byte("not-json"), 0o600))
		a := &kmsAdapter{s: s}
		_, err = a.ResolveKeyForEncryption(meta.KeyID)
		require.Error(t, err)
		assert.NotErrorIs(t, err, s3.ErrKMSKeyNotFound)
		assert.NotErrorIs(t, err, s3.ErrKMSKeyDisabled)
		assert.NotErrorIs(t, err, s3.ErrKMSKeyPendingDeletion)
	})
}

func TestNewMuxError(t *testing.T) {
	t.Run("error when s3 storage fails to init", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "s3"), []byte{}, 0o600))
		mux, cleanup, err := NewMux(context.Background(), dir, time.Minute)
		assert.Error(t, err)
		assert.Nil(t, mux)
		assert.Nil(t, cleanup)
	})

	t.Run("error when dynamodb storage fails to init", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dynamodb"), []byte{}, 0o600))
		mux, cleanup, err := NewMux(context.Background(), dir, time.Minute)
		assert.Error(t, err)
		assert.Nil(t, mux)
		assert.Nil(t, cleanup)
	})

	t.Run("error when kms storage fails to init", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "kms"), []byte{}, 0o600))
		mux, cleanup, err := NewMux(context.Background(), dir, time.Minute)
		assert.Error(t, err)
		assert.Nil(t, mux)
		assert.Nil(t, cleanup)
	})
}

func TestWithCognitoOptions(t *testing.T) {
	o := &options{}
	WithCognitoOptions(cognito.WithBcryptCost(bcrypt.MinCost))(o)
	require.Len(t, o.cognitoOpts, 1)
}

func TestNewMux_WithCognitoOptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	applied := false
	sentinel := cognito.Option(func(*cognito.Router) { applied = true })

	mux, cleanup, err := NewMux(ctx, t.TempDir(), time.Minute,
		WithCognitoOptions(cognito.WithBcryptCost(bcrypt.MinCost), sentinel),
	)
	require.NoError(t, err)
	require.NotNil(t, mux)
	t.Cleanup(cleanup)

	assert.True(t, applied, "NewMux should forward and apply the provided cognito options")
}

func TestNewMux(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mux, cleanup, err := NewMux(ctx, t.TempDir(), time.Minute)
	require.NoError(t, err)
	require.NotNil(t, mux)
	t.Cleanup(cleanup)

	t.Run("routes S3 requests", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
	})

	t.Run("routes DynamoDB requests via X-Amz-Target", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Header.Set("X-Amz-Target", "DynamoDB_20120810.ListTables")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, "application/x-amz-json-1.0", w.Header().Get("Content-Type"))
	})

	t.Run("routes DynamoDBStreams requests via X-Amz-Target", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Header.Set("X-Amz-Target", "DynamoDBStreams_20120810.ListStreams")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/x-amz-json-1.0", w.Header().Get("Content-Type"))
		assert.Contains(t, w.Body.String(), `"Streams"`)
	})

	t.Run("routes STS requests via form-encoded body", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodPost,
			"/",
			strings.NewReader("Action=GetCallerIdentity&Version=2011-06-15"),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Contains(t, w.Header().Get("Content-Type"), "text/xml")
	})

	t.Run("routes Cognito requests via X-Amz-Target", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService.InitiateAuth")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, "application/x-amz-json-1.1", w.Header().Get("Content-Type"))
	})

	t.Run("routes KMS requests via X-Amz-Target", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Header.Set("X-Amz-Target", "TrentService.ListKeys")
		req.Header.Set("Content-Type", "application/x-amz-json-1.1")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, "application/x-amz-json-1.1", w.Header().Get("Content-Type"))
	})

	t.Run("does not route non-POST form-encoded to STS", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
	})

	t.Run(
		"OPTIONS preflight to root is not answered when CORS is disabled",
		func(t *testing.T) {
			// The preflight interceptor can't tell yet which service the
			// browser is about to call, so it can't be scoped to Cognito
			// alone. Answering it unconditionally would let a browser send
			// the unauthenticated DynamoDB/KMS/STS request that follows, so
			// it stays opt-in like every other CORS behavior here; only the
			// *actual* Cognito response defaults to the wildcard origin
			// below.
			req := httptest.NewRequest(http.MethodOptions, "/", nil)
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("Access-Control-Request-Method", "POST")
			req.Header.Set("Access-Control-Request-Headers", "content-type,x-amz-target")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		},
	)

	t.Run(
		"actual DynamoDB response carries no Access-Control-Allow-Origin when CORS is disabled",
		func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
			req.Header.Set("X-Amz-Target", "DynamoDB_20120810.ListTables")
			req.Header.Set("Origin", "http://localhost:5173")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		},
	)

	t.Run(
		"actual Cognito response defaults to Access-Control-Allow-Origin: * when CORS is disabled",
		func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
			req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService.InitiateAuth")
			req.Header.Set("Origin", "http://localhost:5173")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
		},
	)

	t.Run(
		"actual Cognito jwks.json response defaults to Access-Control-Allow-Origin: * when CORS is disabled",
		func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/pool123/.well-known/jwks.json", nil)
			req.Header.Set("Origin", "http://localhost:5173")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
		},
	)

	t.Run(
		"OPTIONS preflight to the Cognito convention Host is answered with the default origin even when CORS is disabled",
		func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, "/", nil)
			req.Host = "cognito-idp.localhost:5566"
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("Access-Control-Request-Method", "POST")
			req.Header.Set("Access-Control-Request-Headers", "content-type,x-amz-target")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
			assert.Equal(t, "POST", w.Header().Get("Access-Control-Allow-Methods"))
		},
	)

	t.Run(
		"actual dispatch is pinned to the Cognito convention Host even when X-Amz-Target names DynamoDB",
		func(t *testing.T) {
			// Regression test for the gap found in review: without pinning
			// dispatch to the Host-identified service, this request would
			// reach dynamoRouter (unauthenticated PutItem) after passing a
			// preflight that trusted the Cognito Host's default-open CORS
			// policy. It must instead land on cognitoRouter, which reports
			// the target as unknown.
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
			req.Host = "cognito-idp.localhost:5566"
			req.Header.Set("X-Amz-Target", "DynamoDB_20120810.PutItem")
			req.Header.Set("Origin", "http://localhost:5173")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, "application/x-amz-json-1.1", w.Header().Get("Content-Type"))
			assert.Contains(t, w.Body.String(), "UnknownOperationException")
			assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
		},
	)

	t.Run(
		"actual dispatch is pinned to the DynamoDB convention Host even when X-Amz-Target names Cognito",
		func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
			req.Host = "dynamodb.localhost:5566"
			req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService.InitiateAuth")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, "application/x-amz-json-1.0", w.Header().Get("Content-Type"))
		},
	)

	t.Run("actual dispatch is pinned to the DynamoDB Streams convention Host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Host = "streams.dynamodb.localhost:5566"
		req.Header.Set("X-Amz-Target", "DynamoDBStreams_20120810.ListStreams")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/x-amz-json-1.0", w.Header().Get("Content-Type"))
		assert.Contains(t, w.Body.String(), `"Streams"`)
	})

	t.Run("actual dispatch is pinned to the KMS convention Host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Host = "kms.localhost:5566"
		req.Header.Set("X-Amz-Target", "TrentService.ListKeys")
		req.Header.Set("Content-Type", "application/x-amz-json-1.1")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, "application/x-amz-json-1.1", w.Header().Get("Content-Type"))
	})

	t.Run("actual dispatch is pinned to the STS convention Host", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodPost,
			"/",
			strings.NewReader("Action=GetCallerIdentity&Version=2011-06-15"),
		)
		req.Host = "sts.localhost:5566"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Contains(t, w.Header().Get("Content-Type"), "text/xml")
	})

	for _, host := range []string{
		"dynamodb.localhost:5566",
		"streams.dynamodb.localhost:5566",
		"kms.localhost:5566",
		"sts.localhost:5566",
	} {
		t.Run(
			"OPTIONS preflight to the "+host+" convention Host is answered 200 without a default origin when CORS is disabled",
			func(t *testing.T) {
				req := httptest.NewRequest(http.MethodOptions, "/", nil)
				req.Host = host
				req.Header.Set("Origin", "http://localhost:5173")
				req.Header.Set("Access-Control-Request-Method", "POST")
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)
				assert.Equal(t, http.StatusOK, w.Code)
				assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
			},
		)
	}
}

func TestNewMux_WithCORSAllowOrigin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mux, cleanup, err := NewMux(
		ctx,
		t.TempDir(),
		time.Minute,
		WithCORSAllowOrigin("http://localhost:5173"),
	)
	require.NoError(t, err)
	require.NotNil(t, mux)
	t.Cleanup(cleanup)

	t.Run("answers root OPTIONS preflight without dispatching to any router", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type,x-amz-target")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "http://localhost:5173", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "POST", w.Header().Get("Access-Control-Allow-Methods"))
		assert.Equal(t, "content-type,x-amz-target", w.Header().Get("Access-Control-Allow-Headers"))
		assert.Equal(t, "Origin", w.Header().Get("Vary"))
		assert.NotEmpty(t, w.Header().Get("Access-Control-Max-Age"))
	})

	t.Run("adds Access-Control-Allow-Origin to the actual DynamoDB response", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Header.Set("X-Amz-Target", "DynamoDB_20120810.ListTables")
		req.Header.Set("Origin", "http://localhost:5173")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, "http://localhost:5173", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run(
		"adds Access-Control-Allow-Origin to the actual DynamoDB Streams response",
		func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
			req.Header.Set("X-Amz-Target", "DynamoDBStreams_20120810.ListStreams")
			req.Header.Set("Origin", "http://localhost:5173")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, "http://localhost:5173", w.Header().Get("Access-Control-Allow-Origin"))
		},
	)

	t.Run("adds Access-Control-Allow-Origin to the actual STS response", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodPost,
			"/",
			strings.NewReader("Action=GetCallerIdentity&Version=2011-06-15"),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		req.Header.Set("Origin", "http://localhost:5173")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, "http://localhost:5173", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("adds Access-Control-Allow-Origin to the actual KMS response", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Header.Set("X-Amz-Target", "TrentService.ListKeys")
		req.Header.Set("Content-Type", "application/x-amz-json-1.1")
		req.Header.Set("Origin", "http://localhost:5173")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, "http://localhost:5173", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("adds Access-Control-Allow-Origin to the actual Cognito response", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService.InitiateAuth")
		req.Header.Set("Origin", "http://localhost:5173")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, "http://localhost:5173", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("does not affect S3 bucket-scoped requests", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/my-bucket/my-key", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", "GET")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run(
		"configured origin wins over the Cognito default for a preflight to the Cognito convention Host",
		func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, "/", nil)
			req.Host = "cognito-idp.localhost:5566"
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("Access-Control-Request-Method", "POST")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "http://localhost:5173", w.Header().Get("Access-Control-Allow-Origin"))
		},
	)

	t.Run(
		"configured origin applies to a preflight to the DynamoDB convention Host",
		func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, "/", nil)
			req.Host = "dynamodb.localhost:5566"
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("Access-Control-Request-Method", "POST")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "http://localhost:5173", w.Header().Get("Access-Control-Allow-Origin"))
		},
	)
}

func TestHostService(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		wantService string
		wantOK      bool
	}{
		{
			"Cognito convention host with port",
			"cognito-idp.localhost:5566",
			hostServiceCognito,
			true,
		},
		{"Cognito convention host without port", "cognito-idp.localhost", hostServiceCognito, true},
		{
			"Cognito convention host is case-insensitive",
			"Cognito-IDP.Localhost:5566",
			hostServiceCognito,
			true,
		},
		{"DynamoDB convention host", "dynamodb.localhost:5566", hostServiceDynamoDB, true},
		{
			"DynamoDB Streams convention host",
			"streams.dynamodb.localhost:5566",
			hostServiceDynamoDBStreams,
			true,
		},
		{"KMS convention host", "kms.localhost:5566", hostServiceKMS, true},
		{"STS convention host", "sts.localhost:5566", hostServiceSTS, true},
		{"default single-endpoint usage does not match", "localhost:5566", "", false},
		{
			"lookalike domain does not match via substring",
			"dynamodb.localhost.evil.example:5566",
			"",
			false,
		},
		{"unrelated host does not match", "example.com:5566", "", false},
		{"empty host does not match", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, ok := hostService(tt.host)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantService, svc)
		})
	}
}

func TestWriteCORSHeaders(t *testing.T) {
	t.Run("no-op when allowOrigin is empty", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		w := httptest.NewRecorder()
		writeCORSHeaders(w, req, "")
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("wildcard origin omits Vary", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		w := httptest.NewRecorder()
		writeCORSHeaders(w, req, "*")
		assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Vary"))
	})

	t.Run("non-OPTIONS request skips preflight-specific headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Access-Control-Request-Method", "POST")
		w := httptest.NewRecorder()
		writeCORSHeaders(w, req, "http://localhost:5173")
		assert.Equal(t, "http://localhost:5173", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Methods"))
	})

	t.Run("OPTIONS without Access-Control-Request-* headers omits them", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		w := httptest.NewRecorder()
		writeCORSHeaders(w, req, "http://localhost:5173")
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Methods"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Headers"))
	})
}
