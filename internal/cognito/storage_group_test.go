package cognito

import (
	"errors"
	"io"
	"os"
	"path/filepath"
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

// ── getGroupLocked read error ──────────────────────────────────────────────────

func TestStorageGetGroup_ReadError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")

	readErr := errors.New("read failed")
	s.readAll = func(io.Reader) ([]byte, error) { return nil, readErr }
	_, err := s.GetGroup(poolID, "admins")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read group")
}

// ── DeleteGroup stat error ─────────────────────────────────────────────────────

func TestStorageDeleteGroup_StatError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")

	statErr := errors.New("stat failed")
	s.statFn = func(string) (os.FileInfo, error) { return nil, statErr }
	err := s.DeleteGroup(poolID, "admins")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stat group")
}

func TestStorageDeleteGroup_MembershipCleanupOnRecreate(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	require.NoError(t, s.DeleteGroup(poolID, "admins"))

	// Recreate with the same name — alice must NOT appear as a member.
	setupStorageGroup(t, s, poolID, "admins")
	users, _, err := s.ListUsersInGroup(poolID, "admins", 60, "")
	require.NoError(t, err)
	assert.Empty(t, users)

	// alice's group list must also be empty.
	names, err := s.GetGroupsForUser(poolID, "alice")
	require.NoError(t, err)
	assert.Empty(t, names)
}

func TestStorageDeleteGroup_ListMembersError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	listErr := errors.New("listdir failed")
	realListDir := s.listDirFn
	memberDir := filepath.Join("pools", poolID, "group_members", groupKey("admins"))
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == memberDir {
			return nil, listErr
		}
		return realListDir(name)
	}
	err := s.DeleteGroup(poolID, "admins")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list group members")
}

func TestStorageDeleteGroup_RemoveUserGroupIndexError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	removeErr := errors.New("remove failed")
	realRemove := s.removeFile
	ugPath := userGroupPath(poolID, "alice", "admins")
	s.removeFile = func(name string) error {
		if name == ugPath {
			return removeErr
		}
		return realRemove(name)
	}
	err := s.DeleteGroup(poolID, "admins")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove user_groups index")
}

// ── ListGroups listDirFn error ─────────────────────────────────────────────────

func TestStorageListGroups_ListDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")

	listErr := errors.New("listdir failed")
	s.listDirFn = func(string) ([]os.DirEntry, error) { return nil, listErr }
	_, _, err := s.ListGroups(poolID, 60, "")
	require.ErrorIs(t, err, listErr)
}

func TestStorageListGroups_ZeroLimit(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		setupStorageGroup(t, s, poolID, name)
	}
	groups, nextToken, err := s.ListGroups(poolID, 0, "")
	require.NoError(t, err)
	assert.Len(t, groups, 3)
	assert.Empty(t, nextToken)
}

// ── AddUserToGroup error paths ─────────────────────────────────────────────────

func TestStorageAddUserToGroup_MkdirErrors(t *testing.T) {
	tests := []struct {
		name        string
		getTarget   func(poolID string) string
		errContains string
	}{
		{
			"group_members root",
			func(poolID string) string { return filepath.Join("pools", poolID, "group_members") },
			"create group_members root",
		},
		{
			"member dir",
			func(poolID string) string {
				return filepath.Join("pools", poolID, "group_members", groupKey("admins"))
			},
			"create group_members dir",
		},
		{
			"user_groups root",
			func(poolID string) string { return filepath.Join("pools", poolID, "user_groups") },
			"create user_groups root",
		},
		{
			"user_groups dir",
			func(poolID string) string {
				return filepath.Join("pools", poolID, "user_groups", groupKey("alice"))
			},
			"create user_groups dir",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStorage(t)
			poolID := setupStoragePool(t, s)
			setupStorageGroup(t, s, poolID, "admins")
			setupStorageUser(t, s, poolID, "alice", "sub-alice")

			mkdirErr := errors.New("mkdir failed")
			realMkdir := s.mkdirFn
			target := tc.getTarget(poolID)
			s.mkdirFn = func(name string, perm os.FileMode) error {
				if name == target {
					return mkdirErr
				}
				return realMkdir(name, perm)
			}
			err := s.AddUserToGroup(poolID, "admins", "alice")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errContains)
		})
	}
}

func TestStorageAddUserToGroup_WriteMemberError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")

	writeErr := errors.New("write failed")
	realOpen := s.openFile
	mPath := memberPath(poolID, "admins", "alice")
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		if name == mPath {
			return nil, writeErr
		}
		return realOpen(name, flag, perm)
	}
	err := s.AddUserToGroup(poolID, "admins", "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write member")
}

func TestStorageAddUserToGroup_WriteUserGroupIndexError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")

	writeErr := errors.New("write failed")
	realOpen := s.openFile
	ugPath := userGroupPath(poolID, "alice", "admins")
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		if name == ugPath {
			return nil, writeErr
		}
		return realOpen(name, flag, perm)
	}
	err := s.AddUserToGroup(poolID, "admins", "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write user_groups index")
}

func TestStorageAddUserToGroup_WriteUserGroupIndexError_RollbackError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")

	writeErr := errors.New("write failed")
	removeErr := errors.New("remove failed")
	realOpen := s.openFile
	realRemove := s.removeFile
	ugPath := userGroupPath(poolID, "alice", "admins")
	mPath := memberPath(poolID, "admins", "alice")
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		if name == ugPath {
			return nil, writeErr
		}
		return realOpen(name, flag, perm)
	}
	s.removeFile = func(name string) error {
		if name == mPath {
			return removeErr
		}
		return realRemove(name)
	}
	err := s.AddUserToGroup(poolID, "admins", "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write user_groups index")
	assert.Contains(t, err.Error(), "rollback")
}

// ── RemoveUserFromGroup error paths ────────────────────────────────────────────

func TestStorageRemoveUserFromGroup_RemoveMemberError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	removeErr := errors.New("remove failed")
	mPath := memberPath(poolID, "admins", "alice")
	s.removeFile = func(name string) error {
		if name == mPath {
			return removeErr
		}
		return nil
	}
	err := s.RemoveUserFromGroup(poolID, "admins", "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove member")
}

func TestStorageRemoveUserFromGroup_RemoveUserGroupIndexError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	removeErr := errors.New("remove failed")
	ugPath := userGroupPath(poolID, "alice", "admins")
	s.removeFile = func(name string) error {
		if name == ugPath {
			return removeErr
		}
		return nil
	}
	err := s.RemoveUserFromGroup(poolID, "admins", "alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove user_groups index")
}

// ── ListGroupsForUser edge cases ───────────────────────────────────────────────

func TestStorageListGroupsForUser_NoGroupsForUser(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)

	groups, nextToken, err := s.ListGroupsForUser(poolID, "alice", 60, "")
	require.NoError(t, err)
	assert.Empty(t, groups)
	assert.Empty(t, nextToken)
}

func TestStorageListGroupsForUser_ListDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	listErr := errors.New("listdir failed")
	realListDir := s.listDirFn
	userGroupDir := filepath.Join("pools", poolID, "user_groups", groupKey("alice"))
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == userGroupDir {
			return nil, listErr
		}
		return realListDir(name)
	}
	_, _, err := s.ListGroupsForUser(poolID, "alice", 60, "")
	require.ErrorIs(t, err, listErr)
}

func TestStorageListGroupsForUser_DeletedGroup(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))
	require.NoError(t, s.DeleteGroup(poolID, "admins"))

	groups, _, err := s.ListGroupsForUser(poolID, "alice", 60, "")
	require.NoError(t, err)
	assert.Empty(t, groups)
}

func TestStorageListGroupsForUser_InvalidNextToken(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	_, _, err := s.ListGroupsForUser(poolID, "alice", 60, "bad-token")
	require.ErrorIs(t, err, errInvalidNextToken)
}

func TestStorageListGroupsForUser_ZeroLimit(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		setupStorageGroup(t, s, poolID, name)
	}
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		require.NoError(t, s.AddUserToGroup(poolID, name, "alice"))
	}
	groups, nextToken, err := s.ListGroupsForUser(poolID, "alice", 0, "")
	require.NoError(t, err)
	assert.Len(t, groups, 3)
	assert.Empty(t, nextToken)
}

func TestStorageListGroupsForUser_GetGroupReadError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	// Make readAll fail for the second call (first = marker file, second = group file).
	readErr := errors.New("disk error")
	callCount := 0
	realReadAll := s.readAll
	s.readAll = func(r io.Reader) ([]byte, error) {
		callCount++
		if callCount == 2 {
			return nil, readErr
		}
		return realReadAll(r)
	}
	_, _, err := s.ListGroupsForUser(poolID, "alice", 60, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, readErr)
}

// ── ListUsersInGroup edge cases ────────────────────────────────────────────────

func TestStorageListUsersInGroup_EmptyGroup(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")

	users, nextToken, err := s.ListUsersInGroup(poolID, "admins", 60, "")
	require.NoError(t, err)
	assert.Empty(t, users)
	assert.Empty(t, nextToken)
}

func TestStorageListUsersInGroup_ListDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	listErr := errors.New("listdir failed")
	realListDir := s.listDirFn
	memberDir := filepath.Join("pools", poolID, "group_members", groupKey("admins"))
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == memberDir {
			return nil, listErr
		}
		return realListDir(name)
	}
	_, _, err := s.ListUsersInGroup(poolID, "admins", 60, "")
	require.ErrorIs(t, err, listErr)
}

func TestStorageListUsersInGroup_DeletedUser(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))
	require.NoError(t, s.DeleteUser(poolID, "alice"))

	users, _, err := s.ListUsersInGroup(poolID, "admins", 60, "")
	require.NoError(t, err)
	assert.Empty(t, users)
}

func TestStorageListUsersInGroup_InvalidNextToken(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	_, _, err := s.ListUsersInGroup(poolID, "admins", 60, "bad-token")
	require.ErrorIs(t, err, errInvalidNextToken)
}

func TestStorageListUsersInGroup_ZeroLimit(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	setupStorageUser(t, s, poolID, "bob", "sub-bob")
	setupStorageUser(t, s, poolID, "charlie", "sub-charlie")
	for _, u := range []string{"alice", "bob", "charlie"} {
		require.NoError(t, s.AddUserToGroup(poolID, "admins", u))
	}
	users, nextToken, err := s.ListUsersInGroup(poolID, "admins", 0, "")
	require.NoError(t, err)
	assert.Len(t, users, 3)
	assert.Empty(t, nextToken)
}

func TestStorageListUsersInGroup_GetUserReadError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	// Make readAll fail for the second call (first = member marker, second = user file).
	readErr := errors.New("disk error")
	callCount := 0
	realReadAll := s.readAll
	s.readAll = func(r io.Reader) ([]byte, error) {
		callCount++
		if callCount == 2 {
			return nil, readErr
		}
		return realReadAll(r)
	}
	_, _, err := s.ListUsersInGroup(poolID, "admins", 60, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, readErr)
}

// ── IsDir skip in ListGroups ──────────────────────────────────────────────────

func TestStorageListGroups_SkipsDirEntry(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")

	realListDir := s.listDirFn
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		entries, err := realListDir(name)
		if err != nil {
			return nil, err
		}
		if filepath.Base(name) == "groups" {
			return append([]os.DirEntry{fakeDirEntryDir("subdir")}, entries...), nil
		}
		return entries, nil
	}

	groups, _, err := s.ListGroups(poolID, 60, "")
	require.NoError(t, err)
	assert.Len(t, groups, 1)
	assert.Equal(t, "admins", groups[0].GroupName)
}

// ── IsDir skip in ListGroupsForUser ──────────────────────────────────────────

func TestStorageListGroupsForUser_SkipsDirEntry(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	realListDir := s.listDirFn
	userGroupDir := filepath.Join("pools", poolID, "user_groups", groupKey("alice"))
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		entries, err := realListDir(name)
		if err != nil {
			return nil, err
		}
		if name == userGroupDir {
			return append([]os.DirEntry{fakeDirEntryDir("subdir")}, entries...), nil
		}
		return entries, nil
	}

	groups, _, err := s.ListGroupsForUser(poolID, "alice", 60, "")
	require.NoError(t, err)
	assert.Len(t, groups, 1)
	assert.Equal(t, "admins", groups[0].GroupName)
}

// ── IsDir skip in GetGroupsForUser ───────────────────────────────────────────

func TestStorageGetGroupsForUser_SkipsDirEntry(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	realListDir := s.listDirFn
	userGroupDir := filepath.Join("pools", poolID, "user_groups", groupKey("alice"))
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		entries, err := realListDir(name)
		if err != nil {
			return nil, err
		}
		if name == userGroupDir {
			return append([]os.DirEntry{fakeDirEntryDir("subdir")}, entries...), nil
		}
		return entries, nil
	}

	names, err := s.GetGroupsForUser(poolID, "alice")
	require.NoError(t, err)
	assert.Equal(t, []string{"admins"}, names)
}

// ── IsDir skip in ListUsersInGroup ────────────────────────────────────────────

func TestStorageListUsersInGroup_SkipsDirEntry(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	realListDir := s.listDirFn
	memberDir := filepath.Join("pools", poolID, "group_members", groupKey("admins"))
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		entries, err := realListDir(name)
		if err != nil {
			return nil, err
		}
		if name == memberDir {
			return append([]os.DirEntry{fakeDirEntryDir("subdir")}, entries...), nil
		}
		return entries, nil
	}

	users, _, err := s.ListUsersInGroup(poolID, "admins", 60, "")
	require.NoError(t, err)
	assert.Len(t, users, 1)
	assert.Equal(t, "alice", users[0].Username)
}

// ── GetGroupsForUser listDirFn error ──────────────────────────────────────────

func TestStorageGetGroupsForUser_ListDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	listErr := errors.New("listdir failed")
	realListDir := s.listDirFn
	userGroupDir := filepath.Join("pools", poolID, "user_groups", groupKey("alice"))
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		if name == userGroupDir {
			return nil, listErr
		}
		return realListDir(name)
	}
	_, err := s.GetGroupsForUser(poolID, "alice")
	require.ErrorIs(t, err, listErr)
}

// ── DeleteGroup IsDir skip in group_members ───────────────────────────────────

func TestStorageDeleteGroup_SkipsDirEntryInMembers(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	realListDir := s.listDirFn
	memberDir := filepath.Join("pools", poolID, "group_members", groupKey("admins"))
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		entries, err := realListDir(name)
		if err != nil {
			return nil, err
		}
		if name == memberDir {
			return append([]os.DirEntry{fakeDirEntryDir("subdir")}, entries...), nil
		}
		return entries, nil
	}
	require.NoError(t, s.DeleteGroup(poolID, "admins"))
	_, err := s.GetGroup(poolID, "admins")
	require.ErrorIs(t, err, errGroupNotFound)
}

// ── DeleteGroup deleteFlatDirLocked error ─────────────────────────────────────

func TestStorageDeleteGroup_DeleteMembersDirError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	deleteErr := errors.New("remove dir failed")
	realRemove := s.removeFile
	memberDir := filepath.Join("pools", poolID, "group_members", groupKey("admins"))
	s.removeFile = func(name string) error {
		if name == memberDir {
			return deleteErr
		}
		return realRemove(name)
	}
	err := s.DeleteGroup(poolID, "admins")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove group_members dir")
}

// ── DeleteGroup with corrupted member marker ──────────────────────────────────

func TestStorageDeleteGroup_CorruptedMemberMarker(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	// Overwrite the member marker with invalid JSON to simulate corruption.
	f, err := s.root.OpenFile(memberPath(poolID, "admins", "alice"), os.O_WRONLY|os.O_TRUNC, 0o640)
	require.NoError(t, err)
	_, err = f.Write([]byte("not-valid-json"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// DeleteGroup must still clean up the user_groups reverse-index entry.
	require.NoError(t, s.DeleteGroup(poolID, "admins"))

	// alice's group list must be empty — no stale entries from the corrupted marker.
	names, err := s.GetGroupsForUser(poolID, "alice")
	require.NoError(t, err)
	assert.Empty(t, names)
}

// ── ListGroupsForUser stale group-not-found ───────────────────────────────────

func TestStorageListGroupsForUser_SkipsStaleGroupNotFound(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	// Delete the group but keep alice's user_groups reverse-index entry stale
	// by skipping the removeFile call for the user_groups path.
	ugPath := userGroupPath(poolID, "alice", "admins")
	realRemove := s.removeFile
	s.removeFile = func(name string) error {
		if name == ugPath {
			return nil // silently skip — leaves stale entry
		}
		return realRemove(name)
	}
	require.NoError(t, s.DeleteGroup(poolID, "admins"))
	s.removeFile = realRemove // restore

	// ListGroupsForUser should skip the stale entry and return nothing.
	groups, _, err := s.ListGroupsForUser(poolID, "alice", 60, "")
	require.NoError(t, err)
	assert.Empty(t, groups)
}

func TestStorageGetGroupsForUser_GetGroupReadError(t *testing.T) {
	s := newTestStorage(t)
	poolID := setupStoragePool(t, s)
	setupStorageGroup(t, s, poolID, "admins")
	setupStorageUser(t, s, poolID, "alice", "sub-alice")
	require.NoError(t, s.AddUserToGroup(poolID, "admins", "alice"))

	// Make readAll fail for the second call (first = marker file, second = group file).
	readErr := errors.New("disk error")
	callCount := 0
	realReadAll := s.readAll
	s.readAll = func(r io.Reader) ([]byte, error) {
		callCount++
		if callCount == 2 {
			return nil, readErr
		}
		return realReadAll(r)
	}
	_, err := s.GetGroupsForUser(poolID, "alice")
	require.Error(t, err)
	assert.ErrorIs(t, err, readErr)
}
