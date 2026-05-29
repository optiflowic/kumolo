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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		assert.Equal(t, "application/x-amz-json-1.0", w.Header().Get("Content-Type"))
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

	t.Run("does not route non-POST form-encoded to STS", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
	})
}
