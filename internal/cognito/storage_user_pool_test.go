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

func TestDeleteUserPool_DirErrors(t *testing.T) {
	tests := []struct {
		subdir string
		errMsg string
	}{
		{"clients", "delete clients dir"},
		{"users", "delete users dir"},
		{"user_index", "delete user_index dir"},
		{"refresh_tokens", "delete refresh_tokens dir"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.subdir, func(t *testing.T) {
			s := newTestStorage(t)
			poolID := setupStoragePool(t, s)
			storageErr := errors.New("disk error")
			realListDir := s.listDirFn
			target := filepath.Join("pools", poolID, tc.subdir)
			s.listDirFn = func(name string) ([]os.DirEntry, error) {
				if name == target {
					return nil, storageErr
				}
				return realListDir(name)
			}
			err := s.DeleteUserPool(poolID)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errMsg)
		})
	}
}

func TestDeleteUserPool_RemoveKeysJSONError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	storageErr := errors.New("permission denied")
	realRemoveFile := s.removeFile
	keysPath := filepath.Join("pools", poolID, "keys.json")
	s.removeFile = func(name string) error {
		if name == keysPath {
			return storageErr
		}
		return realRemoveFile(name)
	}
	err := s.DeleteUserPool(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove keys.json")
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
