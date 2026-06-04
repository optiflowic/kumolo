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

	"github.com/optiflowic/kumolo/internal/kms"
	"github.com/optiflowic/kumolo/internal/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
}
