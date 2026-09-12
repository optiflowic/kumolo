package integration_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/optiflowic/kumolo/internal/server"
	"github.com/stretchr/testify/require"
)

// TestCORSIntegration_PreflightAndActualResponses exercises the CORS opt-in
// over a real HTTP round trip against a fully-wired server (all routers, not
// just the bare mux), unlike the internal/server unit tests which call
// ServeHTTP directly. AWS SDK clients never issue CORS preflight requests
// themselves (that's a browser-only mechanism), so this uses a raw
// http.Client to simulate what a browser sends.
func TestCORSIntegration_PreflightAndActualResponses(t *testing.T) {
	clients, _ := newServerAt(t, t.TempDir(), server.WithCORSAllowOrigin("http://localhost:5173"))

	t.Run("OPTIONS preflight to root returns 200 with CORS headers", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodOptions, clients.baseURL+"/", nil)
		require.NoError(t, err)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type,x-amz-target")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "http://localhost:5173", resp.Header.Get("Access-Control-Allow-Origin"))
		require.Equal(t, "POST", resp.Header.Get("Access-Control-Allow-Methods"))
		require.Equal(
			t,
			"content-type,x-amz-target",
			resp.Header.Get("Access-Control-Allow-Headers"),
		)
	})

	t.Run("actual DynamoDB response carries Access-Control-Allow-Origin", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, clients.baseURL+"/", strings.NewReader(`{}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-amz-json-1.0")
		req.Header.Set("X-Amz-Target", "DynamoDB_20120810.ListTables")
		req.Header.Set("Origin", "http://localhost:5173")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })

		require.Equal(t, "http://localhost:5173", resp.Header.Get("Access-Control-Allow-Origin"))
	})

	t.Run("S3 bucket-scoped requests are unaffected", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodOptions, clients.baseURL+"/my-bucket/my-key", nil)
		require.NoError(t, err)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", "GET")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })

		require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	})
}

// TestCORSIntegration_DisabledByDefault confirms the opt-in flag's absence
// leaves DynamoDB/DynamoDB Streams/KMS/STS behavior fully unchanged, while
// Cognito still gets a default Access-Control-Allow-Origin: * on its actual
// response — matching real cognito-idp, which returns it unconditionally
// (#553). The root OPTIONS preflight itself stays opt-in: it can't be
// scoped to Cognito alone (the eventual target isn't known yet), and
// answering it unconditionally would let a browser send the unauthenticated
// DynamoDB/KMS/STS request that follows.
func TestCORSIntegration_DisabledByDefault(t *testing.T) {
	clients := newTestClients(t)

	t.Run(
		"OPTIONS preflight to root carries no Access-Control-Allow-Origin",
		func(t *testing.T) {
			req, err := http.NewRequest(http.MethodOptions, clients.baseURL+"/", nil)
			require.NoError(t, err)
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("Access-Control-Request-Method", "POST")
			req.Header.Set("Access-Control-Request-Headers", "content-type,x-amz-target")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })

			require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
		},
	)

	t.Run("actual DynamoDB response carries no Access-Control-Allow-Origin", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, clients.baseURL+"/", strings.NewReader(`{}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-amz-json-1.0")
		req.Header.Set("X-Amz-Target", "DynamoDB_20120810.ListTables")
		req.Header.Set("Origin", "http://localhost:5173")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })

		require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	})

	t.Run("actual Cognito response defaults to Access-Control-Allow-Origin: *", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, clients.baseURL+"/", strings.NewReader(`{}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-amz-json-1.1")
		req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService.InitiateAuth")
		req.Header.Set("Origin", "http://localhost:5173")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })

		require.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
	})
}

// TestCORSIntegration_HostBasedServiceIdentification exercises the #567
// convention-hostname mechanism over a real HTTP round trip against a
// fully-wired server. Requests target clients.baseURL (127.0.0.1) directly
// and only override the Host header field on the request — this avoids
// depending on *.localhost DNS resolution, which is reliable on macOS but
// not guaranteed on every CI/Linux resolver configuration, while still
// exercising exactly what the server inspects (the Host header of an
// incoming request).
func TestCORSIntegration_HostBasedServiceIdentification(t *testing.T) {
	clients := newTestClients(t)

	t.Run(
		"OPTIONS preflight to the Cognito convention Host defaults open without KUMOLO_CORS_ALLOW_ORIGIN",
		func(t *testing.T) {
			req, err := http.NewRequest(http.MethodOptions, clients.baseURL+"/", nil)
			require.NoError(t, err)
			req.Host = "cognito-idp.localhost:5566"
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("Access-Control-Request-Method", "POST")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })

			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
		},
	)

	t.Run(
		"OPTIONS preflight to the DynamoDB convention Host stays closed without KUMOLO_CORS_ALLOW_ORIGIN",
		func(t *testing.T) {
			req, err := http.NewRequest(http.MethodOptions, clients.baseURL+"/", nil)
			require.NoError(t, err)
			req.Host = "dynamodb.localhost:5566"
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("Access-Control-Request-Method", "POST")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })

			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
		},
	)

	t.Run(
		"actual dispatch is pinned to the Cognito convention Host even when X-Amz-Target names DynamoDB",
		func(t *testing.T) {
			// Regression coverage for the review finding on #567: without
			// pinning dispatch to the Host-identified service, this request
			// would reach the DynamoDB router (unauthenticated PutItem)
			// after a preflight that trusted the Cognito Host's
			// default-open CORS policy.
			req, err := http.NewRequest(
				http.MethodPost,
				clients.baseURL+"/",
				strings.NewReader(`{}`),
			)
			require.NoError(t, err)
			req.Host = "cognito-idp.localhost:5566"
			req.Header.Set("X-Amz-Target", "DynamoDB_20120810.PutItem")
			req.Header.Set("Origin", "http://localhost:5173")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })

			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Equal(t, "application/x-amz-json-1.1", resp.Header.Get("Content-Type"))
			require.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
		},
	)

	t.Run(
		"actual DynamoDB response over the DynamoDB convention Host carries no Access-Control-Allow-Origin",
		func(t *testing.T) {
			req, err := http.NewRequest(
				http.MethodPost,
				clients.baseURL+"/",
				strings.NewReader(`{}`),
			)
			require.NoError(t, err)
			req.Host = "dynamodb.localhost:5566"
			req.Header.Set("Content-Type", "application/x-amz-json-1.0")
			req.Header.Set("X-Amz-Target", "DynamoDB_20120810.ListTables")
			req.Header.Set("Origin", "http://localhost:5173")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })

			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
		},
	)
}

// TestCORSIntegration_HostBasedServiceIdentification_ExplicitOriginWins
// confirms KUMOLO_CORS_ALLOW_ORIGIN always overrides a convention Host's
// default policy, for both Cognito (default-open) and DynamoDB (no
// default) — matching the WithCORSAllowOrigin contract.
func TestCORSIntegration_HostBasedServiceIdentification_ExplicitOriginWins(t *testing.T) {
	clients, _ := newServerAt(t, t.TempDir(), server.WithCORSAllowOrigin("http://localhost:5173"))

	t.Run("configured origin wins over the Cognito default", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodOptions, clients.baseURL+"/", nil)
		require.NoError(t, err)
		req.Host = "cognito-idp.localhost:5566"
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", "POST")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })

		require.Equal(t, "http://localhost:5173", resp.Header.Get("Access-Control-Allow-Origin"))
	})

	t.Run("configured origin applies to the DynamoDB convention Host", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodOptions, clients.baseURL+"/", nil)
		require.NoError(t, err)
		req.Host = "dynamodb.localhost:5566"
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", "POST")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })

		require.Equal(t, "http://localhost:5173", resp.Header.Get("Access-Control-Allow-Origin"))
	})
}
