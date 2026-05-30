package kms

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStorage(t *testing.T) (*Storage, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func TestNewStorage_mkdirAllFailure(t *testing.T) {
	dir := t.TempDir()
	// Block MkdirAll by placing a regular file at kms/keys.
	kmsDir := filepath.Join(dir, "kms")
	require.NoError(t, os.MkdirAll(kmsDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(kmsDir, "keys"), nil, 0o600))
	_, err := newStorage(dir, os.OpenRoot)
	require.Error(t, err)
}

func TestNewStorage_openRootFailure(t *testing.T) {
	dir := t.TempDir()
	wantErr := errors.New("openRoot failed")
	_, err := newStorage(dir, func(string) (*os.Root, error) { return nil, wantErr })
	require.ErrorIs(t, err, wantErr)
}

func TestCreateKey_randReadFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	s.randRead = func([]byte) (int, error) { return 0, errors.New("rand failed") }
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.Error(t, err)
}

func TestCreateKey_materialKeyRandReadFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	calls := 0
	orig := s.randRead
	s.randRead = func(b []byte) (int, error) {
		calls++
		if calls == 2 { // second call: key bytes generation inside the SYMMETRIC_DEFAULT block
			return 0, errors.New("rand failed")
		}
		return orig(b)
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.Error(t, err)
}

func TestCreateKey_materialIDRandReadFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	calls := 0
	orig := s.randRead
	s.randRead = func(b []byte) (int, error) {
		calls++
		if calls == 3 { // third call: material ID bytes generation
			return 0, errors.New("rand failed")
		}
		return orig(b)
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.Error(t, err)
}

func TestCreateKey_mkdirFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	s.mkdirFn = func(string, os.FileMode) error { return errors.New("mkdir failed") }
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.Error(t, err)
}

func TestCreateKey_metaWriteFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	wantErr := errors.New("open failed")
	calls := 0
	orig := s.openFile
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		calls++
		if calls == 1 {
			return nil, wantErr
		}
		return orig(name, flag, perm)
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.ErrorIs(t, err, wantErr)
}

func TestCreateKey_metaWriteFailure_cleanupFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	wantErr := errors.New("open failed")
	calls := 0
	orig := s.openFile
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		calls++
		if calls == 1 {
			return nil, wantErr
		}
		return orig(name, flag, perm)
	}
	var removedPaths []string
	s.removeFile = func(name string) error {
		removedPaths = append(removedPaths, name)
		return errors.New("remove failed")
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.ErrorIs(t, err, wantErr)
	require.Len(t, removedPaths, 2)
	assert.Contains(t, removedPaths[0], "meta.json")
	assert.NotContains(t, removedPaths[1], "meta.json")
}

func TestCreateKey_materialWriteFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	wantErr := errors.New("open failed")
	calls := 0
	orig := s.openFile
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		calls++
		if calls == 2 { // material.json is the second write for SYMMETRIC_DEFAULT keys
			return nil, wantErr
		}
		return orig(name, flag, perm)
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.ErrorIs(t, err, wantErr)
}

func TestCreateKey_policyWriteFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	wantErr := errors.New("open failed")
	calls := 0
	orig := s.openFile
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		calls++
		if calls == 3 { // policy.json is the third write for SYMMETRIC_DEFAULT keys
			return nil, wantErr
		}
		return orig(name, flag, perm)
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.ErrorIs(t, err, wantErr)
}

func TestListKeyIDs_listDirFnFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	s.listDirFn = func(string) ([]os.DirEntry, error) { return nil, errors.New("list failed") }
	_, err := s.ListKeyIDs()
	require.Error(t, err)
}

func TestListKeyIDs_openKeysFailure(t *testing.T) {
	// Use a non-injected storage so the real listDirFn closure runs.
	dir := t.TempDir()
	s, err := newStorage(dir, os.OpenRoot)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	// Remove the keys directory to make s.root.Open("keys") fail.
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "kms", "keys")))
	_, err = s.ListKeyIDs()
	require.Error(t, err)
}

func TestListKeyIDs_nonDirEntrySkipped(t *testing.T) {
	s, dir := newTestStorage(t)
	// A regular file directly under keys/ is not a key directory and must be skipped.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kms", "keys", "notadir.txt"), nil, 0o600))
	ids, err := s.ListKeyIDs()
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestListKeyIDs_dirWithoutMeta(t *testing.T) {
	s, dir := newTestStorage(t)
	// An orphan key directory without meta.json must be silently skipped.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "kms", "keys", "orphan"), 0o750))
	ids, err := s.ListKeyIDs()
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestListKeyIDs_statFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.NoError(t, err)
	statErr := errors.New("stat failed")
	s.statFn = func(string) (os.FileInfo, error) { return nil, statErr }
	_, err = s.ListKeyIDs()
	require.ErrorIs(t, err, statErr)
}

func TestGetKeyMaterial_readAllFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	// Corrupt the material.json on disk so readJSON returns a non-ErrNotExist error.
	s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read failed") }
	_, err = s.GetKeyMaterial(meta.KeyID)
	require.Error(t, err)
}

func TestGetKeyMetadata_readAllFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read failed") }
	_, err = s.GetKeyMetadata(meta.KeyID)
	require.Error(t, err)
}

func TestGetKeyMetadata_unmarshalFailure(t *testing.T) {
	s, dir := newTestStorage(t)
	keyID := "00000000-0000-0000-0000-000000000000"
	keyDir := filepath.Join(dir, "kms", "keys", keyID)
	require.NoError(t, os.Mkdir(keyDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, "meta.json"), []byte("not json"), 0o600))
	_, err := s.GetKeyMetadata(keyID)
	require.Error(t, err)
}

func TestNewStorage(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStorage(dir)
	require.NoError(t, err)
	require.NotNil(t, s)
	require.NoError(t, s.Close())
}

func TestCreateKey_materialWriteFailure_cleanupFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	wantErr := errors.New("open failed")
	calls := 0
	orig := s.openFile
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		calls++
		if calls == 2 { // material.json
			return nil, wantErr
		}
		return orig(name, flag, perm)
	}
	var removedPaths []string
	s.removeFile = func(name string) error {
		removedPaths = append(removedPaths, name)
		return errors.New("remove failed")
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.ErrorIs(t, err, wantErr)
	// meta.json, material.json, keyDir
	require.Len(t, removedPaths, 3)
	assert.Contains(t, removedPaths[0], "meta.json")
	assert.Contains(t, removedPaths[1], "material.json")
	assert.NotContains(t, removedPaths[2], ".json")
}

func TestCreateKey_policyWriteFailure_cleanupFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	wantErr := errors.New("open failed")
	calls := 0
	orig := s.openFile
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		calls++
		if calls == 3 { // policy.json is the third write for SYMMETRIC_DEFAULT keys
			return nil, wantErr
		}
		return orig(name, flag, perm)
	}
	var removedPaths []string
	s.removeFile = func(name string) error {
		removedPaths = append(removedPaths, name)
		return errors.New("remove failed")
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.ErrorIs(t, err, wantErr)
	// meta.json, material.json (ErrNotExist skipped via non-error remove), keyDir
	// removeFile always returns "remove failed" (not ErrNotExist), so material.json is also logged.
	require.Len(t, removedPaths, 3)
	assert.Contains(t, removedPaths[0], "meta.json")
	assert.Contains(t, removedPaths[1], "material.json")
	assert.NotContains(t, removedPaths[2], ".json")
}

// failCloseWriter wraps a WriteCloser and returns an error on Close.
type failCloseWriter struct {
	io.WriteCloser
	closeErr error
}

func (f *failCloseWriter) Close() error {
	_ = f.WriteCloser.Close()
	return f.closeErr
}

func TestWriteJSON_closeFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	closeErr := errors.New("close failed")
	orig := s.openFile
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		wc, err := orig(name, flag, perm)
		if err != nil {
			return nil, err
		}
		return &failCloseWriter{WriteCloser: wc, closeErr: closeErr}, nil
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.ErrorIs(t, err, closeErr)
}
