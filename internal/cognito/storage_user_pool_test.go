package cognito

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── DeleteUserPool error paths ────────────────────────────────────────────────

func TestDeleteUserPool_DeleteUsersError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	storageErr := errors.New("disk error")
	realListDir := s.listDirFn
	usersDir := filepath.Join("pools", poolID, "users")
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == usersDir {
			return nil, storageErr
		}
		return realListDir(name)
	}

	err := s.DeleteUserPool(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete users dir")
}

func TestDeleteUserPool_DeleteUserIndexError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	storageErr := errors.New("disk error")
	realListDir := s.listDirFn
	userIndexDir := filepath.Join("pools", poolID, "user_index")
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == userIndexDir {
			return nil, storageErr
		}
		return realListDir(name)
	}

	err := s.DeleteUserPool(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete user_index dir")
}

func TestDeleteUserPool_DeleteRefreshTokensError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	storageErr := errors.New("disk error")
	realListDir := s.listDirFn
	rtDir := filepath.Join("pools", poolID, "refresh_tokens")
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == rtDir {
			return nil, storageErr
		}
		return realListDir(name)
	}

	err := s.DeleteUserPool(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete refresh_tokens dir")
}

func TestDeleteUserPool_RemovePoolDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	storageErr := errors.New("permission denied")
	realRemoveFile := s.removeFile
	poolDir := filepath.Join("pools", poolID)
	s.removeFile = func(name string) error {
		if name == poolDir {
			return storageErr
		}
		return realRemoveFile(name)
	}

	err := s.DeleteUserPool(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove pool dir")
}

// ── deleteFlatDirLocked ───────────────────────────────────────────────────────

func TestDeleteFlatDirLocked_WithFiles(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID, &UserMetadata{
		Username: "alice", Sub: "sub-alice", Status: userStatusUnconfirmed,
	}))
	require.NoError(t, s.deleteFlatDirLocked(filepath.Join("pools", poolID, "users")))
}

func TestDeleteFlatDirLocked_ListDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	storageErr := errors.New("disk error")
	s.listDirFn = func(string) ([]os.DirEntry, error) { return nil, storageErr }
	err := s.deleteFlatDirLocked(filepath.Join("pools", poolID, "users"))
	require.ErrorIs(t, err, storageErr)
}

func TestDeleteFlatDirLocked_RemoveFileInLoopError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID, &UserMetadata{
		Username: "alice", Sub: "sub-alice", Status: userStatusUnconfirmed,
	}))
	storageErr := errors.New("disk error")
	s.removeFile = func(string) error { return storageErr }
	err := s.deleteFlatDirLocked(filepath.Join("pools", poolID, "users"))
	require.ErrorIs(t, err, storageErr)
}

func TestDeleteFlatDirLocked_RemoveDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID, &UserMetadata{
		Username: "alice", Sub: "sub-alice", Status: userStatusUnconfirmed,
	}))
	storageErr := errors.New("permission denied")
	realRemoveFile := s.removeFile
	usersDir := filepath.Join("pools", poolID, "users")
	s.removeFile = func(name string) error {
		if name == usersDir {
			return storageErr
		}
		return realRemoveFile(name)
	}
	err := s.deleteFlatDirLocked(usersDir)
	require.ErrorIs(t, err, storageErr)
}
