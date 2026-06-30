package cognito

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// AccessJTI is the JTI of the access token issued in the same auth event.
	// Used by RevokeToken to revoke the paired access token.
	AccessJTI string `json:"AccessJTI,omitempty"`
}

// revokedJTIEntry records a revoked access token JTI with its expiry for future cleanup.
type revokedJTIEntry struct {
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

func revokedJTIPath(poolID, jti string) string {
	return filepath.Join("pools", poolID, "revoked_jtis", jti+".json")
}

func (s *Storage) ensureRevokedJTIsDir(poolID string) error {
	dir := filepath.Join("pools", poolID, "revoked_jtis")
	if err := s.mkdirFn(dir, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create revoked_jtis dir: %w", err)
	}
	return nil
}

// RevokeAccessToken marks an access token JTI as revoked.
// expiresAt is the token's exp claim; stored for future cleanup.
func (s *Storage) RevokeAccessToken(poolID, jti string, expiresAt float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureRevokedJTIsDir(poolID); err != nil {
		return err
	}
	return s.writeJSON(revokedJTIPath(poolID, jti), revokedJTIEntry{ExpiresAt: expiresAt})
}

// IsAccessTokenRevoked reports whether the given JTI has been explicitly revoked.
func (s *Storage) IsAccessTokenRevoked(poolID, jti string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, err := s.statFn(revokedJTIPath(poolID, jti))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("check revoked JTI: %w", err)
}

// DeleteRefreshTokensBySub deletes all refresh tokens belonging to the given user sub.
func (s *Storage) DeleteRefreshTokensBySub(poolID, sub string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join("pools", poolID, "refresh_tokens")
	entries, err := s.listDirFn(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("list refresh tokens: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		token := strings.TrimSuffix(entry.Name(), ".json")
		rt, rerr := readJSON[refreshTokenData](s, refreshTokenPath(poolID, token))
		if rerr != nil {
			continue // untestable: silently skips entries that cannot be deserialized
		}
		if rt.Sub == sub {
			_ = s.removeFile(refreshTokenPath(poolID, token))
		}
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
