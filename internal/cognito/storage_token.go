package cognito

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errRefreshTokenNotFound = errors.New("refresh token not found")

// refreshTokenData stores the context for an issued refresh token.
type refreshTokenData struct {
	Token     string  `json:"Token"`
	PoolID    string  `json:"PoolID"`
	ClientID  string  `json:"ClientID"`
	Username  string  `json:"Username"`
	Sub       string  `json:"Sub"`
	IssuedAt  float64 `json:"IssuedAt"`
	ExpiresAt float64 `json:"ExpiresAt"`
}

// clientIndexEntry maps a client ID to its pool ID for efficient cross-pool lookup.
type clientIndexEntry struct {
	PoolID   string `json:"PoolID"`
	ClientID string `json:"ClientID"`
}

// generateTokenID returns a random 256-bit hex string for use as a token or key ID.
func generateTokenID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// untestable: crypto/rand.Read never errors in Go 1.20+
		return "", fmt.Errorf("read entropy: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func refreshTokenPath(poolID, token string) string {
	return filepath.Join("pools", poolID, "refresh_tokens", token+".json")
}

func (s *Storage) ensureRefreshTokensDir(poolID string) error {
	dir := filepath.Join("pools", poolID, "refresh_tokens")
	if err := s.mkdirFn(dir, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create refresh_tokens dir: %w", err)
	}
	return nil
}

// CreateRefreshToken issues a new opaque refresh token for the given user session.
func (s *Storage) CreateRefreshToken(data *refreshTokenData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureRefreshTokensDir(data.PoolID); err != nil {
		return err
	}
	return s.writeJSON(refreshTokenPath(data.PoolID, data.Token), data)
}

// GetRefreshToken looks up a refresh token within a specific pool.
func (s *Storage) GetRefreshToken(poolID, token string) (*refreshTokenData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rt, err := readJSON[refreshTokenData](s, refreshTokenPath(poolID, token))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errRefreshTokenNotFound
		}
		return nil, fmt.Errorf("read refresh token: %w", err)
	}
	return &rt, nil
}

// DeleteRefreshToken removes a refresh token (used for logout/revocation).
func (s *Storage) DeleteRefreshToken(poolID, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.removeFile(refreshTokenPath(poolID, token)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errRefreshTokenNotFound
		}
		return fmt.Errorf("remove refresh token: %w", err)
	}
	return nil
}

// writeClientIndexLocked records that clientID belongs to poolID.
// Callers must hold s.mu.Lock().
func (s *Storage) writeClientIndexLocked(poolID, clientID string) error {
	entry := clientIndexEntry{PoolID: poolID, ClientID: clientID}
	return s.writeJSON(filepath.Join("client_index", clientID+".json"), entry)
}

// deleteClientIndexLocked removes the client index entry for a given client ID.
// Callers must hold s.mu.Lock().
func (s *Storage) deleteClientIndexLocked(clientID string) error {
	path := filepath.Join("client_index", clientID+".json")
	if err := s.removeFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove client index: %w", err)
	}
	return nil
}

// GetPoolIDForClient looks up which pool a client belongs to.
func (s *Storage) GetPoolIDForClient(clientID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, err := readJSON[clientIndexEntry](s, filepath.Join("client_index", clientID+".json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errUserPoolClientNotFound
		}
		return "", fmt.Errorf("read client index: %w", err)
	}
	return entry.PoolID, nil
}
