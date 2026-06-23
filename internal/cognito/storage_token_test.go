package cognito

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeRT(poolID, token string) *refreshTokenData {
	return &refreshTokenData{
		Token: token, PoolID: poolID, ClientID: "client-1",
		Username: "alice", Sub: "sub-alice", IssuedAt: nowUnix(),
	}
}

// ── CreateRefreshToken ────────────────────────────────────────────────────────

func TestCreateRefreshToken_Success(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	require.NoError(t, s.CreateRefreshToken(makeRT(poolID, "tok-abc")))
}

func TestCreateRefreshToken_MkdirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	s.mkdirFn = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}
	err := s.CreateRefreshToken(makeRT(poolID, "tok-abc"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create refresh_tokens dir")
}

// ── GetRefreshToken ───────────────────────────────────────────────────────────

func TestGetRefreshToken_Success(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateRefreshToken(makeRT(poolID, "tok-get")))

	rt, err := s.GetRefreshToken(poolID, "tok-get")
	require.NoError(t, err)
	assert.Equal(t, "tok-get", rt.Token)
}

func TestGetRefreshToken_NotFound(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	_, err := s.GetRefreshToken(poolID, "no-such-token")
	require.ErrorIs(t, err, errRefreshTokenNotFound)
}

func TestGetRefreshToken_ReadError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateRefreshToken(makeRT(poolID, "tok-err")))

	s.readAll = func(io.Reader) ([]byte, error) {
		return nil, errors.New("read error")
	}
	_, err := s.GetRefreshToken(poolID, "tok-err")
	require.Error(t, err)
	assert.False(t, errors.Is(err, errRefreshTokenNotFound))
}

// ── DeleteRefreshToken ────────────────────────────────────────────────────────

func TestDeleteRefreshToken_Success(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateRefreshToken(makeRT(poolID, "tok-del")))

	require.NoError(t, s.DeleteRefreshToken(poolID, "tok-del"))

	_, err := s.GetRefreshToken(poolID, "tok-del")
	require.ErrorIs(t, err, errRefreshTokenNotFound)
}

func TestDeleteRefreshToken_NotFound(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	err := s.DeleteRefreshToken(poolID, "no-such-token")
	require.ErrorIs(t, err, errRefreshTokenNotFound)
}

func TestDeleteRefreshToken_RemoveError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateRefreshToken(makeRT(poolID, "tok-del2")))

	s.removeFile = func(string) error {
		return errors.New("permission denied")
	}
	err := s.DeleteRefreshToken(poolID, "tok-del2")
	require.Error(t, err)
	assert.False(t, errors.Is(err, errRefreshTokenNotFound))
}

// ── writeClientIndexLocked / deleteClientIndexLocked ─────────────────────────

func TestWriteClientIndexLocked_Success(t *testing.T) {
	s := newTestStorage(t)

	require.NoError(t, s.writeClientIndexLocked("pool-id-1", "client-id-1"))

	poolID, err := s.GetPoolIDForClient("client-id-1")
	require.NoError(t, err)
	assert.Equal(t, "pool-id-1", poolID)
}

func TestWriteClientIndexLocked_WriteError(t *testing.T) {
	s := newTestStorage(t)

	s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
		return nil, errors.New("disk full")
	}
	err := s.writeClientIndexLocked("pool-id-2", "client-id-2")
	require.Error(t, err)
}

func TestDeleteClientIndexLocked_Success(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.writeClientIndexLocked("pool-id-3", "client-id-3"))

	require.NoError(t, s.deleteClientIndexLocked("client-id-3"))

	_, err := s.GetPoolIDForClient("client-id-3")
	require.ErrorIs(t, err, errUserPoolClientNotFound)
}

func TestDeleteClientIndexLocked_NotFoundIsNoop(t *testing.T) {
	s := newTestStorage(t)
	// Deleting a non-existent client index should not return an error.
	require.NoError(t, s.deleteClientIndexLocked("nonexistent-client"))
}

func TestDeleteClientIndexLocked_RemoveError(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.writeClientIndexLocked("pool-id-4", "client-id-4"))

	s.removeFile = func(string) error {
		return errors.New("permission denied")
	}
	err := s.deleteClientIndexLocked("client-id-4")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove client index")
}

// ── GetPoolIDForClient ────────────────────────────────────────────────────────

func TestGetPoolIDForClient_NotFound(t *testing.T) {
	s := newTestStorage(t)

	_, err := s.GetPoolIDForClient("no-such-client")
	require.ErrorIs(t, err, errUserPoolClientNotFound)
}

func TestGetPoolIDForClient_ReadError(t *testing.T) {
	s := newTestStorage(t)
	require.NoError(t, s.writeClientIndexLocked("pool-id-5", "client-id-5"))

	s.readAll = func(io.Reader) ([]byte, error) {
		return nil, errors.New("read error")
	}
	_, err := s.GetPoolIDForClient("client-id-5")
	require.Error(t, err)
	assert.False(t, errors.Is(err, errUserPoolClientNotFound))
}

// ── ensureRefreshTokensDir ────────────────────────────────────────────────────

func TestEnsureRefreshTokensDir_AlreadyExists(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	// Create the directory manually first.
	dir := filepath.Join("pools", poolID, "refresh_tokens")
	require.NoError(t, s.mkdirFn(dir, 0o750))

	// Calling again must not fail (ErrExist is swallowed).
	require.NoError(t, s.ensureRefreshTokensDir(poolID))
}
