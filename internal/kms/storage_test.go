package kms

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testECKeyDER generates an ephemeral P-256 private key and returns its PKCS#8 DER.
func testECKeyDER(t *testing.T) []byte {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(k)
	require.NoError(t, err)
	return der
}

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

func TestGetKeyPolicy_statFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	statErr := errors.New("stat failed")
	s.statFn = func(string) (os.FileInfo, error) { return nil, statErr }
	_, err = s.GetKeyPolicy(meta.KeyID)
	require.ErrorIs(t, err, statErr)
}

func TestPutKeyPolicy_statFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	statErr := errors.New("stat failed")
	s.statFn = func(string) (os.FileInfo, error) { return nil, statErr }
	err = s.PutKeyPolicy(meta.KeyID, "{}")
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

func TestGetKeyMaterial_hmacSizeMismatch(t *testing.T) {
	s, dir := newTestStorage(t)
	meta, err := s.CreateKey(CreateKeyInput{KeySpec: "HMAC_256", KeyUsage: "GENERATE_VERIFY_MAC"})
	require.NoError(t, err)

	// Overwrite material.json with wrong-sized key bytes to simulate a key created
	// before the hmacKeySize fix (old code always allocated 32 bytes regardless of spec).
	wrongMat := KeyMaterial{KeyBytes: make([]byte, 16), KeyMaterialID: "legacy"}
	data, err := json.Marshal(wrongMat)
	require.NoError(t, err)
	matPath := filepath.Join(dir, "kms", "keys", meta.KeyID, "material.json")
	require.NoError(t, os.WriteFile(matPath, data, 0o600))

	_, err = s.GetKeyMaterial(meta.KeyID)
	require.ErrorIs(t, err, ErrKeyMaterialCorrupted)
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

	s.maxAliasesPerKey = 1

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

// ---- EnableKey ---------------------------------------------------------------

func newSymmetricKey(t *testing.T, s *Storage) string {
	t.Helper()
	meta, err := s.CreateKey(
		CreateKeyInput{KeySpec: "SYMMETRIC_DEFAULT", KeyUsage: "ENCRYPT_DECRYPT"},
	)
	require.NoError(t, err)
	return meta.KeyID
}

func putKeyIntoState(t *testing.T, s *Storage, keyID, state string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readKeyMeta(keyID)
	require.NoError(t, err)
	meta.KeyState = state
	meta.Enabled = state == "Enabled"
	require.NoError(t, s.writeJSON(filepath.Join("keys", keyID, "meta.json"), meta))
}

// ---- GetTags / TagResource / UntagResource -----------------------------------

func TestGetTags(t *testing.T) {
	t.Run("returns empty slice when no tags file exists", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)

		tags, err := s.GetTags(keyID)
		require.NoError(t, err)
		assert.Empty(t, tags)
	})

	t.Run("returns ErrKeyNotFound for unknown key", func(t *testing.T) {
		s, _ := newTestStorage(t)
		_, err := s.GetTags("00000000-0000-0000-0000-000000000000")
		require.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("readAll failure wrapped in error", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)

		// Seed a tags file so readAll is called on the tags.json path.
		require.NoError(t, s.TagResource(keyID, []TagEntry{{TagKey: "k", TagValue: "v"}}))

		readErr := errors.New("read failed")
		orig := s.readAll
		calls := 0
		s.readAll = func(r io.Reader) ([]byte, error) {
			calls++
			// keyExistsLocked uses statFn not readAll; first readAll call in GetTags is tags.json.
			if calls == 1 {
				return nil, readErr
			}
			return orig(r)
		}
		_, err := s.GetTags(keyID)
		require.ErrorContains(t, err, "read tags")
	})
}

func TestTagResource(t *testing.T) {
	t.Run("adds tags and reads them back sorted", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)

		err := s.TagResource(keyID, []TagEntry{
			{TagKey: "Z", TagValue: "last"},
			{TagKey: "A", TagValue: "first"},
		})
		require.NoError(t, err)

		tags, err := s.GetTags(keyID)
		require.NoError(t, err)
		require.Len(t, tags, 2)
		assert.Equal(t, "A", tags[0].TagKey)
		assert.Equal(t, "Z", tags[1].TagKey)
	})

	t.Run("overwrites existing tag value", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)

		require.NoError(t, s.TagResource(keyID, []TagEntry{{TagKey: "k", TagValue: "old"}}))
		require.NoError(t, s.TagResource(keyID, []TagEntry{{TagKey: "k", TagValue: "new"}}))

		tags, err := s.GetTags(keyID)
		require.NoError(t, err)
		require.Len(t, tags, 1)
		assert.Equal(t, "new", tags[0].TagValue)
	})

	t.Run("returns ErrKeyNotFound for unknown key", func(t *testing.T) {
		s, _ := newTestStorage(t)
		err := s.TagResource("00000000-0000-0000-0000-000000000000",
			[]TagEntry{{TagKey: "k", TagValue: "v"}})
		require.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("returns ErrInvalidKeyState for PendingDeletion key", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		putKeyIntoState(t, s, keyID, "PendingDeletion")

		err := s.TagResource(keyID, []TagEntry{{TagKey: "k", TagValue: "v"}})
		require.ErrorIs(t, err, ErrInvalidKeyState)
	})

	t.Run("returns ErrTagLimitExceeded when exceeding 50 tags", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)

		// Add exactly 50 tags.
		tags := make([]TagEntry, 50)
		for i := range 50 {
			tags[i] = TagEntry{
				TagKey:   "key" + string(rune('A'+i%26)) + string(rune('0'+i/26)),
				TagValue: "v",
			}
		}
		require.NoError(t, s.TagResource(keyID, tags))

		// Adding one more must fail.
		err := s.TagResource(keyID, []TagEntry{{TagKey: "extra", TagValue: "v"}})
		require.ErrorIs(t, err, ErrTagLimitExceeded)
	})

	t.Run("read existing tags failure is wrapped", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		require.NoError(t, s.TagResource(keyID, []TagEntry{{TagKey: "k", TagValue: "v"}}))

		readErr := errors.New("io error")
		orig := s.readAll
		calls := 0
		s.readAll = func(r io.Reader) ([]byte, error) {
			calls++
			if calls == 2 { // second call: tags.json
				return nil, readErr
			}
			return orig(r)
		}
		err := s.TagResource(keyID, []TagEntry{{TagKey: "k2", TagValue: "v"}})
		require.ErrorContains(t, err, "read existing tags")
	})
}

func TestUntagResource(t *testing.T) {
	t.Run("removes specified tag keys", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		require.NoError(t, s.TagResource(keyID, []TagEntry{
			{TagKey: "A", TagValue: "1"},
			{TagKey: "B", TagValue: "2"},
		}))

		require.NoError(t, s.UntagResource(keyID, []string{"A"}))

		tags, err := s.GetTags(keyID)
		require.NoError(t, err)
		require.Len(t, tags, 1)
		assert.Equal(t, "B", tags[0].TagKey)
	})

	t.Run("silently ignores non-existent tag key", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)

		err := s.UntagResource(keyID, []string{"nonexistent"})
		require.NoError(t, err)
	})

	t.Run("returns ErrKeyNotFound for unknown key", func(t *testing.T) {
		s, _ := newTestStorage(t)
		err := s.UntagResource("00000000-0000-0000-0000-000000000000", []string{"k"})
		require.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("returns ErrInvalidKeyState for PendingDeletion key", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		putKeyIntoState(t, s, keyID, "PendingDeletion")

		err := s.UntagResource(keyID, []string{"k"})
		require.ErrorIs(t, err, ErrInvalidKeyState)
	})

	t.Run("read existing tags failure is wrapped", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		require.NoError(t, s.TagResource(keyID, []TagEntry{{TagKey: "k", TagValue: "v"}}))

		readErr := errors.New("io error")
		orig := s.readAll
		calls := 0
		s.readAll = func(r io.Reader) ([]byte, error) {
			calls++
			if calls == 2 { // second call: tags.json
				return nil, readErr
			}
			return orig(r)
		}
		err := s.UntagResource(keyID, []string{"k"})
		require.ErrorContains(t, err, "read existing tags")
	})
}

func TestEnableKey(t *testing.T) {
	t.Run("Disabled→Enabled", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		putKeyIntoState(t, s, keyID, "Disabled")

		require.NoError(t, s.EnableKey(keyID))

		meta, err := s.GetKeyMetadata(keyID)
		require.NoError(t, err)
		assert.Equal(t, "Enabled", meta.KeyState)
		assert.True(t, meta.Enabled)
	})

	t.Run("already Enabled is no-op", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		require.NoError(t, s.EnableKey(keyID))

		meta, err := s.GetKeyMetadata(keyID)
		require.NoError(t, err)
		assert.Equal(t, "Enabled", meta.KeyState)
	})

	t.Run("key not found returns ErrKeyNotFound", func(t *testing.T) {
		s, _ := newTestStorage(t)
		err := s.EnableKey("00000000-0000-0000-0000-000000000000")
		require.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("PendingDeletion returns ErrInvalidKeyState", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		_, err := s.ScheduleKeyDeletion(keyID, 30)
		require.NoError(t, err)

		err = s.EnableKey(keyID)
		require.ErrorIs(t, err, ErrInvalidKeyState)
	})
}

// ---- DisableKey --------------------------------------------------------------

func TestDisableKey(t *testing.T) {
	t.Run("Enabled→Disabled", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)

		require.NoError(t, s.DisableKey(keyID))

		meta, err := s.GetKeyMetadata(keyID)
		require.NoError(t, err)
		assert.Equal(t, "Disabled", meta.KeyState)
		assert.False(t, meta.Enabled)
	})

	t.Run("already Disabled is no-op", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		putKeyIntoState(t, s, keyID, "Disabled")
		require.NoError(t, s.DisableKey(keyID))

		meta, err := s.GetKeyMetadata(keyID)
		require.NoError(t, err)
		assert.Equal(t, "Disabled", meta.KeyState)
	})

	t.Run("key not found returns ErrKeyNotFound", func(t *testing.T) {
		s, _ := newTestStorage(t)
		err := s.DisableKey("00000000-0000-0000-0000-000000000000")
		require.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("PendingDeletion returns ErrInvalidKeyState", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		_, err := s.ScheduleKeyDeletion(keyID, 30)
		require.NoError(t, err)

		err = s.DisableKey(keyID)
		require.ErrorIs(t, err, ErrInvalidKeyState)
	})
}

// ---- ScheduleKeyDeletion -----------------------------------------------------

func TestScheduleKeyDeletion(t *testing.T) {
	t.Run("sets PendingDeletion and DeletionDate", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)

		meta, err := s.ScheduleKeyDeletion(keyID, 7)
		require.NoError(t, err)
		assert.Equal(t, "PendingDeletion", meta.KeyState)
		assert.False(t, meta.Enabled)
		assert.NotNil(t, meta.DeletionDate)
	})

	t.Run("key not found returns ErrKeyNotFound", func(t *testing.T) {
		s, _ := newTestStorage(t)
		_, err := s.ScheduleKeyDeletion("00000000-0000-0000-0000-000000000000", 30)
		require.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("already PendingDeletion returns ErrInvalidKeyState", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		_, err := s.ScheduleKeyDeletion(keyID, 30)
		require.NoError(t, err)

		_, err = s.ScheduleKeyDeletion(keyID, 30)
		require.ErrorIs(t, err, ErrInvalidKeyState)
	})
}

// ---- CancelKeyDeletion -------------------------------------------------------

func TestCancelKeyDeletion(t *testing.T) {
	t.Run("PendingDeletion→Disabled and clears DeletionDate", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		_, err := s.ScheduleKeyDeletion(keyID, 30)
		require.NoError(t, err)

		meta, err := s.CancelKeyDeletion(keyID)
		require.NoError(t, err)
		assert.Equal(t, "Disabled", meta.KeyState)
		assert.False(t, meta.Enabled)
		assert.Nil(t, meta.DeletionDate)
	})

	t.Run("key not found returns ErrKeyNotFound", func(t *testing.T) {
		s, _ := newTestStorage(t)
		_, err := s.CancelKeyDeletion("00000000-0000-0000-0000-000000000000")
		require.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("Enabled key returns ErrInvalidKeyState", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		_, err := s.CancelKeyDeletion(keyID)
		require.ErrorIs(t, err, ErrInvalidKeyState)
	})
}

// ---- EnableKeyRotation -------------------------------------------------------

func TestEnableKeyRotation(t *testing.T) {
	t.Run("success on Enabled SYMMETRIC_DEFAULT key", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)

		require.NoError(t, s.EnableKeyRotation(keyID, 365))

		_, cfg, err := s.GetKeyRotationStatus(keyID)
		require.NoError(t, err)
		assert.True(t, cfg.Enabled)
		assert.Equal(t, 365, cfg.RotationPeriodInDays)
		assert.NotZero(t, cfg.NextRotationDate)
	})

	t.Run("key not found returns ErrKeyNotFound", func(t *testing.T) {
		s, _ := newTestStorage(t)
		err := s.EnableKeyRotation("00000000-0000-0000-0000-000000000000", 365)
		require.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("PendingDeletion returns ErrInvalidKeyState", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		_, err := s.ScheduleKeyDeletion(keyID, 30)
		require.NoError(t, err)

		err = s.EnableKeyRotation(keyID, 365)
		require.ErrorIs(t, err, ErrInvalidKeyState)
	})

	t.Run("Disabled key returns ErrKeyDisabled", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		putKeyIntoState(t, s, keyID, "Disabled")

		err := s.EnableKeyRotation(keyID, 365)
		require.ErrorIs(t, err, ErrKeyDisabled)
	})

	t.Run("non-SYMMETRIC key returns ErrUnsupportedOp", func(t *testing.T) {
		s, _ := newTestStorage(t)
		meta, err := s.CreateKey(CreateKeyInput{KeySpec: "RSA_2048", KeyUsage: "SIGN_VERIFY"})
		require.NoError(t, err)

		err = s.EnableKeyRotation(meta.KeyID, 365)
		require.ErrorIs(t, err, ErrUnsupportedOp)
	})
}

// ---- DisableKeyRotation ------------------------------------------------------

func TestDisableKeyRotation(t *testing.T) {
	t.Run("success on Enabled SYMMETRIC_DEFAULT key", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		require.NoError(t, s.EnableKeyRotation(keyID, 365))

		require.NoError(t, s.DisableKeyRotation(keyID))

		_, cfg, err := s.GetKeyRotationStatus(keyID)
		require.NoError(t, err)
		assert.False(t, cfg.Enabled)
	})

	t.Run("key not found returns ErrKeyNotFound", func(t *testing.T) {
		s, _ := newTestStorage(t)
		err := s.DisableKeyRotation("00000000-0000-0000-0000-000000000000")
		require.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("PendingDeletion returns ErrInvalidKeyState", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		_, err := s.ScheduleKeyDeletion(keyID, 30)
		require.NoError(t, err)

		err = s.DisableKeyRotation(keyID)
		require.ErrorIs(t, err, ErrInvalidKeyState)
	})

	t.Run("Disabled key returns ErrKeyDisabled", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		putKeyIntoState(t, s, keyID, "Disabled")

		err := s.DisableKeyRotation(keyID)
		require.ErrorIs(t, err, ErrKeyDisabled)
	})

	t.Run("non-SYMMETRIC key returns ErrUnsupportedOp", func(t *testing.T) {
		s, _ := newTestStorage(t)
		meta, err := s.CreateKey(CreateKeyInput{KeySpec: "RSA_2048", KeyUsage: "SIGN_VERIFY"})
		require.NoError(t, err)

		err = s.DisableKeyRotation(meta.KeyID)
		require.ErrorIs(t, err, ErrUnsupportedOp)
	})
}

// ---- GetKeyRotationStatus ----------------------------------------------------

func TestGetKeyRotationStatus(t *testing.T) {
	t.Run("no rotation config returns empty cfg", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)

		meta, cfg, err := s.GetKeyRotationStatus(keyID)
		require.NoError(t, err)
		assert.Equal(t, keyID, meta.KeyID)
		assert.False(t, cfg.Enabled)
	})

	t.Run("returns rotation config after EnableKeyRotation", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		require.NoError(t, s.EnableKeyRotation(keyID, 180))

		_, cfg, err := s.GetKeyRotationStatus(keyID)
		require.NoError(t, err)
		assert.True(t, cfg.Enabled)
		assert.Equal(t, 180, cfg.RotationPeriodInDays)
	})

	t.Run("key not found returns ErrKeyNotFound", func(t *testing.T) {
		s, _ := newTestStorage(t)
		_, _, err := s.GetKeyRotationStatus("00000000-0000-0000-0000-000000000000")
		require.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("PendingDeletion returns ErrInvalidKeyState", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		_, err := s.ScheduleKeyDeletion(keyID, 30)
		require.NoError(t, err)

		_, _, err = s.GetKeyRotationStatus(keyID)
		require.ErrorIs(t, err, ErrInvalidKeyState)
	})

	t.Run("non-SYMMETRIC key returns ErrUnsupportedOp", func(t *testing.T) {
		s, _ := newTestStorage(t)
		meta, err := s.CreateKey(CreateKeyInput{KeySpec: "RSA_2048", KeyUsage: "SIGN_VERIFY"})
		require.NoError(t, err)

		_, _, err = s.GetKeyRotationStatus(meta.KeyID)
		require.ErrorIs(t, err, ErrUnsupportedOp)
	})

	t.Run("rotation.json read failure returns wrapped error", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		require.NoError(t, s.EnableKeyRotation(keyID, 365))

		readErr := errors.New("read failed")
		orig := s.readAll
		calls := 0
		s.readAll = func(r io.Reader) ([]byte, error) {
			calls++
			if calls == 2 { // second readAll: rotation.json (first is meta.json)
				return nil, readErr
			}
			return orig(r)
		}

		_, _, err := s.GetKeyRotationStatus(keyID)
		require.ErrorContains(t, err, "read rotation config")
	})
}

// ---- CreateKey: asymmetric key generation ------------------------------------

func TestCreateKey_asymmetricKey_storesMaterial(t *testing.T) {
	s, _ := newTestStorage(t)
	der := testECKeyDER(t)
	s.generateKeyPairFn = func(string) ([]byte, error) { return der, nil }
	meta, err := s.CreateKey(CreateKeyInput{KeySpec: "ECC_NIST_P256", KeyUsage: "SIGN_VERIFY"})
	require.NoError(t, err)

	mat, err := s.GetKeyMaterial(meta.KeyID)
	require.NoError(t, err)
	assert.Equal(t, der, mat.PrivateKeyDER)
	assert.NotEmpty(t, mat.KeyMaterialID)
}

func TestCreateKey_asymmetricUnsupportedSpec_noMaterial(t *testing.T) {
	s, _ := newTestStorage(t)
	// ECC_SECG_P256K1 returns nil, nil from generateKeyPair — no material.json is written.
	meta, err := s.CreateKey(CreateKeyInput{KeySpec: "ECC_SECG_P256K1", KeyUsage: "SIGN_VERIFY"})
	require.NoError(t, err)

	_, err = s.GetKeyMaterial(meta.KeyID)
	require.ErrorIs(t, err, ErrKeyMaterialNotFound)
}

func TestCreateKey_asymmetricKeyPairGenerationFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	genErr := errors.New("gen failed")
	s.generateKeyPairFn = func(string) ([]byte, error) { return nil, genErr }
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "ECC_NIST_P256", KeyUsage: "SIGN_VERIFY"})
	require.ErrorIs(t, err, genErr)
}

func TestCreateKey_asymmetricKeyPairGenerationFailure_cleanupFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	genErr := errors.New("gen failed")
	s.generateKeyPairFn = func(string) ([]byte, error) { return nil, genErr }
	var removedPaths []string
	s.removeFile = func(name string) error {
		removedPaths = append(removedPaths, name)
		return errors.New("remove failed")
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "ECC_NIST_P256", KeyUsage: "SIGN_VERIFY"})
	require.ErrorIs(t, err, genErr)
	require.Len(t, removedPaths, 2)
	assert.Contains(t, removedPaths[0], "meta.json")
	assert.NotContains(t, removedPaths[1], ".json")
}

func TestCreateKey_asymmetricMaterialIDRandReadFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	der := testECKeyDER(t)
	s.generateKeyPairFn = func(string) ([]byte, error) { return der, nil }
	calls := 0
	orig := s.randRead
	s.randRead = func(b []byte) (int, error) {
		calls++
		if calls == 2 { // second call: material ID bytes in the asymmetric branch
			return 0, errors.New("rand failed")
		}
		return orig(b)
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "ECC_NIST_P256", KeyUsage: "SIGN_VERIFY"})
	require.Error(t, err)
}

func TestCreateKey_asymmetricMaterialIDRandReadFailure_cleanupFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	der := testECKeyDER(t)
	s.generateKeyPairFn = func(string) ([]byte, error) { return der, nil }
	calls := 0
	orig := s.randRead
	s.randRead = func(b []byte) (int, error) {
		calls++
		if calls == 2 {
			return 0, errors.New("rand failed")
		}
		return orig(b)
	}
	s.removeFile = func(string) error { return errors.New("remove failed") }
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "ECC_NIST_P256", KeyUsage: "SIGN_VERIFY"})
	require.Error(t, err)
}

func TestCreateKey_asymmetricMaterialWriteFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	der := testECKeyDER(t)
	s.generateKeyPairFn = func(string) ([]byte, error) { return der, nil }
	wantErr := errors.New("open failed")
	calls := 0
	orig := s.openFile
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		calls++
		if calls == 2 { // material.json is the second write for asymmetric keys
			return nil, wantErr
		}
		return orig(name, flag, perm)
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "ECC_NIST_P256", KeyUsage: "SIGN_VERIFY"})
	require.ErrorIs(t, err, wantErr)
}

func TestCreateKey_asymmetricMaterialWriteFailure_cleanupFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	der := testECKeyDER(t)
	s.generateKeyPairFn = func(string) ([]byte, error) { return der, nil }
	wantErr := errors.New("open failed")
	calls := 0
	orig := s.openFile
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		calls++
		if calls == 2 {
			return nil, wantErr
		}
		return orig(name, flag, perm)
	}
	var removedPaths []string
	s.removeFile = func(name string) error {
		removedPaths = append(removedPaths, name)
		return errors.New("remove failed")
	}
	_, err := s.CreateKey(CreateKeyInput{KeySpec: "ECC_NIST_P256", KeyUsage: "SIGN_VERIFY"})
	require.ErrorIs(t, err, wantErr)
	// meta.json, material.json, keyDir
	require.Len(t, removedPaths, 3)
	assert.Contains(t, removedPaths[0], "meta.json")
	assert.Contains(t, removedPaths[1], "material.json")
	assert.NotContains(t, removedPaths[2], ".json")
}

func TestEnsureAwsS3Key(t *testing.T) {
	t.Run("creates alias/aws/s3 on first call", func(t *testing.T) {
		s, _ := newTestStorage(t)
		arn, err := s.EnsureAwsS3Key()
		require.NoError(t, err)
		assert.Contains(t, arn, "arn:aws:kms:")
		assert.Contains(t, arn, ":key/")

		// Alias must now exist.
		keyID, err := s.ResolveAlias("alias/aws/s3")
		require.NoError(t, err)
		assert.NotEmpty(t, keyID)

		// Key must have KeyManager=AWS.
		meta, err := s.GetKeyMetadata(keyID)
		require.NoError(t, err)
		assert.Equal(t, "AWS", meta.KeyManager)
		assert.Equal(t, "SYMMETRIC_DEFAULT", meta.KeySpec)
	})

	t.Run("idempotent: returns same ARN on repeated calls", func(t *testing.T) {
		s, _ := newTestStorage(t)
		arn1, err := s.EnsureAwsS3Key()
		require.NoError(t, err)
		arn2, err := s.EnsureAwsS3Key()
		require.NoError(t, err)
		assert.Equal(t, arn1, arn2)
	})

	t.Run("ResolveAlias non-ErrAliasNotFound error is propagated", func(t *testing.T) {
		s, dir := newTestStorage(t)
		// Write a corrupt alias file so ResolveAlias returns a JSON parse error
		// rather than ErrAliasNotFound; exercises the error propagation branch.
		aliasFile := filepath.Join(dir, "kms", "aliases", "alias%2Faws%2Fs3.json")
		require.NoError(t, os.WriteFile(aliasFile, []byte("not-json"), 0o600))
		_, err := s.EnsureAwsS3Key()
		require.Error(t, err)
		assert.ErrorContains(t, err, "resolve alias/aws/s3")
	})

	t.Run("CreateKey failure is propagated", func(t *testing.T) {
		s, _ := newTestStorage(t)
		wantErr := errors.New("rand read failure")
		s.randRead = func([]byte) (int, error) { return 0, wantErr }
		_, err := s.EnsureAwsS3Key()
		require.Error(t, err)
		assert.ErrorContains(t, err, "create aws/s3 managed key")
	})

	t.Run("CreateAlias unexpected error is propagated", func(t *testing.T) {
		s, _ := newTestStorage(t)
		origListDir := s.listDirFn
		s.listDirFn = func(name string) ([]os.DirEntry, error) {
			if name == "aliases" {
				return nil, errors.New("simulated listDir failure")
			}
			return origListDir(name)
		}
		_, err := s.EnsureAwsS3Key()
		require.Error(t, err)
		assert.ErrorContains(t, err, "create alias/aws/s3")
	})

	t.Run("CreateAlias ErrAliasAlreadyExists race: ResolveAlias fails", func(t *testing.T) {
		s, _ := newTestStorage(t)
		origStat := s.statFn
		s.statFn = func(name string) (os.FileInfo, error) {
			if name == aliasPath("alias/aws/s3") {
				return nil, nil // simulate "file already exists" inside CreateAlias
			}
			return origStat(name)
		}
		_, err := s.EnsureAwsS3Key()
		require.Error(t, err)
		assert.ErrorContains(t, err, "resolve alias/aws/s3 after race")
	})

	t.Run("alias exists but key metadata missing returns wrapped error", func(t *testing.T) {
		s, dir := newTestStorage(t)
		_, err := s.EnsureAwsS3Key()
		require.NoError(t, err)
		keyID, err := s.ResolveAlias("alias/aws/s3")
		require.NoError(t, err)
		require.NoError(t, os.Remove(filepath.Join(dir, "kms", "keys", keyID, "meta.json")))
		_, err = s.EnsureAwsS3Key()
		require.Error(t, err)
		assert.ErrorContains(t, err, "read alias/aws/s3 metadata")
	})

	t.Run("CreateAlias ErrAliasAlreadyExists race: GetKeyMetadata fails", func(t *testing.T) {
		s, dir := newTestStorage(t)
		// Alias points to a nonexistent key; GetKeyMetadata will return ErrKeyNotFound.
		entry := AliasEntry{
			AliasName:   "alias/aws/s3",
			AliasArn:    aliasARN("alias/aws/s3"),
			TargetKeyId: "00000000-0000-0000-0000-000000000000",
		}
		aliasJSON, err := json.Marshal(entry)
		require.NoError(t, err)
		aliasFile := filepath.Join(dir, "kms", aliasPath("alias/aws/s3"))
		origStat := s.statFn
		s.statFn = func(name string) (os.FileInfo, error) {
			if name == aliasPath("alias/aws/s3") {
				require.NoError(t, os.WriteFile(aliasFile, aliasJSON, 0o600))
				return nil, nil
			}
			return origStat(name)
		}
		_, err = s.EnsureAwsS3Key()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("CreateAlias ErrAliasAlreadyExists race: resolves to winner ARN", func(t *testing.T) {
		s, dir := newTestStorage(t)
		// Pre-create the "race winner" key K1.
		k1, err := s.CreateKey(CreateKeyInput{
			KeySpec:  keySpecSymmetricDefault,
			KeyUsage: "ENCRYPT_DECRYPT",
			Origin:   "AWS_KMS",
		})
		require.NoError(t, err)
		entry := AliasEntry{
			AliasName:   "alias/aws/s3",
			AliasArn:    aliasARN("alias/aws/s3"),
			TargetKeyId: k1.KeyID,
		}
		aliasJSON, err := json.Marshal(entry)
		require.NoError(t, err)
		aliasFile := filepath.Join(dir, "kms", aliasPath("alias/aws/s3"))
		origStat := s.statFn
		s.statFn = func(name string) (os.FileInfo, error) {
			if name == aliasPath("alias/aws/s3") {
				require.NoError(t, os.WriteFile(aliasFile, aliasJSON, 0o600))
				return nil, nil
			}
			return origStat(name)
		}
		arn, err := s.EnsureAwsS3Key()
		require.NoError(t, err)
		assert.Equal(t, k1.Arn, arn)
	})
}

func TestResolveKeyForEncryption(t *testing.T) {
	newStorageWithKey := func(t *testing.T) (*Storage, string) {
		t.Helper()
		s, _ := newTestStorage(t)
		meta, err := s.CreateKey(CreateKeyInput{
			KeySpec:  keySpecSymmetricDefault,
			KeyUsage: "ENCRYPT_DECRYPT",
			Origin:   "AWS_KMS",
		})
		require.NoError(t, err)
		return s, meta.Arn
	}

	t.Run("empty keyRef auto-creates alias/aws/s3 and returns ARN", func(t *testing.T) {
		s, _ := newTestStorage(t)
		arn, err := s.ResolveKeyForEncryption("")
		require.NoError(t, err)
		assert.Contains(t, arn, ":key/")
	})

	t.Run("empty keyRef EnsureAwsS3Key failure propagates error", func(t *testing.T) {
		s, _ := newTestStorage(t)
		wantErr := errors.New("mkdir failed")
		s.mkdirFn = func(string, os.FileMode) error { return wantErr }
		_, err := s.ResolveKeyForEncryption("")
		assert.ErrorIs(t, err, wantErr)
	})

	t.Run("empty keyRef disabled managed key returns ErrKeyDisabled", func(t *testing.T) {
		s, _ := newTestStorage(t)
		arn, err := s.ResolveKeyForEncryption("")
		require.NoError(t, err)
		keyID := arn[len("arn:aws:kms:us-east-1:000000000000:key/"):]
		require.NoError(t, s.DisableKey(keyID))
		_, err = s.ResolveKeyForEncryption("")
		assert.ErrorIs(t, err, ErrKeyDisabled)
	})

	t.Run(
		"empty keyRef pending-deletion managed key returns ErrKeyPendingDeletion",
		func(t *testing.T) {
			s, _ := newTestStorage(t)
			arn, err := s.ResolveKeyForEncryption("")
			require.NoError(t, err)
			keyID := arn[len("arn:aws:kms:us-east-1:000000000000:key/"):]
			_, err = s.ScheduleKeyDeletion(keyID, 7)
			require.NoError(t, err)
			_, err = s.ResolveKeyForEncryption("")
			assert.ErrorIs(t, err, ErrKeyPendingDeletion)
		},
	)

	t.Run("alias/aws/s3 auto-creates managed key", func(t *testing.T) {
		s, _ := newTestStorage(t)
		arn, err := s.ResolveKeyForEncryption("alias/aws/s3")
		require.NoError(t, err)
		assert.Contains(t, arn, ":key/")
	})

	t.Run("alias/aws/s3 disabled managed key returns ErrKeyDisabled", func(t *testing.T) {
		s, _ := newTestStorage(t)
		arn, err := s.ResolveKeyForEncryption("alias/aws/s3")
		require.NoError(t, err)
		keyID := arn[len("arn:aws:kms:us-east-1:000000000000:key/"):]
		require.NoError(t, s.DisableKey(keyID))
		_, err = s.ResolveKeyForEncryption("alias/aws/s3")
		assert.ErrorIs(t, err, ErrKeyDisabled)
	})

	t.Run(
		"alias/aws/s3 pending-deletion managed key returns ErrKeyPendingDeletion",
		func(t *testing.T) {
			s, _ := newTestStorage(t)
			arn, err := s.ResolveKeyForEncryption("alias/aws/s3")
			require.NoError(t, err)
			keyID := arn[len("arn:aws:kms:us-east-1:000000000000:key/"):]
			_, err = s.ScheduleKeyDeletion(keyID, 7)
			require.NoError(t, err)
			_, err = s.ResolveKeyForEncryption("alias/aws/s3")
			assert.ErrorIs(t, err, ErrKeyPendingDeletion)
		},
	)

	t.Run("resolves plain key ID to ARN", func(t *testing.T) {
		s, wantARN := newStorageWithKey(t)
		// Extract key ID from ARN: arn:...:key/<id>
		keyID := wantARN[len("arn:aws:kms:us-east-1:000000000000:key/"):]
		arn, err := s.ResolveKeyForEncryption(keyID)
		require.NoError(t, err)
		assert.Equal(t, wantARN, arn)
	})

	t.Run("resolves key ARN", func(t *testing.T) {
		s, wantARN := newStorageWithKey(t)
		arn, err := s.ResolveKeyForEncryption(wantARN)
		require.NoError(t, err)
		assert.Equal(t, wantARN, arn)
	})

	t.Run("resolves alias name to key ARN", func(t *testing.T) {
		s, wantARN := newStorageWithKey(t)
		keyID := wantARN[len("arn:aws:kms:us-east-1:000000000000:key/"):]
		err := s.CreateAlias("alias/mykey", keyID)
		require.NoError(t, err)
		arn, err := s.ResolveKeyForEncryption("alias/mykey")
		require.NoError(t, err)
		assert.Equal(t, wantARN, arn)
	})

	t.Run("disabled key returns ErrKeyDisabled", func(t *testing.T) {
		s, wantARN := newStorageWithKey(t)
		keyID := wantARN[len("arn:aws:kms:us-east-1:000000000000:key/"):]
		require.NoError(t, s.DisableKey(keyID))
		_, err := s.ResolveKeyForEncryption(keyID)
		assert.ErrorIs(t, err, ErrKeyDisabled)
	})

	t.Run("pending deletion key returns ErrKeyPendingDeletion", func(t *testing.T) {
		s, wantARN := newStorageWithKey(t)
		keyID := wantARN[len("arn:aws:kms:us-east-1:000000000000:key/"):]
		_, err := s.ScheduleKeyDeletion(keyID, 7)
		require.NoError(t, err)
		_, err = s.ResolveKeyForEncryption(keyID)
		assert.ErrorIs(t, err, ErrKeyPendingDeletion)
	})

	t.Run("nonexistent key ID returns ErrKeyNotFound", func(t *testing.T) {
		s, _ := newTestStorage(t)
		_, err := s.ResolveKeyForEncryption("00000000-0000-0000-0000-000000000000")
		assert.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("nonexistent alias returns ErrKeyNotFound", func(t *testing.T) {
		s, _ := newTestStorage(t)
		_, err := s.ResolveKeyForEncryption("alias/no-such-alias")
		assert.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("corrupt alias file causes propagated error", func(t *testing.T) {
		s, dir := newTestStorage(t)
		// Write corrupt JSON at a custom alias path; ResolveAlias will return a
		// non-ErrAliasNotFound error, exercising the error-propagation branch.
		aliasFile := filepath.Join(dir, "kms", "aliases", "alias%2Fbadkey.json")
		require.NoError(t, os.WriteFile(aliasFile, []byte("not-json"), 0o600))
		_, err := s.ResolveKeyForEncryption("alias/badkey")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("ARN-form alias/aws/s3 auto-creates managed key", func(t *testing.T) {
		s, _ := newTestStorage(t)
		arn, err := s.ResolveKeyForEncryption("arn:aws:kms:us-east-1:000000000000:alias/aws/s3")
		require.NoError(t, err)
		assert.Contains(t, arn, ":key/")
	})

	t.Run("ARN-form alias/aws/s3 EnsureAwsS3Key failure propagates error", func(t *testing.T) {
		s, _ := newTestStorage(t)
		wantErr := errors.New("mkdir failed")
		s.mkdirFn = func(string, os.FileMode) error { return wantErr }
		_, err := s.ResolveKeyForEncryption("arn:aws:kms:us-east-1:000000000000:alias/aws/s3")
		assert.ErrorIs(t, err, wantErr)
	})

	t.Run("ARN-form alias/aws/s3 disabled managed key returns ErrKeyDisabled", func(t *testing.T) {
		s, _ := newTestStorage(t)
		arn, err := s.ResolveKeyForEncryption("arn:aws:kms:us-east-1:000000000000:alias/aws/s3")
		require.NoError(t, err)
		keyID := arn[len("arn:aws:kms:us-east-1:000000000000:key/"):]
		require.NoError(t, s.DisableKey(keyID))
		_, err = s.ResolveKeyForEncryption("arn:aws:kms:us-east-1:000000000000:alias/aws/s3")
		assert.ErrorIs(t, err, ErrKeyDisabled)
	})

	t.Run(
		"ARN-form alias/aws/s3 pending-deletion managed key returns ErrKeyPendingDeletion",
		func(t *testing.T) {
			s, _ := newTestStorage(t)
			arn, err := s.ResolveKeyForEncryption("arn:aws:kms:us-east-1:000000000000:alias/aws/s3")
			require.NoError(t, err)
			keyID := arn[len("arn:aws:kms:us-east-1:000000000000:key/"):]
			_, err = s.ScheduleKeyDeletion(keyID, 7)
			require.NoError(t, err)
			_, err = s.ResolveKeyForEncryption("arn:aws:kms:us-east-1:000000000000:alias/aws/s3")
			assert.ErrorIs(t, err, ErrKeyPendingDeletion)
		},
	)

	t.Run("malformed key ARN returns ErrKeyNotFound", func(t *testing.T) {
		s, _ := newTestStorage(t)
		// ARN without :key/ segment — not an alias ref, fails resolveKeyID.
		_, err := s.ResolveKeyForEncryption("arn:aws:kms:us-east-1:000000000000:bogus/foo")
		assert.ErrorIs(t, err, ErrKeyNotFound)
	})
}

// ---- RotateKeyOnDemand error paths ------------------------------------------

func TestRotateKeyOnDemand_historyReadError(t *testing.T) {
	s, dir := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	histPath := filepath.Join(dir, "kms", "keys", keyID, "rotation_history.json")
	require.NoError(t, os.WriteFile(histPath, []byte("{bad json}"), 0o600))
	_, err := s.RotateKeyOnDemand(keyID)
	require.Error(t, err)
}

func TestRotateKeyOnDemand_materialReadError(t *testing.T) {
	s, dir := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	matPath := filepath.Join(dir, "kms", "keys", keyID, "material.json")
	require.NoError(t, os.Remove(matPath))
	_, err := s.RotateKeyOnDemand(keyID)
	require.Error(t, err)
}

func TestRotateKeyOnDemand_mkdirFnFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	s.mkdirFn = func(string, os.FileMode) error { return errors.New("mkdir fail") }
	_, err := s.RotateKeyOnDemand(keyID)
	require.Error(t, err)
}

func TestRotateKeyOnDemand_randReadFailures(t *testing.T) {
	t.Run("first randRead failure", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		callCount := 0
		orig := s.randRead
		s.randRead = func(b []byte) (int, error) {
			callCount++
			if callCount == 1 {
				return 0, errors.New("rand fail")
			}
			return orig(b)
		}
		_, err := s.RotateKeyOnDemand(keyID)
		require.Error(t, err)
	})

	t.Run("second randRead failure", func(t *testing.T) {
		s, _ := newTestStorage(t)
		keyID := newSymmetricKey(t, s)
		callCount := 0
		orig := s.randRead
		s.randRead = func(b []byte) (int, error) {
			callCount++
			if callCount == 2 {
				return 0, errors.New("rand fail")
			}
			return orig(b)
		}
		_, err := s.RotateKeyOnDemand(keyID)
		require.Error(t, err)
	})
}

// ---- ListKeyRotations error paths -------------------------------------------

func TestListKeyRotations_historyReadError(t *testing.T) {
	s, dir := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	histPath := filepath.Join(dir, "kms", "keys", keyID, "rotation_history.json")
	require.NoError(t, os.WriteFile(histPath, []byte("{bad json}"), 0o600))
	_, _, err := s.ListKeyRotations(keyID, 100, "")
	require.Error(t, err)
}

// ---- GetPreviousKeyMaterials error paths ------------------------------------

func TestGetPreviousKeyMaterials_keyNotFound(t *testing.T) {
	s, _ := newTestStorage(t)
	_, err := s.GetPreviousKeyMaterials("00000000-0000-0000-0000-000000000000")
	require.ErrorIs(t, err, ErrKeyNotFound)
}

func TestGetPreviousKeyMaterials_listDirFnFailure(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	_, err := s.RotateKeyOnDemand(keyID)
	require.NoError(t, err)
	orig := s.listDirFn
	expectedPath := filepath.Join("keys", keyID, "materials")
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == expectedPath {
			return nil, errors.New("list fail")
		}
		return orig(name)
	}
	_, err = s.GetPreviousKeyMaterials(keyID)
	require.Error(t, err)
}

func TestGetPreviousKeyMaterials_skipsDirEntry(t *testing.T) {
	s, dir := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	matDir := filepath.Join(dir, "kms", "keys", keyID, "materials")
	require.NoError(t, os.MkdirAll(matDir, 0o750))
	require.NoError(t, os.Mkdir(filepath.Join(matDir, "subdir"), 0o750))
	mats, err := s.GetPreviousKeyMaterials(keyID)
	require.NoError(t, err)
	assert.Empty(t, mats)
}

func TestGetPreviousKeyMaterials_skipsNonJsonFile(t *testing.T) {
	s, dir := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	matDir := filepath.Join(dir, "kms", "keys", keyID, "materials")
	require.NoError(t, os.MkdirAll(matDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(matDir, "readme.txt"), []byte("hello"), 0o600))
	mats, err := s.GetPreviousKeyMaterials(keyID)
	require.NoError(t, err)
	assert.Empty(t, mats)
}

func TestGetPreviousKeyMaterials_skipsUnreadableJson(t *testing.T) {
	s, dir := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	matDir := filepath.Join(dir, "kms", "keys", keyID, "materials")
	require.NoError(t, os.MkdirAll(matDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(matDir, "bad.json"), []byte("{corrupt}"), 0o600))
	mats, err := s.GetPreviousKeyMaterials(keyID)
	require.NoError(t, err)
	assert.Empty(t, mats)
}

// ---- Grant storage tests ----------------------------------------------------

func mustCreateGrantStorage(t *testing.T, s *Storage, keyID string) Grant {
	t.Helper()
	g, err := s.CreateGrant(keyID, CreateGrantInput{
		GranteePrincipal: "arn:aws:iam::000000000000:role/tester",
		Operations:       []string{"Decrypt"},
	})
	require.NoError(t, err)
	return g
}

func TestCreateGrant_basic(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	g, err := s.CreateGrant(keyID, CreateGrantInput{
		GranteePrincipal:  "arn:aws:iam::000000000000:role/tester",
		Operations:        []string{"Decrypt", "Encrypt"},
		RetiringPrincipal: "arn:aws:iam::000000000000:role/admin",
		Name:              "my-grant",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, g.GrantId)
	assert.NotEmpty(t, g.GrantToken)
	assert.Equal(t, "arn:aws:iam::000000000000:role/tester", g.GranteePrincipal)
	assert.Equal(t, "arn:aws:iam::000000000000:role/admin", g.RetiringPrincipal)
	assert.Equal(t, fixedAccount, g.IssuingAccount)
	assert.NotZero(t, g.CreationDate)
}

func TestCreateGrant_disabledKey(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	require.NoError(t, s.DisableKey(keyID))
	_, err := s.CreateGrant(keyID, CreateGrantInput{
		GranteePrincipal: "arn:aws:iam::000000000000:role/r",
		Operations:       []string{"Decrypt"},
	})
	require.ErrorIs(t, err, ErrKeyDisabled)
}

func TestCreateGrant_pendingDeletion(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	_, err := s.ScheduleKeyDeletion(keyID, 7)
	require.NoError(t, err)
	_, err = s.CreateGrant(keyID, CreateGrantInput{
		GranteePrincipal: "arn:aws:iam::000000000000:role/r",
		Operations:       []string{"Decrypt"},
	})
	require.ErrorIs(t, err, ErrInvalidKeyState)
}

func TestCreateGrant_limitExceeded(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)

	// Simulate grants directory at capacity using a fake DirEntry slice.
	fakeEntries := make([]os.DirEntry, maxGrantsPerKey)
	for i := range fakeEntries {
		fakeEntries[i] = fakeDirEntry{filename: "00000000-0000-0000-0000-000000000000.json"}
	}
	origListDir := s.listDirFn
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == grantsDir(keyID) {
			return fakeEntries, nil
		}
		return origListDir(name)
	}

	_, err := s.CreateGrant(keyID, CreateGrantInput{
		GranteePrincipal: "arn:aws:iam::000000000000:role/tester",
		Operations:       []string{"Decrypt"},
	})
	require.ErrorIs(t, err, ErrGrantLimitExceeded)
}

func TestCreateGrant_listDirError(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	origListDir := s.listDirFn
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == grantsDir(keyID) {
			return nil, errors.New("simulated list failure")
		}
		return origListDir(name)
	}
	_, err := s.CreateGrant(keyID, CreateGrantInput{
		GranteePrincipal: "arn:aws:iam::000000000000:role/tester",
		Operations:       []string{"Decrypt"},
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "count grants")
}

// fakeDirEntry is a minimal os.DirEntry for injection in tests.
type fakeDirEntry struct{ filename string }

func (f fakeDirEntry) Name() string               { return f.filename }
func (f fakeDirEntry) IsDir() bool                { return false }
func (f fakeDirEntry) Type() os.FileMode          { return 0 }
func (f fakeDirEntry) Info() (os.FileInfo, error) { return nil, nil }

func TestListGrants_empty(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	grants, nextMarker, err := s.ListGrants(keyID, "", "", 50, "")
	require.NoError(t, err)
	assert.Empty(t, grants)
	assert.Empty(t, nextMarker)
}

func TestListGrants_filterGrantId(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	g1 := mustCreateGrantStorage(t, s, keyID)
	mustCreateGrantStorage(t, s, keyID)

	grants, _, err := s.ListGrants(keyID, g1.GrantId, "", 50, "")
	require.NoError(t, err)
	require.Len(t, grants, 1)
	assert.Equal(t, g1.GrantId, grants[0].GrantId)
}

func TestListGrants_filterGranteePrincipal(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	g1, err := s.CreateGrant(keyID, CreateGrantInput{
		GranteePrincipal: "arn:aws:iam::000000000000:role/a",
		Operations:       []string{"Decrypt"},
	})
	require.NoError(t, err)
	_, err = s.CreateGrant(keyID, CreateGrantInput{
		GranteePrincipal: "arn:aws:iam::000000000000:role/b",
		Operations:       []string{"Encrypt"},
	})
	require.NoError(t, err)

	grants, _, err := s.ListGrants(keyID, "", "arn:aws:iam::000000000000:role/a", 50, "")
	require.NoError(t, err)
	require.Len(t, grants, 1)
	assert.Equal(t, g1.GrantId, grants[0].GrantId)
}

func TestListGrants_pendingDeletion(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	_, err := s.ScheduleKeyDeletion(keyID, 7)
	require.NoError(t, err)
	_, _, err = s.ListGrants(keyID, "", "", 50, "")
	require.ErrorIs(t, err, ErrInvalidKeyState)
}

func TestListGrants_staleMarker(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	mustCreateGrantStorage(t, s, keyID)
	mustCreateGrantStorage(t, s, keyID)

	// All-zeros UUID sorts before all random UUIDs; binary search returns start=0.
	grants, _, err := s.ListGrants(keyID, "", "", 50, "00000000-0000-0000-0000-000000000000")
	require.NoError(t, err)
	assert.Len(t, grants, 2)
}

func TestListGrants_markerPastEnd(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	mustCreateGrantStorage(t, s, keyID)

	// "ffffffff-..." sorts after all random UUIDs; binary search returns start=len.
	grants, _, err := s.ListGrants(keyID, "", "", 50, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	require.NoError(t, err)
	assert.Empty(t, grants)
}

func TestRevokeGrant_basic(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	g := mustCreateGrantStorage(t, s, keyID)
	require.NoError(t, s.RevokeGrant(keyID, g.GrantId))

	grants, _, err := s.ListGrants(keyID, "", "", 50, "")
	require.NoError(t, err)
	assert.Empty(t, grants)
}

func TestRevokeGrant_notFound(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	err := s.RevokeGrant(keyID, "00000000-0000-0000-0000-000000000000")
	require.ErrorIs(t, err, ErrGrantNotFound)
}

func TestRevokeGrant_removeRace(t *testing.T) {
	// Simulate the file disappearing between stat and removeFile (TOCTOU).
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	g := mustCreateGrantStorage(t, s, keyID)
	s.removeFile = func(string) error { return os.ErrNotExist }
	err := s.RevokeGrant(keyID, g.GrantId)
	require.ErrorIs(t, err, ErrGrantNotFound)
}

func TestRevokeGrant_removeFails(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	g := mustCreateGrantStorage(t, s, keyID)
	s.removeFile = func(string) error { return errors.New("disk full") }
	err := s.RevokeGrant(keyID, g.GrantId)
	require.Error(t, err)
	assert.ErrorContains(t, err, "remove grant")
}

func TestRevokeGrant_pendingDeletion(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	g := mustCreateGrantStorage(t, s, keyID)
	_, err := s.ScheduleKeyDeletion(keyID, 7)
	require.NoError(t, err)
	err = s.RevokeGrant(keyID, g.GrantId)
	require.ErrorIs(t, err, ErrInvalidKeyState)
}

func TestRetireGrantByToken_basic(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	g := mustCreateGrantStorage(t, s, keyID)
	require.NoError(t, s.RetireGrantByToken(g.GrantToken))

	grants, _, err := s.ListGrants(keyID, "", "", 50, "")
	require.NoError(t, err)
	assert.Empty(t, grants)
}

func TestRetireGrantByToken_notFound(t *testing.T) {
	s, _ := newTestStorage(t)
	err := s.RetireGrantByToken("00000000-0000-0000-0000-000000000000")
	require.ErrorIs(t, err, ErrGrantNotFound)
}

func TestRetireGrantByToken_listKeysError(t *testing.T) {
	s, _ := newTestStorage(t)
	s.listDirFn = func(string) ([]os.DirEntry, error) { return nil, errors.New("list failed") }
	err := s.RetireGrantByToken("00000000-0000-0000-0000-000000000000")
	require.Error(t, err)
	assert.ErrorContains(t, err, "list keys")
}

func TestRetireGrantByToken_listGrantsError(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	mustCreateGrantStorage(t, s, keyID)
	origListDir := s.listDirFn
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == grantsDir(keyID) {
			return nil, errors.New("grants list failed")
		}
		return origListDir(name)
	}
	err := s.RetireGrantByToken("00000000-0000-0000-0000-000000000000")
	require.Error(t, err)
	assert.ErrorContains(t, err, "grants list failed")
}

func TestRetireGrantByToken_removeFails(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	g := mustCreateGrantStorage(t, s, keyID)
	s.removeFile = func(string) error { return errors.New("disk full") }
	err := s.RetireGrantByToken(g.GrantToken)
	require.Error(t, err)
	assert.ErrorContains(t, err, "remove grant")
}

func TestRetireGrantByID_basic(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	g := mustCreateGrantStorage(t, s, keyID)
	require.NoError(t, s.RetireGrantByID(keyID, g.GrantId))

	grants, _, err := s.ListGrants(keyID, "", "", 50, "")
	require.NoError(t, err)
	assert.Empty(t, grants)
}

func TestRetireGrantByID_notFound(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	err := s.RetireGrantByID(keyID, "00000000-0000-0000-0000-000000000000")
	require.ErrorIs(t, err, ErrGrantNotFound)
}

func TestListRetirableGrants_basic(t *testing.T) {
	s, _ := newTestStorage(t)
	key1 := newSymmetricKey(t, s)
	key2 := newSymmetricKey(t, s)
	retiring := "arn:aws:iam::000000000000:role/admin"

	g1, err := s.CreateGrant(key1, CreateGrantInput{
		GranteePrincipal:  "arn:aws:iam::000000000000:role/r",
		Operations:        []string{"Decrypt"},
		RetiringPrincipal: retiring,
	})
	require.NoError(t, err)
	g2, err := s.CreateGrant(key2, CreateGrantInput{
		GranteePrincipal:  "arn:aws:iam::000000000000:role/r",
		Operations:        []string{"Encrypt"},
		RetiringPrincipal: retiring,
	})
	require.NoError(t, err)
	// Grant without RetiringPrincipal should NOT appear.
	mustCreateGrantStorage(t, s, key1)

	grants, _, err := s.ListRetirableGrants(retiring, 50, "")
	require.NoError(t, err)
	require.Len(t, grants, 2)
	ids := []string{grants[0].GrantId, grants[1].GrantId}
	assert.ElementsMatch(t, []string{g1.GrantId, g2.GrantId}, ids)
}

func TestListGrantsForKeyLocked_skipsSubdirAndNonJson(t *testing.T) {
	s, dir := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	mustCreateGrantStorage(t, s, keyID)

	gDir := filepath.Join(dir, "kms", "keys", keyID, "grants")
	// Create a subdirectory — should be skipped.
	require.NoError(t, os.MkdirAll(filepath.Join(gDir, "subdir"), 0o750))
	// Create a non-.json file — should be skipped.
	require.NoError(t, os.WriteFile(filepath.Join(gDir, "readme.txt"), []byte("hi"), 0o600))
	// Create a corrupt .json file — should be skipped with a warning.
	require.NoError(t, os.WriteFile(filepath.Join(gDir, "corrupt.json"), []byte("{bad}"), 0o600))

	grants, _, err := s.ListGrants(keyID, "", "", 50, "")
	require.NoError(t, err)
	assert.Len(t, grants, 1)
}

func TestListKeyIDsLocked_statError(t *testing.T) {
	s, _ := newTestStorage(t)
	keyID := newSymmetricKey(t, s)

	statErr := errors.New("permission denied")
	origStat := s.statFn
	s.statFn = func(name string) (os.FileInfo, error) {
		metaPath := filepath.Join("keys", keyID, "meta.json")
		if name == metaPath {
			return nil, statErr
		}
		return origStat(name)
	}
	_, _, err := s.ListRetirableGrants("arn:aws:iam::000000000000:role/r", 50, "")
	require.ErrorContains(t, err, "stat key meta")
}

func TestListKeyIDsLocked_skipsNonDirAndMissingMeta(t *testing.T) {
	s, dir := newTestStorage(t)
	keyID := newSymmetricKey(t, s)
	mustCreateGrantStorage(t, s, keyID)

	keysDir := filepath.Join(dir, "kms", "keys")
	// Non-directory file in keys/ — should be skipped.
	require.NoError(t, os.WriteFile(filepath.Join(keysDir, "not-a-dir.json"), []byte("x"), 0o600))
	// Key directory without meta.json — should be skipped (ErrNotExist path).
	require.NoError(t, os.MkdirAll(filepath.Join(keysDir, "orphan-key"), 0o750))

	// ListRetirableGrants calls listKeyIDsLocked internally and should still
	// return only grants from the valid key.
	retiringPrincipal := "arn:aws:iam::000000000000:role/admin"
	_, err := s.CreateGrant(keyID, CreateGrantInput{
		GranteePrincipal:  "arn:aws:iam::000000000000:role/r",
		Operations:        []string{"Decrypt"},
		RetiringPrincipal: retiringPrincipal,
	})
	require.NoError(t, err)

	result, _, err := s.ListRetirableGrants(retiringPrincipal, 50, "")
	require.NoError(t, err)
	assert.Len(t, result, 1)
}
