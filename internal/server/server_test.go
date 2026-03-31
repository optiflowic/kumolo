package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMux(t *testing.T) {
	mux := NewMux()
	require.NotNil(t, mux)

	// Verify that the mux routes S3-style requests.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
}
