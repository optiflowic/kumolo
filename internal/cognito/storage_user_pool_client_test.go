package cognito

import (
	"errors"
	"io"
	"os"
	"testing"

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
