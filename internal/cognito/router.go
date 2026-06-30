package cognito

import (
	"crypto/rsa"
	"io"
	"net/http"
	"strings"
	"time"
)

// store is the storage interface for non-group operations used by Router.
type store interface {
	// User pool operations
	CreateUserPool(meta *UserPoolMetadata) error
	GetUserPool(poolID string) (*UserPoolMetadata, error)
	UpdateUserPool(poolID string, fn func(*UserPoolMetadata) error) error
	DeleteUserPool(poolID string) error
	ListUserPools(maxResults int, nextToken string) ([]*UserPoolMetadata, string, error)

	// User pool client operations
	CreateUserPoolClient(meta *UserPoolClientMetadata) error
	GetUserPoolClient(poolID, clientID string) (*UserPoolClientMetadata, error)
	UpdateUserPoolClient(poolID, clientID string, fn func(*UserPoolClientMetadata) error) error
	DeleteUserPoolClient(poolID, clientID string) error
	ListUserPoolClients(
		poolID string,
		maxResults int,
		nextToken string,
	) ([]*UserPoolClientMetadata, string, error)

	// Client index (clientID → poolID lookup)
	GetPoolIDForClient(clientID string) (string, error)

	// User operations
	CreateUser(poolID string, user *UserMetadata) error
	GetUser(poolID, username string) (*UserMetadata, error)
	GetUserBySub(poolID, sub string) (*UserMetadata, error)
	UpdateUser(poolID, username string, fn func(*UserMetadata) error) error
	DeleteUser(poolID, username string) error

	// RSA key operations
	GetOrCreatePoolKeys(poolID string) (*poolKeys, *rsa.PrivateKey, error)
	GetPoolKeys(poolID string) (*poolKeys, *rsa.PrivateKey, error)

	// Refresh token operations
	CreateRefreshToken(data *refreshTokenData) error
	GetRefreshToken(poolID, token string) (*refreshTokenData, error)
	DeleteRefreshToken(poolID, token string) error

	// Token revocation operations
	RevokeAccessToken(poolID, jti string, expiresAt float64) error
	IsAccessTokenRevoked(poolID, jti string) (bool, error)
	DeleteRefreshTokensBySub(poolID, sub string) error

	// GetGroupsForUser is used by auth handlers to embed group claims in JWTs.
	GetGroupsForUser(poolID, username string) ([]string, error)
}

// groupStore is the focused interface for group CRUD and membership operations.
type groupStore interface {
	GetUserPool(poolID string) (*UserPoolMetadata, error)
	GetUser(poolID, username string) (*UserMetadata, error)

	CreateGroup(poolID string, group *GroupMetadata) error
	GetGroup(poolID, groupName string) (*GroupMetadata, error)
	UpdateGroup(poolID, groupName string, fn func(*GroupMetadata) error) error
	DeleteGroup(poolID, groupName string) error
	ListGroups(poolID string, maxResults int, nextToken string) ([]*GroupMetadata, string, error)

	AddUserToGroup(poolID, groupName, username string) error
	RemoveUserFromGroup(poolID, groupName, username string) error
	ListGroupsForUser(
		poolID, username string,
		maxResults int,
		nextToken string,
	) ([]*GroupMetadata, string, error)
	ListUsersInGroup(
		poolID, groupName string,
		maxResults int,
		nextToken string,
	) ([]*UserMetadata, string, error)
}

// Router handles Cognito User Pools API requests dispatched via the X-Amz-Target header.
type Router struct {
	storage    store
	groups     groupStore
	codeReader io.Reader // injectable for testing; defaults to crypto/rand.Reader
}

func NewRouter(storage *Storage) *Router {
	return &Router{storage: storage, groups: storage, codeReader: randReader}
}

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := newResponseRecorder(w)
	start := time.Now()
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AWSCognitoIdentityProviderService.")

	// JWKS endpoint: path-based routing, no X-Amz-Target header.
	if op == "" && strings.HasSuffix(r.URL.Path, "/.well-known/jwks.json") {
		ro.handleJWKS(rec, r)
		emitRequestLog("GetJWKS", rec, time.Since(start))
		return
	}

	ro.serveHTTP(rec, r, op)
	emitRequestLog(op, rec, time.Since(start))
}

func (ro *Router) serveHTTP(w http.ResponseWriter, r *http.Request, op string) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"failed to read request body",
		)
		return
	}
	switch op {
	case "CreateUserPool":
		ro.handleCreateUserPool(w, body)
	case "DescribeUserPool":
		ro.handleDescribeUserPool(w, body)
	case "UpdateUserPool":
		ro.handleUpdateUserPool(w, body)
	case "DeleteUserPool":
		ro.handleDeleteUserPool(w, body)
	case "ListUserPools":
		ro.handleListUserPools(w, body)
	case "GetUserPoolMfaConfig":
		ro.handleGetUserPoolMfaConfig(w, body)
	case "CreateUserPoolClient":
		ro.handleCreateUserPoolClient(w, body)
	case "DescribeUserPoolClient":
		ro.handleDescribeUserPoolClient(w, body)
	case "UpdateUserPoolClient":
		ro.handleUpdateUserPoolClient(w, body)
	case "DeleteUserPoolClient":
		ro.handleDeleteUserPoolClient(w, body)
	case "ListUserPoolClients":
		ro.handleListUserPoolClients(w, body)
	case "SignUp":
		ro.handleSignUp(w, body)
	case "ConfirmSignUp":
		ro.handleConfirmSignUp(w, body)
	case "ResendConfirmationCode":
		ro.handleResendConfirmationCode(w, body)
	case "InitiateAuth":
		ro.handleInitiateAuth(w, body)
	case "RespondToAuthChallenge":
		ro.handleRespondToAuthChallenge(w, body)
	case "GetUser":
		ro.handleGetUser(w, body)
	case "AdminCreateUser":
		ro.handleAdminCreateUser(w, body)
	case "AdminGetUser":
		ro.handleAdminGetUser(w, body)
	case "AdminSetUserPassword":
		ro.handleAdminSetUserPassword(w, body)
	case "AdminConfirmSignUp":
		ro.handleAdminConfirmSignUp(w, body)
	case "AdminDeleteUser":
		ro.handleAdminDeleteUser(w, body)
	case "CreateGroup":
		ro.handleCreateGroup(w, body)
	case "DeleteGroup":
		ro.handleDeleteGroup(w, body)
	case "GetGroup":
		ro.handleGetGroup(w, body)
	case "UpdateGroup":
		ro.handleUpdateGroup(w, body)
	case "ListGroups":
		ro.handleListGroups(w, body)
	case "AdminAddUserToGroup":
		ro.handleAdminAddUserToGroup(w, body)
	case "AdminRemoveUserFromGroup":
		ro.handleAdminRemoveUserFromGroup(w, body)
	case "AdminListGroupsForUser":
		ro.handleAdminListGroupsForUser(w, body)
	case "ListUsersInGroup":
		ro.handleListUsersInGroup(w, body)
	case "RevokeToken":
		ro.handleRevokeToken(w, body)
	case "GlobalSignOut":
		ro.handleGlobalSignOut(w, body)
	default:
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeUnknownOperationException,
			"Operation not supported: "+op,
		)
	}
}
