package s3

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRouter(t *testing.T) *Router {
	t.Helper()
	storage, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	return NewRouter(storage)
}

func TestParsePath(t *testing.T) {
	tests := []struct {
		path       string
		wantBucket string
		wantKey    string
	}{
		{"/", "", ""},
		{"/my-bucket", "my-bucket", ""},
		{"/my-bucket/", "my-bucket", ""},
		{"/my-bucket/object.txt", "my-bucket", "object.txt"},
		{"/my-bucket/path/to/object.txt", "my-bucket", "path/to/object.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			bucket, key := parsePath(tt.path)
			assert.Equal(t, tt.wantBucket, bucket)
			assert.Equal(t, tt.wantKey, key)
		})
	}
}

func TestRouter(t *testing.T) {
	t.Run("root path", func(t *testing.T) {
		t.Run("non-GET returns 405", func(t *testing.T) {
			ro := newTestRouter(t)
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
			assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
		})
	})

	t.Run("bucket path", func(t *testing.T) {
		t.Run("POST returns 405", func(t *testing.T) {
			ro := newTestRouter(t)
			req := httptest.NewRequest(http.MethodPost, "/my-bucket", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
			assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
		})

		t.Run("GET returns 501", func(t *testing.T) {
			ro := newTestRouter(t)
			req := httptest.NewRequest(http.MethodGet, "/my-bucket", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotImplemented, w.Code)
		})
	})

	t.Run("object path", func(t *testing.T) {
		methods := []struct {
			method string
			want   int
		}{
			{http.MethodGet, http.StatusNotFound}, // 404: bucket does not exist
			{http.MethodHead, http.StatusNotImplemented},
			{http.MethodPut, http.StatusNotFound},    // 404: bucket does not exist
			{http.MethodDelete, http.StatusNotFound}, // 404: bucket does not exist
			{http.MethodPost, http.StatusMethodNotAllowed},
		}
		for _, tt := range methods {
			t.Run(tt.method, func(t *testing.T) {
				ro := newTestRouter(t)
				req := httptest.NewRequest(tt.method, "/my-bucket/object.txt", nil)
				w := httptest.NewRecorder()
				ro.ServeHTTP(w, req)
				assert.Equal(t, tt.want, w.Code)
			})
		}
	})
}

func TestRouterListBuckets(t *testing.T) {
	t.Run("returns 200 with empty list when no buckets exist", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
		assert.Contains(t, w.Body.String(), "ListAllMyBucketsResult")
	})

	t.Run("returns created bucket in list", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "my-bucket")
	})
}

func TestRouterCreateBucket(t *testing.T) {
	t.Run("creates bucket and returns 200 with Location header", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodPut, "/my-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "/my-bucket", w.Header().Get("Location"))
	})

	t.Run("returns 409 when bucket already exists", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodPut, "/my-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, w.Body.String(), "BucketAlreadyOwnedByYou")
	})
}

func TestRouterDeleteBucket(t *testing.T) {
	t.Run("deletes existing bucket and returns 204", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodDelete, "/my-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("returns 404 when bucket does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodDelete, "/nonexistent", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 409 when bucket is not empty", func(t *testing.T) {
		// Object PUT is not yet implemented in the router, so use storage directly.
		storage, err := NewStorage(t.TempDir())
		require.NoError(t, err)
		t.Cleanup(func() { _ = storage.Close() })
		require.NoError(t, storage.CreateBucket("my-bucket"))
		_, err = storage.PutObject("my-bucket", "obj.txt", strings.NewReader("hello"), "text/plain")
		require.NoError(t, err)
		ro := NewRouter(storage)

		req := httptest.NewRequest(http.MethodDelete, "/my-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, w.Body.String(), "BucketNotEmpty")
	})
}

func TestHeadBucket(t *testing.T) {
	t.Run("returns 200 for existing bucket", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodHead, "/my-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("returns 404 for nonexistent bucket", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodHead, "/nonexistent", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// mockStore is a configurable stub for the full store interface.
type mockStore struct {
	listBucketsErr  error
	createBucketErr error
	deleteBucketErr error
	bucketExists    bool
	putObjectErr    error
	putObjectMeta   ObjectMetadata
	getObjectFile   *os.File
	getObjectMeta   ObjectMetadata
	getObjectErr    error
	deleteObjectErr error
	headObjectMeta  ObjectMetadata
	headObjectErr   error
	listObjectsObjs []ObjectInfo
	listObjectsErr  error
}

func (m *mockStore) ListBuckets() ([]BucketInfo, error) { return nil, m.listBucketsErr }
func (m *mockStore) CreateBucket(_ string) error        { return m.createBucketErr }
func (m *mockStore) DeleteBucket(_ string) error        { return m.deleteBucketErr }
func (m *mockStore) BucketExists(_ string) bool         { return m.bucketExists }
func (m *mockStore) PutObject(_ string, _ string, _ io.Reader, _ string) (ObjectMetadata, error) {
	return m.putObjectMeta, m.putObjectErr
}
func (m *mockStore) GetObject(_ string, _ string) (*os.File, ObjectMetadata, error) {
	return m.getObjectFile, m.getObjectMeta, m.getObjectErr
}
func (m *mockStore) DeleteObject(_ string, _ string) error { return m.deleteObjectErr }
func (m *mockStore) HeadObject(_ string, _ string) (ObjectMetadata, error) {
	return m.headObjectMeta, m.headObjectErr
}
func (m *mockStore) ListObjects(_ string) ([]ObjectInfo, error) {
	return m.listObjectsObjs, m.listObjectsErr
}

func newRouterWithMock(store *mockStore) *Router {
	return &Router{storage: store}
}

func TestRouterHandlerErrors(t *testing.T) {
	t.Run("ListBuckets returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{listBucketsErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("CreateBucket returns 500 on unexpected storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{createBucketErr: errors.New("disk full")})
		req := httptest.NewRequest(http.MethodPut, "/my-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("DeleteBucket returns 500 on unexpected storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{deleteBucketErr: errors.New("disk full")})
		req := httptest.NewRequest(http.MethodDelete, "/my-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestRouterPutObject(t *testing.T) {
	t.Run("stores object and returns 200 with ETag", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		body := strings.NewReader("hello world")
		req := httptest.NewRequest(http.MethodPut, "/my-bucket/hello.txt", body)
		req.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NotEmpty(t, w.Header().Get("ETag"))
	})

	t.Run("defaults Content-Type to application/octet-stream", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodPut, "/my-bucket/file.bin", strings.NewReader("data"))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("returns 404 when bucket does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodPut, "/no-bucket/obj.txt", strings.NewReader("data"))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 500 on unexpected storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{putObjectErr: errors.New("disk full")})
		req := httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("data"))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestRouterGetObject(t *testing.T) {
	t.Run("returns 200 with object content and headers", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		body := strings.NewReader("hello world")
		putReq := httptest.NewRequest(http.MethodPut, "/my-bucket/hello.txt", body)
		putReq.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/hello.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "text/plain", w.Header().Get("Content-Type"))
		assert.Equal(t, "11", w.Header().Get("Content-Length"))
		assert.NotEmpty(t, w.Header().Get("ETag"))
		assert.NotEmpty(t, w.Header().Get("Last-Modified"))
		assert.Equal(t, "hello world", w.Body.String())
	})

	t.Run("returns 404 when object does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/missing.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchKey")
	})

	t.Run("returns 404 when bucket does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodGet, "/no-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 500 on unexpected storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{getObjectErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestRouterDeleteObject(t *testing.T) {
	t.Run("deletes existing object and returns 204", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		putReq := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		req := httptest.NewRequest(http.MethodDelete, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("returns 204 even when object does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodDelete, "/my-bucket/missing.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("returns 404 when bucket does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodDelete, "/no-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 500 on unexpected storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{deleteObjectErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodDelete, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestParseSigV4(t *testing.T) {
	t.Run("extracts credentials from valid header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(
			"Authorization",
			"AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20230101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=abc123",
		)

		ctx := ParseSigV4(req)
		assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", ctx.AccessKeyID)
		assert.Equal(t, "us-east-1", ctx.Region)
		assert.Equal(t, "s3", ctx.Service)
	})

	t.Run("returns empty context when Authorization header is missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := ParseSigV4(req)
		assert.Empty(t, ctx.AccessKeyID)
		assert.Empty(t, ctx.Region)
		assert.Empty(t, ctx.Service)
	})

	t.Run("returns empty context when header is not SigV4", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		ctx := ParseSigV4(req)
		assert.Empty(t, ctx.AccessKeyID)
		assert.Empty(t, ctx.Region)
		assert.Empty(t, ctx.Service)
	})

	t.Run("returns empty context when Credential part is missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 SignedHeaders=host, Signature=abc123")
		ctx := ParseSigV4(req)
		assert.Empty(t, ctx.AccessKeyID)
		assert.Empty(t, ctx.Region)
	})

	t.Run("returns empty context when Credential has too few fields", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(
			"Authorization",
			"AWS4-HMAC-SHA256 Credential=KEYID/20230101, SignedHeaders=host, Signature=abc",
		)
		ctx := ParseSigV4(req)
		assert.Empty(t, ctx.AccessKeyID)
		assert.Empty(t, ctx.Region)
	})
}
