package cognito

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failWriter is an http.ResponseWriter whose Write always returns an error.
type failWriter struct{ header http.Header }

func newFailWriter() *failWriter                { return &failWriter{header: http.Header{}} }
func (f *failWriter) Header() http.Header       { return f.header }
func (f *failWriter) WriteHeader(int)           {}
func (f *failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// failWriteCloser is an io.WriteCloser whose Write always fails.
type failWriteCloser struct{}

func (f *failWriteCloser) Write([]byte) (int, error) { return 0, errors.New("write failed") }
func (f *failWriteCloser) Close() error              { return nil }

func newTestRouter(t *testing.T) *Router {
	t.Helper()
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	return NewRouter(storage)
}

func TestRouter_UnknownOperation(t *testing.T) {
	tests := []struct {
		name   string
		target string
		wantOp string
	}{
		{
			name:   "unrecognized operation",
			target: "AWSCognitoIdentityProviderService.DoesNotExist",
			wantOp: "DoesNotExist",
		},
		{
			name:   "empty target header",
			target: "",
			wantOp: "",
		},
		{
			name:   "wrong service prefix",
			target: "DynamoDB_20120810.ListTables",
			wantOp: "DynamoDB_20120810.ListTables",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ro := newTestRouter(t)
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
			if tt.target != "" {
				req.Header.Set("X-Amz-Target", tt.target)
			}
			w := httptest.NewRecorder()

			ro.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, "application/x-amz-json-1.1", w.Header().Get("Content-Type"))

			var resp errResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, ErrTypeUnknownOperationException, resp.Type)
			assert.Contains(t, resp.Message, tt.wantOp)
		})
	}
}

func TestResponseRecorder_WriteHeaderIdempotent(t *testing.T) {
	w := httptest.NewRecorder()
	rec := newResponseRecorder(w)
	rec.WriteHeader(http.StatusOK)
	rec.WriteHeader(http.StatusBadRequest) // second call must be ignored
	assert.Equal(t, http.StatusOK, rec.status)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestResponseRecorder_Flush(t *testing.T) {
	w := httptest.NewRecorder()
	rec := newResponseRecorder(w)
	rec.Flush()
	assert.True(t, w.Flushed)
}

func TestEmitRequestLog_5xx(t *testing.T) {
	w := httptest.NewRecorder()
	rec := newResponseRecorder(w)
	rec.WriteHeader(http.StatusInternalServerError)
	rec.errCode = ErrTypeInternalErrorException
	rec.errMsg = "something went wrong"
	emitRequestLog("SomeOp", rec, time.Millisecond) // exercises status>=500 logging branch
}

func TestWriteError_BrokenWriter(t *testing.T) {
	// exercises the slog.Warn path when json encoding fails due to a broken writer
	writeError(newFailWriter(), http.StatusBadRequest, ErrTypeInvalidParameterException, "test")
}

func TestWriteJSON_BrokenWriter(t *testing.T) {
	// exercises the slog.Warn path when json encoding fails due to a broken writer
	writeJSON(newFailWriter(), http.StatusOK, map[string]string{"key": "value"})
}

func TestStorage_WriteJSON_WriteError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	storage.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
		return &failWriteCloser{}, nil
	}
	err = storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write")
}

func TestStorage_WriteJSON_MarshalError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	// chan is not JSON-serializable, forcing json.Marshal to fail.
	err = storage.writeJSON("test.json", make(chan int))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal json")
}

func TestRouter_ReadBodyError(t *testing.T) {
	ro := newTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/", iotest.ErrReader(errors.New("read error")))
	req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService.InitiateAuth")
	w := httptest.NewRecorder()

	ro.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp errResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
}

func TestStorage_Close(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	assert.NoError(t, storage.Close())
}

// mockStore is a minimal store implementation for testing handler error paths.
type mockStore struct {
	createErr         error
	getErr            error
	updateErr         error
	deleteErr         error
	listErr           error
	createClientErr   error
	getClientErr      error
	updateClientErr   error
	deleteClientErr   error
	listClientErr     error
	getPoolForClient  func(string) (string, error)
	createUserErr     error
	getUserFn         func(string, string) (*UserMetadata, error)
	getUserBySubFn    func(string, string) (*UserMetadata, error)
	updateUserErr     error
	deleteUserErr     error
	getOrCreateKeysFn func(string) (*poolKeys, *rsa.PrivateKey, error)
	getPoolKeysFn     func(string) (*poolKeys, *rsa.PrivateKey, error)
	createRefreshErr  error
	getRefreshFn      func(string, string) (*refreshTokenData, error)
	deleteRefreshErr  error
}

func (m *mockStore) CreateUserPool(*UserPoolMetadata) error { return m.createErr }
func (m *mockStore) GetUserPool(string) (*UserPoolMetadata, error) {
	return nil, m.getErr
}
func (m *mockStore) UpdateUserPool(_ string, _ func(*UserPoolMetadata) error) error {
	return m.updateErr
}
func (m *mockStore) DeleteUserPool(string) error { return m.deleteErr }
func (m *mockStore) ListUserPools(int, string) ([]*UserPoolMetadata, string, error) {
	return nil, "", m.listErr
}
func (m *mockStore) CreateUserPoolClient(*UserPoolClientMetadata) error { return m.createClientErr }
func (m *mockStore) GetUserPoolClient(string, string) (*UserPoolClientMetadata, error) {
	return nil, m.getClientErr
}

func (m *mockStore) UpdateUserPoolClient(
	_ string,
	_ string,
	_ func(*UserPoolClientMetadata) error,
) error {
	return m.updateClientErr
}
func (m *mockStore) DeleteUserPoolClient(string, string) error { return m.deleteClientErr }

func (m *mockStore) ListUserPoolClients(
	string,
	int,
	string,
) ([]*UserPoolClientMetadata, string, error) {
	return nil, "", m.listClientErr
}

func (m *mockStore) GetPoolIDForClient(clientID string) (string, error) {
	if m.getPoolForClient != nil {
		return m.getPoolForClient(clientID)
	}
	return "", errUserPoolClientNotFound
}

func (m *mockStore) CreateUser(_ string, _ *UserMetadata) error { return m.createUserErr }

func (m *mockStore) GetUser(poolID, username string) (*UserMetadata, error) {
	if m.getUserFn != nil {
		return m.getUserFn(poolID, username)
	}
	return nil, errUserNotFound
}

func (m *mockStore) GetUserBySub(poolID, sub string) (*UserMetadata, error) {
	if m.getUserBySubFn != nil {
		return m.getUserBySubFn(poolID, sub)
	}
	return nil, errUserNotFound
}

func (m *mockStore) UpdateUser(_ string, _ string, _ func(*UserMetadata) error) error {
	return m.updateUserErr
}

func (m *mockStore) DeleteUser(string, string) error { return m.deleteUserErr }

func (m *mockStore) GetOrCreatePoolKeys(poolID string) (*poolKeys, *rsa.PrivateKey, error) {
	if m.getOrCreateKeysFn != nil {
		return m.getOrCreateKeysFn(poolID)
	}
	return nil, nil, errors.New("no keys configured")
}

func (m *mockStore) GetPoolKeys(poolID string) (*poolKeys, *rsa.PrivateKey, error) {
	if m.getPoolKeysFn != nil {
		return m.getPoolKeysFn(poolID)
	}
	return nil, nil, os.ErrNotExist
}

func (m *mockStore) CreateRefreshToken(*refreshTokenData) error { return m.createRefreshErr }

func (m *mockStore) GetRefreshToken(poolID, token string) (*refreshTokenData, error) {
	if m.getRefreshFn != nil {
		return m.getRefreshFn(poolID, token)
	}
	return nil, errRefreshTokenNotFound
}

func (m *mockStore) DeleteRefreshToken(string, string) error { return m.deleteRefreshErr }

func (m *mockStore) CreateGroup(_ string, _ *GroupMetadata) error { return nil }

func (m *mockStore) GetGroup(
	string,
	string,
) (*GroupMetadata, error) {
	return nil, errGroupNotFound
}
func (m *mockStore) UpdateGroup(_ string, _ string, _ func(*GroupMetadata) error) error {
	return nil
}
func (m *mockStore) DeleteGroup(string, string) error { return nil }
func (m *mockStore) ListGroups(string, int, string) ([]*GroupMetadata, string, error) {
	return nil, "", nil
}
func (m *mockStore) AddUserToGroup(string, string, string) error      { return nil }
func (m *mockStore) RemoveUserFromGroup(string, string, string) error { return nil }

func (m *mockStore) ListGroupsForUser(
	string,
	string,
	int,
	string,
) ([]*GroupMetadata, string, error) {
	return nil, "", nil
}
func (m *mockStore) ListUsersInGroup(string, string, int, string) ([]*UserMetadata, string, error) {
	return nil, "", nil
}
func (m *mockStore) GetGroupsForUser(string, string) ([]string, error) { return nil, nil }

func TestNewStorage_MkdirError(t *testing.T) {
	dir := t.TempDir()
	// Place a file where the "cognito" directory must be created, forcing MkdirAll to fail.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cognito"), []byte(""), 0o600))
	_, err := NewStorage(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create cognito pools dir")
}

func TestNewStorage_OpenRootError(t *testing.T) {
	_, err := newStorage(t.TempDir(), func(string) (*os.Root, error) {
		return nil, errors.New("open root failed")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open cognito storage root")
}

func TestStorage_WriteJSON_OpenFileError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	storage.openFile = func(string, int, os.FileMode) (io.WriteCloser, error) {
		return nil, errors.New("open failed")
	}
	err = storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"})
	require.Error(t, err)
}

func TestStorage_ReadJSON_ReadAllError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	storage.readAll = func(io.Reader) ([]byte, error) {
		return nil, errors.New("read error")
	}
	_, err = storage.GetUserPool("us-east-1_Test12345")
	require.Error(t, err)
}

func TestStorage_ReadJSON_UnmarshalError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	storage.readAll = func(io.Reader) ([]byte, error) {
		return []byte("not json"), nil
	}
	_, err = storage.GetUserPool("us-east-1_Test12345")
	require.Error(t, err)
}

func TestStorage_CreateUserPool_MkdirError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	storage.mkdirFn = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}
	err = storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create pool dir")
}

func TestStorage_UpdateUserPool_FnError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	err = storage.UpdateUserPool("us-east-1_Test12345", func(*UserPoolMetadata) error {
		return errors.New("fn error")
	})
	require.Error(t, err)
}

func TestStorage_DeleteUserPool_ErrorPaths(t *testing.T) {
	const poolID = "us-east-1_Test12345"

	tests := []struct {
		name       string
		withClient bool
		setup      func(t *testing.T, s *Storage, dataDir string)
		wantMsg    string
		notFound   bool
	}{
		{
			name: "stat error",
			setup: func(_ *testing.T, s *Storage, _ string) {
				s.statFn = func(string) (os.FileInfo, error) {
					return nil, errors.New("stat failed")
				}
			},
			notFound: true,
		},
		{
			name:       "clients dir remove error",
			withClient: true,
			setup: func(_ *testing.T, s *Storage, _ string) {
				realRemove := s.removeFile
				s.removeFile = func(name string) error {
					if filepath.Base(filepath.Dir(name)) == "clients" {
						return errors.New("permission denied")
					}
					return realRemove(name)
				}
			},
			wantMsg: "delete clients dir",
		},
		{
			name: "flat dir list error",
			setup: func(_ *testing.T, s *Storage, _ string) {
				realList := s.listDirFn
				s.listDirFn = func(name string) ([]os.DirEntry, error) {
					if filepath.Base(name) == "users" {
						return nil, errors.New("list error")
					}
					return realList(name)
				}
			},
			wantMsg: "delete users dir",
		},
		{
			name: "flat dir file remove error",
			setup: func(_ *testing.T, s *Storage, _ string) {
				realList := s.listDirFn
				s.listDirFn = func(name string) ([]os.DirEntry, error) {
					if filepath.Base(name) == "users" {
						return []os.DirEntry{fakeDirEntry("u.json")}, nil
					}
					return realList(name)
				}
				realRemove := s.removeFile
				s.removeFile = func(name string) error {
					if filepath.Base(filepath.Dir(name)) == "users" {
						return errors.New("permission denied")
					}
					return realRemove(name)
				}
			},
			wantMsg: "delete users dir",
		},
		{
			name: "flat dir itself remove error",
			setup: func(t *testing.T, s *Storage, dataDir string) {
				usersDir := filepath.Join(dataDir, "cognito", "pools", poolID, "users")
				require.NoError(t, os.MkdirAll(usersDir, 0o750))
				realRemove := s.removeFile
				s.removeFile = func(name string) error {
					if filepath.Base(name) == "users" {
						return errors.New("cannot remove users dir")
					}
					return realRemove(name)
				}
			},
			wantMsg: "delete users dir",
		},
		{
			name: "keys.json remove error",
			setup: func(_ *testing.T, s *Storage, _ string) {
				realRemove := s.removeFile
				s.removeFile = func(name string) error {
					if filepath.Base(name) == "keys.json" {
						return errors.New("permission denied")
					}
					return realRemove(name)
				}
			},
			wantMsg: "remove keys.json",
		},
		{
			name: "meta.json remove error",
			setup: func(_ *testing.T, s *Storage, _ string) {
				realRemove := s.removeFile
				s.removeFile = func(name string) error {
					if filepath.Base(name) == "meta.json" {
						return errors.New("permission denied")
					}
					return realRemove(name)
				}
			},
			wantMsg: "remove pool meta",
		},
		{
			name: "pool dir remove error",
			setup: func(_ *testing.T, s *Storage, _ string) {
				realRemove := s.removeFile
				s.removeFile = func(name string) error {
					if name == filepath.Join("pools", poolID) {
						return errors.New("cannot remove dir")
					}
					return realRemove(name)
				}
			},
			wantMsg: "remove pool dir",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			storage, err := NewStorage(dataDir)
			require.NoError(t, err)
			t.Cleanup(func() { _ = storage.Close() })
			require.NoError(t, storage.CreateUserPool(&UserPoolMetadata{ID: poolID, Name: "test"}))
			if tt.withClient {
				require.NoError(t, storage.CreateUserPoolClient(&UserPoolClientMetadata{
					UserPoolID: poolID,
					ClientID:   "testclientid0000000000000000",
					ClientName: "app",
				}))
			}
			tt.setup(t, storage, dataDir)
			err = storage.DeleteUserPool(poolID)
			require.Error(t, err)
			if tt.notFound {
				assert.False(t, errors.Is(err, errUserPoolNotFound))
			}
			if tt.wantMsg != "" {
				assert.Contains(t, err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestStorage_ListUserPools_PoolsDirDeleted(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewStorage(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "cognito", "pools")))
	pools, nextToken, err := storage.ListUserPools(10, "")
	require.NoError(t, err)
	assert.Empty(t, pools)
	assert.Empty(t, nextToken)
}

func TestStorage_ListUserPools_ListDirError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	storage.listDirFn = func(string) ([]os.DirEntry, error) {
		return nil, errors.New("permission denied")
	}
	_, _, err = storage.ListUserPools(10, "")
	require.Error(t, err)
}

func TestStorage_CreateUserPoolClient_MkdirError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	storage.mkdirFn = func(string, os.FileMode) error {
		return errors.New("mkdir failed")
	}
	err = storage.CreateUserPoolClient(&UserPoolClientMetadata{
		UserPoolID: "us-east-1_Test12345",
		ClientID:   "testclientid0000000000000000",
		ClientName: "app",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create clients dir")
}

func TestStorage_getPoolClientLocked_ReadError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	require.NoError(t, storage.CreateUserPoolClient(&UserPoolClientMetadata{
		UserPoolID: "us-east-1_Test12345",
		ClientID:   "testclientid0000000000000000",
		ClientName: "app",
	}))
	storage.readAll = func(io.Reader) ([]byte, error) {
		return nil, errors.New("read error")
	}
	_, err = storage.GetUserPoolClient("us-east-1_Test12345", "testclientid0000000000000000")
	require.Error(t, err)
	assert.False(t, errors.Is(err, errUserPoolClientNotFound))
}

func TestStorage_UpdateUserPoolClient_FnError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	require.NoError(t, storage.CreateUserPoolClient(&UserPoolClientMetadata{
		UserPoolID: "us-east-1_Test12345",
		ClientID:   "testclientid0000000000000000",
		ClientName: "app",
	}))
	err = storage.UpdateUserPoolClient(
		"us-east-1_Test12345",
		"testclientid0000000000000000",
		func(*UserPoolClientMetadata) error {
			return errors.New("fn error")
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fn error")
}

func TestStorage_DeleteUserPoolClient_RemoveError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	require.NoError(t, storage.CreateUserPoolClient(&UserPoolClientMetadata{
		UserPoolID: "us-east-1_Test12345",
		ClientID:   "testclientid0000000000000000",
		ClientName: "app",
	}))
	storage.removeFile = func(string) error {
		return errors.New("permission denied")
	}
	err = storage.DeleteUserPoolClient("us-east-1_Test12345", "testclientid0000000000000000")
	require.Error(t, err)
	assert.False(t, errors.Is(err, errUserPoolClientNotFound))
}

func TestStorage_ListUserPoolClients_ZeroMaxResults(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	_, _, err = storage.ListUserPoolClients("us-east-1_Test12345", 0, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxResults must be positive")
}

func TestStorage_ListUserPoolClients_ListDirError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	require.NoError(t, storage.CreateUserPoolClient(&UserPoolClientMetadata{
		UserPoolID: "us-east-1_Test12345",
		ClientID:   "testclientid0000000000000000",
		ClientName: "app",
	}))
	realList := storage.listDirFn
	storage.listDirFn = func(name string) ([]os.DirEntry, error) {
		if filepath.Base(name) == "clients" {
			return nil, errors.New("permission denied")
		}
		return realList(name)
	}
	_, _, err = storage.ListUserPoolClients("us-east-1_Test12345", 10, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

func TestStorage_ListUserPoolClients_ReadError(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	require.NoError(
		t,
		storage.CreateUserPool(&UserPoolMetadata{ID: "us-east-1_Test12345", Name: "test"}),
	)
	require.NoError(t, storage.CreateUserPoolClient(&UserPoolClientMetadata{
		UserPoolID: "us-east-1_Test12345",
		ClientID:   "testclientid0000000000000000",
		ClientName: "app",
	}))
	realReadAll := storage.readAll
	calls := 0
	storage.readAll = func(r io.Reader) ([]byte, error) {
		calls++
		if calls == 1 {
			return realReadAll(r) // pool metadata read: success
		}
		return nil, errors.New("read error") // client file read: fail
	}
	_, _, err = storage.ListUserPoolClients("us-east-1_Test12345", 10, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read user pool client")
}

// fakeDirEntry is a minimal os.DirEntry implementation for tests.
type fakeDirEntry string

func (f fakeDirEntry) Name() string               { return string(f) }
func (f fakeDirEntry) IsDir() bool                { return false }
func (f fakeDirEntry) Type() os.FileMode          { return 0 }
func (f fakeDirEntry) Info() (os.FileInfo, error) { return nil, nil }

func TestRouter_ErrorResponseFormat(t *testing.T) {
	ro := newTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService.UnknownOp")
	w := httptest.NewRecorder()

	ro.ServeHTTP(w, req)

	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Contains(t, body, "__type", "error response must contain __type field")
	assert.Contains(t, body, "message", "error response must contain message field")
	_, hasCode := body["code"]
	assert.False(t, hasCode, "Cognito errors use __type, not code")
}
