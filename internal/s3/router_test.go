package s3

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

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
		t.Run("POST without query param returns 501", func(t *testing.T) {
			ro := newTestRouter(t)
			req := httptest.NewRequest(http.MethodPost, "/my-bucket", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotImplemented, w.Code)
		})

		t.Run("PATCH returns 405", func(t *testing.T) {
			ro := newTestRouter(t)
			req := httptest.NewRequest(http.MethodPatch, "/my-bucket", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
			assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
		})

		t.Run("GET returns 404 for non-existent bucket (ListObjects V1)", func(t *testing.T) {
			ro := newTestRouter(t)
			req := httptest.NewRequest(http.MethodGet, "/my-bucket", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
		})
	})

	t.Run("object path", func(t *testing.T) {
		methods := []struct {
			method string
			want   int
		}{
			{http.MethodGet, http.StatusNotFound},    // 404: bucket does not exist
			{http.MethodHead, http.StatusNotFound},   // 404: bucket does not exist
			{http.MethodPut, http.StatusNotFound},    // 404: bucket does not exist
			{http.MethodDelete, http.StatusNotFound}, // 404: bucket does not exist
			{
				http.MethodPost,
				http.StatusNotImplemented,
			}, // 501: POST without multipart query params
			{http.MethodPatch, http.StatusMethodNotAllowed}, // 405: unsupported method
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
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		putReq := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/obj.txt",
			strings.NewReader("hello"),
		)
		putReq.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

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
	listBucketsErr              error
	createBucketErr             error
	deleteBucketErr             error
	bucketExists                bool
	getBucketRegionStr          string
	getBucketRegionErr          error
	putObjectErr                error
	putObjectMeta               ObjectMetadata
	getObjectFile               *os.File
	getObjectMeta               ObjectMetadata
	getObjectErr                error
	copyObjectMeta              ObjectMetadata
	copyObjectErr               error
	deleteObjectErr             error
	headObjectMeta              ObjectMetadata
	headObjectErr               error
	listObjectsObjs             []ObjectInfo
	listObjectsErr              error
	createMultipartUploadID     string
	createMultipartUploadErr    error
	uploadPartETag              string
	uploadPartErr               error
	completeMultipartUploadMeta ObjectMetadata
	completeMultipartUploadErr  error
	abortMultipartUploadErr     error
	listMultipartUploadsResult  []MultipartUploadInfo
	listMultipartUploadsErr     error
	listPartsUploadMeta         uploadMeta
	listPartsResult             []PartInfo
	listPartsErr                error
}

func (m *mockStore) ListBuckets() ([]BucketInfo, error)    { return nil, m.listBucketsErr }
func (m *mockStore) CreateBucket(_ string, _ string) error { return m.createBucketErr }
func (m *mockStore) DeleteBucket(_ string) error           { return m.deleteBucketErr }
func (m *mockStore) BucketExists(_ string) bool            { return m.bucketExists }
func (m *mockStore) GetBucketRegion(_ string) (string, error) {
	return m.getBucketRegionStr, m.getBucketRegionErr
}
func (m *mockStore) PutObject(_ string, _ string, _ io.Reader, _ string) (ObjectMetadata, error) {
	return m.putObjectMeta, m.putObjectErr
}
func (m *mockStore) GetObject(_ string, _ string) (*os.File, ObjectMetadata, error) {
	return m.getObjectFile, m.getObjectMeta, m.getObjectErr
}
func (m *mockStore) CopyObject(_, _, _, _ string) (ObjectMetadata, error) {
	return m.copyObjectMeta, m.copyObjectErr
}
func (m *mockStore) DeleteObject(_ string, _ string) error { return m.deleteObjectErr }
func (m *mockStore) HeadObject(_ string, _ string) (ObjectMetadata, error) {
	return m.headObjectMeta, m.headObjectErr
}
func (m *mockStore) ListObjects(_ string) ([]ObjectInfo, error) {
	return m.listObjectsObjs, m.listObjectsErr
}
func (m *mockStore) CreateMultipartUpload(_ string, _ string, _ string) (string, error) {
	return m.createMultipartUploadID, m.createMultipartUploadErr
}
func (m *mockStore) UploadPart(_ string, _ int, _ io.Reader) (string, error) {
	return m.uploadPartETag, m.uploadPartErr
}
func (m *mockStore) CompleteMultipartUpload(_ string, _ []CompletePart) (ObjectMetadata, error) {
	return m.completeMultipartUploadMeta, m.completeMultipartUploadErr
}
func (m *mockStore) AbortMultipartUpload(_ string) error { return m.abortMultipartUploadErr }
func (m *mockStore) ListMultipartUploads(_ string) ([]MultipartUploadInfo, error) {
	return m.listMultipartUploadsResult, m.listMultipartUploadsErr
}
func (m *mockStore) ListParts(_ string) (uploadMeta, []PartInfo, error) {
	return m.listPartsUploadMeta, m.listPartsResult, m.listPartsErr
}

func newRouterWithMock(store *mockStore) *Router {
	return &Router{storage: store, now: time.Now}
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

	t.Run("logs warning when response body write fails", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		putReq := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		w := &failBodyWriter{ResponseRecorder: httptest.NewRecorder()}
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// failBodyWriter wraps ResponseRecorder and returns an error on Write after headers are sent.
type failBodyWriter struct {
	*httptest.ResponseRecorder
	headerWritten bool
}

func (f *failBodyWriter) WriteHeader(code int) {
	f.headerWritten = true
	f.ResponseRecorder.WriteHeader(code)
}

func (f *failBodyWriter) Write(b []byte) (int, error) {
	if f.headerWritten {
		return 0, errors.New("simulated write failure")
	}
	return f.ResponseRecorder.Write(b)
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

func TestRouterHeadObject(t *testing.T) {
	t.Run("returns 200 with headers and no body", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		putReq := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/obj.txt",
			strings.NewReader("hello"),
		)
		putReq.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "text/plain", w.Header().Get("Content-Type"))
		assert.Equal(t, "5", w.Header().Get("Content-Length"))
		assert.NotEmpty(t, w.Header().Get("ETag"))
		assert.NotEmpty(t, w.Header().Get("Last-Modified"))
		assert.Empty(t, w.Body.String())
	})

	t.Run("returns 404 when object does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/missing.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("returns 404 when bucket does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodHead, "/no-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("returns 500 on unexpected storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{headObjectErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestRouterListObjectsV2(t *testing.T) {
	t.Run("returns 200 with object list", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"a.txt", "b.txt", "c.txt"} {
			req := httptest.NewRequest(http.MethodPut, "/my-bucket/"+key, strings.NewReader("data"))
			req.Header.Set("Content-Type", "text/plain")
			ro.ServeHTTP(httptest.NewRecorder(), req)
		}

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
		body := w.Body.String()
		assert.Contains(t, body, "ListBucketResult")
		assert.Contains(t, body, "a.txt")
		assert.Contains(t, body, "b.txt")
		assert.Contains(t, body, "c.txt")
	})

	t.Run("returns 200 with empty list when bucket is empty", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "ListBucketResult")
	})

	t.Run("returns 200 when list-type is not 2 (ListObjects V1)", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "ListBucketResult")
	})

	t.Run("returns 404 when bucket does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodGet, "/no-bucket?list-type=2", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 500 on unexpected storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{listObjectsErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("filters by prefix and echoes prefix in response", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"logs/a.txt", "logs/b.txt", "data/c.txt"} {
			req := httptest.NewRequest(http.MethodPut, "/my-bucket/"+key, strings.NewReader("data"))
			req.Header.Set("Content-Type", "text/plain")
			ro.ServeHTTP(httptest.NewRecorder(), req)
		}

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2&prefix=logs/", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "logs/a.txt")
		assert.Contains(t, body, "logs/b.txt")
		assert.NotContains(t, body, "data/c.txt")
		assert.Contains(t, body, "<Prefix>logs/</Prefix>")
	})

	t.Run("returns all objects when prefix is empty", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"logs/a.txt", "data/b.txt"} {
			req := httptest.NewRequest(http.MethodPut, "/my-bucket/"+key, strings.NewReader("data"))
			req.Header.Set("Content-Type", "text/plain")
			ro.ServeHTTP(httptest.NewRecorder(), req)
		}

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		body := w.Body.String()
		assert.Contains(t, body, "logs/a.txt")
		assert.Contains(t, body, "data/b.txt")
	})
}

func TestRouterCopyObject(t *testing.T) {
	setup := func(t *testing.T) *Router {
		t.Helper()
		ro := newTestRouter(t)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/src-bucket", nil),
		)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/dst-bucket", nil),
		)
		putReq := httptest.NewRequest(
			http.MethodPut,
			"/src-bucket/orig.txt",
			strings.NewReader("hello"),
		)
		putReq.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)
		return ro
	}

	t.Run("copies object and returns 200 with CopyObjectResult XML", func(t *testing.T) {
		ro := setup(t)
		req := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		req.Header.Set("x-amz-copy-source", "/src-bucket/orig.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
		assert.Contains(t, w.Body.String(), "CopyObjectResult")
		assert.Contains(t, w.Body.String(), "ETag")
	})

	t.Run("copied object is retrievable with correct content", func(t *testing.T) {
		ro := setup(t)
		copyReq := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		copyReq.Header.Set("x-amz-copy-source", "/src-bucket/orig.txt")
		ro.ServeHTTP(httptest.NewRecorder(), copyReq)

		req := httptest.NewRequest(http.MethodGet, "/dst-bucket/copy.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "hello", w.Body.String())
	})

	t.Run("handles URL-encoded copy source", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		putReq := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/path/to/obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		req := httptest.NewRequest(http.MethodPut, "/my-bucket/copy.txt", nil)
		req.Header.Set("x-amz-copy-source", "/my-bucket/path%2Fto%2Fobj.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("returns 404 when source bucket does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/dst-bucket", nil),
		)
		req := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		req.Header.Set("x-amz-copy-source", "/missing-bucket/obj.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 404 when source key does not exist", func(t *testing.T) {
		ro := setup(t)
		req := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		req.Header.Set("x-amz-copy-source", "/src-bucket/missing.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchKey")
	})

	t.Run("returns 404 when destination bucket does not exist", func(t *testing.T) {
		ro := setup(t)
		req := httptest.NewRequest(http.MethodPut, "/no-bucket/copy.txt", nil)
		req.Header.Set("x-amz-copy-source", "/src-bucket/orig.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 400 when copy source is invalid percent-encoding", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/dst-bucket", nil),
		)
		req := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		req.Header.Set("x-amz-copy-source", "%ZZ")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("returns 400 when copy source has no key", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/dst-bucket", nil),
		)
		req := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		req.Header.Set("x-amz-copy-source", "/only-bucket")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("returns 500 on unexpected storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{copyObjectErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		req.Header.Set("x-amz-copy-source", "/src-bucket/orig.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PUT without x-amz-copy-source still routes to handlePutObject", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		req := httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("data"))
		req.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.NotEmpty(t, w.Header().Get("ETag"))
	})
}

func TestRouterGetBucketLocation(t *testing.T) {
	t.Run(
		"returns 200 with empty LocationConstraint for bucket created without region",
		func(t *testing.T) {
			ro := newTestRouter(t)
			// Create bucket without Authorization header → region stored as ""
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil),
			)

			req := httptest.NewRequest(http.MethodGet, "/my-bucket?location", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
			assert.Contains(t, w.Body.String(), "<LocationConstraint></LocationConstraint>")
		},
	)

	t.Run("returns region the bucket was created in", func(t *testing.T) {
		ro := newTestRouter(t)
		// Create bucket with Authorization header → region "us-west-2" is stored
		createReq := httptest.NewRequest(http.MethodPut, "/my-bucket", nil)
		createReq.Header.Set(
			"Authorization",
			"AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20230101/us-west-2/s3/aws4_request, SignedHeaders=host, Signature=abc123",
		)
		ro.ServeHTTP(httptest.NewRecorder(), createReq)

		// GetBucketLocation reads from storage, not the request header
		req := httptest.NewRequest(http.MethodGet, "/my-bucket?location", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "<LocationConstraint>us-west-2</LocationConstraint>")
	})

	t.Run("returns 404 when bucket does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodGet, "/no-bucket?location", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{getBucketRegionErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodGet, "/my-bucket?location", nil)
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

	t.Run("parses presigned URL credential from query param", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		q := req.URL.Query()
		q.Set("X-Amz-Credential", "AKIAIOSFODNN7EXAMPLE/20231001/ap-northeast-1/s3/aws4_request")
		req.URL.RawQuery = q.Encode()
		ctx := ParseSigV4(req)
		assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", ctx.AccessKeyID)
		assert.Equal(t, "ap-northeast-1", ctx.Region)
		assert.Equal(t, "s3", ctx.Service)
	})

	t.Run("query param credential takes precedence over Authorization header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(
			"Authorization",
			"AWS4-HMAC-SHA256 Credential=HEADERKEY/20231001/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=abc",
		)
		q := req.URL.Query()
		q.Set("X-Amz-Credential", "QUERYKEY/20231001/eu-west-1/s3/aws4_request")
		req.URL.RawQuery = q.Encode()
		ctx := ParseSigV4(req)
		assert.Equal(t, "QUERYKEY", ctx.AccessKeyID)
		assert.Equal(t, "eu-west-1", ctx.Region)
	})

	t.Run("presigned credential with too few fields returns empty context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		q := req.URL.Query()
		q.Set("X-Amz-Credential", "KEY/20231001")
		req.URL.RawQuery = q.Encode()
		ctx := ParseSigV4(req)
		assert.Empty(t, ctx.AccessKeyID)
		assert.Empty(t, ctx.Region)
	})
}

func TestRouterMultipartUpload(t *testing.T) {
	setup := func(t *testing.T) (*Router, string) {
		t.Helper()
		ro := newTestRouter(t)
		// Create bucket
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		return ro, "/my-bucket/big.txt"
	}

	initiateUpload := func(t *testing.T, ro *Router, path string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path+"?uploads", nil)
		req.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		var result initiateMultipartUploadResult
		require.NoError(t, xml.NewDecoder(w.Body).Decode(&result))
		return result.UploadID
	}

	uploadPart := func(t *testing.T, ro *Router, path, uploadID string, partNumber int, body string) string {
		t.Helper()
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber="+strconv.Itoa(partNumber)+"&uploadId="+uploadID,
			strings.NewReader(body),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Header().Get("ETag")
	}

	completeUpload := func(t *testing.T, ro *Router, path, uploadID string, parts []xmlCompletePart) *httptest.ResponseRecorder {
		t.Helper()
		body, err := xml.Marshal(completeMultipartUploadRequest{Parts: parts})
		require.NoError(t, err)
		req := httptest.NewRequest(
			http.MethodPost,
			path+"?uploadId="+uploadID,
			strings.NewReader(string(body)),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		return w
	}

	t.Run("full lifecycle returns 200 with ETag containing part count", func(t *testing.T) {
		ro, path := setup(t)
		uploadID := initiateUpload(t, ro, path)

		etag1 := uploadPart(t, ro, path, uploadID, 1, "hello ")
		etag2 := uploadPart(t, ro, path, uploadID, 2, "world")

		w := completeUpload(t, ro, path, uploadID, []xmlCompletePart{
			{PartNumber: 1, ETag: etag1},
			{PartNumber: 2, ETag: etag2},
		})
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "-2")

		getReq := httptest.NewRequest(http.MethodGet, path, nil)
		getW := httptest.NewRecorder()
		ro.ServeHTTP(getW, getReq)
		assert.Equal(t, http.StatusOK, getW.Code)
		assert.Equal(t, "hello world", getW.Body.String())
	})

	t.Run("CreateMultipartUpload returns 404 for missing bucket", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodPost, "/no-bucket/key?uploads", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("CreateMultipartUpload returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{createMultipartUploadErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodPost, "/my-bucket/key?uploads", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("UploadPart returns 400 for invalid partNumber", func(t *testing.T) {
		ro, path := setup(t)
		uploadID := initiateUpload(t, ro, path)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=0&uploadId="+uploadID,
			strings.NewReader("data"),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("UploadPart returns 400 for non-integer partNumber", func(t *testing.T) {
		ro, path := setup(t)
		uploadID := initiateUpload(t, ro, path)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=abc&uploadId="+uploadID,
			strings.NewReader("data"),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("UploadPart returns 400 for missing uploadId", func(t *testing.T) {
		ro, path := setup(t)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId=",
			strings.NewReader("data"),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("UploadPart returns 404 for unknown uploadId", func(t *testing.T) {
		ro, path := setup(t)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId=nonexistent",
			strings.NewReader("data"),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchUpload")
	})

	t.Run("UploadPart returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{uploadPartErr: errors.New("disk failure")})
		req := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/key?partNumber=1&uploadId=abc",
			strings.NewReader("data"),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("CompleteMultipartUpload returns 400 for missing uploadId", func(t *testing.T) {
		ro, path := setup(t)
		req := httptest.NewRequest(
			http.MethodPost,
			path+"?uploadId=",
			strings.NewReader("<CompleteMultipartUpload/>"),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("CompleteMultipartUpload returns 400 for malformed XML", func(t *testing.T) {
		ro, path := setup(t)
		uploadID := initiateUpload(t, ro, path)
		req := httptest.NewRequest(
			http.MethodPost,
			path+"?uploadId="+uploadID,
			strings.NewReader("not-xml"),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "MalformedXML")
	})

	t.Run("CompleteMultipartUpload returns 404 for unknown uploadId", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{completeMultipartUploadErr: ErrUploadNotFound})
		body, _ := xml.Marshal(
			completeMultipartUploadRequest{
				Parts: []xmlCompletePart{{PartNumber: 1, ETag: `"abc"`}},
			},
		)
		req := httptest.NewRequest(
			http.MethodPost,
			"/my-bucket/key?uploadId=nonexistent",
			strings.NewReader(string(body)),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchUpload")
	})

	t.Run("CompleteMultipartUpload returns 400 for invalid part", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{completeMultipartUploadErr: ErrInvalidPart})
		body, _ := xml.Marshal(
			completeMultipartUploadRequest{
				Parts: []xmlCompletePart{{PartNumber: 1, ETag: `"abc"`}},
			},
		)
		req := httptest.NewRequest(
			http.MethodPost,
			"/my-bucket/key?uploadId=abc",
			strings.NewReader(string(body)),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidPart")
	})

	t.Run("CompleteMultipartUpload returns 400 for invalid part order", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{completeMultipartUploadErr: ErrInvalidPartOrder})
		body, _ := xml.Marshal(
			completeMultipartUploadRequest{
				Parts: []xmlCompletePart{{PartNumber: 1, ETag: `"abc"`}},
			},
		)
		req := httptest.NewRequest(
			http.MethodPost,
			"/my-bucket/key?uploadId=abc",
			strings.NewReader(string(body)),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidPartOrder")
	})

	t.Run("CompleteMultipartUpload returns 404 for missing bucket", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{completeMultipartUploadErr: ErrBucketNotFound})
		body, _ := xml.Marshal(
			completeMultipartUploadRequest{
				Parts: []xmlCompletePart{{PartNumber: 1, ETag: `"abc"`}},
			},
		)
		req := httptest.NewRequest(
			http.MethodPost,
			"/my-bucket/key?uploadId=abc",
			strings.NewReader(string(body)),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("CompleteMultipartUpload returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{completeMultipartUploadErr: errors.New("disk failure")})
		body, _ := xml.Marshal(
			completeMultipartUploadRequest{
				Parts: []xmlCompletePart{{PartNumber: 1, ETag: `"abc"`}},
			},
		)
		req := httptest.NewRequest(
			http.MethodPost,
			"/my-bucket/key?uploadId=abc",
			strings.NewReader(string(body)),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("AbortMultipartUpload returns 204", func(t *testing.T) {
		ro, path := setup(t)
		uploadID := initiateUpload(t, ro, path)
		req := httptest.NewRequest(http.MethodDelete, path+"?uploadId="+uploadID, nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("AbortMultipartUpload returns 400 for missing uploadId", func(t *testing.T) {
		ro, path := setup(t)
		req := httptest.NewRequest(http.MethodDelete, path+"?uploadId=", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("AbortMultipartUpload returns 404 for unknown uploadId", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{abortMultipartUploadErr: ErrUploadNotFound})
		req := httptest.NewRequest(http.MethodDelete, "/my-bucket/key?uploadId=nonexistent", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchUpload")
	})

	t.Run("AbortMultipartUpload returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{abortMultipartUploadErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodDelete, "/my-bucket/key?uploadId=abc", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("DELETE without uploadId still routes to DeleteObject", func(t *testing.T) {
		ro, path := setup(t)
		putReq := httptest.NewRequest(http.MethodPut, path, strings.NewReader("data"))
		putReq.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		req := httptest.NewRequest(http.MethodDelete, path, nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})
}

func TestRouterPresignedURL(t *testing.T) {
	const amzDate = "20240101T000000Z"
	baseTime, err := time.Parse("20060102T150405Z", amzDate)
	require.NoError(t, err)

	presignedReq := func(method, path string) *http.Request {
		req := httptest.NewRequest(method, path, nil)
		q := req.URL.Query()
		q.Set("X-Amz-Signature", "fakesignature")
		q.Set("X-Amz-Date", amzDate)
		q.Set("X-Amz-Expires", "3600")
		req.URL.RawQuery = q.Encode()
		return req
	}

	setup := func(t *testing.T, now time.Time) (*Router, string) {
		t.Helper()
		ro := newTestRouter(t)
		ro.now = func() time.Time { return now }
		bucket := "presign-bucket"
		putBucket := httptest.NewRequest(http.MethodPut, "/"+bucket, nil)
		ro.ServeHTTP(httptest.NewRecorder(), putBucket)
		return ro, "/" + bucket + "/file.txt"
	}

	t.Run("expired presigned GET returns 403", func(t *testing.T) {
		ro, path := setup(t, baseTime.Add(2*time.Hour))
		req := presignedReq(http.MethodGet, path)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "AccessDenied")
	})

	t.Run("expired presigned PUT returns 403", func(t *testing.T) {
		ro, path := setup(t, baseTime.Add(2*time.Hour))
		req := presignedReq(http.MethodPut, path)
		req.Body = io.NopCloser(strings.NewReader("hello"))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("valid presigned PUT uploads object", func(t *testing.T) {
		ro, path := setup(t, baseTime.Add(30*time.Minute))
		req := presignedReq(http.MethodPut, path)
		req.Body = io.NopCloser(strings.NewReader("hello presigned"))
		req.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("valid presigned GET serves object", func(t *testing.T) {
		ro, path := setup(t, baseTime.Add(30*time.Minute))
		putReq := httptest.NewRequest(http.MethodPut, path, strings.NewReader("hello presigned"))
		putReq.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		req := presignedReq(http.MethodGet, path)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "hello presigned", w.Body.String())
	})

	t.Run("missing X-Amz-Date is treated as not expired", func(t *testing.T) {
		ro, path := setup(t, baseTime.Add(30*time.Minute))
		putReq := httptest.NewRequest(http.MethodPut, path, strings.NewReader("data"))
		putReq.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		req := httptest.NewRequest(http.MethodGet, path, nil)
		q := req.URL.Query()
		q.Set("X-Amz-Signature", "fakesignature")
		// No X-Amz-Date or X-Amz-Expires
		req.URL.RawQuery = q.Encode()
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("unsupported algorithm returns 400", func(t *testing.T) {
		ro, path := setup(t, baseTime.Add(30*time.Minute))
		req := httptest.NewRequest(http.MethodGet, path, nil)
		q := req.URL.Query()
		q.Set("X-Amz-Signature", "fakesignature")
		q.Set("X-Amz-Algorithm", "UNSUPPORTED-ALGO")
		q.Set("X-Amz-Date", amzDate)
		q.Set("X-Amz-Expires", "3600")
		req.URL.RawQuery = q.Encode()
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "AuthorizationQueryParametersError")
	})

	t.Run("X-Amz-Expires exceeds maximum returns 400", func(t *testing.T) {
		ro, path := setup(t, baseTime.Add(30*time.Minute))
		req := httptest.NewRequest(http.MethodGet, path, nil)
		q := req.URL.Query()
		q.Set("X-Amz-Signature", "fakesignature")
		q.Set("X-Amz-Date", amzDate)
		q.Set("X-Amz-Expires", "604801")
		req.URL.RawQuery = q.Encode()
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "AuthorizationQueryParametersError")
	})
}

func TestCheckPresigned(t *testing.T) {
	const amzDate = "20240101T120000Z"
	baseTime, err := time.Parse("20060102T150405Z", amzDate)
	require.NoError(t, err)

	makeReq := func(algo, date, expires string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		q := req.URL.Query()
		if algo != "" {
			q.Set("X-Amz-Algorithm", algo)
		}
		if date != "" {
			q.Set("X-Amz-Date", date)
		}
		if expires != "" {
			q.Set("X-Amz-Expires", expires)
		}
		req.URL.RawQuery = q.Encode()
		return req
	}

	tests := []struct {
		name       string
		req        *http.Request
		now        time.Time
		wantStatus int
		wantCode   string
	}{
		{
			name:       "valid: not yet expired",
			req:        makeReq("AWS4-HMAC-SHA256", amzDate, "3600"),
			now:        baseTime.Add(30 * time.Minute),
			wantStatus: 0,
		},
		{
			name:       "expired: exactly at expiry boundary",
			req:        makeReq("AWS4-HMAC-SHA256", amzDate, "3600"),
			now:        baseTime.Add(3600 * time.Second),
			wantStatus: http.StatusForbidden,
			wantCode:   "AccessDenied",
		},
		{
			name:       "valid: missing algorithm is allowed",
			req:        makeReq("", amzDate, "3600"),
			now:        baseTime.Add(30 * time.Minute),
			wantStatus: 0,
		},
		{
			name:       "valid: X-Amz-Expires=604800 (max)",
			req:        makeReq("AWS4-HMAC-SHA256", amzDate, "604800"),
			now:        baseTime.Add(30 * time.Minute),
			wantStatus: 0,
		},
		{
			name:       "valid: missing date/expires passes through",
			req:        makeReq("AWS4-HMAC-SHA256", "", ""),
			now:        baseTime.Add(2 * time.Hour),
			wantStatus: 0,
		},
		{
			name:       "expired: one second past expiry",
			req:        makeReq("AWS4-HMAC-SHA256", amzDate, "3600"),
			now:        baseTime.Add(3601 * time.Second),
			wantStatus: http.StatusForbidden,
			wantCode:   "AccessDenied",
		},
		{
			name:       "invalid algorithm",
			req:        makeReq("UNSUPPORTED-ALGO", amzDate, "3600"),
			now:        baseTime.Add(30 * time.Minute),
			wantStatus: http.StatusBadRequest,
			wantCode:   "AuthorizationQueryParametersError",
		},
		{
			name:       "X-Amz-Expires=0 is invalid",
			req:        makeReq("AWS4-HMAC-SHA256", amzDate, "0"),
			now:        baseTime.Add(30 * time.Minute),
			wantStatus: http.StatusBadRequest,
			wantCode:   "AuthorizationQueryParametersError",
		},
		{
			name:       "X-Amz-Expires exceeds 604800",
			req:        makeReq("AWS4-HMAC-SHA256", amzDate, "604801"),
			now:        baseTime.Add(30 * time.Minute),
			wantStatus: http.StatusBadRequest,
			wantCode:   "AuthorizationQueryParametersError",
		},
		{
			name:       "X-Amz-Expires negative is invalid",
			req:        makeReq("AWS4-HMAC-SHA256", amzDate, "-1"),
			now:        baseTime.Add(30 * time.Minute),
			wantStatus: http.StatusBadRequest,
			wantCode:   "AuthorizationQueryParametersError",
		},
		{
			name:       "non-numeric X-Amz-Expires is invalid",
			req:        makeReq("AWS4-HMAC-SHA256", amzDate, "notanumber"),
			now:        baseTime.Add(2 * time.Hour),
			wantStatus: http.StatusBadRequest,
			wantCode:   "AuthorizationQueryParametersError",
		},
		{
			name:       "invalid X-Amz-Date passes through",
			req:        makeReq("AWS4-HMAC-SHA256", "notadate", "3600"),
			now:        baseTime.Add(2 * time.Hour),
			wantStatus: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, code, _ := checkPresigned(tt.req, tt.now)
			assert.Equal(t, tt.wantStatus, status)
			if tt.wantCode != "" {
				assert.Equal(t, tt.wantCode, code)
			}
		})
	}
}

func TestRouterDeleteObjects(t *testing.T) {
	const xmlBody = `<Delete><Object><Key>a.txt</Key></Object><Object><Key>b.txt</Key></Object></Delete>`

	t.Run("deletes multiple objects and returns Deleted elements", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"a.txt", "b.txt"} {
			ro.ServeHTTP(
				httptest.NewRecorder(),
				putRequest("/my-bucket/"+key, "hello"),
			)
		}

		req := httptest.NewRequest(http.MethodPost, "/my-bucket?delete", strings.NewReader(xmlBody))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "<Key>a.txt</Key>")
		assert.Contains(t, body, "<Key>b.txt</Key>")
		assert.NotContains(t, body, "<Error>")
	})

	t.Run("treats non-existent objects as successfully deleted", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodPost, "/my-bucket?delete", strings.NewReader(xmlBody))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "<Key>a.txt</Key>")
		assert.NotContains(t, w.Body.String(), "<Error>")
	})

	t.Run("quiet mode omits Deleted elements", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/a.txt", "hello"))

		body := `<Delete><Quiet>true</Quiet><Object><Key>a.txt</Key></Object></Delete>`
		req := httptest.NewRequest(http.MethodPost, "/my-bucket?delete", strings.NewReader(body))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.NotContains(t, w.Body.String(), "<Deleted>")
		assert.NotContains(t, w.Body.String(), "<Error>")
	})

	t.Run("returns 404 when bucket does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodPost, "/no-bucket?delete", strings.NewReader(xmlBody))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 400 on malformed XML", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(
			http.MethodPost,
			"/my-bucket?delete",
			strings.NewReader("not xml"),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "MalformedXML")
	})

	t.Run("returns Error element on storage failure", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			deleteObjectErr: errors.New("disk full"),
		})
		req := httptest.NewRequest(
			http.MethodPost,
			"/my-bucket?delete",
			strings.NewReader(`<Delete><Object><Key>a.txt</Key></Object></Delete>`),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "<Error>")
		assert.Contains(t, body, "InternalError")
	})
}

// putRequest is a helper that creates a PUT request with a text/plain body.
func putRequest(path, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	return req
}

func TestRouterListObjects(t *testing.T) {
	t.Run("returns 200 with object list", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"a.txt", "b.txt", "c.txt"} {
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/"+key, "data"))
		}

		req := httptest.NewRequest(http.MethodGet, "/my-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
		body := w.Body.String()
		assert.Contains(t, body, "ListBucketResult")
		assert.Contains(t, body, "a.txt")
		assert.Contains(t, body, "b.txt")
		assert.Contains(t, body, "c.txt")
	})

	t.Run("returns 404 when bucket does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodGet, "/no-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{listObjectsErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodGet, "/my-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("filters by prefix", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"logs/a.txt", "logs/b.txt", "data/c.txt"} {
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/"+key, "data"))
		}

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?prefix=logs/", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "logs/a.txt")
		assert.Contains(t, body, "logs/b.txt")
		assert.NotContains(t, body, "data/c.txt")
	})

	t.Run("paginates with marker", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"a.txt", "b.txt", "c.txt"} {
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/"+key, "data"))
		}

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?marker=a.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.NotContains(t, body, "<Key>a.txt</Key>")
		assert.Contains(t, body, "<Key>b.txt</Key>")
		assert.Contains(t, body, "<Key>c.txt</Key>")
	})

	t.Run("respects max-keys and sets IsTruncated with NextMarker", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"a.txt", "b.txt", "c.txt"} {
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/"+key, "data"))
		}

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?max-keys=2", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "<IsTruncated>true</IsTruncated>")
		assert.Contains(t, body, "<NextMarker>b.txt</NextMarker>")
		assert.NotContains(t, body, "c.txt")
	})

	t.Run("groups common prefixes with delimiter", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"logs/a.txt", "logs/b.txt", "data/c.txt"} {
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/"+key, "data"))
		}

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?delimiter=/", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "<Delimiter>/</Delimiter>")
		// CommonPrefixes must appear in alphabetical order.
		dataIdx := strings.Index(body, "<Prefix>data/</Prefix>")
		logsIdx := strings.Index(body, "<Prefix>logs/</Prefix>")
		assert.Greater(t, dataIdx, -1)
		assert.Greater(t, logsIdx, -1)
		assert.Less(t, dataIdx, logsIdx)
		assert.NotContains(t, body, "a.txt")
	})

	t.Run("invalid max-keys is ignored and uses default 1000", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?max-keys=abc", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "<MaxKeys>1000</MaxKeys>")
	})
}

func TestRouterListMultipartUploads(t *testing.T) {
	t.Run("returns 200 with in-progress uploads", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		// Initiate two uploads.
		for _, key := range []string{"big.txt", "other.txt"} {
			req := httptest.NewRequest(
				http.MethodPost,
				"/my-bucket/"+key+"?uploads",
				nil,
			)
			ro.ServeHTTP(httptest.NewRecorder(), req)
		}

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?uploads", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "ListMultipartUploadsResult")
		assert.Contains(t, body, "big.txt")
		assert.Contains(t, body, "other.txt")
	})

	t.Run("returns 200 with empty list when no uploads", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?uploads", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "ListMultipartUploadsResult")
	})

	t.Run("returns 404 when bucket does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodGet, "/no-bucket?uploads", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:            true,
			listMultipartUploadsErr: errors.New("disk failure"),
		})
		req := httptest.NewRequest(http.MethodGet, "/my-bucket?uploads", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestRouterListParts(t *testing.T) {
	t.Run("returns 200 with uploaded parts", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		// Initiate upload and upload two parts.
		initReq := httptest.NewRequest(http.MethodPost, "/my-bucket/big.txt?uploads", nil)
		initW := httptest.NewRecorder()
		ro.ServeHTTP(initW, initReq)
		require.Equal(t, http.StatusOK, initW.Code)

		var initResult struct {
			UploadID string `xml:"UploadId"`
		}
		require.NoError(t, xml.Unmarshal(initW.Body.Bytes(), &initResult))
		uploadID := initResult.UploadID

		for _, pn := range []string{"1", "2"} {
			req := httptest.NewRequest(
				http.MethodPut,
				"/my-bucket/big.txt?partNumber="+pn+"&uploadId="+uploadID,
				strings.NewReader("part data"),
			)
			ro.ServeHTTP(httptest.NewRecorder(), req)
		}

		req := httptest.NewRequest(
			http.MethodGet,
			"/my-bucket/big.txt?uploadId="+uploadID,
			nil,
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "ListPartsResult")
		assert.Contains(t, body, "<PartNumber>1</PartNumber>")
		assert.Contains(t, body, "<PartNumber>2</PartNumber>")
	})

	t.Run("returns 404 when upload does not exist", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(
			http.MethodGet,
			"/my-bucket/big.txt?uploadId=nonexistent",
			nil,
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchUpload")
	})

	t.Run("returns 400 when uploadId is missing", func(t *testing.T) {
		ro := newTestRouter(t)
		// uploadId= (empty string) triggers the missing-uploadId validation path.
		req := httptest.NewRequest(http.MethodGet, "/my-bucket/big.txt?uploadId=", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{listPartsErr: errors.New("disk failure")})
		req := httptest.NewRequest(
			http.MethodGet,
			"/my-bucket/big.txt?uploadId=abc",
			nil,
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}
