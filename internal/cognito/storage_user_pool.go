package cognito

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

var errUserPoolNotFound = errors.New("user pool not found")

const (
	poolRegion  = "us-east-1"
	poolAccount = "000000000000"
)

// UserPoolMetadata stores the full state of a Cognito user pool.
// JSON tags match the AWS API UserPoolType shape for direct serialization.
type UserPoolMetadata struct {
	ID                          string            `json:"Id"`
	Name                        string            `json:"Name"`
	Arn                         string            `json:"Arn"`
	Status                      string            `json:"Status"`
	CreationDate                float64           `json:"CreationDate"`
	LastModifiedDate            float64           `json:"LastModifiedDate"`
	EstimatedNumberOfUsers      int               `json:"EstimatedNumberOfUsers"`
	MfaConfiguration            string            `json:"MfaConfiguration,omitempty"`
	DeletionProtection          string            `json:"DeletionProtection,omitempty"`
	SchemaAttributes            json.RawMessage   `json:"SchemaAttributes,omitempty"`
	AliasAttributes             []string          `json:"AliasAttributes,omitempty"`
	AutoVerifiedAttributes      []string          `json:"AutoVerifiedAttributes,omitempty"`
	UsernameAttributes          []string          `json:"UsernameAttributes,omitempty"`
	UsernameConfiguration       json.RawMessage   `json:"UsernameConfiguration,omitempty"`
	Policies                    json.RawMessage   `json:"Policies,omitempty"`
	LambdaConfig                json.RawMessage   `json:"LambdaConfig,omitempty"`
	EmailConfiguration          json.RawMessage   `json:"EmailConfiguration,omitempty"`
	SmsConfiguration            json.RawMessage   `json:"SmsConfiguration,omitempty"`
	DeviceConfiguration         json.RawMessage   `json:"DeviceConfiguration,omitempty"`
	AdminCreateUserConfig       json.RawMessage   `json:"AdminCreateUserConfig,omitempty"`
	AccountRecoverySetting      json.RawMessage   `json:"AccountRecoverySetting,omitempty"`
	UserAttributeUpdateSettings json.RawMessage   `json:"UserAttributeUpdateSettings,omitempty"`
	UserPoolAddOns              json.RawMessage   `json:"UserPoolAddOns,omitempty"`
	VerificationMessageTemplate json.RawMessage   `json:"VerificationMessageTemplate,omitempty"`
	UserPoolTags                map[string]string `json:"UserPoolTags,omitempty"`
	UserPoolTier                string            `json:"UserPoolTier,omitempty"`
	EmailVerificationMessage    string            `json:"EmailVerificationMessage,omitempty"`
	EmailVerificationSubject    string            `json:"EmailVerificationSubject,omitempty"`
	SmsAuthenticationMessage    string            `json:"SmsAuthenticationMessage,omitempty"`
	SmsVerificationMessage      string            `json:"SmsVerificationMessage,omitempty"`
}

func poolARN(poolID string) string {
	return fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s", poolRegion, poolAccount, poolID)
}

func nowUnix() float64 {
	return float64(time.Now().UnixMilli()) / 1000.0
}

func (s *Storage) CreateUserPool(meta *UserPoolMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join("pools", meta.ID)
	if err := s.mkdirFn(dir, 0o750); err != nil {
		return fmt.Errorf("create pool dir: %w", err)
	}
	return s.writeJSON(filepath.Join(dir, "meta.json"), meta)
}

func (s *Storage) GetUserPool(poolID string) (*UserPoolMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getUserPoolLocked(poolID)
}

func (s *Storage) getUserPoolLocked(poolID string) (*UserPoolMetadata, error) {
	path := filepath.Join("pools", poolID, "meta.json")
	meta, err := readJSON[UserPoolMetadata](s, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errUserPoolNotFound
		}
		return nil, err
	}
	return &meta, nil
}

func (s *Storage) UpdateUserPool(poolID string, fn func(*UserPoolMetadata) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.getUserPoolLocked(poolID)
	if err != nil {
		return err
	}
	if err := fn(meta); err != nil {
		return err
	}
	meta.LastModifiedDate = nowUnix()
	return s.writeJSON(filepath.Join("pools", poolID, "meta.json"), meta)
}

func (s *Storage) DeleteUserPool(poolID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	metaPath := filepath.Join("pools", poolID, "meta.json")
	if err := s.removeFile(metaPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errUserPoolNotFound
		}
		return err
	}
	if err := s.removeFile(filepath.Join("pools", poolID)); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Storage) ListUserPools(
	maxResults int,
	nextToken string,
) ([]*UserPoolMetadata, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := s.listDirFn("pools")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", err
	}

	var poolIDs []string
	for _, e := range entries {
		if e.IsDir() {
			poolIDs = append(poolIDs, e.Name())
		}
	}
	sort.Strings(poolIDs)

	if nextToken != "" {
		start := len(poolIDs)
		for i, id := range poolIDs {
			if id == nextToken {
				start = i + 1
				break
			}
		}
		poolIDs = poolIDs[start:]
	}

	var retNextToken string
	if len(poolIDs) > maxResults {
		retNextToken = poolIDs[maxResults-1]
		poolIDs = poolIDs[:maxResults]
	}

	pools := make([]*UserPoolMetadata, 0, len(poolIDs))
	for _, id := range poolIDs {
		meta, err := readJSON[UserPoolMetadata](s, filepath.Join("pools", id, "meta.json"))
		if err != nil {
			// untestable: dir exists but meta.json is unreadable — only from external corruption
			continue
		}
		pools = append(pools, &meta)
	}
	return pools, retNextToken, nil
}
