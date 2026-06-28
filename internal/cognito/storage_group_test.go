package cognito

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupStorageGroup(t *testing.T, s *Storage, poolID, groupName string) {
	t.Helper()
	ts := nowUnix()
	require.NoError(t, s.CreateGroup(poolID, &GroupMetadata{
		GroupName:        groupName,
		UserPoolId:       poolID,
		CreationDate:     ts,
		LastModifiedDate: ts,
	}))
}

func setupStorageUser(t *testing.T, s *Storage, poolID, username, sub string) {
	t.Helper()
	ts := nowUnix()
	require.NoError(t, s.CreateUser(poolID, &UserMetadata{
		Username:  username,
		Sub:       sub,
		Status:    userStatusConfirmed,
		CreatedAt: ts,
		UpdatedAt: ts,
	}))
}

// ── CreateGroup ───────────────────────────────────────────────────────────────

func TestStorageCreateGroup_Success(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	ts := nowUnix()
	err := s.CreateGroup(poolID, &GroupMetadata{
		GroupName:        "admins",
		UserPoolId:       poolID,
		CreationDate:     ts,
		LastModifiedDate: ts,
	})
	require.NoError(t, err)
}

func TestStorageCreateGroup_Duplicate(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")

	ts := nowUnix()
	err := s.CreateGroup(poolID, &GroupMetadata{
		GroupName:        "admins",
		UserPoolId:       poolID,
		CreationDate:     ts,
		LastModifiedDate: ts,
	})
	require.ErrorIs(t, err, errGroupExists)
}

func TestStorageCreateGroup_MkdirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	s.mkdirFn = func(string, os.FileMode) error { return errors.New("mkdir failed") }
	ts := nowUnix()
	err := s.CreateGroup(poolID, &GroupMetadata{
		GroupName: "admins", UserPoolId: poolID,
		CreationDate: ts, LastModifiedDate: ts,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create groups dir")
}

// ── GetGroup ──────────────────────────────────────────────────────────────────

func TestStorageGetGroup_Success(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")

	g, err := s.GetGroup(poolID, "admins")
	require.NoError(t, err)
	assert.Equal(t, "admins", g.GroupName)
	assert.Equal(t, poolID, g.UserPoolId)
}

func TestStorageGetGroup_NotFound(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	_, err := s.GetGroup(poolID, "nonexistent")
	require.ErrorIs(t, err, errGroupNotFound)
}

// ── UpdateGroup ───────────────────────────────────────────────────────────────

func TestStorageUpdateGroup_Success(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")

	prec := 5
	err := s.UpdateGroup(poolID, "admins", func(g *GroupMetadata) error {
		g.Description = "Admin group"
		g.Precedence = &prec
		return nil
	})
	require.NoError(t, err)

	g, err := s.GetGroup(poolID, "admins")
	require.NoError(t, err)
	assert.Equal(t, "Admin group", g.Description)
	require.NotNil(t, g.Precedence)
	assert.Equal(t, 5, *g.Precedence)
}

func TestStorageUpdateGroup_NotFound(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	err := s.UpdateGroup(poolID, "nonexistent", func(g *GroupMetadata) error { return nil })
	require.ErrorIs(t, err, errGroupNotFound)
}

func TestStorageUpdateGroup_FnError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")

	err := s.UpdateGroup(poolID, "admins", func(g *GroupMetadata) error {
		return errors.New("validation error")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation error")
}

// ── DeleteGroup ───────────────────────────────────────────────────────────────

func TestStorageDeleteGroup_Success(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")

	require.NoError(t, s.DeleteGroup(poolID, "admins"))

	_, err := s.GetGroup(poolID, "admins")
	require.ErrorIs(t, err, errGroupNotFound)
}

func TestStorageDeleteGroup_NotFound(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	err := s.DeleteGroup(poolID, "nonexistent")
	require.ErrorIs(t, err, errGroupNotFound)
}

// ── ListGroups ────────────────────────────────────────────────────────────────

func TestStorageListGroups_Empty(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	groups, nextToken, err := s.ListGroups(poolID, 60, "")
	require.NoError(t, err)
	assert.Empty(t, groups)
	assert.Empty(t, nextToken)
}

func TestStorageListGroups_SortedAndPaginated(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	for _, name := range []string{"zebra", "alpha", "beta"} {
		setupStorageGroup(t, s, poolID, name)
	}

	// First page: limit 2.
	groups, nextToken, err := s.ListGroups(poolID, 2, "")
	require.NoError(t, err)
	require.Len(t, groups, 2)
	assert.Equal(t, "alpha", groups[0].GroupName)
	assert.Equal(t, "beta", groups[1].GroupName)
	assert.Equal(t, "beta", nextToken)

	// Second page.
	groups2, nextToken2, err := s.ListGroups(poolID, 2, nextToken)
	require.NoError(t, err)
	require.Len(t, groups2, 1)
	assert.Equal(t, "zebra", groups2[0].GroupName)
	assert.Empty(t, nextToken2)
}

func TestStorageListGroups_InvalidNextToken(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")

	_, _, err := s.ListGroups(poolID, 60, "nonexistent-token")
	require.ErrorIs(t, err, errInvalidNextToken)
}

// ── AddUserToGroup / RemoveUserFromGroup ──────────────────────────────────────

func TestStorageAddRemoveUserToGroup(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")

	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	// User appears in group.
	users, _, err := s.ListUsersInGroup(poolID, "admins", 60, "")
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "alice", users[0].Username)

	// Group appears for user.
	groups, _, err := s.ListGroupsForUser(poolID, "alice", 60, "")
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "admins", groups[0].GroupName)

	// Remove.
	require.NoError(t, s.RemoveUserFromGroup(poolID, "admins", "alice"))

	users2, _, err := s.ListUsersInGroup(poolID, "admins", 60, "")
	require.NoError(t, err)
	assert.Empty(t, users2)
}

func TestStorageAddUserToGroup_Idempotent(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")

	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	users, _, err := s.ListUsersInGroup(poolID, "admins", 60, "")
	require.NoError(t, err)
	assert.Len(t, users, 1)
}

func TestStorageRemoveUserFromGroup_Idempotent(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")

	// Removing a non-member is silent.
	require.NoError(t, s.RemoveUserFromGroup(poolID, "admins", "alice"))
}

// ── ListUsersInGroup ──────────────────────────────────────────────────────────

func TestStorageListUsersInGroup_SortedAndPaginated(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "charlie", "sub-c")
	setupStorageUser(t, s, poolID, "alice", "sub-a")
	setupStorageUser(t, s, poolID, "bob", "sub-b")
	for _, u := range []string{"charlie", "alice", "bob"} {
		require.NoError(t, s.AddUserToGroup(poolID, "admins", u))
	}

	users, nextToken, err := s.ListUsersInGroup(poolID, "admins", 2, "")
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "alice", users[0].Username)
	assert.Equal(t, "bob", users[1].Username)
	assert.Equal(t, "bob", nextToken)

	users2, nextToken2, err := s.ListUsersInGroup(poolID, "admins", 2, nextToken)
	require.NoError(t, err)
	require.Len(t, users2, 1)
	assert.Equal(t, "charlie", users2[0].Username)
	assert.Empty(t, nextToken2)
}

// ── AdminListGroupsForUser ────────────────────────────────────────────────────

func TestStorageListGroupsForUser_SortedAndPaginated(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	for _, name := range []string{"zebra", "alpha", "beta"} {
		setupStorageGroup(t, s, poolID, name)
	}
	setupStorageUser(t, s, poolID, "alice", "sub-a")
	for _, name := range []string{"zebra", "alpha", "beta"} {
		require.NoError(t, s.AddUserToGroup(poolID, name, "alice"))
	}

	groups, nextToken, err := s.ListGroupsForUser(poolID, "alice", 2, "")
	require.NoError(t, err)
	require.Len(t, groups, 2)
	assert.Equal(t, "alpha", groups[0].GroupName)
	assert.Equal(t, "beta", groups[1].GroupName)
	assert.Equal(t, "beta", nextToken)

	groups2, nextToken2, err := s.ListGroupsForUser(poolID, "alice", 2, nextToken)
	require.NoError(t, err)
	require.Len(t, groups2, 1)
	assert.Equal(t, "zebra", groups2[0].GroupName)
	assert.Empty(t, nextToken2)
}

// ── GetGroupsForUser ──────────────────────────────────────────────────────────

func TestStorageGetGroupsForUser_SortedNames(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	for _, name := range []string{"zebra", "alpha"} {
		setupStorageGroup(t, s, poolID, name)
	}
	setupStorageUser(t, s, poolID, "alice", "sub-a")
	require.NoError(t, s.AddUserToGroup(poolID, "zebra", "alice"))
	require.NoError(t, s.AddUserToGroup(poolID, "alpha", "alice"))

	names, err := s.GetGroupsForUser(poolID, "alice")
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "zebra"}, names)
}

func TestStorageGetGroupsForUser_DeletedGroupSkipped(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-a")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))
	require.NoError(t, s.DeleteGroup(poolID, "admins"))

	names, err := s.GetGroupsForUser(poolID, "alice")
	require.NoError(t, err)
	assert.Empty(t, names)
}

func TestStorageGetGroupsForUser_NoGroups(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	names, err := s.GetGroupsForUser(poolID, "alice")
	require.NoError(t, err)
	assert.Nil(t, names)
}
