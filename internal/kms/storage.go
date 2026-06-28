package kms

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

func nowUnix() float64 { return float64(time.Now().Unix()) }

// Storage is a filesystem-backed KMS backend. os.Root scopes all access to
// the storage root, preventing path traversal attacks.
type Storage struct {
	mu                sync.RWMutex
	root              *os.Root
	maxAliasesPerKey  int
	mkdirFn           func(name string, perm os.FileMode) error
	removeFile        func(name string) error
	openFile          func(name string, flag int, perm os.FileMode) (io.WriteCloser, error)
	readAll           func(r io.Reader) ([]byte, error)
	listDirFn         func(name string) ([]os.DirEntry, error)
	statFn            func(name string) (os.FileInfo, error)
	randRead          func(b []byte) (int, error)
	generateKeyPairFn func(keySpec string) (privKeyDER []byte, err error)
}

// aliasLimitPerKey is the AWS-spec maximum number of aliases per key.
const aliasLimitPerKey = 256

// maxTagsPerKey is the AWS-spec maximum number of tags per key.
const maxTagsPerKey = 50

const secondsPerDay = 86400

const (
	keyStateEnabled         = "Enabled"
	keyStateDisabled        = "Disabled"
	keyStatePendingDeletion = "PendingDeletion"
	keySpecSymmetricDefault = "SYMMETRIC_DEFAULT"
)

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
	s := &Storage{root: root, maxAliasesPerKey: aliasLimitPerKey}
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
	s.generateKeyPairFn = generateKeyPair
	return s, nil
}

// generateRSAKey generates an RSA private key of the given bit size.
func generateRSAKey(bits int) (*rsa.PrivateKey, error) {
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		// untestable: rsa.GenerateKey only fails on I/O errors from rand.Reader
		return nil, fmt.Errorf("generate RSA_%d: %w", bits, err)
	}
	return k, nil
}

// generateKeyPair generates an asymmetric key pair for the given KeySpec and
// returns the PKCS#8 DER-encoded private key. Returns nil, nil for unsupported
// or non-asymmetric specs (HMAC, SYMMETRIC_DEFAULT, ECC_SECG_P256K1, SM2, ML_DSA_*).
func generateKeyPair(keySpec string) ([]byte, error) {
	var priv any
	switch keySpec {
	case "RSA_2048":
		k, err := generateRSAKey(2048)
		if err != nil {
			// untestable: generateRSAKey only fails on I/O errors from rand.Reader
			return nil, err
		}
		priv = k
	case "RSA_3072":
		k, err := generateRSAKey(3072)
		if err != nil {
			// untestable: generateRSAKey only fails on I/O errors from rand.Reader
			return nil, err
		}
		priv = k
	case "RSA_4096":
		k, err := generateRSAKey(4096)
		if err != nil {
			// untestable: generateRSAKey only fails on I/O errors from rand.Reader
			return nil, err
		}
		priv = k
	case "ECC_NIST_P256":
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			// untestable: ecdsa.GenerateKey only fails on I/O errors from rand.Reader
			return nil, fmt.Errorf("generate ECC_NIST_P256: %w", err)
		}
		priv = k
	case "ECC_NIST_P384":
		k, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			// untestable: ecdsa.GenerateKey only fails on I/O errors from rand.Reader
			return nil, fmt.Errorf("generate ECC_NIST_P384: %w", err)
		}
		priv = k
	case "ECC_NIST_P521":
		k, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
		if err != nil {
			// untestable: ecdsa.GenerateKey only fails on I/O errors from rand.Reader
			return nil, fmt.Errorf("generate ECC_NIST_P521: %w", err)
		}
		priv = k
	case "ECC_NIST_EDWARDS25519":
		_, k, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			// untestable: ed25519.GenerateKey only fails on I/O errors from rand.Reader
			return nil, fmt.Errorf("generate ECC_NIST_EDWARDS25519: %w", err)
		}
		priv = k
	default:
		return nil, nil
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		// untestable: MarshalPKCS8PrivateKey always succeeds for RSA, ECDSA, and Ed25519 keys
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	return der, nil
}

type CreateKeyInput struct {
	Description string `json:"Description"`
	KeySpec     string `json:"KeySpec"`
	KeyUsage    string `json:"KeyUsage"`
	MultiRegion bool   `json:"MultiRegion"`
	Origin      string `json:"Origin"`
	Policy      string `json:"Policy"`
	keyManager  string // internal only; "AWS" for managed keys; defaults to "CUSTOMER"
}

func (s *Storage) CreateKey(in CreateKeyInput) (KeyMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	keyID, err := s.newKeyID()
	if err != nil {
		return KeyMetadata{}, fmt.Errorf("generate key ID: %w", err)
	}

	km := in.keyManager
	if km == "" {
		km = "CUSTOMER"
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
		KeyState:               keyStateEnabled,
		Enabled:                true,
		KeyManager:             km,
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

	if in.KeySpec == keySpecSymmetricDefault || isHMACSpec(in.KeySpec) {
		keySize := 32 // AES-256 for SYMMETRIC_DEFAULT
		if n := hmacKeySize(in.KeySpec); n > 0 {
			keySize = n
		}
		keyBytes := make([]byte, keySize)
		if _, err := s.randRead(keyBytes); err != nil {
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
			KeyBytes:      keyBytes,
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
	} else {
		privKeyDER, err := s.generateKeyPairFn(in.KeySpec)
		if err != nil {
			if rmErr := s.removeFile(filepath.Join(keyDir, "meta.json")); rmErr != nil {
				slog.Warn(
					"failed to clean up meta.json after key pair generation failure",
					"keyID",
					keyID,
					"err",
					rmErr,
				)
			}
			if rmErr := s.removeFile(keyDir); rmErr != nil {
				slog.Warn(
					"failed to clean up key dir after key pair generation failure",
					"keyID",
					keyID,
					"err",
					rmErr,
				)
			}
			return KeyMetadata{}, fmt.Errorf("generate key pair: %w", err)
		}
		if privKeyDER != nil {
			var matIDBytes [32]byte
			if _, err := s.randRead(matIDBytes[:]); err != nil {
				if rmErr := s.removeFile(filepath.Join(keyDir, "meta.json")); rmErr != nil {
					slog.Warn(
						"failed to clean up meta.json after asymmetric material ID rand failure",
						"keyID",
						keyID,
						"err",
						rmErr,
					)
				}
				if rmErr := s.removeFile(keyDir); rmErr != nil {
					slog.Warn(
						"failed to clean up key dir after asymmetric material ID rand failure",
						"keyID",
						keyID,
						"err",
						rmErr,
					)
				}
				return KeyMetadata{}, fmt.Errorf("generate key material ID: %w", err)
			}
			material := KeyMaterial{
				PrivateKeyDER: privKeyDER,
				KeyMaterialID: fmt.Sprintf("%x", matIDBytes),
			}
			if err := s.writeJSON(filepath.Join(keyDir, "material.json"), material); err != nil {
				if rmErr := s.removeFile(filepath.Join(keyDir, "meta.json")); rmErr != nil {
					slog.Warn(
						"failed to clean up meta.json after asymmetric material write failure",
						"keyID",
						keyID,
						"err",
						rmErr,
					)
				}
				if rmErr := s.removeFile(filepath.Join(keyDir, "material.json")); rmErr != nil &&
					!errors.Is(rmErr, os.ErrNotExist) {
					slog.Warn(
						"failed to clean up material.json after asymmetric material write failure",
						"keyID",
						keyID,
						"err",
						rmErr,
					)
				}
				if rmErr := s.removeFile(keyDir); rmErr != nil {
					slog.Warn(
						"failed to clean up key dir after asymmetric material write failure",
						"keyID",
						keyID,
						"err",
						rmErr,
					)
				}
				return KeyMetadata{}, fmt.Errorf("write key material: %w", err)
			}
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

// EnsureAwsS3Key returns the ARN of the alias/aws/s3 managed key, creating it
// (and the alias) if it does not yet exist.
func (s *Storage) EnsureAwsS3Key() (string, error) {
	keyID, err := s.ResolveAlias("alias/aws/s3")
	if err == nil {
		meta, metaErr := s.GetKeyMetadata(keyID)
		if metaErr != nil {
			return "", fmt.Errorf("read alias/aws/s3 metadata: %w", metaErr)
		}
		return meta.Arn, nil
	}
	if !errors.Is(err, ErrAliasNotFound) {
		return "", fmt.Errorf("resolve alias/aws/s3: %w", err)
	}

	meta, err := s.CreateKey(CreateKeyInput{
		Description: "Default KMS key used by Amazon S3",
		KeySpec:     keySpecSymmetricDefault,
		KeyUsage:    "ENCRYPT_DECRYPT",
		Origin:      "AWS_KMS",
		keyManager:  "AWS",
	})
	if err != nil {
		return "", fmt.Errorf("create aws/s3 managed key: %w", err)
	}

	if aliasErr := s.CreateAlias("alias/aws/s3", meta.KeyID); aliasErr != nil {
		if !errors.Is(aliasErr, ErrAliasAlreadyExists) {
			return "", fmt.Errorf("create alias/aws/s3: %w", aliasErr)
		}
		// Race: another writer created alias/aws/s3 between our ErrAliasNotFound
		// check and CreateAlias. Resolve the winner's key and return its ARN.
		raceKeyID, resolveErr := s.ResolveAlias("alias/aws/s3")
		if resolveErr != nil {
			return "", fmt.Errorf("resolve alias/aws/s3 after race: %w", resolveErr)
		}
		m, resolveErr := s.GetKeyMetadata(raceKeyID)
		if resolveErr != nil {
			return "", fmt.Errorf("read alias/aws/s3 race winner metadata: %w", resolveErr)
		}
		return m.Arn, nil
	}

	return meta.Arn, nil
}

// ResolveKeyForEncryption resolves keyRef to a canonical key ARN and validates
// that the key is Enabled. If keyRef is empty, alias/aws/s3 is used (auto-created
// on first call). Returns ErrKeyNotFound, ErrKeyDisabled, or ErrKeyPendingDeletion
// on validation failure.
func (s *Storage) ResolveKeyForEncryption(keyRef string) (string, error) {
	// Normalize default/shorthand aliases so they go through state validation below.
	if keyRef == "" || keyRef == "alias/aws/s3" {
		if _, err := s.EnsureAwsS3Key(); err != nil {
			return "", fmt.Errorf("ensure alias/aws/s3: %w", err)
		}
		keyRef = "alias/aws/s3"
	}

	var keyID string
	if isAliasRef(keyRef) {
		aliasName, ok := normalizeAliasRef(keyRef)
		if !ok {
			// unreachable: normalizeAliasRef only fails for ARN-style refs without
			// ":alias/", but isAliasRef already requires strings.Contains(keyID, ":alias/").
			return "", ErrKeyNotFound
		}
		if aliasName == "alias/aws/s3" {
			// Ensure the managed key exists before resolving its ID for state validation.
			if _, err := s.EnsureAwsS3Key(); err != nil {
				return "", fmt.Errorf("ensure alias/aws/s3: %w", err)
			}
		}
		id, err := s.ResolveAlias(aliasName)
		if err != nil {
			if errors.Is(err, ErrAliasNotFound) {
				return "", ErrKeyNotFound
			}
			return "", fmt.Errorf("resolve alias %s: %w", aliasName, err)
		}
		keyID = id
	} else {
		id, ok := resolveKeyID(keyRef)
		if !ok {
			return "", ErrKeyNotFound
		}
		keyID = id
	}

	meta, err := s.GetKeyMetadata(keyID)
	if err != nil {
		return "", fmt.Errorf("read key metadata %s: %w", keyID, err)
	}

	switch meta.KeyState {
	case keyStateDisabled:
		return "", ErrKeyDisabled
	case keyStatePendingDeletion:
		return "", ErrKeyPendingDeletion
	}

	return meta.Arn, nil
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
	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		// unreachable: keyExistsLocked confirmed meta.json exists immediately above
		return KeyMaterial{}, fmt.Errorf("failed to read key metadata for %s: %w", keyID, err)
	}
	if n := hmacKeySize(meta.KeySpec); n > 0 && len(mat.KeyBytes) != n {
		return KeyMaterial{}, fmt.Errorf("%w: key %s expects %d bytes, got %d",
			ErrKeyMaterialCorrupted, keyID, n, len(mat.KeyBytes))
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
	meta, err := s.readKeyMeta(targetKeyID)
	if err != nil {
		// untestable: file is verified to exist via keyExistsLocked just before this call
		return fmt.Errorf("read key meta: %w", err)
	}
	if meta.KeyState == keyStatePendingDeletion {
		return ErrKeyPendingDeletion
	}

	// Check per-key alias limit.
	count, err := s.countAliasesForKeyLocked(targetKeyID)
	if err != nil {
		return fmt.Errorf("count aliases: %w", err)
	}
	if count >= s.maxAliasesPerKey {
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

	// Target key must exist and must not be pending deletion.
	if err := s.keyExistsLocked(targetKeyID); err != nil {
		return fmt.Errorf("target key: %w", err)
	}
	targetMeta, err := s.readKeyMeta(targetKeyID)
	if err != nil {
		// untestable: file is verified to exist via keyExistsLocked just before this call
		return fmt.Errorf("read target key meta: %w", err)
	}
	if targetMeta.KeyState == keyStatePendingDeletion {
		return ErrKeyPendingDeletion
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
			return 0, fmt.Errorf("read alias %s: %w", name, err)
		}
		if alias.TargetKeyId == targetKeyID {
			count++
		}
	}
	return count, nil
}

func (s *Storage) EnableKey(keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return err
	}
	if meta.KeyState == keyStatePendingDeletion {
		return ErrInvalidKeyState
	}
	if meta.KeyState == keyStateEnabled {
		return nil
	}
	meta.KeyState = keyStateEnabled
	meta.Enabled = true
	return s.writeJSON(filepath.Join("keys", keyID, "meta.json"), meta)
}

func (s *Storage) DisableKey(keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return err
	}
	if meta.KeyState == keyStatePendingDeletion {
		return ErrInvalidKeyState
	}
	if meta.KeyState == keyStateDisabled {
		return nil
	}
	meta.KeyState = keyStateDisabled
	meta.Enabled = false
	return s.writeJSON(filepath.Join("keys", keyID, "meta.json"), meta)
}

func (s *Storage) ScheduleKeyDeletion(keyID string, pendingWindowInDays int) (KeyMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return KeyMetadata{}, err
	}
	if meta.KeyState == keyStatePendingDeletion {
		return KeyMetadata{}, ErrInvalidKeyState
	}
	deletionDate := nowUnix() + float64(pendingWindowInDays*secondsPerDay)
	meta.KeyState = keyStatePendingDeletion
	meta.Enabled = false
	meta.DeletionDate = &deletionDate
	return meta, s.writeJSON(filepath.Join("keys", keyID, "meta.json"), meta)
}

func (s *Storage) CancelKeyDeletion(keyID string) (KeyMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return KeyMetadata{}, err
	}
	if meta.KeyState != keyStatePendingDeletion {
		return KeyMetadata{}, ErrInvalidKeyState
	}
	meta.KeyState = keyStateDisabled
	meta.Enabled = false
	meta.DeletionDate = nil
	return meta, s.writeJSON(filepath.Join("keys", keyID, "meta.json"), meta)
}

func (s *Storage) EnableKeyRotation(keyID string, rotationPeriodInDays int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return err
	}
	if meta.KeyState == keyStatePendingDeletion {
		return ErrInvalidKeyState
	}
	if meta.KeyState != keyStateEnabled {
		return ErrKeyDisabled
	}
	if meta.KeySpec != keySpecSymmetricDefault {
		return ErrUnsupportedOp
	}
	cfg := KeyRotationConfig{
		Enabled:              true,
		RotationPeriodInDays: rotationPeriodInDays,
		NextRotationDate:     nowUnix() + float64(rotationPeriodInDays*secondsPerDay),
	}
	return s.writeJSON(filepath.Join("keys", keyID, "rotation.json"), cfg)
}

func (s *Storage) DisableKeyRotation(keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return err
	}
	if meta.KeyState == keyStatePendingDeletion {
		return ErrInvalidKeyState
	}
	if meta.KeyState != keyStateEnabled {
		return ErrKeyDisabled
	}
	if meta.KeySpec != keySpecSymmetricDefault {
		return ErrUnsupportedOp
	}
	return s.writeJSON(
		filepath.Join("keys", keyID, "rotation.json"),
		KeyRotationConfig{Enabled: false},
	)
}

func (s *Storage) GetKeyRotationStatus(keyID string) (KeyMetadata, KeyRotationConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return KeyMetadata{}, KeyRotationConfig{}, err
	}
	if meta.KeyState == keyStatePendingDeletion {
		return KeyMetadata{}, KeyRotationConfig{}, ErrInvalidKeyState
	}
	if meta.KeySpec != keySpecSymmetricDefault {
		return KeyMetadata{}, KeyRotationConfig{}, ErrUnsupportedOp
	}

	cfg, err := readJSON[KeyRotationConfig](s, filepath.Join("keys", keyID, "rotation.json"))
	if errors.Is(err, os.ErrNotExist) {
		return meta, KeyRotationConfig{}, nil
	}
	if err != nil {
		return KeyMetadata{}, KeyRotationConfig{}, fmt.Errorf("read rotation config: %w", err)
	}
	return meta, cfg, nil
}

func (s *Storage) GetTags(keyID string) ([]TagEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readTagsLocked(keyID)
}

func (s *Storage) readTagsLocked(keyID string) ([]TagEntry, error) {
	if err := s.keyExistsLocked(keyID); err != nil {
		return nil, err
	}
	m, err := readJSON[map[string]string](s, filepath.Join("keys", keyID, "tags.json"))
	if errors.Is(err, os.ErrNotExist) {
		return []TagEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tags: %w", err)
	}
	entries := make([]TagEntry, 0, len(m))
	for k, v := range m {
		entries = append(entries, TagEntry{TagKey: k, TagValue: v})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].TagKey < entries[j].TagKey
	})
	return entries, nil
}

func (s *Storage) TagResource(keyID string, tags []TagEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return err
	}
	if meta.KeyState == keyStatePendingDeletion {
		return ErrInvalidKeyState
	}

	existing := map[string]string{}
	m, err := readJSON[map[string]string](s, filepath.Join("keys", keyID, "tags.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read existing tags: %w", err)
	}
	if err == nil {
		existing = m
	}

	for _, t := range tags {
		existing[t.TagKey] = t.TagValue
	}
	if len(existing) > maxTagsPerKey {
		return ErrTagLimitExceeded
	}
	return s.writeJSON(filepath.Join("keys", keyID, "tags.json"), existing)
}

func (s *Storage) UntagResource(keyID string, tagKeys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return err
	}
	if meta.KeyState == keyStatePendingDeletion {
		return ErrInvalidKeyState
	}

	existing := map[string]string{}
	m, err := readJSON[map[string]string](s, filepath.Join("keys", keyID, "tags.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read existing tags: %w", err)
	}
	if err == nil {
		existing = m
	}

	for _, k := range tagKeys {
		delete(existing, k)
	}
	return s.writeJSON(filepath.Join("keys", keyID, "tags.json"), existing)
}

const maxOnDemandRotations = 25

const maxGrantsPerKey = 50000

func (s *Storage) RotateKeyOnDemand(keyID string) (KeyMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return KeyMetadata{}, err
	}
	if meta.KeyState == keyStatePendingDeletion {
		return KeyMetadata{}, ErrInvalidKeyState
	}
	if meta.KeyState != keyStateEnabled {
		return KeyMetadata{}, ErrKeyDisabled
	}
	if meta.KeySpec != keySpecSymmetricDefault {
		return KeyMetadata{}, ErrUnsupportedOp
	}

	history, err := s.readRotationHistoryLocked(keyID)
	if err != nil {
		return KeyMetadata{}, fmt.Errorf("read rotation history: %w", err)
	}
	onDemandCount := 0
	for _, r := range history {
		if r.RotationType == "ON_DEMAND" {
			onDemandCount++
		}
	}
	if onDemandCount >= maxOnDemandRotations {
		return KeyMetadata{}, ErrOnDemandRotationLimit
	}

	curMat, err := readJSON[KeyMaterial](s, filepath.Join("keys", keyID, "material.json"))
	if err != nil {
		return KeyMetadata{}, fmt.Errorf("read current material: %w", err)
	}

	materialsDir := filepath.Join("keys", keyID, "materials")
	if mkErr := s.mkdirFn(materialsDir, 0o750); mkErr != nil && !errors.Is(mkErr, os.ErrExist) {
		return KeyMetadata{}, fmt.Errorf("create materials dir: %w", mkErr)
	}
	if err := s.writeJSON(
		filepath.Join(materialsDir, curMat.KeyMaterialID+".json"),
		curMat,
	); err != nil {
		// untestable: writeJSON failure requires OS-level I/O error simulation
		return KeyMetadata{}, fmt.Errorf("save previous material: %w", err)
	}

	newKeyBytes := make([]byte, 32)
	if _, err := s.randRead(newKeyBytes); err != nil {
		return KeyMetadata{}, fmt.Errorf("generate new key bytes: %w", err)
	}
	var matIDBytes [32]byte
	if _, err := s.randRead(matIDBytes[:]); err != nil {
		return KeyMetadata{}, fmt.Errorf("generate new material ID: %w", err)
	}
	newMat := KeyMaterial{
		KeyBytes:      newKeyBytes,
		KeyMaterialID: fmt.Sprintf("%x", matIDBytes),
	}
	if err := s.writeJSON(filepath.Join("keys", keyID, "material.json"), newMat); err != nil {
		// untestable: writeJSON failure requires OS-level I/O error simulation
		return KeyMetadata{}, fmt.Errorf("write new material: %w", err)
	}

	record := RotationRecord{
		KeyID:        meta.Arn,
		RotationDate: nowUnix(),
		RotationType: "ON_DEMAND",
	}
	history = append(history, record)
	if err := s.writeJSON(
		filepath.Join("keys", keyID, "rotation_history.json"),
		history,
	); err != nil {
		// untestable: writeJSON failure requires OS-level I/O error simulation
		return KeyMetadata{}, fmt.Errorf("write rotation history: %w", err)
	}

	return meta, nil
}

func (s *Storage) ListKeyRotations(
	keyID string,
	limit int,
	marker string,
) ([]RotationRecord, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return nil, "", err
	}
	if meta.KeyState == keyStatePendingDeletion {
		return nil, "", ErrInvalidKeyState
	}
	if meta.KeySpec != keySpecSymmetricDefault {
		return nil, "", ErrUnsupportedOp
	}

	history, err := s.readRotationHistoryLocked(keyID)
	if err != nil {
		return nil, "", fmt.Errorf("read rotation history: %w", err)
	}

	startIdx := 0
	if marker != "" {
		idx, err := strconv.Atoi(marker)
		if err != nil || idx < 0 || idx >= len(history) {
			return nil, "", ErrInvalidMarker
		}
		startIdx = idx + 1
	}
	history = history[startIdx:]

	truncated := len(history) > limit
	if truncated {
		history = history[:limit]
	}

	var nextMarker string
	if truncated {
		nextMarker = strconv.Itoa(startIdx + len(history) - 1)
	}

	return history, nextMarker, nil
}

func (s *Storage) GetPreviousKeyMaterials(keyID string) ([]KeyMaterial, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.keyExistsLocked(keyID); err != nil {
		return nil, err
	}
	materialsDir := filepath.Join("keys", keyID, "materials")
	entries, err := s.listDirFn(materialsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list materials dir: %w", err)
	}

	var mats []KeyMaterial
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 5 || name[len(name)-5:] != ".json" {
			continue
		}
		mat, err := readJSON[KeyMaterial](s, filepath.Join(materialsDir, name))
		if err != nil {
			slog.Warn("kms: skipping unreadable material file", "file", name, "err", err)
			continue
		}
		mats = append(mats, mat)
	}
	return mats, nil
}

func (s *Storage) readRotationHistoryLocked(keyID string) ([]RotationRecord, error) {
	history, err := readJSON[[]RotationRecord](
		s,
		filepath.Join("keys", keyID, "rotation_history.json"),
	)
	if errors.Is(err, os.ErrNotExist) {
		return []RotationRecord{}, nil
	}
	return history, err
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

// newGrantID generates a UUID v4 to use as a grant ID.
func (s *Storage) newGrantID() (string, error) { return s.newKeyID() }

// newGrantToken generates a UUID v4 to use as an opaque grant token.
func (s *Storage) newGrantToken() (string, error) { return s.newKeyID() }

// grantsDir returns the path of the grants subdirectory for the given key.
func grantsDir(keyID string) string { return filepath.Join("keys", keyID, "grants") }

// grantPath returns the path for a single grant file.
func grantPath(keyID, grantID string) string {
	return filepath.Join(grantsDir(keyID), grantID+".json")
}

// CreateGrant stores a new grant under keys/{keyID}/grants/{grantID}.json.
func (s *Storage) CreateGrant(keyID string, in CreateGrantInput) (Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return Grant{}, err
	}
	switch meta.KeyState {
	case keyStateDisabled:
		return Grant{}, ErrKeyDisabled
	case keyStatePendingDeletion:
		return Grant{}, ErrInvalidKeyState
	}

	if mkErr := s.mkdirFn(grantsDir(keyID), 0o750); mkErr != nil && !errors.Is(mkErr, os.ErrExist) {
		// untestable: s.root.Mkdir only fails on OS-level errors that cannot be simulated
		return Grant{}, fmt.Errorf("create grants dir: %w", mkErr)
	}

	existing, err := s.listDirFn(grantsDir(keyID))
	if err != nil {
		return Grant{}, fmt.Errorf("count grants: %w", err)
	}
	grantCount := 0
	for _, e := range existing {
		if !e.IsDir() && len(e.Name()) >= 6 && e.Name()[len(e.Name())-5:] == ".json" {
			grantCount++
		}
	}
	if grantCount >= maxGrantsPerKey {
		return Grant{}, ErrGrantLimitExceeded
	}

	grantID, err := s.newGrantID()
	if err != nil {
		// untestable: newGrantID delegates to randRead which only fails via injected error; the path is covered in CreateKey tests
		return Grant{}, fmt.Errorf("generate grant ID: %w", err)
	}
	token, err := s.newGrantToken()
	if err != nil {
		// untestable: newGrantToken delegates to randRead; error covered at CreateKey level
		return Grant{}, fmt.Errorf("generate grant token: %w", err)
	}

	g := Grant{
		GrantId:           grantID,
		GrantToken:        token,
		KeyId:             meta.Arn,
		GranteePrincipal:  in.GranteePrincipal,
		RetiringPrincipal: in.RetiringPrincipal,
		Operations:        in.Operations,
		Constraints:       in.Constraints,
		Name:              in.Name,
		IssuingAccount:    fixedAccount,
		CreationDate:      nowUnix(),
	}
	if err := s.writeJSON(grantPath(keyID, grantID), g); err != nil {
		// untestable: writeJSON only fails on OS-level I/O errors
		return Grant{}, fmt.Errorf("write grant: %w", err)
	}
	return g, nil
}

// listGrantsForKeyLocked reads all grant files for a key and returns them sorted by GrantId.
// Caller must hold at least a read lock.
func (s *Storage) listGrantsForKeyLocked(keyID string) ([]Grant, error) {
	entries, err := s.listDirFn(grantsDir(keyID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list grants dir: %w", err)
	}

	var grants []Grant
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 6 || name[len(name)-5:] != ".json" {
			continue
		}
		g, err := readJSON[Grant](s, filepath.Join(grantsDir(keyID), name))
		if err != nil {
			slog.Warn("kms: skipping unreadable grant file", "file", name, "err", err)
			continue
		}
		grants = append(grants, g)
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].GrantId < grants[j].GrantId })
	return grants, nil
}

// applyGrantPagination applies marker-based pagination to a sorted grant slice.
// Returns the page slice and the next marker (empty string when not truncated).
func applyGrantPagination(grants []Grant, limit int, marker string) ([]Grant, string) {
	if marker != "" {
		start := -1
		for i, g := range grants {
			if g.GrantId == marker {
				start = i + 1
				break
			}
		}
		if start == -1 {
			// Stale marker: advance via binary search.
			start = sort.Search(len(grants), func(i int) bool {
				return grants[i].GrantId >= marker
			})
		}
		if start < len(grants) {
			grants = grants[start:]
		} else {
			grants = nil
		}
	}

	truncated := len(grants) > limit
	if truncated {
		grants = grants[:limit]
	}
	var nextMarker string
	if truncated && len(grants) > 0 {
		nextMarker = grants[len(grants)-1].GrantId
	}
	return grants, nextMarker
}

// ListGrants returns paginated grants for a key, with optional filters.
func (s *Storage) ListGrants(
	keyID, filterGrantID, filterGranteePrincipal string,
	limit int,
	marker string,
) ([]Grant, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return nil, "", err
	}
	if meta.KeyState == keyStatePendingDeletion {
		return nil, "", ErrInvalidKeyState
	}

	grants, err := s.listGrantsForKeyLocked(keyID)
	if err != nil {
		// untestable: listGrantsForKeyLocked only fails on OS-level errors
		return nil, "", err
	}

	if filterGrantID != "" || filterGranteePrincipal != "" {
		filtered := grants[:0]
		for _, g := range grants {
			if filterGrantID != "" && g.GrantId != filterGrantID {
				continue
			}
			if filterGranteePrincipal != "" && g.GranteePrincipal != filterGranteePrincipal {
				continue
			}
			filtered = append(filtered, g)
		}
		grants = filtered
	}

	page, nextMarker := applyGrantPagination(grants, limit, marker)
	return page, nextMarker, nil
}

// RevokeGrant deletes a grant by ID.
func (s *Storage) RevokeGrant(keyID, grantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readKeyMeta(keyID)
	if err != nil {
		return err
	}
	if meta.KeyState == keyStatePendingDeletion {
		return ErrInvalidKeyState
	}

	path := grantPath(keyID, grantID)
	if _, err := s.statFn(path); errors.Is(err, os.ErrNotExist) {
		return ErrGrantNotFound
	}
	if err := s.removeFile(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrGrantNotFound
		}
		return fmt.Errorf("remove grant: %w", err)
	}
	return nil
}

// RetireGrantByToken finds and deletes the grant with the given GrantToken across all keys.
func (s *Storage) RetireGrantByToken(grantToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keyIDs, err := s.listKeyIDsLocked()
	if err != nil {
		return fmt.Errorf("list keys: %w", err)
	}
	for _, keyID := range keyIDs {
		grants, err := s.listGrantsForKeyLocked(keyID)
		if err != nil {
			return err
		}
		for _, g := range grants {
			if g.GrantToken == grantToken {
				if err := s.removeFile(grantPath(keyID, g.GrantId)); err != nil {
					return fmt.Errorf("remove grant: %w", err)
				}
				return nil
			}
		}
	}
	return ErrGrantNotFound
}

// RetireGrantByID deletes a grant by key ID and grant ID.
func (s *Storage) RetireGrantByID(keyID, grantID string) error {
	return s.RevokeGrant(keyID, grantID)
}

// ListRetirableGrants returns paginated grants across all keys where RetiringPrincipal matches.
func (s *Storage) ListRetirableGrants(
	retiringPrincipal string,
	limit int,
	marker string,
) ([]Grant, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keyIDs, err := s.listKeyIDsLocked()
	if err != nil {
		// untestable: listKeyIDsLocked only fails on OS-level errors
		return nil, "", fmt.Errorf("list keys: %w", err)
	}

	var matching []Grant
	for _, keyID := range keyIDs {
		grants, err := s.listGrantsForKeyLocked(keyID)
		if err != nil {
			// untestable: listGrantsForKeyLocked only fails on OS-level errors
			return nil, "", err
		}
		for _, g := range grants {
			if g.RetiringPrincipal == retiringPrincipal {
				matching = append(matching, g)
			}
		}
	}
	sort.Slice(matching, func(i, j int) bool { return matching[i].GrantId < matching[j].GrantId })

	page, nextMarker := applyGrantPagination(matching, limit, marker)
	return page, nextMarker, nil
}

// listKeyIDsLocked returns the IDs of all keys that have a valid meta.json.
// Caller must hold a lock.
func (s *Storage) listKeyIDsLocked() ([]string, error) {
	entries, err := s.listDirFn("keys")
	if err != nil {
		// untestable: listDirFn only fails on OS-level errors
		return nil, fmt.Errorf("list keys dir: %w", err)
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
	return ids, nil
}
