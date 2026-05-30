package kms

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

func nowUnix() float64 { return float64(time.Now().Unix()) }

// Storage is a filesystem-backed KMS backend. os.Root scopes all access to
// the storage root, preventing path traversal attacks.
type Storage struct {
	mu         sync.RWMutex
	root       *os.Root
	mkdirFn    func(name string, perm os.FileMode) error
	removeFile func(name string) error
	openFile   func(name string, flag int, perm os.FileMode) (io.WriteCloser, error)
	readAll    func(r io.Reader) ([]byte, error)
	listDirFn  func(name string) ([]os.DirEntry, error)
	statFn     func(name string) (os.FileInfo, error)
	randRead   func(b []byte) (int, error)
}

const maxAliasesPerKey = 256

// NewStorage roots the storage at dataDir/kms, creating the directory if needed.
func NewStorage(dataDir string) (*Storage, error) {
	return newStorage(dataDir, os.OpenRoot)
}

// Close releases the os.Root handle held by the storage.
func (s *Storage) Close() error {
	return s.root.Close()
}

func newStorage(dataDir string, openRoot func(string) (*os.Root, error)) (*Storage, error) {
	rootPath := filepath.Join(dataDir, "kms")
	if err := os.MkdirAll(filepath.Join(rootPath, "keys"), 0o750); err != nil {
		return nil, fmt.Errorf("create kms storage root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(rootPath, "aliases"), 0o750); err != nil {
		return nil, fmt.Errorf("create kms aliases dir: %w", err)
	}
	root, err := openRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open kms storage root: %w", err)
	}
	s := &Storage{root: root}
	s.mkdirFn = s.root.Mkdir
	s.removeFile = s.root.Remove
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		return s.root.OpenFile(name, flag, perm)
	}
	s.readAll = io.ReadAll
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		f, err := s.root.Open(name)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return f.ReadDir(-1)
	}
	s.statFn = s.root.Stat
	s.randRead = rand.Read
	return s, nil
}

type CreateKeyInput struct {
	Description string `json:"Description"`
	KeySpec     string `json:"KeySpec"`
	KeyUsage    string `json:"KeyUsage"`
	MultiRegion bool   `json:"MultiRegion"`
	Origin      string `json:"Origin"`
	Policy      string `json:"Policy"`
}

func (s *Storage) CreateKey(in CreateKeyInput) (KeyMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	keyID, err := s.newKeyID()
	if err != nil {
		return KeyMetadata{}, fmt.Errorf("generate key ID: %w", err)
	}

	now := nowUnix()
	meta := KeyMetadata{
		KeyID:                  keyID,
		Arn:                    keyARN(keyID),
		AWSAccountID:           fixedAccount,
		Description:            in.Description,
		KeySpec:                in.KeySpec,
		CustomerMasterKeySpec:  in.KeySpec,
		KeyUsage:               in.KeyUsage,
		KeyState:               "Enabled",
		Enabled:                true,
		KeyManager:             "CUSTOMER",
		Origin:                 in.Origin,
		MultiRegion:            in.MultiRegion,
		CreationDate:           now,
		EncryptionAlgorithms:   encryptionAlgorithmsForKey(in.KeySpec, in.KeyUsage),
		SigningAlgorithms:      signingAlgorithmsForKey(in.KeySpec, in.KeyUsage),
		KeyAgreementAlgorithms: keyAgreementAlgorithmsForKey(in.KeySpec, in.KeyUsage),
		MacAlgorithms:          macAlgorithmsForKey(in.KeySpec),
	}

	keyDir := filepath.Join("keys", keyID)
	if err := s.mkdirFn(keyDir, 0o750); err != nil {
		return KeyMetadata{}, fmt.Errorf("create key directory: %w", err)
	}

	if err := s.writeJSON(filepath.Join(keyDir, "meta.json"), meta); err != nil {
		if rmErr := s.removeFile(filepath.Join(keyDir, "meta.json")); rmErr != nil &&
			!errors.Is(rmErr, os.ErrNotExist) {
			slog.Warn(
				"failed to clean up meta.json after meta write failure",
				"keyID",
				keyID,
				"err",
				rmErr,
			)
		}
		if rmErr := s.removeFile(keyDir); rmErr != nil {
			slog.Warn(
				"failed to clean up key dir after meta write failure",
				"keyID",
				keyID,
				"err",
				rmErr,
			)
		}
		return KeyMetadata{}, fmt.Errorf("write key metadata: %w", err)
	}

	if in.KeySpec == "SYMMETRIC_DEFAULT" {
		var keyBytes [32]byte
		if _, err := s.randRead(keyBytes[:]); err != nil {
			if rmErr := s.removeFile(filepath.Join(keyDir, "meta.json")); rmErr != nil {
				slog.Warn(
					"failed to clean up meta.json after material rand failure",
					"keyID",
					keyID,
					"err",
					rmErr,
				)
			}
			if rmErr := s.removeFile(keyDir); rmErr != nil {
				slog.Warn(
					"failed to clean up key dir after material rand failure",
					"keyID",
					keyID,
					"err",
					rmErr,
				)
			}
			return KeyMetadata{}, fmt.Errorf("generate key material: %w", err)
		}
		var matIDBytes [32]byte
		if _, err := s.randRead(matIDBytes[:]); err != nil {
			if rmErr := s.removeFile(filepath.Join(keyDir, "meta.json")); rmErr != nil {
				slog.Warn(
					"failed to clean up meta.json after material ID rand failure",
					"keyID",
					keyID,
					"err",
					rmErr,
				)
			}
			if rmErr := s.removeFile(keyDir); rmErr != nil {
				slog.Warn(
					"failed to clean up key dir after material ID rand failure",
					"keyID",
					keyID,
					"err",
					rmErr,
				)
			}
			return KeyMetadata{}, fmt.Errorf("generate key material ID: %w", err)
		}
		material := KeyMaterial{
			KeyBytes:      keyBytes[:],
			KeyMaterialID: fmt.Sprintf("%x", matIDBytes),
		}
		if err := s.writeJSON(filepath.Join(keyDir, "material.json"), material); err != nil {
			if rmErr := s.removeFile(filepath.Join(keyDir, "meta.json")); rmErr != nil {
				slog.Warn(
					"failed to clean up meta.json after material write failure",
					"keyID",
					keyID,
					"err",
					rmErr,
				)
			}
			if rmErr := s.removeFile(filepath.Join(keyDir, "material.json")); rmErr != nil &&
				!errors.Is(rmErr, os.ErrNotExist) {
				slog.Warn(
					"failed to clean up material.json after material write failure",
					"keyID",
					keyID,
					"err",
					rmErr,
				)
			}
			if rmErr := s.removeFile(keyDir); rmErr != nil {
				slog.Warn(
					"failed to clean up key dir after material write failure",
					"keyID",
					keyID,
					"err",
					rmErr,
				)
			}
			return KeyMetadata{}, fmt.Errorf("write key material: %w", err)
		}
	}

	policy := in.Policy
	if policy == "" {
		policy = defaultPolicy
	}
	if err := s.writeJSON(filepath.Join(keyDir, "policy.json"), policy); err != nil {
		if rmErr := s.removeFile(filepath.Join(keyDir, "meta.json")); rmErr != nil {
			slog.Warn(
				"failed to clean up meta.json after policy write failure",
				"keyID",
				keyID,
				"err",
				rmErr,
			)
		}
		if rmErr := s.removeFile(filepath.Join(keyDir, "material.json")); rmErr != nil &&
			!errors.Is(rmErr, os.ErrNotExist) {
			slog.Warn(
				"failed to clean up material.json after policy write failure",
				"keyID",
				keyID,
				"err",
				rmErr,
			)
		}
		if rmErr := s.removeFile(keyDir); rmErr != nil {
			slog.Warn(
				"failed to clean up key dir after policy write failure",
				"keyID",
				keyID,
				"err",
				rmErr,
			)
		}
		return KeyMetadata{}, fmt.Errorf("write key policy: %w", err)
	}

	return meta, nil
}

func (s *Storage) GetKeyMetadata(keyID string) (KeyMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readKeyMeta(keyID)
}

func (s *Storage) ListKeyIDs() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := s.listDirFn("keys")
	if err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join("keys", e.Name(), "meta.json")
		if _, err := s.statFn(metaPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("stat key meta %s: %w", metaPath, err)
		}
		ids = append(ids, e.Name())
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Storage) GetKeyPolicy(keyID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.keyExistsLocked(keyID); err != nil {
		return "", err
	}
	return readJSON[string](s, filepath.Join("keys", keyID, "policy.json"))
}

func (s *Storage) PutKeyPolicy(keyID, policy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.keyExistsLocked(keyID); err != nil {
		return err
	}
	return s.writeJSON(filepath.Join("keys", keyID, "policy.json"), policy)
}

func (s *Storage) GetKeyMaterial(keyID string) (KeyMaterial, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.keyExistsLocked(keyID); err != nil {
		if !errors.Is(err, ErrKeyNotFound) {
			return KeyMaterial{}, fmt.Errorf("key %s existence check failed: %w", keyID, err)
		}
		return KeyMaterial{}, err
	}
	mat, err := readJSON[KeyMaterial](s, filepath.Join("keys", keyID, "material.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return KeyMaterial{}, ErrKeyMaterialNotFound
		}
		return KeyMaterial{}, fmt.Errorf("failed to read key material for %s: %w", keyID, err)
	}
	return mat, nil
}

func (s *Storage) keyExistsLocked(keyID string) error {
	_, err := s.statFn(filepath.Join("keys", keyID, "meta.json"))
	if errors.Is(err, os.ErrNotExist) {
		return ErrKeyNotFound
	}
	return err
}

func (s *Storage) readKeyMeta(keyID string) (KeyMetadata, error) {
	meta, err := readJSON[KeyMetadata](s, filepath.Join("keys", keyID, "meta.json"))
	if errors.Is(err, os.ErrNotExist) {
		return KeyMetadata{}, ErrKeyNotFound
	}
	return meta, err
}

func (s *Storage) writeJSON(path string, v any) (retErr error) {
	data, err := json.Marshal(v)
	if err != nil {
		// untestable: KeyMetadata and string always marshal without error
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	f, err := s.openFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil && retErr == nil {
			retErr = err
		}
	}()
	_, retErr = f.Write(data)
	return
}

func readJSON[T any](s *Storage, path string) (T, error) {
	var zero T
	f, err := s.root.Open(path)
	if err != nil {
		return zero, err
	}
	defer func() { _ = f.Close() }()
	data, err := s.readAll(f)
	if err != nil {
		return zero, err
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return zero, err
	}
	return v, nil
}

// aliasFilename encodes an alias name (e.g. "alias/foo") into a flat filename
// by URL-escaping the slashes: "alias%2Ffoo.json".
func aliasFilename(aliasName string) string {
	return url.PathEscape(aliasName) + ".json"
}

// aliasPath returns the path within the storage root for the given alias.
func aliasPath(aliasName string) string {
	return filepath.Join("aliases", aliasFilename(aliasName))
}

func (s *Storage) CreateAlias(aliasName, targetKeyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.keyExistsLocked(targetKeyID); err != nil {
		return fmt.Errorf("target key: %w", err)
	}

	// Check per-key alias limit.
	count, err := s.countAliasesForKeyLocked(targetKeyID)
	if err != nil {
		return fmt.Errorf("count aliases: %w", err)
	}
	if count >= maxAliasesPerKey {
		// untestable: requires creating maxAliasesPerKey (256) real aliases
		return fmt.Errorf("alias limit exceeded: %w", ErrAliasLimitExceeded)
	}

	// Fail if alias already exists.
	if _, err := s.statFn(aliasPath(aliasName)); err == nil {
		return ErrAliasAlreadyExists
	}

	now := nowUnix()
	entry := AliasEntry{
		AliasName:       aliasName,
		AliasArn:        aliasARN(aliasName),
		TargetKeyId:     targetKeyID,
		CreationDate:    now,
		LastUpdatedDate: now,
	}
	return s.writeJSON(aliasPath(aliasName), entry)
}

func (s *Storage) DeleteAlias(aliasName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.statFn(aliasPath(aliasName)); errors.Is(err, os.ErrNotExist) {
		return ErrAliasNotFound
	}
	if err := s.removeFile(aliasPath(aliasName)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrAliasNotFound
		}
		return fmt.Errorf("remove alias: %w", err)
	}
	return nil
}

func (s *Storage) UpdateAlias(aliasName, targetKeyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Alias must exist.
	existing, err := readJSON[AliasEntry](s, aliasPath(aliasName))
	if errors.Is(err, os.ErrNotExist) {
		return ErrAliasNotFound
	}
	if err != nil {
		return fmt.Errorf("read alias: %w", err)
	}

	// Target key must exist.
	if err := s.keyExistsLocked(targetKeyID); err != nil {
		return fmt.Errorf("target key: %w", err)
	}

	updated := existing
	updated.TargetKeyId = targetKeyID
	updated.LastUpdatedDate = nowUnix()
	return s.writeJSON(aliasPath(aliasName), updated)
}

func (s *Storage) ListAliases(filterKeyID string) ([]AliasEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := s.listDirFn("aliases")
	if err != nil {
		return nil, fmt.Errorf("list aliases dir: %w", err)
	}

	var aliases []AliasEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 5 || name[len(name)-5:] != ".json" {
			continue
		}
		alias, err := readJSON[AliasEntry](s, filepath.Join("aliases", name))
		if err != nil {
			slog.Warn("kms: skipping unreadable alias file", "file", name, "err", err)
			continue
		}
		if filterKeyID != "" && alias.TargetKeyId != filterKeyID {
			continue
		}
		aliases = append(aliases, alias)
	}
	sort.Slice(aliases, func(i, j int) bool {
		return aliases[i].AliasName < aliases[j].AliasName
	})
	return aliases, nil
}

func (s *Storage) ResolveAlias(aliasName string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, err := readJSON[AliasEntry](s, aliasPath(aliasName))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrAliasNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read alias: %w", err)
	}
	return entry.TargetKeyId, nil
}

func (s *Storage) countAliasesForKeyLocked(targetKeyID string) (int, error) {
	entries, err := s.listDirFn("aliases")
	if err != nil {
		return 0, fmt.Errorf("list aliases: %w", err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 5 || name[len(name)-5:] != ".json" {
			continue
		}
		alias, err := readJSON[AliasEntry](s, filepath.Join("aliases", name))
		if err != nil {
			continue
		}
		if alias.TargetKeyId == targetKeyID {
			count++
		}
	}
	return count, nil
}

func (s *Storage) newKeyID() (string, error) {
	var b [16]byte
	if _, err := s.randRead(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // UUID v4 version bits
	b[8] = (b[8] & 0x3f) | 0x80 // UUID v4 variant bits
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16],
	), nil
}
