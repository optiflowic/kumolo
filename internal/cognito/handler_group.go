package cognito

import (
	"encoding/json"
	"errors"
	"net/http"
)

// groupTypeResponse matches the AWS GroupType JSON shape.
type groupTypeResponse struct {
	GroupName        string  `json:"GroupName"`
	UserPoolId       string  `json:"UserPoolId"`
	Description      string  `json:"Description,omitempty"`
	Precedence       *int    `json:"Precedence,omitempty"`
	RoleArn          string  `json:"RoleArn,omitempty"`
	CreationDate     float64 `json:"CreationDate"`
	LastModifiedDate float64 `json:"LastModifiedDate"`
}

func groupToResponse(g *GroupMetadata) groupTypeResponse {
	return groupTypeResponse{
		GroupName:        g.GroupName,
		UserPoolId:       g.UserPoolId,
		Description:      g.Description,
		Precedence:       g.Precedence,
		RoleArn:          g.RoleArn,
		CreationDate:     g.CreationDate,
		LastModifiedDate: g.LastModifiedDate,
	}
}

// ──── CreateGroup ─────────────────────────────────────────────────────────────

type createGroupRequest struct {
	UserPoolID  string `json:"UserPoolId"`
	GroupName   string `json:"GroupName"`
	Description string `json:"Description"`
	Precedence  *int   `json:"Precedence"`
	RoleArn     string `json:"RoleArn"`
}

func (ro *Router) handleCreateGroup(w http.ResponseWriter, body []byte) {
	var req createGroupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "invalid request body")
		return
	}
	if req.UserPoolID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "UserPoolId is required")
		return
	}
	if req.GroupName == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "GroupName is required")
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user pool")
		return
	}

	ts := nowUnix()
	group := &GroupMetadata{
		GroupName:        req.GroupName,
		UserPoolId:       req.UserPoolID,
		Description:      req.Description,
		Precedence:       req.Precedence,
		RoleArn:          req.RoleArn,
		CreationDate:     ts,
		LastModifiedDate: ts,
	}

	if err := ro.storage.CreateGroup(req.UserPoolID, group); err != nil {
		if errors.Is(err, errGroupExists) {
			writeError(w, http.StatusBadRequest, ErrTypeGroupExistsException,
				"A group with the name already exists.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to create group")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"Group": groupToResponse(group)})
}

// ──── DeleteGroup ─────────────────────────────────────────────────────────────

type deleteGroupRequest struct {
	UserPoolID string `json:"UserPoolId"`
	GroupName  string `json:"GroupName"`
}

func (ro *Router) handleDeleteGroup(w http.ResponseWriter, body []byte) {
	var req deleteGroupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "invalid request body")
		return
	}
	if req.UserPoolID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "UserPoolId is required")
		return
	}
	if req.GroupName == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "GroupName is required")
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user pool")
		return
	}

	if err := ro.storage.DeleteGroup(req.UserPoolID, req.GroupName); err != nil {
		if errors.Is(err, errGroupNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "Group not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to delete group")
		return
	}

	writeEmpty(w)
}

// ──── GetGroup ────────────────────────────────────────────────────────────────

type getGroupRequest struct {
	UserPoolID string `json:"UserPoolId"`
	GroupName  string `json:"GroupName"`
}

func (ro *Router) handleGetGroup(w http.ResponseWriter, body []byte) {
	var req getGroupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "invalid request body")
		return
	}
	if req.UserPoolID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "UserPoolId is required")
		return
	}
	if req.GroupName == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "GroupName is required")
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user pool")
		return
	}

	group, err := ro.storage.GetGroup(req.UserPoolID, req.GroupName)
	if err != nil {
		if errors.Is(err, errGroupNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "Group not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get group")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"Group": groupToResponse(group)})
}

// ──── UpdateGroup ─────────────────────────────────────────────────────────────

type updateGroupRequest struct {
	UserPoolID  string  `json:"UserPoolId"`
	GroupName   string  `json:"GroupName"`
	Description *string `json:"Description"`
	Precedence  *int    `json:"Precedence"`
	RoleArn     *string `json:"RoleArn"`
}

func (ro *Router) handleUpdateGroup(w http.ResponseWriter, body []byte) {
	var req updateGroupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "invalid request body")
		return
	}
	if req.UserPoolID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "UserPoolId is required")
		return
	}
	if req.GroupName == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "GroupName is required")
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user pool")
		return
	}

	var updated *GroupMetadata
	err := ro.storage.UpdateGroup(req.UserPoolID, req.GroupName, func(g *GroupMetadata) error {
		if req.Description != nil {
			g.Description = *req.Description
		}
		if req.Precedence != nil {
			g.Precedence = req.Precedence
		}
		if req.RoleArn != nil {
			g.RoleArn = *req.RoleArn
		}
		updated = g
		return nil
	})
	if err != nil {
		if errors.Is(err, errGroupNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "Group not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to update group")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"Group": groupToResponse(updated)})
}

// ──── ListGroups ──────────────────────────────────────────────────────────────

type listGroupsRequest struct {
	UserPoolID string `json:"UserPoolId"`
	Limit      int    `json:"Limit"`
	NextToken  string `json:"NextToken"`
}

const defaultGroupLimit = 60

func (ro *Router) handleListGroups(w http.ResponseWriter, body []byte) {
	var req listGroupsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "invalid request body")
		return
	}
	if req.UserPoolID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "UserPoolId is required")
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user pool")
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > defaultGroupLimit {
		limit = defaultGroupLimit
	}

	groups, nextToken, err := ro.storage.ListGroups(req.UserPoolID, limit, req.NextToken)
	if err != nil {
		if errors.Is(err, errInvalidNextToken) {
			writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "invalid pagination token")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to list groups")
		return
	}

	resp := map[string]any{"Groups": groupsToResponse(groups)}
	if nextToken != "" {
		resp["NextToken"] = nextToken
	}
	writeJSON(w, http.StatusOK, resp)
}

func groupsToResponse(groups []*GroupMetadata) []groupTypeResponse {
	out := make([]groupTypeResponse, len(groups))
	for i, g := range groups {
		out[i] = groupToResponse(g)
	}
	return out
}

// ──── AdminAddUserToGroup ─────────────────────────────────────────────────────

type adminAddUserToGroupRequest struct {
	UserPoolID string `json:"UserPoolId"`
	GroupName  string `json:"GroupName"`
	Username   string `json:"Username"`
}

func (ro *Router) handleAdminAddUserToGroup(w http.ResponseWriter, body []byte) {
	var req adminAddUserToGroupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "invalid request body")
		return
	}
	if req.UserPoolID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "UserPoolId is required")
		return
	}
	if req.GroupName == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "GroupName is required")
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "Username is required")
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user pool")
		return
	}

	if _, err := ro.storage.GetGroup(req.UserPoolID, req.GroupName); err != nil {
		if errors.Is(err, errGroupNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "Group not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get group")
		return
	}

	if _, err := ro.storage.GetUser(req.UserPoolID, req.Username); err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException, "User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user")
		return
	}

	if err := ro.storage.AddUserToGroup(req.UserPoolID, req.GroupName, req.Username); err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to add user to group")
		return
	}

	writeEmpty(w)
}

// ──── AdminRemoveUserFromGroup ────────────────────────────────────────────────

type adminRemoveUserFromGroupRequest struct {
	UserPoolID string `json:"UserPoolId"`
	GroupName  string `json:"GroupName"`
	Username   string `json:"Username"`
}

func (ro *Router) handleAdminRemoveUserFromGroup(w http.ResponseWriter, body []byte) {
	var req adminRemoveUserFromGroupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "invalid request body")
		return
	}
	if req.UserPoolID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "UserPoolId is required")
		return
	}
	if req.GroupName == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "GroupName is required")
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "Username is required")
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user pool")
		return
	}

	if _, err := ro.storage.GetGroup(req.UserPoolID, req.GroupName); err != nil {
		if errors.Is(err, errGroupNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "Group not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get group")
		return
	}

	if _, err := ro.storage.GetUser(req.UserPoolID, req.Username); err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException, "User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user")
		return
	}

	if err := ro.storage.RemoveUserFromGroup(req.UserPoolID, req.GroupName, req.Username); err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to remove user from group")
		return
	}

	writeEmpty(w)
}

// ──── AdminListGroupsForUser ──────────────────────────────────────────────────

type adminListGroupsForUserRequest struct {
	UserPoolID string `json:"UserPoolId"`
	Username   string `json:"Username"`
	Limit      int    `json:"Limit"`
	NextToken  string `json:"NextToken"`
}

func (ro *Router) handleAdminListGroupsForUser(w http.ResponseWriter, body []byte) {
	var req adminListGroupsForUserRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "invalid request body")
		return
	}
	if req.UserPoolID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "UserPoolId is required")
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "Username is required")
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user pool")
		return
	}

	if _, err := ro.storage.GetUser(req.UserPoolID, req.Username); err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException, "User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user")
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > defaultGroupLimit {
		limit = defaultGroupLimit
	}

	groups, nextToken, err := ro.storage.ListGroupsForUser(req.UserPoolID, req.Username, limit, req.NextToken)
	if err != nil {
		if errors.Is(err, errInvalidNextToken) {
			writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "invalid pagination token")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to list groups for user")
		return
	}

	resp := map[string]any{"Groups": groupsToResponse(groups)}
	if nextToken != "" {
		resp["NextToken"] = nextToken
	}
	writeJSON(w, http.StatusOK, resp)
}

// ──── ListUsersInGroup ────────────────────────────────────────────────────────

type listUsersInGroupRequest struct {
	UserPoolID string `json:"UserPoolId"`
	GroupName  string `json:"GroupName"`
	Limit      int    `json:"Limit"`
	NextToken  string `json:"NextToken"`
}

func (ro *Router) handleListUsersInGroup(w http.ResponseWriter, body []byte) {
	var req listUsersInGroupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "invalid request body")
		return
	}
	if req.UserPoolID == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "UserPoolId is required")
		return
	}
	if req.GroupName == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "GroupName is required")
		return
	}

	if _, err := ro.storage.GetUserPool(req.UserPoolID); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "User pool not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get user pool")
		return
	}

	if _, err := ro.storage.GetGroup(req.UserPoolID, req.GroupName); err != nil {
		if errors.Is(err, errGroupNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeResourceNotFoundException, "Group not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to get group")
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > defaultGroupLimit {
		limit = defaultGroupLimit
	}

	users, nextToken, err := ro.storage.ListUsersInGroup(req.UserPoolID, req.GroupName, limit, req.NextToken)
	if err != nil {
		if errors.Is(err, errInvalidNextToken) {
			writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, "invalid pagination token")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException, "failed to list users in group")
		return
	}

	userResps := make([]userTypeResponse, len(users))
	for i, u := range users {
		userResps[i] = newUserTypeResponse(u)
	}

	resp := map[string]any{"Users": userResps}
	if nextToken != "" {
		resp["NextToken"] = nextToken
	}
	writeJSON(w, http.StatusOK, resp)
}
