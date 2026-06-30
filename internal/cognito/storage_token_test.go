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

// ── RevokeAccessToken / IsAccessTokenRevoked ─────────────────────────────────

func TestRevokeAccessToken_Success(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	require.NoError(t, s.RevokeAccessToken(poolID, "jti-abc", 9999999999))

	revoked, err := s.IsAccessTokenRevoked(poolID, "jti-abc")
	require.NoError(t, err)
	assert.True(t, revoked)
}

func TestIsAccessTokenRevoked_NotRevoked(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	revoked, err := s.IsAccessTokenRevoked(poolID, "no-such-jti")
	require.NoError(t, err)
	assert.False(t, revoked)
}

func TestEnsureRevokedJTIsDir_AlreadyExists(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	dir := filepath.Join("pools", poolID, "revoked_jtis")
	require.NoError(t, s.mkdirFn(dir, 0o750))

	// Calling again must not fail.
	require.NoError(t, s.ensureRevokedJTIsDir(poolID))
}

// ── DeleteRefreshTokensBySub ──────────────────────────────────────────────────

func TestDeleteRefreshTokensBySub_DeletesMatchingTokens(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	rt1 := &refreshTokenData{
		Token:    "tok-1",
		PoolID:   poolID,
		ClientID: "c",
		Username: "alice",
		Sub:      "sub-alice",
	}
	rt2 := &refreshTokenData{
		Token:    "tok-2",
		PoolID:   poolID,
		ClientID: "c",
		Username: "alice",
		Sub:      "sub-alice",
	}
	rt3 := &refreshTokenData{
		Token:    "tok-3",
		PoolID:   poolID,
		ClientID: "c",
		Username: "bob",
		Sub:      "sub-bob",
	}
	require.NoError(t, s.CreateRefreshToken(rt1))
	require.NoError(t, s.CreateRefreshToken(rt2))
	require.NoError(t, s.CreateRefreshToken(rt3))

	require.NoError(t, s.DeleteRefreshTokensBySub(poolID, "sub-alice"))

	_, err1 := s.GetRefreshToken(poolID, "tok-1")
	assert.ErrorIs(t, err1, errRefreshTokenNotFound)
	_, err2 := s.GetRefreshToken(poolID, "tok-2")
	assert.ErrorIs(t, err2, errRefreshTokenNotFound)
	// Bob's token must still exist.
	_, err3 := s.GetRefreshToken(poolID, "tok-3")
	assert.NoError(t, err3)
}

func TestDeleteRefreshTokensBySub_NoTokensForSub(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	rt := &refreshTokenData{
		Token:    "tok-1",
		PoolID:   poolID,
		ClientID: "c",
		Username: "alice",
		Sub:      "sub-alice",
	}
	require.NoError(t, s.CreateRefreshToken(rt))

	// Deleting for an unrelated sub must succeed without error.
	require.NoError(t, s.DeleteRefreshTokensBySub(poolID, "sub-nobody"))

	// alice's token must still exist.
	_, err := s.GetRefreshToken(poolID, "tok-1")
	assert.NoError(t, err)
}

func TestDeleteRefreshTokensBySub_NoRefreshTokensDir(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	// No refresh_tokens dir exists yet — must return nil.
	require.NoError(t, s.DeleteRefreshTokensBySub(poolID, "sub-alice"))
}

func TestDeleteRefreshTokensBySub_ListDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	rt := &refreshTokenData{Token: "tok-1", PoolID: poolID, ClientID: "c", Sub: "sub-alice"}
	require.NoError(t, s.CreateRefreshToken(rt))

	s.listDirFn = func(string) ([]os.DirEntry, error) {
		return nil, errors.New("disk error")
	}
	err := s.DeleteRefreshTokensBySub(poolID, "sub-alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list refresh tokens")
}

func TestDeleteRefreshTokensBySub_SkipsDirectory(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	// Create a subdirectory inside refresh_tokens — it must be silently skipped.
	require.NoError(t, s.CreateRefreshToken(
		&refreshTokenData{Token: "tok-1", PoolID: poolID, ClientID: "c", Sub: "sub-alice"},
	))
	subdir := filepath.Join("pools", poolID, "refresh_tokens", "subdir")
	require.NoError(t, s.mkdirFn(subdir, 0o750))

	require.NoError(t, s.DeleteRefreshTokensBySub(poolID, "sub-alice"))
	_, err := s.GetRefreshToken(poolID, "tok-1")
	assert.ErrorIs(t, err, errRefreshTokenNotFound)
}

// ── ensureRevokedJTIsDir / RevokeAccessToken error paths ─────────────────────

func TestEnsureRevokedJTIsDir_MkdirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	s.mkdirFn = func(string, os.FileMode) error {
		return errors.New("permission denied")
	}
	err := s.ensureRevokedJTIsDir(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create revoked_jtis dir")
}

func TestRevokeAccessToken_EnsureDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	s.mkdirFn = func(string, os.FileMode) error {
		return errors.New("permission denied")
	}
	err := s.RevokeAccessToken(poolID, "jti-abc", 9999999999)
	require.Error(t, err)
}

// ── IsAccessTokenRevoked stat error ──────────────────────────────────────────

func TestIsAccessTokenRevoked_StatError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	s.statFn = func(string) (os.FileInfo, error) {
		return nil, errors.New("permission denied")
	}
	_, err := s.IsAccessTokenRevoked(poolID, "jti-abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check revoked JTI")
}
