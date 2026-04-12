package s3

import (
	"encoding/xml"
	"errors"
	"fmt"
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
	putObjectTaggingErr         error
	getObjectTaggingTags        []Tag
	getObjectTaggingErr         error
	deleteObjectTaggingErr      error
	putBucketTaggingErr         error
	getBucketTaggingTags        []Tag
	getBucketTaggingErr         error
	deleteBucketTaggingErr      error
	putBucketVersioningErr      error
	getBucketVersioningStatus   string
	getBucketVersioningErr      error
	putBucketCorsErr            error
	getBucketCorsRules          []CORSRule
	getBucketCorsErr            error
	deleteBucketCorsErr         error
	putBucketPolicyErr          error
	getBucketPolicyResult       string
	getBucketPolicyErr          error
	deleteBucketPolicyErr       error
}

func (m *mockStore) ListBuckets() ([]BucketInfo, error)    { return nil, m.listBucketsErr }
func (m *mockStore) CreateBucket(_ string, _ string) error { return m.createBucketErr }
func (m *mockStore) DeleteBucket(_ string) error           { return m.deleteBucketErr }
func (m *mockStore) BucketExists(_ string) bool            { return m.bucketExists }
func (m *mockStore) GetBucketRegion(_ string) (string, error) {
	return m.getBucketRegionStr, m.getBucketRegionErr
}

func (m *mockStore) PutObject(
	_ string,
	_ string,
	_ io.Reader,
	_ string,
	_ map[string]string,
) (ObjectMetadata, error) {
	return m.putObjectMeta, m.putObjectErr
}
func (m *mockStore) GetObject(_ string, _ string) (*os.File, ObjectMetadata, error) {
	return m.getObjectFile, m.getObjectMeta, m.getObjectErr
}
func (m *mockStore) CopyObject(_, _, _, _, _ string, _ map[string]string) (ObjectMetadata, error) {
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
func (m *mockStore) PutObjectTagging(_ string, _ string, _ []Tag) error {
	return m.putObjectTaggingErr
}
func (m *mockStore) GetObjectTagging(_ string, _ string) ([]Tag, error) {
	return m.getObjectTaggingTags, m.getObjectTaggingErr
}
func (m *mockStore) DeleteObjectTagging(_ string, _ string) error {
	return m.deleteObjectTaggingErr
}
func (m *mockStore) PutBucketTagging(_ string, _ []Tag) error {
	return m.putBucketTaggingErr
}
func (m *mockStore) GetBucketTagging(_ string) ([]Tag, error) {
	return m.getBucketTaggingTags, m.getBucketTaggingErr
}
func (m *mockStore) DeleteBucketTagging(_ string) error {
	return m.deleteBucketTaggingErr
}
func (m *mockStore) PutBucketVersioning(_ string, _ string) error {
	return m.putBucketVersioningErr
}
func (m *mockStore) GetBucketVersioning(_ string) (string, error) {
	return m.getBucketVersioningStatus, m.getBucketVersioningErr
}
func (m *mockStore) PutBucketCors(_ string, _ []CORSRule) error {
	return m.putBucketCorsErr
}
func (m *mockStore) GetBucketCors(_ string) ([]CORSRule, error) {
	return m.getBucketCorsRules, m.getBucketCorsErr
}
func (m *mockStore) DeleteBucketCors(_ string) error {
	return m.deleteBucketCorsErr
}
func (m *mockStore) PutBucketPolicy(_ string, _ string) error {
	return m.putBucketPolicyErr
}
func (m *mockStore) GetBucketPolicy(_ string) (string, error) {
	return m.getBucketPolicyResult, m.getBucketPolicyErr
}
func (m *mockStore) DeleteBucketPolicy(_ string) error {
	return m.deleteBucketPolicyErr
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

	t.Run("stores and returns user metadata via HeadObject", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		putReq := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set("x-amz-meta-original-filename", "photo.jpg")
		putReq.Header.Set("x-amz-meta-uploader", "user1")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "photo.jpg", w.Header().Get("x-amz-meta-original-filename"))
		assert.Equal(t, "user1", w.Header().Get("x-amz-meta-uploader"))
	})

	t.Run("stores and returns user metadata via GetObject", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		putReq := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set("x-amz-meta-category", "images")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "images", w.Header().Get("x-amz-meta-category"))
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

	t.Run("returns x-amz-tagging-count header when object has tags", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("data")))
		tagging := `<Tagging><TagSet><Tag><Key>k1</Key><Value>v1</Value></Tag><Tag><Key>k2</Key><Value>v2</Value></Tag></TagSet></Tagging>`
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(
				http.MethodPut,
				"/my-bucket/obj.txt?tagging",
				strings.NewReader(tagging),
			),
		)

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "2", w.Header().Get("x-amz-tagging-count"))
	})

	t.Run("does not return x-amz-tagging-count when object has no tags", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("data")))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Header().Get("x-amz-tagging-count"))
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

	t.Run("returns Accept-Ranges header", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(
				http.MethodPut,
				"/my-bucket/obj.txt",
				strings.NewReader("hello world"),
			),
		)

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "bytes", w.Header().Get("Accept-Ranges"))
	})

	t.Run("returns 206 with partial content for Range request", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(
				http.MethodPut,
				"/my-bucket/obj.txt",
				strings.NewReader("hello world"),
			),
		)

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		req.Header.Set("Range", "bytes=0-4")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusPartialContent, w.Code)
		assert.Equal(t, "bytes 0-4/11", w.Header().Get("Content-Range"))
		assert.Equal(t, "5", w.Header().Get("Content-Length"))
		assert.Equal(t, "hello", w.Body.String())
	})

	t.Run("returns 206 for suffix range", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(
				http.MethodPut,
				"/my-bucket/obj.txt",
				strings.NewReader("hello world"),
			),
		)

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		req.Header.Set("Range", "bytes=-5")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusPartialContent, w.Code)
		assert.Equal(t, "world", w.Body.String())
	})

	t.Run("returns 304 for matching If-None-Match", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		// First request to get the ETag.
		getReq := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		getW := httptest.NewRecorder()
		ro.ServeHTTP(getW, getReq)
		etag := getW.Header().Get("ETag")
		require.NotEmpty(t, etag)

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		req.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotModified, w.Code)
		assert.Empty(t, w.Body.String())
	})

	t.Run("returns 200 for non-matching If-None-Match", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		req.Header.Set("If-None-Match", `"stale-etag"`)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("returns 412 for non-matching If-Match", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		req.Header.Set("If-Match", `"wrong-etag"`)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusPreconditionFailed, w.Code)
	})

	t.Run("returns 200 for matching If-Match", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		getReq := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		getW := httptest.NewRecorder()
		ro.ServeHTTP(getW, getReq)
		etag := getW.Header().Get("ETag")

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		req.Header.Set("If-Match", etag)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("returns 304 for If-Modified-Since when not modified", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		req.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotModified, w.Code)
	})

	t.Run("returns 412 for If-Unmodified-Since when modified after", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		req.Header.Set(
			"If-Unmodified-Since",
			time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusPreconditionFailed, w.Code)
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

	t.Run("returns x-amz-tagging-count header when object has tags", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("data")))
		tagging := `<Tagging><TagSet><Tag><Key>k</Key><Value>v</Value></Tag></TagSet></Tagging>`
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(
				http.MethodPut,
				"/my-bucket/obj.txt?tagging",
				strings.NewReader(tagging),
			),
		)

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "1", w.Header().Get("x-amz-tagging-count"))
	})

	t.Run("does not return x-amz-tagging-count when object has no tags", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("data")))

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Header().Get("x-amz-tagging-count"))
	})

	t.Run("returns 500 on unexpected storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{headObjectErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("returns Accept-Ranges header", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "bytes", w.Header().Get("Accept-Ranges"))
	})

	t.Run("returns 304 for matching If-None-Match", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		getReq := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		getW := httptest.NewRecorder()
		ro.ServeHTTP(getW, getReq)
		etag := getW.Header().Get("ETag")
		require.NotEmpty(t, etag)

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		req.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotModified, w.Code)
	})

	t.Run("returns 200 for non-matching If-None-Match", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		req.Header.Set("If-None-Match", `"stale-etag"`)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("returns 412 for non-matching If-Match", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		req.Header.Set("If-Match", `"wrong-etag"`)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusPreconditionFailed, w.Code)
	})

	t.Run("returns 200 for matching If-Match", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		getReq := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		getW := httptest.NewRecorder()
		ro.ServeHTTP(getW, getReq)
		etag := getW.Header().Get("ETag")

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		req.Header.Set("If-Match", etag)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("returns 304 for If-Modified-Since when not modified", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		req.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotModified, w.Code)
	})

	t.Run("returns 412 for If-Unmodified-Since when modified after", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil)
		req.Header.Set(
			"If-Unmodified-Since",
			time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusPreconditionFailed, w.Code)
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

	t.Run("COPY directive inherits source user metadata", func(t *testing.T) {
		ro := setup(t)
		// Add user metadata to the source object first.
		putReq := httptest.NewRequest(
			http.MethodPut,
			"/src-bucket/meta-obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set("x-amz-meta-author", "alice")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		copyReq := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		copyReq.Header.Set("x-amz-copy-source", "/src-bucket/meta-obj.txt")
		ro.ServeHTTP(httptest.NewRecorder(), copyReq)

		req := httptest.NewRequest(http.MethodHead, "/dst-bucket/copy.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "alice", w.Header().Get("x-amz-meta-author"))
	})

	t.Run("REPLACE directive uses new user metadata", func(t *testing.T) {
		ro := setup(t)
		putReq := httptest.NewRequest(
			http.MethodPut,
			"/src-bucket/meta-obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set("x-amz-meta-author", "alice")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		copyReq := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		copyReq.Header.Set("x-amz-copy-source", "/src-bucket/meta-obj.txt")
		copyReq.Header.Set("x-amz-metadata-directive", "REPLACE")
		copyReq.Header.Set("x-amz-meta-author", "bob")
		ro.ServeHTTP(httptest.NewRecorder(), copyReq)

		req := httptest.NewRequest(http.MethodHead, "/dst-bucket/copy.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "bob", w.Header().Get("x-amz-meta-author"))
	})

	t.Run("REPLACE directive replaces content type", func(t *testing.T) {
		ro := setup(t)
		putReq := httptest.NewRequest(
			http.MethodPut,
			"/src-bucket/obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		copyReq := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		copyReq.Header.Set("x-amz-copy-source", "/src-bucket/obj.txt")
		copyReq.Header.Set("x-amz-metadata-directive", "REPLACE")
		copyReq.Header.Set("Content-Type", "application/json")
		ro.ServeHTTP(httptest.NewRecorder(), copyReq)

		req := httptest.NewRequest(http.MethodHead, "/dst-bucket/copy.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	})

	t.Run("REPLACE directive with no metadata headers clears user metadata", func(t *testing.T) {
		ro := setup(t)
		putReq := httptest.NewRequest(
			http.MethodPut,
			"/src-bucket/obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set("x-amz-meta-author", "alice")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		copyReq := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		copyReq.Header.Set("x-amz-copy-source", "/src-bucket/obj.txt")
		copyReq.Header.Set("x-amz-metadata-directive", "REPLACE")
		ro.ServeHTTP(httptest.NewRecorder(), copyReq)

		req := httptest.NewRequest(http.MethodHead, "/dst-bucket/copy.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Header().Get("x-amz-meta-author"))
	})

	t.Run(
		"REPLACE directive with no Content-Type defaults to application/octet-stream",
		func(t *testing.T) {
			ro := setup(t)
			putReq := httptest.NewRequest(
				http.MethodPut,
				"/src-bucket/obj.txt",
				strings.NewReader("data"),
			)
			putReq.Header.Set("Content-Type", "text/plain")
			ro.ServeHTTP(httptest.NewRecorder(), putReq)

			copyReq := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
			copyReq.Header.Set("x-amz-copy-source", "/src-bucket/obj.txt")
			copyReq.Header.Set("x-amz-metadata-directive", "REPLACE")
			ro.ServeHTTP(httptest.NewRecorder(), copyReq)

			req := httptest.NewRequest(http.MethodHead, "/dst-bucket/copy.txt", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
		},
	)
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

func TestObjectTaggingHandlers(t *testing.T) {
	taggingBody := `<Tagging><TagSet><Tag><Key>env</Key><Value>prod</Value></Tag></TagSet></Tagging>`

	t.Run("PutObjectTagging", func(t *testing.T) {
		t.Run("roundtrip via real storage", func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(
					http.MethodPut,
					"/my-bucket/key.txt",
					strings.NewReader("data"),
				),
			)

			req := httptest.NewRequest(http.MethodPut, "/my-bucket/key.txt?tagging",
				strings.NewReader(taggingBody))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)

			req = httptest.NewRequest(http.MethodGet, "/my-bucket/key.txt?tagging", nil)
			w = httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), "<Key>env</Key>")
			assert.Contains(t, w.Body.String(), "<Value>prod</Value>")

			req = httptest.NewRequest(http.MethodDelete, "/my-bucket/key.txt?tagging", nil)
			w = httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNoContent, w.Code)

			req = httptest.NewRequest(http.MethodGet, "/my-bucket/key.txt?tagging", nil)
			w = httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.NotContains(t, w.Body.String(), "<Key>env</Key>")
		})

		t.Run("returns 400 on malformed XML", func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(
					http.MethodPut,
					"/my-bucket/key.txt",
					strings.NewReader("data"),
				),
			)
			req := httptest.NewRequest(http.MethodPut, "/my-bucket/key.txt?tagging",
				strings.NewReader("not-xml"))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "MalformedXML")
		})

		t.Run("returns 400 InvalidTag for constraint violations", func(t *testing.T) {
			tooManyTags := "<Tagging><TagSet>" +
				"<Tag><Key>k1</Key><Value>v</Value></Tag>" +
				"<Tag><Key>k2</Key><Value>v</Value></Tag>" +
				"<Tag><Key>k3</Key><Value>v</Value></Tag>" +
				"<Tag><Key>k4</Key><Value>v</Value></Tag>" +
				"<Tag><Key>k5</Key><Value>v</Value></Tag>" +
				"<Tag><Key>k6</Key><Value>v</Value></Tag>" +
				"<Tag><Key>k7</Key><Value>v</Value></Tag>" +
				"<Tag><Key>k8</Key><Value>v</Value></Tag>" +
				"<Tag><Key>k9</Key><Value>v</Value></Tag>" +
				"<Tag><Key>k10</Key><Value>v</Value></Tag>" +
				"<Tag><Key>k11</Key><Value>v</Value></Tag>" +
				"</TagSet></Tagging>"
			longKey := strings.Repeat("a", 129)
			longVal := strings.Repeat("a", 257)
			tests := []struct {
				name string
				body string
			}{
				{
					name: "more than 10 tags",
					body: tooManyTags,
				},
				{
					name: "tag key exceeds 128 characters",
					body: "<Tagging><TagSet><Tag><Key>" + longKey + "</Key><Value>v</Value></Tag></TagSet></Tagging>",
				},
				{
					name: "tag value exceeds 256 characters",
					body: "<Tagging><TagSet><Tag><Key>k</Key><Value>" + longVal + "</Value></Tag></TagSet></Tagging>",
				},
				{
					name: "duplicate tag keys",
					body: "<Tagging><TagSet><Tag><Key>dup</Key><Value>v1</Value></Tag><Tag><Key>dup</Key><Value>v2</Value></Tag></TagSet></Tagging>",
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					ro := newRouterWithMock(&mockStore{})
					req := httptest.NewRequest(http.MethodPut, "/my-bucket/key.txt?tagging",
						strings.NewReader(tt.body))
					w := httptest.NewRecorder()
					ro.ServeHTTP(w, req)
					assert.Equal(t, http.StatusBadRequest, w.Code)
					assert.Contains(t, w.Body.String(), "InvalidTag")
				})
			}
		})

		t.Run("returns 404 for missing bucket", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{putObjectTaggingErr: ErrBucketNotFound})
			req := httptest.NewRequest(http.MethodPut, "/no-bucket/key.txt?tagging",
				strings.NewReader(taggingBody))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		})

		t.Run("returns 404 for missing object", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{putObjectTaggingErr: ErrObjectNotFound})
			req := httptest.NewRequest(http.MethodPut, "/my-bucket/no-key.txt?tagging",
				strings.NewReader(taggingBody))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchKey")
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{putObjectTaggingErr: errors.New("disk full")})
			req := httptest.NewRequest(http.MethodPut, "/my-bucket/key.txt?tagging",
				strings.NewReader(taggingBody))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})

	t.Run("GetObjectTagging", func(t *testing.T) {
		t.Run("returns tags as XML", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{
				getObjectTaggingTags: []Tag{{Key: "env", Value: "prod"}},
			})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket/key.txt?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), "<Key>env</Key>")
		})

		t.Run("returns 404 for missing bucket", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getObjectTaggingErr: ErrBucketNotFound})
			req := httptest.NewRequest(http.MethodGet, "/no-bucket/key.txt?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		})

		t.Run("returns 404 for missing object", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getObjectTaggingErr: ErrObjectNotFound})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket/no-key.txt?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchKey")
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getObjectTaggingErr: errors.New("disk failure")})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket/key.txt?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})

	t.Run("DeleteObjectTagging", func(t *testing.T) {
		t.Run("returns 204 on success", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{})
			req := httptest.NewRequest(http.MethodDelete, "/my-bucket/key.txt?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNoContent, w.Code)
		})

		t.Run("returns 404 for missing bucket", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{deleteObjectTaggingErr: ErrBucketNotFound})
			req := httptest.NewRequest(http.MethodDelete, "/no-bucket/key.txt?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		})

		t.Run("returns 404 for missing object", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{deleteObjectTaggingErr: ErrObjectNotFound})
			req := httptest.NewRequest(http.MethodDelete, "/my-bucket/no-key.txt?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchKey")
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{deleteObjectTaggingErr: errors.New("disk failure")})
			req := httptest.NewRequest(http.MethodDelete, "/my-bucket/key.txt?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})
}

func TestBucketTaggingHandlers(t *testing.T) {
	taggingBody := `<Tagging><TagSet><Tag><Key>env</Key><Value>prod</Value></Tag></TagSet></Tagging>`

	t.Run("PutBucketTagging", func(t *testing.T) {
		t.Run("roundtrip via real storage", func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

			// GET before any tags are set → 404 NoSuchTagSet
			w0 := httptest.NewRecorder()
			ro.ServeHTTP(w0, httptest.NewRequest(http.MethodGet, "/my-bucket?tagging", nil))
			assert.Equal(t, http.StatusNotFound, w0.Code)
			assert.Contains(t, w0.Body.String(), "NoSuchTagSet")

			// PUT tags
			req := httptest.NewRequest(http.MethodPut, "/my-bucket?tagging",
				strings.NewReader(taggingBody))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)

			// GET after PUT → 200 with tags
			req2 := httptest.NewRequest(http.MethodGet, "/my-bucket?tagging", nil)
			w2 := httptest.NewRecorder()
			ro.ServeHTTP(w2, req2)
			assert.Equal(t, http.StatusOK, w2.Code)
			assert.Contains(t, w2.Body.String(), "env")
			assert.Contains(t, w2.Body.String(), "prod")

			// DELETE tags then GET → 404 NoSuchTagSet
			ro.ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequest(http.MethodDelete, "/my-bucket?tagging", nil))
			w3 := httptest.NewRecorder()
			ro.ServeHTTP(w3, httptest.NewRequest(http.MethodGet, "/my-bucket?tagging", nil))
			assert.Equal(t, http.StatusNotFound, w3.Code)
			assert.Contains(t, w3.Body.String(), "NoSuchTagSet")
		})

		t.Run("returns 400 for malformed XML", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{bucketExists: true})
			req := httptest.NewRequest(http.MethodPut, "/my-bucket?tagging",
				strings.NewReader("not-xml"))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "MalformedXML")
		})

		t.Run("returns 400 for too many tags", func(t *testing.T) {
			var tags strings.Builder
			tags.WriteString("<Tagging><TagSet>")
			for i := range 51 {
				fmt.Fprintf(&tags, "<Tag><Key>k%d</Key><Value>v</Value></Tag>", i)
			}
			tags.WriteString("</TagSet></Tagging>")
			ro := newRouterWithMock(&mockStore{bucketExists: true})
			req := httptest.NewRequest(http.MethodPut, "/my-bucket?tagging",
				strings.NewReader(tags.String()))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "InvalidTag")
		})

		t.Run("returns 400 for tag key too long", func(t *testing.T) {
			longKey := strings.Repeat("a", 129)
			body := fmt.Sprintf(
				`<Tagging><TagSet><Tag><Key>%s</Key><Value>v</Value></Tag></TagSet></Tagging>`,
				longKey,
			)
			ro := newRouterWithMock(&mockStore{bucketExists: true})
			req := httptest.NewRequest(http.MethodPut, "/my-bucket?tagging",
				strings.NewReader(body))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "InvalidTag")
		})

		t.Run("returns 400 for tag value too long", func(t *testing.T) {
			longVal := strings.Repeat("v", 257)
			body := fmt.Sprintf(
				`<Tagging><TagSet><Tag><Key>k</Key><Value>%s</Value></Tag></TagSet></Tagging>`,
				longVal,
			)
			ro := newRouterWithMock(&mockStore{bucketExists: true})
			req := httptest.NewRequest(http.MethodPut, "/my-bucket?tagging",
				strings.NewReader(body))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "InvalidTag")
		})

		t.Run("returns 400 for duplicate tag key", func(t *testing.T) {
			body := `<Tagging><TagSet><Tag><Key>k</Key><Value>v1</Value></Tag><Tag><Key>k</Key><Value>v2</Value></Tag></TagSet></Tagging>`
			ro := newRouterWithMock(&mockStore{bucketExists: true})
			req := httptest.NewRequest(http.MethodPut, "/my-bucket?tagging",
				strings.NewReader(body))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "InvalidTag")
		})

		t.Run("returns 404 for missing bucket", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{putBucketTaggingErr: ErrBucketNotFound})
			req := httptest.NewRequest(http.MethodPut, "/no-bucket?tagging",
				strings.NewReader(taggingBody))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{putBucketTaggingErr: errors.New("disk full")})
			req := httptest.NewRequest(http.MethodPut, "/my-bucket?tagging",
				strings.NewReader(taggingBody))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})

	t.Run("GetBucketTagging", func(t *testing.T) {
		t.Run("returns tags as XML", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{
				getBucketTaggingTags: []Tag{{Key: "env", Value: "prod"}},
			})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), "env")
		})

		t.Run("returns 404 NoSuchTagSet when no tags set", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketTaggingTags: []Tag{}})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchTagSet")
		})

		t.Run("returns 404 for missing bucket", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketTaggingErr: ErrBucketNotFound})
			req := httptest.NewRequest(http.MethodGet, "/no-bucket?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketTaggingErr: errors.New("disk failure")})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})

	t.Run("DeleteBucketTagging", func(t *testing.T) {
		t.Run("returns 204 on success", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{})
			req := httptest.NewRequest(http.MethodDelete, "/my-bucket?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNoContent, w.Code)
		})

		t.Run("returns 404 for missing bucket", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{deleteBucketTaggingErr: ErrBucketNotFound})
			req := httptest.NewRequest(http.MethodDelete, "/no-bucket?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{deleteBucketTaggingErr: errors.New("disk failure")})
			req := httptest.NewRequest(http.MethodDelete, "/my-bucket?tagging", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})
}

func TestBucketCORSHandlers(t *testing.T) {
	const validBody = `<CORSConfiguration><CORSRule><AllowedOrigin>*</AllowedOrigin><AllowedMethod>GET</AllowedMethod></CORSRule></CORSConfiguration>`

	t.Run("PutBucketCors", func(t *testing.T) {
		t.Run("returns 200 on success", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{})
			req := httptest.NewRequest(
				http.MethodPut,
				"/my-bucket?cors",
				strings.NewReader(validBody),
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		})

		t.Run("returns 400 on invalid input", func(t *testing.T) {
			tests := []struct {
				name string
				body string
			}{
				{name: "malformed XML", body: "not-xml"},
				{name: "empty rules", body: `<CORSConfiguration></CORSConfiguration>`},
				{
					name: "rule missing AllowedOrigin",
					body: `<CORSConfiguration><CORSRule><AllowedMethod>GET</AllowedMethod></CORSRule></CORSConfiguration>`,
				},
				{
					name: "rule missing AllowedMethod",
					body: `<CORSConfiguration><CORSRule><AllowedOrigin>*</AllowedOrigin></CORSRule></CORSConfiguration>`,
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					ro := newRouterWithMock(&mockStore{})
					req := httptest.NewRequest(
						http.MethodPut,
						"/my-bucket?cors",
						strings.NewReader(tt.body),
					)
					w := httptest.NewRecorder()
					ro.ServeHTTP(w, req)
					assert.Equal(t, http.StatusBadRequest, w.Code)
				})
			}
		})

		t.Run("returns 400 with InvalidArgument on invalid method", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{})
			body := `<CORSConfiguration><CORSRule><AllowedOrigin>*</AllowedOrigin><AllowedMethod>PATCH</AllowedMethod></CORSRule></CORSConfiguration>`
			req := httptest.NewRequest(http.MethodPut, "/my-bucket?cors", strings.NewReader(body))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "InvalidArgument")
		})

		t.Run("returns 404 on bucket not found", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{putBucketCorsErr: ErrBucketNotFound})
			req := httptest.NewRequest(
				http.MethodPut,
				"/my-bucket?cors",
				strings.NewReader(validBody),
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{putBucketCorsErr: errors.New("disk full")})
			req := httptest.NewRequest(
				http.MethodPut,
				"/my-bucket?cors",
				strings.NewReader(validBody),
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})

	t.Run("GetBucketCors", func(t *testing.T) {
		t.Run("returns cors configuration", func(t *testing.T) {
			rules := []CORSRule{{
				AllowedOrigins: []string{"http://example.com"},
				AllowedMethods: []string{"GET", "PUT"},
				AllowedHeaders: []string{"*"},
				ExposeHeaders:  []string{"x-amz-meta-custom"},
				MaxAgeSeconds:  3000,
			}}
			ro := newRouterWithMock(&mockStore{getBucketCorsRules: rules})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?cors", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), "CORSConfiguration")
			assert.Contains(t, w.Body.String(), "http://example.com")
		})

		t.Run("returns 404 when no cors configuration", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketCorsErr: ErrNoCORSConfiguration})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?cors", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchCORSConfiguration")
		})

		t.Run("returns 404 on bucket not found", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketCorsErr: ErrBucketNotFound})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?cors", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketCorsErr: errors.New("disk failure")})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?cors", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})

	t.Run("DeleteBucketCors", func(t *testing.T) {
		t.Run("returns 204 on success", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{})
			req := httptest.NewRequest(http.MethodDelete, "/my-bucket?cors", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNoContent, w.Code)
		})

		t.Run("returns 404 on bucket not found", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{deleteBucketCorsErr: ErrBucketNotFound})
			req := httptest.NewRequest(http.MethodDelete, "/my-bucket?cors", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{deleteBucketCorsErr: errors.New("disk failure")})
			req := httptest.NewRequest(http.MethodDelete, "/my-bucket?cors", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})
}

func TestBucketPolicyHandlers(t *testing.T) {
	validPolicy := `{"Version":"2012-10-17","Statement":[]}`

	t.Run("PutBucketPolicy", func(t *testing.T) {
		t.Run("returns 204 on success", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{})
			req := httptest.NewRequest(
				http.MethodPut,
				"/my-bucket?policy",
				strings.NewReader(validPolicy),
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNoContent, w.Code)
		})

		t.Run("returns 400 on body read error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{})
			req := httptest.NewRequest(http.MethodPut, "/my-bucket?policy", errReader{})
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "MalformedPolicy")
		})

		t.Run("returns 400 MalformedPolicy on invalid input", func(t *testing.T) {
			tests := []struct {
				name string
				body string
			}{
				{name: "malformed JSON", body: "not-json"},
				{name: "JSON array", body: `[]`},
				{name: "JSON string", body: `"just a string"`},
				{name: "JSON number", body: `42`},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					ro := newRouterWithMock(&mockStore{})
					req := httptest.NewRequest(
						http.MethodPut,
						"/my-bucket?policy",
						strings.NewReader(tt.body),
					)
					w := httptest.NewRecorder()
					ro.ServeHTTP(w, req)
					assert.Equal(t, http.StatusBadRequest, w.Code)
					assert.Contains(t, w.Body.String(), "MalformedPolicy")
				})
			}
		})

		t.Run("returns 404 on bucket not found", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{putBucketPolicyErr: ErrBucketNotFound})
			req := httptest.NewRequest(
				http.MethodPut,
				"/my-bucket?policy",
				strings.NewReader(validPolicy),
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{putBucketPolicyErr: errors.New("disk full")})
			req := httptest.NewRequest(
				http.MethodPut,
				"/my-bucket?policy",
				strings.NewReader(validPolicy),
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})

	t.Run("GetBucketPolicy", func(t *testing.T) {
		t.Run("returns policy JSON", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketPolicyResult: validPolicy})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?policy", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
			assert.Equal(t, validPolicy, w.Body.String())
		})

		t.Run("returns 404 when no policy", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketPolicyErr: ErrNoBucketPolicy})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?policy", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucketPolicy")
		})

		t.Run("returns 404 on bucket not found", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketPolicyErr: ErrBucketNotFound})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?policy", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketPolicyErr: errors.New("disk failure")})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?policy", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})

	t.Run("DeleteBucketPolicy", func(t *testing.T) {
		t.Run("returns 204 on success", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{})
			req := httptest.NewRequest(http.MethodDelete, "/my-bucket?policy", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNoContent, w.Code)
		})

		t.Run("returns 404 on bucket not found", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{deleteBucketPolicyErr: ErrBucketNotFound})
			req := httptest.NewRequest(http.MethodDelete, "/my-bucket?policy", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{deleteBucketPolicyErr: errors.New("disk failure")})
			req := httptest.NewRequest(http.MethodDelete, "/my-bucket?policy", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})
}

func TestBucketVersioningHandlers(t *testing.T) {
	t.Run("PutBucketVersioning", func(t *testing.T) {
		t.Run("returns 200 for valid statuses", func(t *testing.T) {
			tests := []struct {
				name   string
				status string
			}{
				{name: "Enabled", status: "Enabled"},
				{name: "Suspended", status: "Suspended"},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					ro := newRouterWithMock(&mockStore{})
					body := `<VersioningConfiguration><Status>` + tt.status + `</Status></VersioningConfiguration>`
					req := httptest.NewRequest(
						http.MethodPut,
						"/my-bucket?versioning",
						strings.NewReader(body),
					)
					w := httptest.NewRecorder()
					ro.ServeHTTP(w, req)
					assert.Equal(t, http.StatusOK, w.Code)
				})
			}
		})

		t.Run("returns 400 on malformed XML", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{})
			req := httptest.NewRequest(
				http.MethodPut,
				"/my-bucket?versioning",
				strings.NewReader("not-xml"),
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})

		t.Run("returns 400 on invalid status", func(t *testing.T) {
			tests := []struct {
				name string
				body string
			}{
				{
					name: "invalid value",
					body: `<VersioningConfiguration><Status>Invalid</Status></VersioningConfiguration>`,
				},
				{name: "empty status", body: `<VersioningConfiguration></VersioningConfiguration>`},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					ro := newRouterWithMock(&mockStore{})
					req := httptest.NewRequest(
						http.MethodPut,
						"/my-bucket?versioning",
						strings.NewReader(tt.body),
					)
					w := httptest.NewRecorder()
					ro.ServeHTTP(w, req)
					assert.Equal(t, http.StatusBadRequest, w.Code)
				})
			}
		})

		t.Run("returns 404 on bucket not found", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{putBucketVersioningErr: ErrBucketNotFound})
			body := `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`
			req := httptest.NewRequest(
				http.MethodPut,
				"/my-bucket?versioning",
				strings.NewReader(body),
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{putBucketVersioningErr: errors.New("disk full")})
			body := `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`
			req := httptest.NewRequest(
				http.MethodPut,
				"/my-bucket?versioning",
				strings.NewReader(body),
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})

	t.Run("GetBucketVersioning", func(t *testing.T) {
		t.Run("returns versioning status", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketVersioningStatus: "Enabled"})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?versioning", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), "Enabled")
		})

		t.Run("returns empty VersioningConfiguration when not set", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketVersioningStatus: ""})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?versioning", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), "VersioningConfiguration")
		})

		t.Run("returns 404 on bucket not found", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketVersioningErr: ErrBucketNotFound})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?versioning", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
		})

		t.Run("returns 500 on storage error", func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketVersioningErr: errors.New("disk failure")})
			req := httptest.NewRequest(http.MethodGet, "/my-bucket?versioning", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
		})
	})
}

func TestEtagListContains(t *testing.T) {
	tests := []struct {
		name      string
		headerVal string
		etag      string
		want      bool
	}{
		{"wildcard matches any", "*", `"abc"`, true},
		{"exact match", `"abc"`, `"abc"`, true},
		{"no match", `"abc"`, `"def"`, false},
		{"multiple ETags match", `"abc", "def"`, `"def"`, true},
		{"multiple ETags no match", `"abc", "def"`, `"ghi"`, false},
		{"whitespace trimmed", `  "abc"  `, `"abc"`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, etagListContains(tt.headerVal, tt.etag))
		})
	}
}
