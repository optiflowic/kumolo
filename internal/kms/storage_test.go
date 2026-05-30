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

func TestGetKeyMaterial_materialMissing(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	// Simulate material.json missing by returning os.ErrNotExist from readAll.
	s.readAll = func(io.Reader) ([]byte, error) { return nil, os.ErrNotExist }
	_, err = s.GetKeyMaterial(meta.KeyID)
	require.ErrorIs(t, err, ErrKeyMaterialNotFound)
}

func TestGetKeyMaterial_statFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	statErr := errors.New("stat failed")
	s.statFn = func(string) (os.FileInfo, error) { return nil, statErr }
	_, err = s.GetKeyMaterial(meta.KeyID)
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

// ---- GetKeyMaterial: key not found -----------------------------------------

func TestGetKeyMaterial_keyNotFound(t *testing.T) {
	s, _ := newTestStorage(t)
	_, err := s.GetKeyMaterial("00000000-0000-0000-0000-000000000000")
	require.ErrorIs(t, err, ErrKeyNotFound)
}

// ---- CreateKey material rand failure + removeFile failure ------------------

func TestCreateKey_materialKeyRandReadFailure_cleanupFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	calls := 0
	orig := s.randRead
	s.randRead = func(b []byte) (int, error) {
		calls++
		if calls == 2 { // key bytes generation
			return 0, errors.New("rand failed")
		}
		return orig(b)
	}
	s.removeFile = func(string) error { return errors.New("remove failed") }
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.Error(t, err)
}

func TestCreateKey_materialIDRandReadFailure_cleanupFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	calls := 0
	orig := s.randRead
	s.randRead = func(b []byte) (int, error) {
		calls++
		if calls == 3 { // material ID bytes generation
			return 0, errors.New("rand failed")
		}
		return orig(b)
	}
	s.removeFile = func(string) error { return errors.New("remove failed") }
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"})
	require.Error(t, err)
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

// ---- alias storage ----------------------------------------------------------

func TestCreateAlias_and_basic_operations(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)

	require.NoError(t, s.CreateAlias("alias/my-key", meta.KeyID))

	// ResolveAlias returns the key ID.
	resolved, err := s.ResolveAlias("alias/my-key")
	require.NoError(t, err)
	assert.Equal(t, meta.KeyID, resolved)

	// ListAliases returns the alias.
	aliases, err := s.ListAliases("")
	require.NoError(t, err)
	require.Len(t, aliases, 1)
	assert.Equal(t, "alias/my-key", aliases[0].AliasName)

	// UpdateAlias changes the target.
	meta2, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	require.NoError(t, s.UpdateAlias("alias/my-key", meta2.KeyID))

	resolved, err = s.ResolveAlias("alias/my-key")
	require.NoError(t, err)
	assert.Equal(t, meta2.KeyID, resolved)

	// DeleteAlias removes the alias.
	require.NoError(t, s.DeleteAlias("alias/my-key"))
	_, err = s.ResolveAlias("alias/my-key")
	require.ErrorIs(t, err, ErrAliasNotFound)
}

func TestListAliases_filterByKeyID(t *testing.T) {
	s, _ := newTestStorage(t)
	key1, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	key2, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	require.NoError(t, s.CreateAlias("alias/for-key1", key1.KeyID))
	require.NoError(t, s.CreateAlias("alias/for-key2", key2.KeyID))

	aliases, err := s.ListAliases(key1.KeyID)
	require.NoError(t, err)
	require.Len(t, aliases, 1)
	assert.Equal(t, "alias/for-key1", aliases[0].AliasName)
}

func TestCreateAlias_countAliasesListFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)

	listErr := errors.New("list failed")
	s.listDirFn = func(string) ([]os.DirEntry, error) { return nil, listErr }
	err = s.CreateAlias("alias/my-key", meta.KeyID)
	require.ErrorIs(t, err, listErr)
}

func TestListAliases_listDirFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	listErr := errors.New("list failed")
	s.listDirFn = func(string) ([]os.DirEntry, error) { return nil, listErr }
	_, err := s.ListAliases("")
	require.ErrorIs(t, err, listErr)
}

func TestListAliases_nonJsonFileSkipped(t *testing.T) {
	s, dir := newTestStorage(t)
	// A file without .json extension in aliases/ must be silently skipped.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kms", "aliases", "notjson"), nil, 0o600))
	aliases, err := s.ListAliases("")
	require.NoError(t, err)
	assert.Empty(t, aliases)
}

func TestListAliases_unreadableFileSkipped(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	require.NoError(t, s.CreateAlias("alias/my-key", meta.KeyID))

	// Inject a readAll failure so the alias file is unreadable.
	s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read failed") }
	aliases, err := s.ListAliases("")
	require.NoError(t, err)
	assert.Empty(t, aliases) // skipped with a warning
}

func TestResolveAlias_notFound(t *testing.T) {
	s, _ := newTestStorage(t)
	_, err := s.ResolveAlias("alias/nonexistent")
	require.ErrorIs(t, err, ErrAliasNotFound)
}

func TestResolveAlias_readFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	require.NoError(t, s.CreateAlias("alias/my-key", meta.KeyID))

	s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read failed") }
	_, err = s.ResolveAlias("alias/my-key")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrAliasNotFound)
}

func TestUpdateAlias_notFound(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	err = s.UpdateAlias("alias/nonexistent", meta.KeyID)
	require.ErrorIs(t, err, ErrAliasNotFound)
}

func TestUpdateAlias_readFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	require.NoError(t, s.CreateAlias("alias/my-key", meta.KeyID))

	s.readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read failed") }
	err = s.UpdateAlias("alias/my-key", meta.KeyID)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrAliasNotFound)
}

func TestDeleteAlias_notFound(t *testing.T) {
	s, _ := newTestStorage(t)
	err := s.DeleteAlias("alias/nonexistent")
	require.ErrorIs(t, err, ErrAliasNotFound)
}

func TestCreateAlias_alreadyExists(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	require.NoError(t, s.CreateAlias("alias/my-key", meta.KeyID))
	err = s.CreateAlias("alias/my-key", meta.KeyID)
	require.ErrorIs(t, err, ErrAliasAlreadyExists)
}

func TestCreateAlias_targetKeyNotFound(t *testing.T) {
	s, _ := newTestStorage(t)
	err := s.CreateAlias("alias/my-key", "00000000-0000-0000-0000-000000000000")
	require.ErrorIs(t, err, ErrKeyNotFound)
}

func TestUpdateAlias_targetKeyNotFound(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	require.NoError(t, s.CreateAlias("alias/my-key", meta.KeyID))
	err = s.UpdateAlias("alias/my-key", "00000000-0000-0000-0000-000000000000")
	require.ErrorIs(t, err, ErrKeyNotFound)
}

func TestAliasFilename_encoding(t *testing.T) {
	// aliasFilename must be reversible — slashes encoded so the result is a flat filename.
	name := "alias/my/nested/key"
	fn := aliasFilename(name)
	assert.NotContains(t, fn, "/", "slash must be percent-encoded")
	assert.Contains(t, fn, ".json")
}

func TestDeleteAlias_removeFileRaceCondition(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	require.NoError(t, s.CreateAlias("alias/my-key", meta.KeyID))

	// Simulate a race: stat sees the alias but removeFile gets os.ErrNotExist.
	s.removeFile = func(string) error { return os.ErrNotExist }
	err = s.DeleteAlias("alias/my-key")
	require.ErrorIs(t, err, ErrAliasNotFound)
}

func TestDeleteAlias_removeFileFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	require.NoError(t, s.CreateAlias("alias/my-key", meta.KeyID))

	wantErr := errors.New("remove failed")
	s.removeFile = func(string) error { return wantErr }
	err = s.DeleteAlias("alias/my-key")
	require.ErrorIs(t, err, wantErr)
}

func TestListAliases_dirEntrySkipped(t *testing.T) {
	s, dir := newTestStorage(t)
	// A subdirectory inside aliases/ must be silently skipped.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "kms", "aliases", "subdir"), 0o750))
	aliases, err := s.ListAliases("")
	require.NoError(t, err)
	assert.Empty(t, aliases)
}

func TestCountAliases_dirEntrySkipped(t *testing.T) {
	s, dir := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)

	// A subdirectory inside aliases/ must be silently skipped when counting.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "kms", "aliases", "subdir"), 0o750))
	// CreateAlias must still succeed (subdir doesn't count).
	require.NoError(t, s.CreateAlias("alias/my-key", meta.KeyID))
}

func TestCountAliases_nonJsonFileSkipped(t *testing.T) {
	s, dir := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)

	// A non-.json file in aliases/ must be silently skipped when counting.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kms", "aliases", "not.txt"), nil, 0o600))
	require.NoError(t, s.CreateAlias("alias/my-key", meta.KeyID))
}

func TestCountAliases_corruptFileErrors(t *testing.T) {
	s, dir := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)

	// A corrupt alias file must cause CreateAlias to fail so we don't undercount.
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, "kms", "aliases", "bad.json"), []byte("not json"), 0o600),
	)
	err = s.CreateAlias("alias/my-key", meta.KeyID)
	require.Error(t, err)
}

func TestCreateAlias_limitExceeded(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)

	orig := maxAliasesPerKey
	maxAliasesPerKey = 1
	t.Cleanup(func() { maxAliasesPerKey = orig })

	require.NoError(t, s.CreateAlias("alias/first", meta.KeyID))
	err = s.CreateAlias("alias/second", meta.KeyID)
	require.ErrorIs(t, err, ErrAliasLimitExceeded)
}

func TestNewStorage_aliasesDirFailure(t *testing.T) {
	dir := t.TempDir()
	kmsDir := filepath.Join(dir, "kms")
	require.NoError(t, os.MkdirAll(filepath.Join(kmsDir, "keys"), 0o750))
	// Block MkdirAll for aliases by placing a regular file there.
	require.NoError(t, os.WriteFile(filepath.Join(kmsDir, "aliases"), nil, 0o600))
	_, err := newStorage(dir, os.OpenRoot)
	require.Error(t, err)
}
