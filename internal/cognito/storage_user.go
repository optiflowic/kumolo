package cognito

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	errUserNotFound   = errors.New("user not found")
	errUsernameExists = errors.New("username already exists")
)

// AttributeType is a name-value attribute pair matching the AWS AttributeType shape.
type AttributeType struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

// UserMetadata stores the full state of a Cognito user.
type UserMetadata struct {
	Username         string          `json:"Username"`
	Sub              string          `json:"Sub"`
	Status           string          `json:"Status"`
	Enabled          bool            `json:"Enabled"`
	PasswordHash     string          `json:"PasswordHash"`
	Attributes       []AttributeType `json:"Attributes"`
	ConfirmationCode string          `json:"ConfirmationCode"`
	// VerificationCodes holds pending attribute-verification codes keyed by
	// attribute name (e.g. "email", "phone_number"), set by UpdateUserAttributes
	// / GetUserAttributeVerificationCode and consumed by VerifyUserAttribute.
	VerificationCodes map[string]string `json:"VerificationCodes,omitempty"`
	// PasswordResetCode holds the pending code set by ForgotPassword and consumed
	// by ConfirmForgotPassword. Independent of ConfirmationCode/VerificationCodes.
	PasswordResetCode string  `json:"PasswordResetCode,omitempty"`
	CreatedAt         float64 `json:"CreatedAt"`
	UpdatedAt         float64 `json:"UpdatedAt"`
}

type userIndexEntry struct {
	Username string `json:"Username"`
	Sub      string `json:"Sub"`
}

// userIndexKey returns the filename (without extension) used to index a username within a pool.
func userIndexKey(username string) string {
	h := sha256.Sum256([]byte(strings.ToLower(username)))
	return fmt.Sprintf("%x", h)
}

func userIndexPath(poolID, username string) string {
	return filepath.Join("pools", poolID, "user_index", userIndexKey(username)+".json")
}

func userPath(poolID, sub string) string {
	return filepath.Join("pools", poolID, "users", sub+".json")
}

// CreateUser stores a new user and its username index entry.
func (s *Storage) CreateUser(poolID string, user *UserMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idxPath := userIndexPath(poolID, user.Username)

	if _, err := s.statFn(idxPath); err == nil {
		return errUsernameExists
	}

	usersDir := filepath.Join("pools", poolID, "users")
	if err := s.mkdirFn(usersDir, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create users dir: %w", err)
	}

	idxDir := filepath.Join("pools", poolID, "user_index")
	if err := s.mkdirFn(idxDir, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create user_index dir: %w", err)
	}

	uPath := userPath(poolID, user.Sub)
	if err := s.writeJSON(uPath, user); err != nil {
		return fmt.Errorf("write user: %w", err)
	}

	idx := userIndexEntry{Username: user.Username, Sub: user.Sub}
	if err := s.writeJSON(idxPath, idx); err != nil {
		if rbErr := s.removeFile(uPath); rbErr != nil {
			return errors.Join(
				fmt.Errorf("write user index: %w", err),
				fmt.Errorf("rollback: %w", rbErr),
			)
		}
		return fmt.Errorf("write user index: %w", err)
	}

	return nil
}

// GetUser retrieves a user by username.
func (s *Storage) GetUser(poolID, username string) (*UserMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getUserLocked(poolID, username)
}

func (s *Storage) getUserLocked(poolID, username string) (*UserMetadata, error) {
	idx, err := readJSON[userIndexEntry](s, userIndexPath(poolID, username))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errUserNotFound
		}
		return nil, fmt.Errorf("read user index: %w", err)
	}

	user, err := readJSON[UserMetadata](s, userPath(poolID, idx.Sub))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// unreachable: index exists but user file missing — only from external corruption
			return nil, errUserNotFound
		}
		return nil, fmt.Errorf("read user: %w", err)
	}
	return &user, nil
}

// GetUserBySub retrieves a user by their sub (UUID).
func (s *Storage) GetUserBySub(poolID, sub string) (*UserMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, err := readJSON[UserMetadata](s, userPath(poolID, sub))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errUserNotFound
		}
		return nil, fmt.Errorf("read user: %w", err)
	}
	return &user, nil
}

// DeleteUser removes a user and its username index entry from storage.
// Reading the index directly (not via getUserLocked) makes DeleteUser retryable
// after a partial failure: if the user file was removed but index removal failed,
// a retry can find the index, skip the already-gone user file, and finish the cleanup.
func (s *Storage) DeleteUser(poolID, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, err := readJSON[userIndexEntry](s, userIndexPath(poolID, username))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errUserNotFound
		}
		return fmt.Errorf("read user index: %w", err)
	}

	if err := s.removeFile(userPath(poolID, idx.Sub)); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove user: %w", err)
	}
	if err := s.removeFile(userIndexPath(poolID, username)); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove user index: %w", err)
	}
	return nil
}

// ListUsers returns a page of users in the pool matching filter, sorted by Username.
// filter may be nil to match all users.
func (s *Storage) ListUsers(
	poolID string,
	filter func(*UserMetadata) bool,
	maxResults int,
	nextToken string,
) ([]*UserMetadata, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := filepath.Join("pools", poolID, "users")
	entries, err := s.listDirFn(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("list users dir: %w", err)
	}

	var users []*UserMetadata
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		user, err := readJSON[UserMetadata](s, filepath.Join(dir, e.Name()))
		if err != nil {
			// untestable: dir entry exists but file is unreadable — only from external corruption
			continue
		}
		if filter == nil || filter(&user) {
			users = append(users, &user)
		}
	}
	sort.Slice(users, func(i, j int) bool {
		return users[i].Username < users[j].Username
	})

	if nextToken != "" {
		found := false
		for i, u := range users {
			if u.Username == nextToken {
				users = users[i+1:]
				found = true
				break
			}
		}
		if !found {
			return nil, "", errInvalidNextToken
		}
	}

	var retNextToken string
	if maxResults > 0 && len(users) > maxResults {
		retNextToken = users[maxResults-1].Username
		users = users[:maxResults]
	}
	return users, retNextToken, nil
}

// UpdateUser applies fn to the user and persists the result.
func (s *Storage) UpdateUser(poolID, username string, fn func(*UserMetadata) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.getUserLocked(poolID, username)
	if err != nil {
		return err
	}

	if err := fn(user); err != nil {
		return err
	}

	user.UpdatedAt = nowUnix()
	return s.writeJSON(userPath(poolID, user.Sub), user)
}
