package cognito

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStorage(t *testing.T) *Storage {
	t.Helper()
	s, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	s.generateKeyFn = testGenerateRSAKey
	return s
}

// testRSAKeyPool lazily generates a small set of real RSA-2048 keys once per
// test binary run (gosec G403 requires at least 2048 bits, even in test code,
// so key *size* can't be reduced to speed up tests). Repeated RSA-2048
// generation is the dominant cost across this package's tests; a shared,
// round-robin pool amortizes that cost across hundreds of tests instead of
// paying it on every call.
var testRSAKeyPool = sync.OnceValue(func() []*rsa.PrivateKey {
	const poolSize = 4
	keys := make([]*rsa.PrivateKey, poolSize)
	for i := range keys {
		k, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
		if err != nil {
			panic(err)
		}
		keys[i] = k
	}
	return keys
})

var testRSAKeyIdx atomic.Uint32

// nextTestRSAKey returns a key from the shared pool, cycling round-robin so
// consecutive calls always return different keys. Some tests rely on two
// pools (or two otherwise-related keys) having distinct RSA keys — e.g.
// TestRespondToAuthChallenge_WrongSessionPool checks that a session token
// signed by one pool's key fails verification against another pool's key —
// round-robin over a pool of >1 keys preserves that for any two calls in
// immediate succession. This assumes no test in this package calls
// t.Parallel(): interleaved calls from unrelated parallel tests could shift
// which pool index a given test's calls land on, silently breaking that
// distinctness guarantee.
func nextTestRSAKey() *rsa.PrivateKey {
	keys := testRSAKeyPool()
	idx := testRSAKeyIdx.Add(1) - 1
	return keys[int(idx)%len(keys)]
}

func testGenerateRSAKey() (*rsa.PrivateKey, error) {
	return nextTestRSAKey(), nil
}

// testRSAKey returns a key from the shared test RSA key pool (see
// nextTestRSAKey) for use in ad-hoc test fixtures that build their own JWTs.
func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	return nextTestRSAKey()
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

	// After partial failure: user file is gone but stale index remains.
	// Restore removeFile and verify retry converges (stale index is cleaned up).
	s.removeFile = realRemove
	require.NoError(t, s.DeleteUser(poolID, "alice"))
	_, err = s.GetUser(poolID, "alice")
	require.ErrorIs(t, err, errUserNotFound)
}

func TestDeleteUser_IndexReadError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID,
		&UserMetadata{Username: "alice", Sub: "sub-alice", Status: userStatusUnconfirmed},
	))

	s.readAll = func(io.Reader) ([]byte, error) {
		return nil, errors.New("read error")
	}
	err := s.DeleteUser(poolID, "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read user index")
}

// ── ListUsers ────────────────────────────────────────────────────────────────

func TestStorageListUsers_NoUsersDir(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	users, nextToken, err := s.ListUsers(poolID, nil, 60, "")
	require.NoError(t, err)
	assert.Empty(t, users)
	assert.Empty(t, nextToken)
}

func TestStorageListUsers_SortedAndPaginated(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	for _, name := range []string{"charlie", "alice", "bob"} {
		require.NoError(t, s.CreateUser(poolID,
			&UserMetadata{Username: name, Sub: "sub-" + name, Status: userStatusConfirmed},
		))
	}

	users, nextToken, err := s.ListUsers(poolID, nil, 2, "")
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "alice", users[0].Username)
	assert.Equal(t, "bob", users[1].Username)
	require.NotEmpty(t, nextToken)

	users2, nextToken2, err := s.ListUsers(poolID, nil, 2, nextToken)
	require.NoError(t, err)
	require.Len(t, users2, 1)
	assert.Equal(t, "charlie", users2[0].Username)
	assert.Empty(t, nextToken2)
}

func TestStorageListUsers_WithFilter(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	for _, name := range []string{"alice", "bob"} {
		require.NoError(t, s.CreateUser(poolID,
			&UserMetadata{Username: name, Sub: "sub-" + name, Status: userStatusConfirmed},
		))
	}

	filter := func(u *UserMetadata) bool { return u.Username == "alice" }
	users, _, err := s.ListUsers(poolID, filter, 60, "")
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "alice", users[0].Username)
}

func TestStorageListUsers_InvalidNextToken(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID,
		&UserMetadata{Username: "alice", Sub: "sub-alice", Status: userStatusConfirmed},
	))

	_, _, err := s.ListUsers(poolID, nil, 60, "bad-token")
	require.ErrorIs(t, err, errInvalidNextToken)
}

func TestStorageListUsers_ListDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	s.listDirFn = func(string) ([]os.DirEntry, error) {
		return nil, errors.New("disk error")
	}
	_, _, err := s.ListUsers(poolID, nil, 60, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list users dir")
}

func TestStorageListUsers_SkipsDirEntry(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID,
		&UserMetadata{Username: "alice", Sub: "sub-alice", Status: userStatusConfirmed},
	))

	realListDir := s.listDirFn
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		entries, err := realListDir(name)
		if err != nil {
			return nil, err
		}
		if filepath.Base(name) == "users" {
			return append([]os.DirEntry{fakeDirEntryDir("subdir")}, entries...), nil
		}
		return entries, nil
	}

	users, _, err := s.ListUsers(poolID, nil, 60, "")
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "alice", users[0].Username)
}

func TestStorageListUsers_CorruptedUserFileSkipped(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	require.NoError(t, s.CreateUser(poolID,
		&UserMetadata{Username: "alice", Sub: "sub-alice", Status: userStatusConfirmed},
	))

	calls := 0
	realReadAll := s.readAll
	s.readAll = func(r io.Reader) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("corrupted")
		}
		return realReadAll(r)
	}
	users, _, err := s.ListUsers(poolID, nil, 60, "")
	require.NoError(t, err)
	assert.Empty(t, users)
}
