package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMuxError(t *testing.T) {
	// Place a file where NewStorage expects to create a directory.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s3"), []byte{}, 0o600))

	mux, cleanup, err := NewMux(dir)
	assert.Error(t, err)
	assert.Nil(t, mux)
	assert.Nil(t, cleanup)
}

func TestNewMux(t *testing.T) {
	mux, cleanup, err := NewMux(t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, mux)
	t.Cleanup(cleanup)

	// Verify that the mux routes S3-style requests.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
}
