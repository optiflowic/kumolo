package cognito

import (
	"crypto/rsa"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── rsaKeyFromPEM ─────────────────────────────────────────────────────────────

func TestRsaKeyFromPEM_EmptyPEM(t *testing.T) {
	_, err := rsaKeyFromPEM("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid PEM block")
}

func TestRsaKeyFromPEM_InvalidDER(t *testing.T) {
	// A valid PEM block but with garbage DER bytes.
	pemStr := "-----BEGIN RSA PRIVATE KEY-----\naW52YWxpZA==\n-----END RSA PRIVATE KEY-----\n" //nolint:gosec
	_, err := rsaKeyFromPEM(pemStr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse RSA key")
}

// ── GetOrCreatePoolKeys ───────────────────────────────────────────────────────

func TestGetOrCreatePoolKeys_CreateNew(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	keys, privateKey, err := s.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)
	require.NotNil(t, keys)
	require.NotNil(t, privateKey)
	assert.NotEmpty(t, keys.KeyID)
	assert.NotEmpty(t, keys.PrivateKeyPEM)
}

func TestGetOrCreatePoolKeys_ReturnsCachedKey(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	keys1, _, err := s.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)

	keys2, _, err := s.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)

	assert.Equal(t, keys1.KeyID, keys2.KeyID, "key ID must be stable across calls")
	assert.Equal(t, keys1.PrivateKeyPEM, keys2.PrivateKeyPEM)
}

func TestGetOrCreatePoolKeys_ReadError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	// Create keys first so the file exists.
	_, _, err := s.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)

	s.readAll = func(io.Reader) ([]byte, error) {
		return nil, errors.New("read error")
	}
	_, _, err = s.GetOrCreatePoolKeys(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read pool keys")
}

func TestGetOrCreatePoolKeys_BadPEMInStoredKey(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	// Store a keys.json with invalid PEM.
	bad := &poolKeys{KeyID: "kid-bad", PrivateKeyPEM: "not-a-pem"}
	require.NoError(t, s.writeJSON(keysPath(poolID), bad))

	_, _, err := s.GetOrCreatePoolKeys(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load pool RSA key")
}

func TestGetOrCreatePoolKeys_GenerateKeyError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	s.generateKeyFn = func() (*rsa.PrivateKey, error) {
		return nil, errors.New("entropy failed")
	}
	_, _, err := s.GetOrCreatePoolKeys(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generate RSA key")
}

func TestGetOrCreatePoolKeys_WriteError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
		return nil, errors.New("disk full")
	}
	_, _, err := s.GetOrCreatePoolKeys(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write pool keys")
}

// ── GetPoolKeys ───────────────────────────────────────────────────────────────

func TestGetPoolKeys_NotExist(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	_, _, err := s.GetPoolKeys(poolID)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestGetPoolKeys_ReturnsExisting(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	created, _, err := s.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)

	got, _, err := s.GetPoolKeys(poolID)
	require.NoError(t, err)
	assert.Equal(t, created.KeyID, got.KeyID)
}

func TestGetPoolKeys_ReadError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	_, _, err := s.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)

	s.readAll = func(io.Reader) ([]byte, error) {
		return nil, errors.New("read error")
	}
	_, _, err = s.GetPoolKeys(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read pool keys")
}

func TestGetPoolKeys_BadPEM(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	require.NoError(
		t,
		s.writeJSON(keysPath(poolID), &poolKeys{KeyID: "k", PrivateKeyPEM: "bad-pem"}),
	)

	_, _, err := s.GetPoolKeys(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load pool RSA key")
}
