package cognito

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var errUserPoolClientNotFound = errors.New("user pool client not found")

// UserPoolClientMetadata holds the full state of a Cognito app client.
// JSON tags match the AWS UserPoolClientType shape for direct serialization.
type UserPoolClientMetadata struct {
	UserPoolID                               string          `json:"UserPoolId"`
	ClientID                                 string          `json:"ClientId"`
	ClientName                               string          `json:"ClientName"`
	ClientSecret                             string          `json:"ClientSecret,omitempty"`
	CreationDate                             float64         `json:"CreationDate"`
	LastModifiedDate                         float64         `json:"LastModifiedDate"`
	RefreshTokenValidity                     int             `json:"RefreshTokenValidity,omitempty"`
	AccessTokenValidity                      int             `json:"AccessTokenValidity,omitempty"`
	IdTokenValidity                          int             `json:"IdTokenValidity,omitempty"`
	AuthSessionValidity                      int             `json:"AuthSessionValidity,omitempty"`
	TokenValidityUnits                       json.RawMessage `json:"TokenValidityUnits,omitempty"`
	ExplicitAuthFlows                        []string        `json:"ExplicitAuthFlows,omitempty"`
	AllowedOAuthFlows                        []string        `json:"AllowedOAuthFlows,omitempty"`
	AllowedOAuthScopes                       []string        `json:"AllowedOAuthScopes,omitempty"`
	AllowedOAuthFlowsUserPoolClient          bool            `json:"AllowedOAuthFlowsUserPoolClient"`
	CallbackURLs                             []string        `json:"CallbackURLs,omitempty"`
	LogoutURLs                               []string        `json:"LogoutURLs,omitempty"`
	DefaultRedirectURI                       string          `json:"DefaultRedirectURI,omitempty"`
	SupportedIdentityProviders               []string        `json:"SupportedIdentityProviders,omitempty"`
	ReadAttributes                           []string        `json:"ReadAttributes,omitempty"`
	WriteAttributes                          []string        `json:"WriteAttributes,omitempty"`
	PreventUserExistenceErrors               string          `json:"PreventUserExistenceErrors,omitempty"`
	EnableTokenRevocation                    bool            `json:"EnableTokenRevocation"`
	EnablePropagateAdditionalUserContextData bool            `json:"EnablePropagateAdditionalUserContextData"`
	AnalyticsConfiguration                   json.RawMessage `json:"AnalyticsConfiguration,omitempty"`
	RefreshTokenRotation                     json.RawMessage `json:"RefreshTokenRotation,omitempty"`
}

func (s *Storage) CreateUserPoolClient(meta *UserPoolClientMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.getUserPoolLocked(meta.UserPoolID); err != nil {
		return err
	}

	clientsDir := filepath.Join("pools", meta.UserPoolID, "clients")
	if err := s.mkdirFn(clientsDir, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create clients dir: %w", err)
	}
	return s.writeJSON(filepath.Join(clientsDir, meta.ClientID+".json"), meta)
}

func (s *Storage) GetUserPoolClient(poolID, clientID string) (*UserPoolClientMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getPoolClientLocked(poolID, clientID)
}

func (s *Storage) getPoolClientLocked(poolID, clientID string) (*UserPoolClientMetadata, error) {
	path := filepath.Join("pools", poolID, "clients", clientID+".json")
	meta, err := readJSON[UserPoolClientMetadata](s, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errUserPoolClientNotFound
		}
		return nil, err
	}
	return &meta, nil
}

func (s *Storage) UpdateUserPoolClient(
	poolID, clientID string,
	fn func(*UserPoolClientMetadata) error,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.getPoolClientLocked(poolID, clientID)
	if err != nil {
		return err
	}
	if err := fn(meta); err != nil {
		return err
	}
	meta.LastModifiedDate = nowUnix()
	return s.writeJSON(filepath.Join("pools", poolID, "clients", clientID+".json"), meta)
}

func (s *Storage) DeleteUserPoolClient(poolID, clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join("pools", poolID, "clients", clientID+".json")
	if err := s.removeFile(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errUserPoolClientNotFound
		}
		return err
	}
	return nil
}

func (s *Storage) ListUserPoolClients(
	poolID string,
	maxResults int,
	nextToken string,
) ([]*UserPoolClientMetadata, string, error) {
	if maxResults <= 0 {
		return nil, "", fmt.Errorf("maxResults must be positive, got %d", maxResults)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, err := s.getUserPoolLocked(poolID); err != nil {
		return nil, "", err
	}

	clientsDir := filepath.Join("pools", poolID, "clients")
	entries, err := s.listDirFn(clientsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", err
	}

	var clientIDs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			clientIDs = append(clientIDs, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(clientIDs)

	if nextToken != "" {
		found := false
		for i, id := range clientIDs {
			if id == nextToken {
				clientIDs = clientIDs[i+1:]
				found = true
				break
			}
		}
		if !found {
			return nil, "", errInvalidNextToken
		}
	}

	var retNextToken string
	if len(clientIDs) > maxResults {
		retNextToken = clientIDs[maxResults-1]
		clientIDs = clientIDs[:maxResults]
	}

	clients := make([]*UserPoolClientMetadata, 0, len(clientIDs))
	for _, id := range clientIDs {
		meta, err := readJSON[UserPoolClientMetadata](s, filepath.Join(clientsDir, id+".json"))
		if err != nil {
			return nil, "", fmt.Errorf("read user pool client %q: %w", id, err)
		}
		clients = append(clients, &meta)
	}
	return clients, retNextToken, nil
}

// deleteClientsDirLocked removes all client files and the clients directory for the given pool.
// It is a no-op when the clients directory does not exist.
func (s *Storage) deleteClientsDirLocked(poolID string) error {
	clientsDir := filepath.Join("pools", poolID, "clients")
	entries, err := s.listDirFn(clientsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := s.removeFile(filepath.Join(clientsDir, e.Name())); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := s.removeFile(clientsDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
