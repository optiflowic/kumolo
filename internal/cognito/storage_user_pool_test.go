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

// ── deleteNestedDirLocked ─────────────────────────────────────────────────────

func TestDeleteUserPool_GroupDirErrors(t *testing.T) {
	ts := nowUnix()
	setupGroupsAndMember := func(s *Storage, poolID string) {
		require.NoError(t, s.CreateGroup(poolID, &GroupMetadata{
			GroupName: "g", UserPoolId: poolID, CreationDate: ts, LastModifiedDate: ts,
		}))
		require.NoError(t, s.CreateUser(poolID, &UserMetadata{
			Username: "u", Sub: "sub-u", Status: userStatusConfirmed, CreatedAt: ts, UpdatedAt: ts,
		}))
		require.NoError(t, s.AddUserToGroup(poolID, "g", "u"))
	}
	tests := []struct {
		name   string
		setup  func(s *Storage, poolID string)
		target func(poolID string) string
		errMsg string
	}{
		{
			"groups dir",
			func(s *Storage, poolID string) {
				require.NoError(t, s.CreateGroup(poolID, &GroupMetadata{
					GroupName: "g", UserPoolId: poolID, CreationDate: ts, LastModifiedDate: ts,
				}))
			},
			func(poolID string) string { return filepath.Join("pools", poolID, "groups") },
			"delete groups dir",
		},
		{
			"group_members dir",
			setupGroupsAndMember,
			func(poolID string) string { return filepath.Join("pools", poolID, "group_members") },
			"delete group_members dir",
		},
		{
			"user_groups dir",
			setupGroupsAndMember,
			func(poolID string) string { return filepath.Join("pools", poolID, "user_groups") },
			"delete user_groups dir",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStorage(t)
			poolID := setupStoragePool(t, s)
			tc.setup(s, poolID)
			storageErr := errors.New("disk error")
			realListDir := s.listDirFn
			target := tc.target(poolID)
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

// ── deleteNestedDirLocked error paths ─────────────────────────────────────────

func TestDeleteNestedDirLocked_ListDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	ts := nowUnix()
	require.NoError(t, s.CreateGroup(poolID, &GroupMetadata{
		GroupName: "admins", UserPoolId: poolID, CreationDate: ts, LastModifiedDate: ts,
	}))
	require.NoError(t, s.CreateUser(poolID, &UserMetadata{
		Username:  "alice",
		Sub:       "sub-alice",
		Status:    userStatusConfirmed,
		CreatedAt: ts,
		UpdatedAt: ts,
	}))
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	listErr := errors.New("listdir error")
	realListDir := s.listDirFn
	dir := filepath.Join("pools", poolID, "group_members")
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == dir {
			return nil, listErr
		}
		return realListDir(name)
	}
	err := s.deleteNestedDirLocked(dir)
	require.ErrorIs(t, err, listErr)
}

func TestDeleteNestedDirLocked_DeleteSubdirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	ts := nowUnix()
	require.NoError(t, s.CreateGroup(poolID, &GroupMetadata{
		GroupName: "admins", UserPoolId: poolID, CreationDate: ts, LastModifiedDate: ts,
	}))
	require.NoError(t, s.CreateUser(poolID, &UserMetadata{
		Username:  "alice",
		Sub:       "sub-alice",
		Status:    userStatusConfirmed,
		CreatedAt: ts,
		UpdatedAt: ts,
	}))
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	listErr := errors.New("listdir subdir error")
	realListDir := s.listDirFn
	hashDir := filepath.Join("pools", poolID, "group_members", groupKey("admins"))
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == hashDir {
			return nil, listErr
		}
		return realListDir(name)
	}
	dir := filepath.Join("pools", poolID, "group_members")
	err := s.deleteNestedDirLocked(dir)
	require.ErrorIs(t, err, listErr)
}

func TestDeleteNestedDirLocked_RemoveDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	ts := nowUnix()
	require.NoError(t, s.CreateGroup(poolID, &GroupMetadata{
		GroupName: "admins", UserPoolId: poolID, CreationDate: ts, LastModifiedDate: ts,
	}))
	require.NoError(t, s.CreateUser(poolID, &UserMetadata{
		Username:  "alice",
		Sub:       "sub-alice",
		Status:    userStatusConfirmed,
		CreatedAt: ts,
		UpdatedAt: ts,
	}))
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	removeErr := errors.New("remove error")
	realRemoveFile := s.removeFile
	dir := filepath.Join("pools", poolID, "group_members")
	s.removeFile = func(name string) error {
		if name == dir {
			return removeErr
		}
		return realRemoveFile(name)
	}
	err := s.deleteNestedDirLocked(dir)
	require.ErrorIs(t, err, removeErr)
}

func TestDeleteNestedDirLocked_RemovesNonDirEntry(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	ts := nowUnix()
	require.NoError(t, s.CreateGroup(poolID, &GroupMetadata{
		GroupName: "admins", UserPoolId: poolID, CreationDate: ts, LastModifiedDate: ts,
	}))
	require.NoError(t, s.CreateUser(poolID, &UserMetadata{
		Username: "alice", Sub: "sub-alice", Status: userStatusConfirmed,
		CreatedAt: ts, UpdatedAt: ts,
	}))
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	// Write a stray file directly inside the top-level nested dir.
	strayPath := filepath.Join("pools", poolID, "group_members", "stray.json")
	require.NoError(t, s.writeJSON(strayPath, struct{}{}))

	dir := filepath.Join("pools", poolID, "group_members")
	require.NoError(t, s.deleteNestedDirLocked(dir))

	// The stray file must be gone.
	_, err := s.statFn(strayPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDeleteNestedDirLocked_NonDirRemoveError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	dir := filepath.Join("pools", poolID, "group_members")
	strayPath := filepath.Join(dir, "stray.json")
	removeErr := errors.New("remove failed")
	realListDir := s.listDirFn
	realRemove := s.removeFile
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == dir {
			return []os.DirEntry{fakeDirEntry("stray.json")}, nil
		}
		return realListDir(name)
	}
	s.removeFile = func(name string) error {
		if name == strayPath {
			return removeErr
		}
		return realRemove(name)
	}
	err := s.deleteNestedDirLocked(dir)
	require.ErrorIs(t, err, removeErr)
}

func TestDeleteUserPool_WithGroups(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	ts := nowUnix()
	require.NoError(t, s.CreateGroup(poolID, &GroupMetadata{
		GroupName: "admins", UserPoolId: poolID,
		CreationDate: ts, LastModifiedDate: ts,
	}))
	require.NoError(t, s.CreateUser(poolID, &UserMetadata{
		Username: "alice", Sub: "sub-alice", Status: userStatusConfirmed,
		CreatedAt: ts, UpdatedAt: ts,
	}))
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	require.NoError(t, s.DeleteUserPool(poolID))

	_, err := s.GetUserPool(poolID)
	require.ErrorIs(t, err, errUserPoolNotFound)
}
