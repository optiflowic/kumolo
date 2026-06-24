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

// ── CreateUserPoolClient storage-level error paths ────────────────────────────

func TestCreateUserPoolClient_WriteClientJSONError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	// Fail any file write after the pool directory is created.
	s.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
		return nil, errors.New("disk full")
	}
	err := s.CreateUserPoolClient(&UserPoolClientMetadata{
		UserPoolID: poolID,
		ClientID:   "client-1",
		ClientName: "test",
	})
	require.Error(t, err)
}

func TestCreateUserPoolClient_WriteClientIndexError_RollsBackClientFile(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	realOpenFile := s.openFile
	calls := 0
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		calls++
		if calls == 1 {
			return realOpenFile(name, flag, perm) // client JSON: success
		}
		return nil, errors.New("disk full on index write")
	}
	err := s.CreateUserPoolClient(&UserPoolClientMetadata{
		UserPoolID: poolID,
		ClientID:   "client-rollback",
		ClientName: "test",
	})
	require.Error(t, err)

	// The client file must have been rolled back: a subsequent create must succeed.
	s.openFile = realOpenFile
	err = s.CreateUserPoolClient(&UserPoolClientMetadata{
		UserPoolID: poolID,
		ClientID:   "client-rollback",
		ClientName: "test",
	})
	require.NoError(t, err)
}

func TestCreateUserPoolClient_WriteClientIndexError_RollbackDeleteFails(t *testing.T) {
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
	err := s.CreateUserPoolClient(&UserPoolClientMetadata{
		UserPoolID: poolID,
		ClientID:   "client-rollback-fail",
		ClientName: "test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write client index")
	assert.Contains(t, err.Error(), "rollback")
}

func TestDeleteClientsDirLocked_ClientIndexDeleteError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	require.NoError(t, s.CreateUserPoolClient(&UserPoolClientMetadata{
		UserPoolID: poolID,
		ClientID:   "client-1",
		ClientName: "test",
	}))

	storageErr := errors.New("permission denied")
	realRemoveFile := s.removeFile
	clientIndexPath := filepath.Join("client_index", "client-1.json")
	s.removeFile = func(name string) error {
		if name == clientIndexPath {
			return storageErr
		}
		return realRemoveFile(name)
	}

	err := s.deleteClientsDirLocked(poolID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove client index")
}

func TestDeleteClientsDirLocked_RemoveDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	require.NoError(t, s.CreateUserPoolClient(&UserPoolClientMetadata{
		UserPoolID: poolID,
		ClientID:   "client-1",
		ClientName: "test",
	}))

	storageErr := errors.New("permission denied")
	realRemoveFile := s.removeFile
	clientsDir := filepath.Join("pools", poolID, "clients")
	s.removeFile = func(name string) error {
		if name == clientsDir {
			return storageErr
		}
		return realRemoveFile(name)
	}

	err := s.deleteClientsDirLocked(poolID)
	require.ErrorIs(t, err, storageErr)
}
