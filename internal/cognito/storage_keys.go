package cognito

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const rsaKeyBits = 2048

type poolKeys struct {
	KeyID         string `json:"KeyID"`
	PrivateKeyPEM string `json:"PrivateKeyPEM"`
}

// generateRSAKey generates a new 2048-bit RSA key pair and returns the private key
// and a random key ID.
func generateRSAKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, rsaKeyBits)
}

func rsaKeyToPEM(key *rsa.PrivateKey) (string, error) {
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block)), nil
}

func rsaKeyFromPEM(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("invalid PEM block")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA key: %w", err)
	}
	return key, nil
}

func keysPath(poolID string) string {
	return filepath.Join("pools", poolID, "keys.json")
}

// GetPoolKeys returns the RSA key pair for an existing pool.
// Returns (nil, nil, os.ErrNotExist) if no keys have been persisted yet.
func (s *Storage) GetPoolKeys(poolID string) (*poolKeys, *rsa.PrivateKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	existing, err := readJSON[poolKeys](s, keysPath(poolID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, os.ErrNotExist
		}
		return nil, nil, fmt.Errorf("read pool keys: %w", err)
	}
	key, err := rsaKeyFromPEM(existing.PrivateKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("load pool RSA key: %w", err)
	}
	return &existing, key, nil
}

// GetOrCreatePoolKeys returns the RSA key pair for a pool, generating one if not present.
func (s *Storage) GetOrCreatePoolKeys(poolID string) (*poolKeys, *rsa.PrivateKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := readJSON[poolKeys](s, keysPath(poolID))
	if err == nil {
		key, err := rsaKeyFromPEM(existing.PrivateKeyPEM)
		if err != nil {
			return nil, nil, fmt.Errorf("load pool RSA key: %w", err)
		}
		return &existing, key, nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("read pool keys: %w", err)
	}

	privateKey, err := s.generateKeyFn()
	if err != nil {
		return nil, nil, fmt.Errorf("generate RSA key: %w", err)
	}

	keyID, err := generateTokenID()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		return nil, nil, fmt.Errorf("generate key ID: %w", err)
	}

	pemStr, err := rsaKeyToPEM(privateKey)
	if err != nil {
		// unreachable: MarshalPKCS1PrivateKey does not fail for valid RSA keys
		return nil, nil, fmt.Errorf("encode RSA key: %w", err)
	}

	keys := &poolKeys{KeyID: keyID, PrivateKeyPEM: pemStr}
	if err := s.writeJSON(keysPath(poolID), keys); err != nil {
		return nil, nil, fmt.Errorf("write pool keys: %w", err)
	}

	return keys, privateKey, nil
}
