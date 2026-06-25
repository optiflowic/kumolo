package cognito

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStorage(t *testing.T) *Storage {
	t.Helper()
	s, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func setupStoragePool(t *testing.T, s *Storage) string {
	t.Helper()
	poolID := "us-east-1_TestPool"
	require.NoError(t, s.CreateUserPool(&UserPoolMetadata{ID: poolID, Name: "test"}))
	return poolID
}

// ── CreateUser ────────────────────────────────────────────────────────────────

func TestCreateUser_Success(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	user := &UserMetadata{Username: "alice", Sub: "sub-alice", Status: userStatusUnconfirmed}
	require.NoError(t, s.CreateUser(poolID, user))
}

func TestCreateUser_DuplicateUsername(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	user := &UserMetadata{Username: "alice", Sub: "sub-alice", Status: userStatusUnconfirmed}
	require.NoError(t, s.CreateUser(poolID, user))

	user2 := &UserMetadata{Username: "alice", Sub: "sub-alice2", Status: userStatusUnconfirmed}
	err := s.CreateUser(poolID, user2)
	require.ErrorIs(t, err, errUsernameExists)
}

func TestCreateUser_UsersDirMkdirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	s.mkdirFn = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}
	user := &UserMetadata{Username: "bob", Sub: "sub-bob", Status: userStatusUnconfirmed}
	err := s.CreateUser(poolID, user)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create users dir")
}

func TestCreateUser_UserIndexDirMkdirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	realMkdir := s.mkdirFn
	calls := 0
	s.mkdirFn = func(path string, perm os.FileMode) error {
		calls++
		if calls == 1 {
			return realMkdir(path, perm) // users dir: success
		}
		return errors.New("mkdir failed for user_index")
	}
	user := &UserMetadata{Username: "bob", Sub: "sub-bob", Status: userStatusUnconfirmed}
	err := s.CreateUser(poolID, user)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create user_index dir")
}

func TestCreateUser_WriteUserFileError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
		return nil, errors.New("disk full")
	}
	user := &UserMetadata{Username: "carol", Sub: "sub-carol", Status: userStatusUnconfirmed}
	err := s.CreateUser(poolID, user)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write user")
}

func TestCreateUser_WriteIndexFileError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	realOpenFile := s.openFile
	calls := 0
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		calls++
		if calls == 1 {
			return realOpenFile(name, flag, perm) // user file: success
		}
		return nil, errors.New("disk full on index write")
	}
	user := &UserMetadata{Username: "dave", Sub: "sub-dave", Status: userStatusUnconfirmed}
	err := s.CreateUser(poolID, user)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write user index")
}

func TestCreateUser_WriteIndexFileError_RollsBackUserFile(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	realOpenFile := s.openFile
	calls := 0
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		calls++
		if calls == 1 {
			return realOpenFile(name, flag, perm) // user file: success
		}
		return nil, errors.New("disk full on index write")
	}
	user := &UserMetadata{Username: "eve", Sub: "sub-eve", Status: userStatusUnconfirmed}
	err := s.CreateUser(poolID, user)
	require.Error(t, err)

	// The user file must have been rolled back: a subsequent create must succeed.
	s.openFile = realOpenFile
	err = s.CreateUser(poolID, user)
	require.NoError(t, err)
}

func TestCreateUser_WriteIndexFileError_RollbackDeleteFails(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	realOpenFile := s.openFile
	calls := 0
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		calls++
		if calls == 1 {
			return realOpenFile(name, flag, perm)
		}
		return nil, errors.New("disk full on index write")
	}
	s.removeFile = func(string) error {
		return errors.New("disk full on rollback delete")
	}
	user := &UserMetadata{Username: "frank", Sub: "sub-frank", Status: userStatusUnconfirmed}
	err := s.CreateUser(poolID, user)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write user index")
	assert.Contains(t, err.Error(), "rollback")
}

// ── GetUser / getUserLocked ───────────────────────────────────────────────────

func TestGetUser_NotFound(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	_, err := s.GetUser(poolID, "nobody")
	require.ErrorIs(t, err, errUserNotFound)
}

func TestGetUser_IndexReadError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID,
		&UserMetadata{Username: "alice", Sub: "sub-alice", Status: userStatusUnconfirmed},
	))

	s.readAll = func(io.Reader) ([]byte, error) {
		return nil, errors.New("read error")
	}
	_, err := s.GetUser(poolID, "alice")
	require.Error(t, err)
	assert.False(t, errors.Is(err, errUserNotFound))
}

func TestGetUser_UserFileReadError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID,
		&UserMetadata{Username: "alice", Sub: "sub-alice", Status: userStatusUnconfirmed},
	))

	realReadAll := s.readAll
	calls := 0
	s.readAll = func(r io.Reader) ([]byte, error) {
		calls++
		if calls == 1 {
			return realReadAll(r) // index read: success
		}
		return nil, errors.New("corrupt user file")
	}
	_, err := s.GetUser(poolID, "alice")
	require.Error(t, err)
	assert.False(t, errors.Is(err, errUserNotFound))
}

// ── GetUserBySub ─────────────────────────────────────────────────────────────

func TestGetUserBySub_NotFound(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	_, err := s.GetUserBySub(poolID, "nonexistent-sub")
	require.ErrorIs(t, err, errUserNotFound)
}

func TestGetUserBySub_ReadError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID,
		&UserMetadata{Username: "alice", Sub: "sub-alice", Status: userStatusUnconfirmed},
	))

	s.readAll = func(io.Reader) ([]byte, error) {
		return nil, errors.New("read error")
	}
	_, err := s.GetUserBySub(poolID, "sub-alice")
	require.Error(t, err)
	assert.False(t, errors.Is(err, errUserNotFound))
}

// ── UpdateUser ────────────────────────────────────────────────────────────────

func TestUpdateUser_Success(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID,
		&UserMetadata{Username: "alice", Sub: "sub-alice", Status: userStatusUnconfirmed},
	))

	err := s.UpdateUser(poolID, "alice", func(u *UserMetadata) error {
		u.Status = userStatusConfirmed
		return nil
	})
	require.NoError(t, err)

	user, err := s.GetUser(poolID, "alice")
	require.NoError(t, err)
	assert.Equal(t, userStatusConfirmed, user.Status)
}

func TestUpdateUser_NotFound(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	err := s.UpdateUser(poolID, "nobody", func(*UserMetadata) error { return nil })
	require.ErrorIs(t, err, errUserNotFound)
}

// ── DeleteUser ────────────────────────────────────────────────────────────────

func TestDeleteUser_RemoveUserFileError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID,
		&UserMetadata{Username: "alice", Sub: "sub-alice", Status: userStatusUnconfirmed},
	))

	calls := 0
	realRemove := s.removeFile
	s.removeFile = func(name string) error {
		calls++
		if calls == 1 {
			return errors.New("disk full")
		}
		return realRemove(name)
	}
	err := s.DeleteUser(poolID, "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove user")
}

func TestDeleteUser_RemoveIndexFileError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID,
		&UserMetadata{Username: "alice", Sub: "sub-alice", Status: userStatusUnconfirmed},
	))

	calls := 0
	realRemove := s.removeFile
	s.removeFile = func(name string) error {
		calls++
		if calls == 1 {
			return realRemove(name) // user file: success
		}
		return errors.New("disk full")
	}
	err := s.DeleteUser(poolID, "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove user index")
}
