package cognito

import (
	"io"
	"net/http"
	"strings"
	"time"
)

// store is the storage interface used by Router.
// Methods are added incrementally as operations are implemented.
type store interface {
	CreateUserPool(meta *UserPoolMetadata) error
	GetUserPool(poolID string) (*UserPoolMetadata, error)
	UpdateUserPool(poolID string, fn func(*UserPoolMetadata) error) error
	DeleteUserPool(poolID string) error
	ListUserPools(maxResults int, nextToken string) ([]*UserPoolMetadata, string, error)
	CreateUserPoolClient(meta *UserPoolClientMetadata) error
	GetUserPoolClient(poolID, clientID string) (*UserPoolClientMetadata, error)
	UpdateUserPoolClient(poolID, clientID string, fn func(*UserPoolClientMetadata) error) error
	DeleteUserPoolClient(poolID, clientID string) error
	ListUserPoolClients(
		poolID string,
		maxResults int,
		nextToken string,
	) ([]*UserPoolClientMetadata, string, error)
}

// Router handles Cognito User Pools API requests dispatched via the X-Amz-Target header.
type Router struct {
	storage store
}

func NewRouter(storage *Storage) *Router {
	return &Router{storage: storage}
}

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := newResponseRecorder(w)
	start := time.Now()
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AWSCognitoIdentityProviderService.")
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
	default:
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeUnknownOperationException,
			"Operation not supported: "+op,
		)
	}
}
