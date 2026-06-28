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
	errGroupNotFound = errors.New("group not found")
	errGroupExists   = errors.New("group already exists")
)

// GroupMetadata stores the persistent state of a Cognito user pool group.
// JSON tags match the AWS GroupType shape for direct serialization.
type GroupMetadata struct {
	GroupName        string  `json:"GroupName"`
	UserPoolId       string  `json:"UserPoolId"`
	Description      string  `json:"Description,omitempty"`
	Precedence       *int    `json:"Precedence,omitempty"`
	RoleArn          string  `json:"RoleArn,omitempty"`
	CreationDate     float64 `json:"CreationDate"`
	LastModifiedDate float64 `json:"LastModifiedDate"`
}

// groupKey returns a safe filename (no extension) for a group name.
func groupKey(groupName string) string {
	h := sha256.Sum256([]byte(groupName))
	return fmt.Sprintf("%x", h)
}

func groupPath(poolID, groupName string) string {
	return filepath.Join("pools", poolID, "groups", groupKey(groupName)+".json")
}

// memberPath is the file that marks user's membership in a group.
func memberPath(poolID, groupName, username string) string {
	return filepath.Join(
		"pools", poolID, "group_members",
		groupKey(groupName), groupKey(username)+".json",
	)
}

// userGroupPath is the reverse index: user → groups.
func userGroupPath(poolID, username, groupName string) string {
	return filepath.Join(
		"pools", poolID, "user_groups",
		groupKey(username), groupKey(groupName)+".json",
	)
}

// memberMarker is stored in each member file so we can recover GroupName/Username without scanning.
type memberMarker struct {
	Username  string `json:"Username"`
	GroupName string `json:"GroupName"`
}

// userGroupMarker is stored in user_groups index files.
type userGroupMarker struct {
	Username  string `json:"Username"`
	GroupName string `json:"GroupName"`
}

// CreateGroup persists a new group. Returns errGroupExists if the name is taken.
func (s *Storage) CreateGroup(poolID string, group *GroupMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	gPath := groupPath(poolID, group.GroupName)
	if _, err := s.statFn(gPath); err == nil {
		return errGroupExists
	}

	dir := filepath.Join("pools", poolID, "groups")
	if err := s.mkdirFn(dir, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create groups dir: %w", err)
	}

	return s.writeJSON(gPath, group)
}

// GetGroup retrieves a group by name.
func (s *Storage) GetGroup(poolID, groupName string) (*GroupMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getGroupLocked(poolID, groupName)
}

func (s *Storage) getGroupLocked(poolID, groupName string) (*GroupMetadata, error) {
	g, err := readJSON[GroupMetadata](s, groupPath(poolID, groupName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errGroupNotFound
		}
		return nil, fmt.Errorf("read group: %w", err)
	}
	return &g, nil
}

// UpdateGroup applies fn to the group and persists the result.
func (s *Storage) UpdateGroup(poolID, groupName string, fn func(*GroupMetadata) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	group, err := s.getGroupLocked(poolID, groupName)
	if err != nil {
		return err
	}
	if err := fn(group); err != nil {
		return err
	}
	group.LastModifiedDate = nowUnix()
	return s.writeJSON(groupPath(poolID, groupName), group)
}

// DeleteGroup removes a group and cleans up its membership indexes so that
// recreating a group with the same name does not inherit the old members.
func (s *Storage) DeleteGroup(poolID, groupName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	gPath := groupPath(poolID, groupName)
	if _, err := s.statFn(gPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errGroupNotFound
		}
		return fmt.Errorf("stat group: %w", err)
	}

	// Remove per-user reverse-index entries before deleting the group.
	memberDir := filepath.Join("pools", poolID, "group_members", groupKey(groupName))
	memberEntries, err := s.listDirFn(memberDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("list group members: %w", err)
	}
	for _, e := range memberEntries {
		if e.IsDir() {
			continue
		}
		// The member filename is groupKey(username)+".json", so we can derive the
		// reverse-index path without reading the marker JSON — this handles corrupted
		// marker files without leaking stale user_groups entries.
		userKey := strings.TrimSuffix(e.Name(), ".json")
		ugPath := filepath.Join("pools", poolID, "user_groups", userKey, groupKey(groupName)+".json")
		if err := s.removeFile(ugPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove user_groups index: %w", err)
		}
	}
	if err := s.deleteFlatDirLocked(memberDir); err != nil {
		return fmt.Errorf("remove group_members dir: %w", err)
	}

	return s.removeFile(gPath)
}

// ListGroups returns a page of groups in the pool, sorted by GroupName.
func (s *Storage) ListGroups(poolID string, maxResults int, nextToken string) (
	[]*GroupMetadata, string, error,
) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := filepath.Join("pools", poolID, "groups")
	entries, err := s.listDirFn(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("list groups dir: %w", err)
	}

	var groups []*GroupMetadata
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		g, err := readJSON[GroupMetadata](s, filepath.Join(dir, e.Name()))
		if err != nil {
			// untestable: dir entry exists but file is unreadable — only from external corruption
			continue
		}
		groups = append(groups, &g)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].GroupName < groups[j].GroupName
	})

	if nextToken != "" {
		found := false
		for i, g := range groups {
			if g.GroupName == nextToken {
				groups = groups[i+1:]
				found = true
				break
			}
		}
		if !found {
			return nil, "", errInvalidNextToken
		}
	}

	var retNextToken string
	if maxResults > 0 && len(groups) > maxResults {
		retNextToken = groups[maxResults-1].GroupName
		groups = groups[:maxResults]
	}
	return groups, retNextToken, nil
}

// AddUserToGroup records membership in both the group-members and user-groups indices.
func (s *Storage) AddUserToGroup(poolID, groupName, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure parent dirs exist before creating the per-group/per-user subdirectory.
	groupMembersRoot := filepath.Join("pools", poolID, "group_members")
	if err := s.mkdirFn(groupMembersRoot, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create group_members root: %w", err)
	}
	memberDir := filepath.Join(groupMembersRoot, groupKey(groupName))
	if err := s.mkdirFn(memberDir, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create group_members dir: %w", err)
	}

	userGroupsRoot := filepath.Join("pools", poolID, "user_groups")
	if err := s.mkdirFn(userGroupsRoot, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create user_groups root: %w", err)
	}
	userGroupDir := filepath.Join(userGroupsRoot, groupKey(username))
	if err := s.mkdirFn(userGroupDir, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create user_groups dir: %w", err)
	}

	mPath := memberPath(poolID, groupName, username)
	if err := s.writeJSON(
		mPath,
		memberMarker{Username: username, GroupName: groupName},
	); err != nil {
		return fmt.Errorf("write member: %w", err)
	}

	ugPath := userGroupPath(poolID, username, groupName)
	if err := s.writeJSON(
		ugPath,
		userGroupMarker{Username: username, GroupName: groupName},
	); err != nil {
		if rbErr := s.removeFile(mPath); rbErr != nil {
			return fmt.Errorf("write user_groups index: %w (rollback: %v)", err, rbErr)
		}
		return fmt.Errorf("write user_groups index: %w", err)
	}
	return nil
}

// RemoveUserFromGroup deletes membership records. Silently succeeds if the user is not a member.
func (s *Storage) RemoveUserFromGroup(poolID, groupName, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.removeFile(memberPath(poolID, groupName, username)); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove member: %w", err)
	}
	if err := s.removeFile(userGroupPath(poolID, username, groupName)); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove user_groups index: %w", err)
	}
	return nil
}

// ListGroupsForUser returns a page of groups the user belongs to, sorted by GroupName.
func (s *Storage) ListGroupsForUser(poolID, username string, maxResults int, nextToken string) (
	[]*GroupMetadata, string, error,
) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := filepath.Join("pools", poolID, "user_groups", groupKey(username))
	entries, err := s.listDirFn(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("list user_groups dir: %w", err)
	}

	var groups []*GroupMetadata
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		marker, err := readJSON[userGroupMarker](s, filepath.Join(dir, e.Name()))
		if err != nil {
			// untestable: only from external corruption
			continue
		}
		g, err := s.getGroupLocked(poolID, marker.GroupName)
		if err != nil {
			if errors.Is(err, errGroupNotFound) {
				continue // stale membership record; group was deleted
			}
			return nil, "", fmt.Errorf("read group %q: %w", marker.GroupName, err)
		}
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].GroupName < groups[j].GroupName
	})

	if nextToken != "" {
		found := false
		for i, g := range groups {
			if g.GroupName == nextToken {
				groups = groups[i+1:]
				found = true
				break
			}
		}
		if !found {
			return nil, "", errInvalidNextToken
		}
	}

	var retNextToken string
	if maxResults > 0 && len(groups) > maxResults {
		retNextToken = groups[maxResults-1].GroupName
		groups = groups[:maxResults]
	}
	return groups, retNextToken, nil
}

// GetGroupsForUser returns all group names for a user (used for JWT cognito:groups claim).
func (s *Storage) GetGroupsForUser(poolID, username string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := filepath.Join("pools", poolID, "user_groups", groupKey(username))
	entries, err := s.listDirFn(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		marker, err := readJSON[userGroupMarker](s, filepath.Join(dir, e.Name()))
		if err != nil {
			// untestable: only from external corruption
			continue
		}
		// Only include groups that still exist.
		_, gErr := s.getGroupLocked(poolID, marker.GroupName)
		if gErr == nil {
			names = append(names, marker.GroupName)
		} else if !errors.Is(gErr, errGroupNotFound) {
			return nil, fmt.Errorf("read group %q: %w", marker.GroupName, gErr)
		}
	}
	sort.Strings(names)
	return names, nil
}

// ListUsersInGroup returns a page of users in the group, sorted by Username.
func (s *Storage) ListUsersInGroup(poolID, groupName string, maxResults int, nextToken string) (
	[]*UserMetadata, string, error,
) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := filepath.Join("pools", poolID, "group_members", groupKey(groupName))
	entries, err := s.listDirFn(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("list group_members dir: %w", err)
	}

	var users []*UserMetadata
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		marker, err := readJSON[memberMarker](s, filepath.Join(dir, e.Name()))
		if err != nil {
			// untestable: only from external corruption
			continue
		}
		user, err := s.getUserLocked(poolID, marker.Username)
		if err != nil {
			if errors.Is(err, errUserNotFound) {
				continue // stale membership record; user was deleted
			}
			return nil, "", fmt.Errorf("read user %q: %w", marker.Username, err)
		}
		users = append(users, user)
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
