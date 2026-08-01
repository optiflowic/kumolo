package integration_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/optiflowic/kumolo/internal/server"
	"github.com/stretchr/testify/require"
)

// TestCORSIntegration_PreflightAndActualResponses exercises the CORS opt-in
// (#515) over a real HTTP round trip against a fully-wired server (all
// routers, not just the bare mux), unlike the internal/server unit tests
// which call ServeHTTP directly. AWS SDK clients never issue CORS preflight
// requests themselves (that's a browser-only mechanism), so this uses a raw
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
// leaves the dispatcher's behavior fully unchanged, over a real HTTP round trip.
func TestCORSIntegration_DisabledByDefault(t *testing.T) {
	clients := newTestClients(t)

	req, err := http.NewRequest(http.MethodOptions, clients.baseURL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type,x-amz-target")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
}
