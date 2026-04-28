package s3

import (
	"bytes"
	"crypto/md5" //nolint:gosec // MD5 used for data-integrity checking per S3 spec, not cryptographic security
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

	t.Run("x-amz-object-lock-enabled enables versioning and object lock", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodPut, "/my-bucket", nil)
		req.Header.Set(amzObjectLockEnabled, "true")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)

		// Versioning should be Enabled.
		vReq := httptest.NewRequest(http.MethodGet, "/my-bucket?versioning", nil)
		vW := httptest.NewRecorder()
		ro.ServeHTTP(vW, vReq)
		assert.Contains(t, vW.Body.String(), "Enabled")

		// ObjectLock configuration should reflect enabled.
		olReq := httptest.NewRequest(http.MethodGet, "/my-bucket?object-lock", nil)
		olW := httptest.NewRecorder()
		ro.ServeHTTP(olW, olReq)
		assert.Contains(t, olW.Body.String(), "ObjectLockEnabled")
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
	listBucketsErr                error
	createBucketErr               error
	deleteBucketErr               error
	bucketExists                  bool
	getBucketRegionStr            string
	getBucketRegionErr            error
	putObjectErr                  error
	putObjectMeta                 ObjectMetadata
	getObjectFile                 *os.File
	getObjectMeta                 ObjectMetadata
	getObjectErr                  error
	copyObjectMeta                ObjectMetadata
	copyObjectErr                 error
	deleteObjectErr               error
	deleteObjectVersionErr        error
	headObjectMeta                ObjectMetadata
	headObjectErr                 error
	setObjectRestoreInitiatedErr  error
	listObjectsObjs               []ObjectInfo
	listObjectsErr                error
	listObjectVersionsErr         error
	createMultipartUploadID       string
	createMultipartUploadErr      error
	uploadPartETag                string
	uploadPartErr                 error
	uploadPartCopyETag            string
	uploadPartCopyLastModified    time.Time
	uploadPartCopySourceVersionID string
	uploadPartCopyErr             error
	completeMultipartUploadMeta   ObjectMetadata
	completeMultipartUploadErr    error
	abortMultipartUploadErr       error
	listMultipartUploadsResult    []MultipartUploadInfo
	listMultipartUploadsErr       error
	listPartsUploadMeta           uploadMeta
	listPartsResult               []PartInfo
	listPartsErr                  error
	putObjectTaggingErr           error
	getObjectTaggingTags          []Tag
	getObjectTaggingErr           error
	deleteObjectTaggingErr        error
	putBucketTaggingErr           error
	getBucketTaggingTags          []Tag
	getBucketTaggingErr           error
	deleteBucketTaggingErr        error
	putBucketVersioningErr        error
	getBucketVersioningStatus     string
	getBucketVersioningErr        error
	putBucketCorsErr              error
	getBucketCorsRules            []CORSRule
	getBucketCorsErr              error
	deleteBucketCorsErr           error
	putBucketPolicyErr            error
	getBucketPolicyResult         string
	getBucketPolicyErr            error
	deleteBucketPolicyErr         error

	putPublicAccessBlockErr          error
	getPublicAccessBlockResult       string
	getPublicAccessBlockErr          error
	deletePublicAccessBlockErr       error
	putBucketEncryptionErr           error
	getBucketEncryptionResult        string
	getBucketEncryptionErr           error
	deleteBucketEncryptionErr        error
	putBucketOwnershipControlsErr    error
	getBucketOwnershipControlsResult string
	getBucketOwnershipControlsErr    error
	deleteBucketOwnershipControlsErr error
	putBucketNotificationErr         error
	getBucketNotificationResult      string
	getBucketNotificationErr         error
	putBucketLifecycleErr            error
	getBucketLifecycleResult         string
	getBucketLifecycleErr            error
	deleteBucketLifecycleErr         error
	putBucketWebsiteErr              error
	getBucketWebsiteResult           string
	getBucketWebsiteErr              error
	deleteBucketWebsiteErr           error
	putBucketLoggingErr              error
	getBucketLoggingResult           string
	getBucketLoggingErr              error
	putBucketAccelerateErr           error
	getBucketAccelerateResult        string
	getBucketAccelerateErr           error
	putBucketReplicationErr          error
	getBucketReplicationResult       string
	getBucketReplicationErr          error
	deleteBucketReplicationErr       error
	putBucketRequestPaymentErr       error
	getBucketRequestPaymentResult    string
	getBucketRequestPaymentErr       error

	putBucketObjectLockErr    error
	getBucketObjectLockResult string
	getBucketObjectLockErr    error
	putObjectRetentionErr     error
	getObjectRetentionResult  ObjectRetention
	getObjectRetentionErr     error
	putObjectLegalHoldErr     error
	getObjectLegalHoldResult  string
	getObjectLegalHoldErr     error
}

func (m *mockStore) ListBuckets() ([]BucketInfo, error) { return nil, m.listBucketsErr }
func (m *mockStore) CreateBucket(_ string, _ string, _ bool) error {
	return m.createBucketErr
}
func (m *mockStore) DeleteBucket(_ string) error { return m.deleteBucketErr }
func (m *mockStore) BucketExists(_ string) bool  { return m.bucketExists }
func (m *mockStore) GetBucketRegion(_ string) (string, error) {
	return m.getBucketRegionStr, m.getBucketRegionErr
}

func (m *mockStore) PutObject(
	_ string,
	_ string,
	_ io.Reader,
	_ string,
	_ map[string]string,
	_, _ string,
	_ *ObjectRetention,
	_ *ObjectLegalHold,
) (ObjectMetadata, error) {
	return m.putObjectMeta, m.putObjectErr
}
func (m *mockStore) GetObject(_ string, _ string) (*os.File, ObjectMetadata, error) {
	return m.getObjectFile, m.getObjectMeta, m.getObjectErr
}

func (m *mockStore) GetObjectVersion(
	_ string,
	_ string,
	_ string,
) (*os.File, ObjectMetadata, error) {
	return m.getObjectFile, m.getObjectMeta, m.getObjectErr
}

func (m *mockStore) CopyObject(
	_, _, _, _, _, _ string,
	_ map[string]string,
	_, _ string,
	_ *ObjectRetention,
	_ *ObjectLegalHold,
) (ObjectMetadata, error) {
	return m.copyObjectMeta, m.copyObjectErr
}
func (m *mockStore) DeleteObject(_ string, _ string, _ bool) error { return m.deleteObjectErr }
func (m *mockStore) DeleteObjectVersioned(_ string, _ string, _ bool) (string, bool, error) {
	return "", false, m.deleteObjectErr
}
func (m *mockStore) DeleteObjectVersion(_ string, _ string, _ string, _ bool) (bool, error) {
	return false, m.deleteObjectVersionErr
}
func (m *mockStore) HeadObject(_ string, _ string) (ObjectMetadata, error) {
	return m.headObjectMeta, m.headObjectErr
}
func (m *mockStore) HeadObjectVersion(_ string, _ string, _ string) (ObjectMetadata, error) {
	return m.headObjectMeta, m.headObjectErr
}
func (m *mockStore) SetObjectRestoreInitiated(_ string, _ string) error {
	return m.setObjectRestoreInitiatedErr
}
func (m *mockStore) ListObjects(_ string) ([]ObjectInfo, error) {
	return m.listObjectsObjs, m.listObjectsErr
}
func (m *mockStore) ListObjectVersions(_ string) ([]VersionInfo, []DeleteMarkerInfo, error) {
	return nil, nil, m.listObjectVersionsErr
}

func (m *mockStore) CreateMultipartUpload(
	_, _, _, _, _ string,
	_ *ObjectRetention,
	_ *ObjectLegalHold,
) (string, error) {
	return m.createMultipartUploadID, m.createMultipartUploadErr
}
func (m *mockStore) UploadPart(_ string, _ int, _ io.Reader) (string, error) {
	return m.uploadPartETag, m.uploadPartErr
}

func (m *mockStore) UploadPartCopy(
	_ string,
	_ int,
	_, _, _ string,
	_ *byteRange,
) (string, time.Time, string, error) {
	return m.uploadPartCopyETag, m.uploadPartCopyLastModified, m.uploadPartCopySourceVersionID, m.uploadPartCopyErr
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

func (m *mockStore) PutPublicAccessBlock(_, _ string) error { return m.putPublicAccessBlockErr }
func (m *mockStore) GetPublicAccessBlock(_ string) (string, error) {
	return m.getPublicAccessBlockResult, m.getPublicAccessBlockErr
}
func (m *mockStore) DeletePublicAccessBlock(_ string) error { return m.deletePublicAccessBlockErr }

func (m *mockStore) PutBucketEncryption(_, _ string) error { return m.putBucketEncryptionErr }
func (m *mockStore) GetBucketEncryption(_ string) (string, error) {
	return m.getBucketEncryptionResult, m.getBucketEncryptionErr
}
func (m *mockStore) DeleteBucketEncryption(_ string) error { return m.deleteBucketEncryptionErr }

func (m *mockStore) PutBucketOwnershipControls(_, _ string) error {
	return m.putBucketOwnershipControlsErr
}
func (m *mockStore) GetBucketOwnershipControls(_ string) (string, error) {
	return m.getBucketOwnershipControlsResult, m.getBucketOwnershipControlsErr
}
func (m *mockStore) DeleteBucketOwnershipControls(_ string) error {
	return m.deleteBucketOwnershipControlsErr
}

func (m *mockStore) PutBucketNotification(_, _ string) error { return m.putBucketNotificationErr }
func (m *mockStore) GetBucketNotification(_ string) (string, error) {
	return m.getBucketNotificationResult, m.getBucketNotificationErr
}

func (m *mockStore) PutBucketLifecycle(_, _ string) error { return m.putBucketLifecycleErr }
func (m *mockStore) GetBucketLifecycle(_ string) (string, error) {
	return m.getBucketLifecycleResult, m.getBucketLifecycleErr
}
func (m *mockStore) DeleteBucketLifecycle(_ string) error { return m.deleteBucketLifecycleErr }

func (m *mockStore) PutBucketWebsite(_, _ string) error { return m.putBucketWebsiteErr }
func (m *mockStore) GetBucketWebsite(_ string) (string, error) {
	return m.getBucketWebsiteResult, m.getBucketWebsiteErr
}
func (m *mockStore) DeleteBucketWebsite(_ string) error { return m.deleteBucketWebsiteErr }

func (m *mockStore) PutBucketLogging(_, _ string) error { return m.putBucketLoggingErr }
func (m *mockStore) GetBucketLogging(_ string) (string, error) {
	return m.getBucketLoggingResult, m.getBucketLoggingErr
}

func (m *mockStore) PutBucketAccelerate(_, _ string) error { return m.putBucketAccelerateErr }
func (m *mockStore) GetBucketAccelerate(_ string) (string, error) {
	return m.getBucketAccelerateResult, m.getBucketAccelerateErr
}

func (m *mockStore) PutBucketReplication(_, _ string) error { return m.putBucketReplicationErr }
func (m *mockStore) GetBucketReplication(_ string) (string, error) {
	return m.getBucketReplicationResult, m.getBucketReplicationErr
}
func (m *mockStore) DeleteBucketReplication(_ string) error { return m.deleteBucketReplicationErr }

func (m *mockStore) PutBucketRequestPayment(
	_, _ string,
) error {
	return m.putBucketRequestPaymentErr
}
func (m *mockStore) GetBucketRequestPayment(_ string) (string, error) {
	return m.getBucketRequestPaymentResult, m.getBucketRequestPaymentErr
}

func (m *mockStore) PutBucketObjectLock(_, _ string) error { return m.putBucketObjectLockErr }
func (m *mockStore) GetBucketObjectLock(_ string) (string, error) {
	return m.getBucketObjectLockResult, m.getBucketObjectLockErr
}

func (m *mockStore) PutObjectRetention(_, _, _ string, _ ObjectRetention) error {
	return m.putObjectRetentionErr
}
func (m *mockStore) GetObjectRetention(_, _, _ string) (ObjectRetention, error) {
	return m.getObjectRetentionResult, m.getObjectRetentionErr
}

func (m *mockStore) PutObjectLegalHold(_, _, _, _ string) error { return m.putObjectLegalHoldErr }
func (m *mockStore) GetObjectLegalHold(_, _, _ string) (string, error) {
	return m.getObjectLegalHoldResult, m.getObjectLegalHoldErr
}

func newRouterWithMock(store *mockStore) *Router {
	return &Router{storage: store, now: time.Now}
}

func newMockStore(fields func(m *mockStore)) *mockStore {
	m := &mockStore{}
	fields(m)
	return m
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

	t.Run(
		"Object Lock headers are stored and reflected in GetObjectRetention/GetObjectLegalHold",
		func(t *testing.T) {
			ro := newTestRouter(t)
			// Create bucket with Object Lock enabled.
			createReq := httptest.NewRequest(http.MethodPut, "/my-bucket", nil)
			createReq.Header.Set(amzObjectLockEnabled, "true")
			ro.ServeHTTP(httptest.NewRecorder(), createReq)

			putReq := httptest.NewRequest(
				http.MethodPut,
				"/my-bucket/obj.txt",
				strings.NewReader("data"),
			)
			putReq.Header.Set(amzObjectLockMode, "GOVERNANCE")
			putReq.Header.Set(amzObjectLockRetainUntilDate, "2099-01-01T00:00:00Z")
			putReq.Header.Set(amzObjectLockLegalHold, "ON")
			ro.ServeHTTP(httptest.NewRecorder(), putReq)

			retReq := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt?retention", nil)
			retW := httptest.NewRecorder()
			ro.ServeHTTP(retW, retReq)
			assert.Equal(t, http.StatusOK, retW.Code)
			assert.Contains(t, retW.Body.String(), "GOVERNANCE")

			holdReq := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt?legal-hold", nil)
			holdW := httptest.NewRecorder()
			ro.ServeHTTP(holdW, holdReq)
			assert.Equal(t, http.StatusOK, holdW.Code)
			assert.Contains(t, holdW.Body.String(), "ON")
		},
	)

	t.Run("returns 400 on invalid Object Lock mode", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		putReq := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set(amzObjectLockMode, "INVALID")
		putReq.Header.Set(amzObjectLockRetainUntilDate, "2099-01-01T00:00:00Z")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, putReq)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("returns 400 on invalid retain-until-date", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		putReq := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set(amzObjectLockMode, "GOVERNANCE")
		putReq.Header.Set(amzObjectLockRetainUntilDate, "not-a-date")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, putReq)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("returns 400 on invalid legal-hold value", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		putReq := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set(amzObjectLockLegalHold, "MAYBE")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, putReq)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("returns 400 when only retain-until-date is set without mode", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		putReq := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set(amzObjectLockRetainUntilDate, "2099-01-01T00:00:00Z")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, putReq)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("returns 400 when only mode is set without retain-until-date", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		putReq := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/obj.txt",
			strings.NewReader("data"),
		)
		putReq.Header.Set(amzObjectLockMode, "GOVERNANCE")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, putReq)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("Content-MD5 valid digest returns 200", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		data := []byte("hello world")
		sum := md5.Sum(data) //nolint:gosec
		req := httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", bytes.NewReader(data))
		req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(sum[:]))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Content-MD5 mismatched digest returns 400 InvalidDigest", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		req := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/obj.txt",
			strings.NewReader("hello world"),
		)
		wrong := md5.Sum([]byte("wrong")) //nolint:gosec
		req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(wrong[:]))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidDigest")
	})

	t.Run("Content-MD5 invalid base64 returns 400 InvalidDigest", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		req := httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("data"))
		req.Header.Set("Content-MD5", "!!!not-base64!!!")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidDigest")
	})

	t.Run("Content-MD5 valid base64 but wrong length returns 400 InvalidDigest", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		req := httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("data"))
		req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString([]byte("short")))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidDigest")
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

	t.Run("returns 200 when body write fails mid-stream", func(t *testing.T) {
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

	t.Run("returns 412 with XML body for non-matching If-Match", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt", strings.NewReader("hello")))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
		req.Header.Set("If-Match", `"wrong-etag"`)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusPreconditionFailed, w.Code)
		assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
		assert.Contains(t, w.Body.String(), "PreconditionFailed")
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

	t.Run(
		"returns 412 with XML body for If-Unmodified-Since when modified after",
		func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil),
			)
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(
					http.MethodPut,
					"/my-bucket/obj.txt",
					strings.NewReader("hello"),
				),
			)

			req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
			req.Header.Set(
				"If-Unmodified-Since",
				time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat),
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusPreconditionFailed, w.Code)
			assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
			assert.Contains(t, w.Body.String(), "PreconditionFailed")
		},
	)

	t.Run(
		"returns 416 with XML body and Content-Range for unsatisfiable Range",
		func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil),
			)
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(
					http.MethodPut,
					"/my-bucket/obj.txt",
					strings.NewReader("hello"),
				),
			)

			req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil)
			req.Header.Set("Range", "bytes=100-200")
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)

			assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, w.Code)
			assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
			assert.Equal(t, "bytes */5", w.Header().Get("Content-Range"))
			assert.Contains(t, w.Body.String(), "InvalidRange")
		},
	)
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

	t.Run("returns 403 when object is locked", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{deleteObjectErr: ErrObjectLocked})
		req := httptest.NewRequest(http.MethodDelete, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "AccessDenied")
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

	t.Run(
		"respects max-keys and returns IsTruncated with NextContinuationToken",
		func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil),
			)
			for _, key := range []string{"a.txt", "b.txt", "c.txt"} {
				ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/"+key, "data"))
			}

			req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2&max-keys=2", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()
			assert.Contains(t, body, "<IsTruncated>true</IsTruncated>")
			assert.Contains(t, body, "<MaxKeys>2</MaxKeys>")
			assert.Contains(t, body, "NextContinuationToken")
			assert.Contains(t, body, "a.txt")
			assert.Contains(t, body, "b.txt")
			assert.NotContains(t, body, "c.txt")
		},
	)

	t.Run("paginates with continuation-token", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"a.txt", "b.txt", "c.txt"} {
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/"+key, "data"))
		}

		// First page: max-keys=2
		req1 := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2&max-keys=2", nil)
		w1 := httptest.NewRecorder()
		ro.ServeHTTP(w1, req1)
		require.Equal(t, http.StatusOK, w1.Code)

		// Extract NextContinuationToken from first response.
		var result1 listObjectsV2Result
		require.NoError(t, xml.Unmarshal(w1.Body.Bytes(), &result1))
		require.True(t, result1.IsTruncated)
		require.NotEmpty(t, result1.NextContinuationToken)

		// Second page using continuation-token (URL-encoded so base64 padding is safe).
		req2 := httptest.NewRequest(
			http.MethodGet,
			"/my-bucket?list-type=2&max-keys=2&continuation-token="+url.QueryEscape(
				result1.NextContinuationToken,
			),
			nil,
		)
		w2 := httptest.NewRecorder()
		ro.ServeHTTP(w2, req2)
		require.Equal(t, http.StatusOK, w2.Code)

		body2 := w2.Body.String()
		assert.NotContains(t, body2, "a.txt")
		assert.NotContains(t, body2, "b.txt")
		assert.Contains(t, body2, "c.txt")
		assert.Contains(t, body2, "<IsTruncated>false</IsTruncated>")
	})

	t.Run("skips objects at or before start-after", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"a.txt", "b.txt", "c.txt"} {
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/"+key, "data"))
		}

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2&start-after=b.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.NotContains(t, body, "<Key>a.txt</Key>")
		assert.NotContains(t, body, "<Key>b.txt</Key>")
		assert.Contains(t, body, "<Key>c.txt</Key>")
		assert.Contains(t, body, "<StartAfter>b.txt</StartAfter>")
	})

	t.Run("groups common prefixes with delimiter", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"logs/a.txt", "logs/b.txt", "data/c.txt"} {
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/"+key, "data"))
		}

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2&delimiter=/", nil)
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

	t.Run("includes Owner when fetch-owner=true", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/a.txt", "data"))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2&fetch-owner=true", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "<Owner>")
	})

	t.Run("does not include Owner when fetch-owner is not set", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/a.txt", "data"))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NotContains(t, w.Body.String(), "<Owner>")
	})

	t.Run("KeyCount equals number of contents plus common prefixes", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"logs/a.txt", "data/b.txt", "root.txt"} {
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/"+key, "data"))
		}

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2&delimiter=/", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var result listObjectsV2Result
		require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &result))
		// root.txt is a content, logs/ and data/ are common prefixes → KeyCount=3
		assert.Equal(t, 3, result.KeyCount)
	})

	t.Run("invalid max-keys is ignored and uses default 1000", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2&max-keys=abc", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "<MaxKeys>1000</MaxKeys>")
	})

	t.Run("max-keys=0 returns empty result with IsTruncated false", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/a.txt", "data"))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?list-type=2&max-keys=0", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		var result listObjectsV2Result
		require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &result))
		assert.Equal(t, 0, result.KeyCount)
		assert.False(t, result.IsTruncated)
		assert.Empty(t, result.NextContinuationToken)
	})

	t.Run("truncates when new common prefix would exceed max-keys", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		for _, key := range []string{"a/x.txt", "b/y.txt"} {
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/"+key, "data"))
		}

		req := httptest.NewRequest(
			http.MethodGet,
			"/my-bucket?list-type=2&delimiter=/&max-keys=1",
			nil,
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		var result listObjectsV2Result
		require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &result))
		assert.True(t, result.IsTruncated)
		assert.Equal(t, 1, result.KeyCount)
		require.NotEmpty(t, result.NextContinuationToken)
		require.Len(t, result.CommonPrefixes, 1)
		assert.Equal(t, "a/", result.CommonPrefixes[0].Prefix)

		// Second page: token from first page must advance past a/ and return b/.
		req2 := httptest.NewRequest(
			http.MethodGet,
			"/my-bucket?list-type=2&delimiter=/&max-keys=1&continuation-token="+url.QueryEscape(
				result.NextContinuationToken,
			),
			nil,
		)
		w2 := httptest.NewRecorder()
		ro.ServeHTTP(w2, req2)
		require.Equal(t, http.StatusOK, w2.Code)
		var result2 listObjectsV2Result
		require.NoError(t, xml.Unmarshal(w2.Body.Bytes(), &result2))
		assert.False(t, result2.IsTruncated)
		require.Len(t, result2.CommonPrefixes, 1)
		assert.Equal(t, "b/", result2.CommonPrefixes[0].Prefix)
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

	t.Run("invalid Object Lock mode header on CopyObject returns 400", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/src-bucket", nil),
		)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/src-bucket/orig.txt", strings.NewReader("hello")),
		)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/dst-bucket", nil),
		)

		copyReq := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		copyReq.Header.Set("X-Amz-Copy-Source", "/src-bucket/orig.txt")
		// Only mode without retain-until-date → parseObjectLockHeaders returns !ok.
		copyReq.Header.Set(amzObjectLockMode, "GOVERNANCE")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, copyReq)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Object Lock headers on CopyObject are stored", func(t *testing.T) {
		ro := newTestRouter(t)
		// Create dst bucket with Object Lock enabled.
		createReq := httptest.NewRequest(http.MethodPut, "/dst-bucket", nil)
		createReq.Header.Set("x-amz-object-lock-enabled", "true")
		ro.ServeHTTP(httptest.NewRecorder(), createReq)

		// Create src bucket and put source object.
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/src-bucket", nil),
		)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/src-bucket/orig.txt", strings.NewReader("hello")),
		)

		copyReq := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		copyReq.Header.Set("X-Amz-Copy-Source", "/src-bucket/orig.txt")
		copyReq.Header.Set(amzObjectLockMode, "GOVERNANCE")
		copyReq.Header.Set(amzObjectLockRetainUntilDate, "2099-01-01T00:00:00Z")
		copyReq.Header.Set(amzObjectLockLegalHold, "ON")
		ro.ServeHTTP(httptest.NewRecorder(), copyReq)

		retReq := httptest.NewRequest(http.MethodGet, "/dst-bucket/copy.txt?retention", nil)
		retW := httptest.NewRecorder()
		ro.ServeHTTP(retW, retReq)
		assert.Equal(t, http.StatusOK, retW.Code)
		assert.Contains(t, retW.Body.String(), "GOVERNANCE")
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

	t.Run("CreateMultipartUpload returns 400 on invalid Object Lock header", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		req := httptest.NewRequest(http.MethodPost, "/my-bucket/key?uploads", nil)
		// Only mode without retain-until-date → parseObjectLockHeaders returns !ok.
		req.Header.Set(amzObjectLockMode, "GOVERNANCE")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
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

	t.Run("UploadPart with valid Content-MD5 returns 200", func(t *testing.T) {
		ro, path := setup(t)
		uploadID := initiateUpload(t, ro, path)
		data := []byte("part data")
		sum := md5.Sum(data) //nolint:gosec
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			bytes.NewReader(data),
		)
		req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(sum[:]))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("UploadPart with mismatched Content-MD5 returns 400 InvalidDigest", func(t *testing.T) {
		ro, path := setup(t)
		uploadID := initiateUpload(t, ro, path)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			strings.NewReader("part data"),
		)
		wrong := md5.Sum([]byte("wrong")) //nolint:gosec
		req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(wrong[:]))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidDigest")
	})

	t.Run(
		"UploadPart with invalid base64 Content-MD5 returns 400 InvalidDigest",
		func(t *testing.T) {
			ro, path := setup(t)
			uploadID := initiateUpload(t, ro, path)
			req := httptest.NewRequest(
				http.MethodPut,
				path+"?partNumber=1&uploadId="+uploadID,
				strings.NewReader("part data"),
			)
			req.Header.Set("Content-MD5", "!!!not-base64!!!")
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "InvalidDigest")
		},
	)

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

	t.Run("returns AccessDenied error element for locked object", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			deleteObjectErr: ErrObjectLocked,
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
		assert.Contains(t, body, "AccessDenied")
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

func TestRouterUploadPartCopy(t *testing.T) {
	setup := func(t *testing.T) (*Router, string, string) {
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
			"/src-bucket/source.txt",
			strings.NewReader("hello world"),
		)
		putReq.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putReq)

		initReq := httptest.NewRequest(http.MethodPost, "/dst-bucket/dest.txt?uploads", nil)
		initW := httptest.NewRecorder()
		ro.ServeHTTP(initW, initReq)
		require.Equal(t, http.StatusOK, initW.Code)
		var initResult struct {
			UploadID string `xml:"UploadId"`
		}
		require.NoError(t, xml.Unmarshal(initW.Body.Bytes(), &initResult))
		return ro, initResult.UploadID, "/dst-bucket/dest.txt"
	}

	t.Run("copies full source object as part and returns CopyPartResult", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/source.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "CopyPartResult")
		assert.Contains(t, w.Body.String(), "ETag")
		assert.Contains(t, w.Body.String(), "LastModified")
	})

	t.Run("copies byte range of source as part", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/source.txt")
		req.Header.Set("x-amz-copy-source-range", "bytes=0-4")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "CopyPartResult")
	})

	t.Run("part copy integrates with complete multipart upload", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/source.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		var copyResult copyPartResult
		require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &copyResult))

		completeBody, err := xml.Marshal(completeMultipartUploadRequest{
			Parts: []xmlCompletePart{{PartNumber: 1, ETag: copyResult.ETag}},
		})
		require.NoError(t, err)
		completeReq := httptest.NewRequest(
			http.MethodPost,
			path+"?uploadId="+uploadID,
			strings.NewReader(string(completeBody)),
		)
		completeW := httptest.NewRecorder()
		ro.ServeHTTP(completeW, completeReq)
		require.Equal(t, http.StatusOK, completeW.Code)

		getReq := httptest.NewRequest(http.MethodGet, path, nil)
		getW := httptest.NewRecorder()
		ro.ServeHTTP(getW, getReq)
		assert.Equal(t, http.StatusOK, getW.Code)
		assert.Equal(t, "hello world", getW.Body.String())
	})

	t.Run("returns 404 when upload does not exist", func(t *testing.T) {
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
			"/src-bucket/obj.txt",
			strings.NewReader("data"),
		)
		ro.ServeHTTP(httptest.NewRecorder(), putReq)
		req := httptest.NewRequest(
			http.MethodPut,
			"/dst-bucket/dest.txt?partNumber=1&uploadId=nonexistent",
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/obj.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchUpload")
	})

	t.Run("returns 404 when source object does not exist", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/nonexistent.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchKey")
	})

	t.Run(
		"returns 404 NoSuchKey when precondition header set but source does not exist",
		func(t *testing.T) {
			// When hasCopySourceConditions is true but HeadObject returns ErrObjectNotFound,
			// the precondition check is skipped and UploadPartCopy returns NoSuchKey (404),
			// not PreconditionFailed (412).
			ro, uploadID, path := setup(t)
			req := httptest.NewRequest(
				http.MethodPut,
				path+"?partNumber=1&uploadId="+uploadID,
				nil,
			)
			req.Header.Set("x-amz-copy-source", "/src-bucket/nonexistent.txt")
			req.Header.Set("x-amz-copy-source-if-match", `"some-etag"`)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchKey")
		},
	)

	t.Run("returns 400 for invalid partNumber", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		req := httptest.NewRequest(http.MethodPut, "/bucket/key?partNumber=0&uploadId=abc", nil)
		req.Header.Set("x-amz-copy-source", "/src/obj.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("returns 400 for missing uploadId", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		req := httptest.NewRequest(http.MethodPut, "/bucket/key?partNumber=1&uploadId=", nil)
		req.Header.Set("x-amz-copy-source", "/src/obj.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("routes to UploadPart when x-amz-copy-source is absent", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{uploadPartETag: `"abc"`})
		req := httptest.NewRequest(
			http.MethodPut,
			"/bucket/key?partNumber=1&uploadId=abc",
			strings.NewReader("data"),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, `"abc"`, w.Header().Get("ETag"))
	})

	t.Run("returns 400 for invalid x-amz-copy-source-range", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		req := httptest.NewRequest(http.MethodPut, "/bucket/key?partNumber=1&uploadId=abc", nil)
		req.Header.Set("x-amz-copy-source", "/src/obj.txt")
		req.Header.Set("x-amz-copy-source-range", "invalid-range")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("returns 400 for invalid x-amz-copy-source", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		req := httptest.NewRequest(http.MethodPut, "/bucket/key?partNumber=1&uploadId=abc", nil)
		req.Header.Set("x-amz-copy-source", "no-slash-no-key")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("returns 404 when source bucket does not exist", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{uploadPartCopyErr: ErrBucketNotFound})
		req := httptest.NewRequest(http.MethodPut, "/bucket/key?partNumber=1&uploadId=abc", nil)
		req.Header.Set("x-amz-copy-source", "/no-such-bucket/obj.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 400 for invalid percent-encoding in copy source", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		req := httptest.NewRequest(http.MethodPut, "/bucket/key?partNumber=1&uploadId=abc", nil)
		req.Header.Set("x-amz-copy-source", "/bucket/%ZZ")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run(
		"returns 400 for invalid percent-encoding in copy source query string",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{})
			req := httptest.NewRequest(http.MethodPut, "/bucket/key?partNumber=1&uploadId=abc", nil)
			// %25 decodes to '%' via PathUnescape, leaving '%ZZ' in the query string.
			// url.ParseQuery then sees '%ZZ' as an invalid escape and returns an error.
			req.Header.Set("x-amz-copy-source", "/bucket/key?versionId=%25ZZ")
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "InvalidArgument")
		},
	)

	t.Run("parses versionId from copy source query string", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		// Enable versioning on src-bucket and capture the first version's ID.
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(
				http.MethodPut,
				"/src-bucket?versioning",
				strings.NewReader(
					`<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`,
				),
			),
		)
		putReq := httptest.NewRequest(
			http.MethodPut,
			"/src-bucket/ver.txt",
			strings.NewReader("v1"),
		)
		putReq.Header.Set("Content-Type", "text/plain")
		putW := httptest.NewRecorder()
		ro.ServeHTTP(putW, putReq)
		require.Equal(t, http.StatusOK, putW.Code)
		v1ID := putW.Header().Get("x-amz-version-id")
		require.NotEmpty(t, v1ID)

		// Overwrite so the current version is v2.
		putReq2 := httptest.NewRequest(
			http.MethodPut,
			"/src-bucket/ver.txt",
			strings.NewReader("v2"),
		)
		ro.ServeHTTP(httptest.NewRecorder(), putReq2)

		// UploadPartCopy referencing v1 via ?versionId= in copy source.
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/ver.txt?versionId="+v1ID)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "CopyPartResult")
	})

	t.Run("returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{uploadPartCopyErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodPut, "/bucket/key?partNumber=1&uploadId=abc", nil)
		req.Header.Set("x-amz-copy-source", "/src-bucket/obj.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("returns 500 when HeadObject fails during precondition check", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{headObjectErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodPut, "/bucket/key?partNumber=1&uploadId=abc", nil)
		req.Header.Set("x-amz-copy-source", "/src-bucket/obj.txt")
		req.Header.Set("x-amz-copy-source-if-match", `"some-etag"`)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "InternalError")
	})

	t.Run("sets x-amz-copy-source-version-id header when source has version", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		// Enable versioning and capture the version ID.
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(
				http.MethodPut,
				"/src-bucket?versioning",
				strings.NewReader(
					`<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`,
				),
			),
		)
		putReq := httptest.NewRequest(
			http.MethodPut,
			"/src-bucket/ver.txt",
			strings.NewReader("versioned content"),
		)
		putW := httptest.NewRecorder()
		ro.ServeHTTP(putW, putReq)
		require.Equal(t, http.StatusOK, putW.Code)
		versionID := putW.Header().Get("x-amz-version-id")
		require.NotEmpty(t, versionID)

		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/ver.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, versionID, w.Header().Get("x-amz-copy-source-version-id"))
	})

	t.Run(
		"does not set x-amz-copy-source-version-id when source is unversioned",
		func(t *testing.T) {
			ro, uploadID, path := setup(t)
			req := httptest.NewRequest(
				http.MethodPut,
				path+"?partNumber=1&uploadId="+uploadID,
				nil,
			)
			req.Header.Set("x-amz-copy-source", "/src-bucket/source.txt")
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			assert.Empty(t, w.Header().Get("x-amz-copy-source-version-id"))
		},
	)

	t.Run("returns 412 when x-amz-copy-source-if-match fails", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/source.txt")
		req.Header.Set("x-amz-copy-source-if-match", `"wrong-etag"`)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusPreconditionFailed, w.Code)
		assert.Contains(t, w.Body.String(), "PreconditionFailed")
	})

	t.Run("returns 200 when x-amz-copy-source-if-match succeeds", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		// Fetch ETag of the source object.
		headReq := httptest.NewRequest(http.MethodHead, "/src-bucket/source.txt", nil)
		headW := httptest.NewRecorder()
		ro.ServeHTTP(headW, headReq)
		require.Equal(t, http.StatusOK, headW.Code)
		srcETag := headW.Header().Get("ETag")
		require.NotEmpty(t, srcETag)

		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/source.txt")
		req.Header.Set("x-amz-copy-source-if-match", srcETag)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("returns 412 when x-amz-copy-source-if-none-match matches", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		headReq := httptest.NewRequest(http.MethodHead, "/src-bucket/source.txt", nil)
		headW := httptest.NewRecorder()
		ro.ServeHTTP(headW, headReq)
		require.Equal(t, http.StatusOK, headW.Code)
		srcETag := headW.Header().Get("ETag")

		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/source.txt")
		req.Header.Set("x-amz-copy-source-if-none-match", srcETag)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusPreconditionFailed, w.Code)
		assert.Contains(t, w.Body.String(), "PreconditionFailed")
	})

	t.Run("returns 200 when x-amz-copy-source-if-none-match does not match", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/source.txt")
		req.Header.Set("x-amz-copy-source-if-none-match", `"different-etag"`)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("returns 412 when x-amz-copy-source-if-unmodified-since fails", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		// Use a time in the past so the object appears modified since then.
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/source.txt")
		req.Header.Set("x-amz-copy-source-if-unmodified-since",
			time.Now().Add(-24*time.Hour).UTC().Format(http.TimeFormat))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusPreconditionFailed, w.Code)
		assert.Contains(t, w.Body.String(), "PreconditionFailed")
	})

	t.Run("returns 200 when x-amz-copy-source-if-modified-since is met", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/source.txt")
		req.Header.Set("x-amz-copy-source-if-modified-since",
			time.Now().Add(-24*time.Hour).UTC().Format(http.TimeFormat))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("returns 412 when x-amz-copy-source-if-modified-since fails", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		// Use a time in the future: object has not been modified since then → 412.
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/source.txt")
		req.Header.Set("x-amz-copy-source-if-modified-since",
			time.Now().Add(24*time.Hour).UTC().Format(http.TimeFormat))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusPreconditionFailed, w.Code)
		assert.Contains(t, w.Body.String(), "PreconditionFailed")
	})

	t.Run("returns 200 when x-amz-copy-source-if-unmodified-since succeeds", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		// Use a time in the future: object has not been modified since then → condition passes.
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		req.Header.Set("x-amz-copy-source", "/src-bucket/source.txt")
		req.Header.Set("x-amz-copy-source-if-unmodified-since",
			time.Now().Add(24*time.Hour).UTC().Format(http.TimeFormat))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("accepts x-amz-copy-source without leading slash", func(t *testing.T) {
		ro, uploadID, path := setup(t)
		req := httptest.NewRequest(
			http.MethodPut,
			path+"?partNumber=1&uploadId="+uploadID,
			nil,
		)
		// bucket/key format without leading slash — both forms are valid per AWS spec.
		req.Header.Set("x-amz-copy-source", "src-bucket/source.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "CopyPartResult")
	})

	t.Run(
		"evaluates precondition via HeadObjectVersion when copy source includes versionId",
		func(t *testing.T) {
			ro, uploadID, path := setup(t)
			// Enable versioning and put a versioned object.
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(
					http.MethodPut,
					"/src-bucket?versioning",
					strings.NewReader(
						`<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`,
					),
				),
			)
			putReq := httptest.NewRequest(
				http.MethodPut,
				"/src-bucket/versioned.txt",
				strings.NewReader("versioned content"),
			)
			putReq.Header.Set("Content-Type", "text/plain")
			putW := httptest.NewRecorder()
			ro.ServeHTTP(putW, putReq)
			require.Equal(t, http.StatusOK, putW.Code)
			vID := putW.Header().Get("x-amz-version-id")
			require.NotEmpty(t, vID)

			// Fetch the ETag of the versioned object.
			headReq := httptest.NewRequest(
				http.MethodHead,
				"/src-bucket/versioned.txt?versionId="+vID,
				nil,
			)
			headW := httptest.NewRecorder()
			ro.ServeHTTP(headW, headReq)
			require.Equal(t, http.StatusOK, headW.Code)
			srcETag := headW.Header().Get("ETag")
			require.NotEmpty(t, srcETag)

			// UploadPartCopy with ?versionId= AND a matching if-match header → 200.
			req := httptest.NewRequest(
				http.MethodPut,
				path+"?partNumber=1&uploadId="+uploadID,
				nil,
			)
			req.Header.Set("x-amz-copy-source", "/src-bucket/versioned.txt?versionId="+vID)
			req.Header.Set("x-amz-copy-source-if-match", srcETag)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), "CopyPartResult")
		},
	)
}

func TestParseCopySourceRange(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    *byteRange
	}{
		{
			name:  "valid full range",
			input: "bytes=0-99",
			want:  &byteRange{Start: 0, End: 99},
		},
		{
			name:  "valid single byte",
			input: "bytes=5-5",
			want:  &byteRange{Start: 5, End: 5},
		},
		{
			name:    "missing bytes= prefix",
			input:   "0-99",
			wantErr: true,
		},
		{
			name:    "no dash (missing end)",
			input:   "bytes=100",
			wantErr: true,
		},
		{
			name:    "non-numeric start",
			input:   "bytes=abc-99",
			wantErr: true,
		},
		{
			name:    "end less than start",
			input:   "bytes=10-5",
			wantErr: true,
		},
		{
			name:    "non-numeric end",
			input:   "bytes=0-abc",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCopySourceRange(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
				assert.Nil(t, got)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
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

func TestCORSHelpers(t *testing.T) {
	t.Run("corsMatchOrigin", func(t *testing.T) {
		tests := []struct {
			name     string
			patterns []string
			origin   string
			want     bool
		}{
			{"wildcard star matches any", []string{"*"}, "http://example.com", true},
			{"exact match", []string{"http://example.com"}, "http://example.com", true},
			{"no match", []string{"http://other.com"}, "http://example.com", false},
			{
				"subdomain wildcard",
				[]string{"http://*.example.com"},
				"http://foo.example.com",
				true,
			},
			{
				"subdomain wildcard no match",
				[]string{"http://*.example.com"},
				"http://example.com",
				false,
			},
			{
				"subdomain wildcard rejects empty label",
				[]string{"http://*.example.com"},
				"http://.example.com",
				false,
			},
			{
				"multiple patterns first matches",
				[]string{"http://a.com", "http://b.com"},
				"http://a.com",
				true,
			},
			{
				"multiple patterns second matches",
				[]string{"http://a.com", "http://b.com"},
				"http://b.com",
				true,
			},
			{"empty patterns", []string{}, "http://example.com", false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assert.Equal(t, tt.want, corsMatchOrigin(tt.patterns, tt.origin))
			})
		}
	})

	t.Run("corsMatchMethod", func(t *testing.T) {
		tests := []struct {
			name    string
			allowed []string
			method  string
			want    bool
		}{
			{"exact match", []string{"GET"}, "GET", true},
			{"case insensitive", []string{"get"}, "GET", true},
			{"no match", []string{"PUT"}, "GET", false},
			{"multiple allowed", []string{"GET", "PUT"}, "PUT", true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assert.Equal(t, tt.want, corsMatchMethod(tt.allowed, tt.method))
			})
		}
	})

	t.Run("corsMatchRequestedHeaders", func(t *testing.T) {
		tests := []struct {
			name             string
			allowed          []string
			requestedHeaders string
			want             bool
		}{
			{"empty requested always matches", []string{"Content-Type"}, "", true},
			{"wildcard allowed matches any", []string{"*"}, "X-Custom-Header", true},
			{"exact match case insensitive", []string{"Content-Type"}, "content-type", true},
			{
				"multiple requested all match",
				[]string{"content-type", "x-amz-meta-foo"},
				"content-type, x-amz-meta-foo",
				true,
			},
			{
				"one unmatched header fails",
				[]string{"content-type"},
				"content-type, x-custom",
				false,
			},
			{"empty allowed rejects any header", []string{}, "content-type", false},
			{
				"empty token after split is skipped",
				[]string{"content-type"},
				"content-type, , ",
				true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assert.Equal(t, tt.want, corsMatchRequestedHeaders(tt.allowed, tt.requestedHeaders))
			})
		}
	})
}

func TestCORSPreflight(t *testing.T) {
	rules := []CORSRule{{
		AllowedOrigins: []string{"http://example.com"},
		AllowedMethods: []string{"GET", "PUT"},
		AllowedHeaders: []string{"Content-Type", "X-Amz-Date"},
		ExposeHeaders:  []string{"x-amz-request-id"},
		MaxAgeSeconds:  3600,
	}}

	t.Run("returns 200 with CORS headers on match", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{getBucketCorsRules: rules})
		req := httptest.NewRequest(http.MethodOptions, "/my-bucket/key", nil)
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Access-Control-Request-Method", "PUT")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "http://example.com", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "GET, PUT", w.Header().Get("Access-Control-Allow-Methods"))
		assert.Equal(t, "3600", w.Header().Get("Access-Control-Max-Age"))
		assert.Contains(t, w.Header().Get("Vary"), "Origin")
	})

	t.Run("returns 200 with wildcard origin", func(t *testing.T) {
		wildRules := []CORSRule{{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET"},
		}}
		ro := newRouterWithMock(&mockStore{getBucketCorsRules: wildRules})
		req := httptest.NewRequest(http.MethodOptions, "/my-bucket/key", nil)
		req.Header.Set("Origin", "http://anything.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Vary"))
	})

	t.Run("includes Access-Control-Allow-Headers when present", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{getBucketCorsRules: rules})
		req := httptest.NewRequest(http.MethodOptions, "/my-bucket/key", nil)
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("Access-Control-Request-Headers", "Content-Type")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"))
	})

	t.Run("returns 403 when Origin header is missing", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{getBucketCorsRules: rules})
		req := httptest.NewRequest(http.MethodOptions, "/my-bucket/key", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("returns 403 when no CORS config", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{getBucketCorsErr: ErrNoCORSConfiguration})
		req := httptest.NewRequest(http.MethodOptions, "/my-bucket/key", nil)
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("returns 403 when no rule matches origin", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{getBucketCorsRules: rules})
		req := httptest.NewRequest(http.MethodOptions, "/my-bucket/key", nil)
		req.Header.Set("Origin", "http://evil.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("returns 403 when method not allowed", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{getBucketCorsRules: rules})
		req := httptest.NewRequest(http.MethodOptions, "/my-bucket/key", nil)
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Access-Control-Request-Method", "DELETE")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("returns 403 when requested headers not allowed", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{getBucketCorsRules: rules})
		req := httptest.NewRequest(http.MethodOptions, "/my-bucket/key", nil)
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("Access-Control-Request-Headers", "X-Not-Allowed")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("OPTIONS on bucket endpoint also handled", func(t *testing.T) {
		wildRules := []CORSRule{{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"PUT"},
		}}
		ro := newRouterWithMock(&mockStore{getBucketCorsRules: wildRules})
		req := httptest.NewRequest(http.MethodOptions, "/my-bucket", nil)
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Access-Control-Request-Method", "PUT")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run(
		"returns 403 when Origin present but Access-Control-Request-Method absent",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{getBucketCorsRules: rules})
			req := httptest.NewRequest(http.MethodOptions, "/my-bucket/key", nil)
			req.Header.Set("Origin", "http://example.com")
			// No Access-Control-Request-Method header → empty string won't match any AllowedMethod
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusForbidden, w.Code)
		},
	)

	t.Run("returns 404 when bucket does not exist", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{getBucketCorsErr: ErrBucketNotFound})
		req := httptest.NewRequest(http.MethodOptions, "/no-such-bucket/key", nil)
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})
}

func TestCORSSimpleRequest(t *testing.T) {
	// setUp creates a bucket, puts an object, and configures CORS rules.
	setUp := func(t *testing.T, corsBody string) *Router {
		t.Helper()
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		putObj := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/key.txt",
			strings.NewReader("hello"),
		)
		putObj.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putObj)
		putCORS := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket?cors",
			strings.NewReader(corsBody),
		)
		ro.ServeHTTP(httptest.NewRecorder(), putCORS)
		return ro
	}

	t.Run("GET with matching origin gets CORS headers", func(t *testing.T) {
		corsBody := `<CORSConfiguration><CORSRule>` +
			`<AllowedOrigin>http://example.com</AllowedOrigin>` +
			`<AllowedMethod>GET</AllowedMethod>` +
			`<ExposeHeader>x-amz-request-id</ExposeHeader>` +
			`<ExposeHeader>ETag</ExposeHeader>` +
			`</CORSRule></CORSConfiguration>`
		ro := setUp(t, corsBody)
		req := httptest.NewRequest(http.MethodGet, "/my-bucket/key.txt", nil)
		req.Header.Set("Origin", "http://example.com")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, "http://example.com", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "x-amz-request-id, ETag", w.Header().Get("Access-Control-Expose-Headers"))
		assert.Equal(t, "Origin", w.Header().Get("Vary"))
	})

	t.Run("GET with wildcard origin gets Access-Control-Allow-Origin: *", func(t *testing.T) {
		corsBody := `<CORSConfiguration><CORSRule>` +
			`<AllowedOrigin>*</AllowedOrigin>` +
			`<AllowedMethod>GET</AllowedMethod>` +
			`</CORSRule></CORSConfiguration>`
		ro := setUp(t, corsBody)
		req := httptest.NewRequest(http.MethodGet, "/my-bucket/key.txt", nil)
		req.Header.Set("Origin", "http://anything.com")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Vary"))
	})

	t.Run("GET without Origin header gets no CORS headers", func(t *testing.T) {
		corsBody := `<CORSConfiguration><CORSRule>` +
			`<AllowedOrigin>http://example.com</AllowedOrigin>` +
			`<AllowedMethod>GET</AllowedMethod>` +
			`</CORSRule></CORSConfiguration>`
		ro := setUp(t, corsBody)
		req := httptest.NewRequest(http.MethodGet, "/my-bucket/key.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("GET with non-matching origin gets no CORS headers", func(t *testing.T) {
		corsBody := `<CORSConfiguration><CORSRule>` +
			`<AllowedOrigin>http://example.com</AllowedOrigin>` +
			`<AllowedMethod>GET</AllowedMethod>` +
			`</CORSRule></CORSConfiguration>`
		ro := setUp(t, corsBody)
		req := httptest.NewRequest(http.MethodGet, "/my-bucket/key.txt", nil)
		req.Header.Set("Origin", "http://evil.com")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("GET with no CORS config gets no CORS headers", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		putObj := httptest.NewRequest(
			http.MethodPut,
			"/my-bucket/key.txt",
			strings.NewReader("hello"),
		)
		putObj.Header.Set("Content-Type", "text/plain")
		ro.ServeHTTP(httptest.NewRecorder(), putObj)
		req := httptest.NewRequest(http.MethodGet, "/my-bucket/key.txt", nil)
		req.Header.Set("Origin", "http://example.com")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
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

func TestIsRangeSatisfiable(t *testing.T) {
	tests := []struct {
		name   string
		header string
		size   int64
		want   bool
	}{
		{"start within object", "bytes=0-4", 10, true},
		{"start at last byte", "bytes=9-9", 10, true},
		{"start beyond end", "bytes=10-20", 10, false},
		{"start well beyond end", "bytes=100-200", 5, false},
		{"suffix range", "bytes=-5", 10, true},
		{"multiple ranges one satisfiable", "bytes=100-200,0-4", 10, true},
		{"multiple ranges all unsatisfiable", "bytes=100-200,50-60", 10, false},
		{"non-bytes range passes through", "other=0-4", 10, true},
		{"malformed no dash passes through", "bytes=abc", 10, true},
		{"malformed start passes through", "bytes=abc-10", 10, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isRangeSatisfiable(tt.header, tt.size))
		})
	}
}

func TestRouterListObjectVersions(t *testing.T) {
	t.Run("returns versions and delete markers", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("my-bucket", "Enabled"))
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "v1"))
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "v2"))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?versions", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "<ListVersionsResult")
		assert.Contains(t, body, "<Key>obj.txt</Key>")
	})

	t.Run("returns 404 for missing bucket", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(http.MethodGet, "/no-bucket?versions", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{listObjectVersionsErr: errors.New("disk failure")})
		req := httptest.NewRequest(http.MethodGet, "/my-bucket?versions", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "InternalError")
	})

	t.Run("includes delete markers in response", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("my-bucket", "Enabled"))
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "v1"))
		// Delete without versionId creates a delete marker.
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodDelete, "/my-bucket/obj.txt", nil),
		)

		req := httptest.NewRequest(http.MethodGet, "/my-bucket?versions", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "<DeleteMarker>")
		assert.Contains(t, body, "<Key>obj.txt</Key>")
		assert.Contains(t, body, "<VersionId>")
	})
}

func TestRouterDeleteObjectVersioned(t *testing.T) {
	t.Run("delete without versionId returns 204", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "hello"))

		req := httptest.NewRequest(http.MethodDelete, "/my-bucket/obj.txt", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run(
		"delete without versionId on versioned bucket sets x-amz-delete-marker header",
		func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil),
			)
			require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("my-bucket", "Enabled"))
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "hello"))

			req := httptest.NewRequest(http.MethodDelete, "/my-bucket/obj.txt", nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)

			assert.Equal(t, http.StatusNoContent, w.Code)
			assert.Equal(t, "true", w.Header().Get(amzDeleteMarker))
			assert.NotEmpty(t, w.Header().Get(amzVersionID))
		},
	)

	t.Run("delete with versionId permanently removes version", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("my-bucket", "Enabled"))
		putW := httptest.NewRecorder()
		ro.ServeHTTP(putW, putRequest("/my-bucket/obj.txt", "hello"))
		versionID := putW.Header().Get(amzVersionID)
		require.NotEmpty(t, versionID)

		req := httptest.NewRequest(
			http.MethodDelete,
			"/my-bucket/obj.txt?versionId="+versionID,
			nil,
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, versionID, w.Header().Get(amzVersionID))
	})

	t.Run("delete with unknown versionId returns 204", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))

		req := httptest.NewRequest(
			http.MethodDelete,
			"/my-bucket/obj.txt?versionId=deadbeefdeadbeef",
			nil,
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("delete with versionId returns 404 when bucket not found", func(t *testing.T) {
		ro := newTestRouter(t)
		req := httptest.NewRequest(
			http.MethodDelete,
			"/no-bucket/obj.txt?versionId=abc123",
			nil,
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("delete with versionId returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			&mockStore{deleteObjectVersionErr: errors.New("disk failure")},
		)
		req := httptest.NewRequest(
			http.MethodDelete,
			"/my-bucket/obj.txt?versionId=abc123",
			nil,
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "InternalError")
	})

	t.Run("delete with versionId returns 403 when object is locked", func(t *testing.T) {
		ro := newRouterWithMock(
			&mockStore{deleteObjectVersionErr: ErrObjectLocked},
		)
		req := httptest.NewRequest(
			http.MethodDelete,
			"/my-bucket/obj.txt?versionId=abc123",
			nil,
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "AccessDenied")
	})

	t.Run(
		"delete with versionId sets x-amz-delete-marker when target is a delete marker",
		func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil),
			)
			require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("my-bucket", "Enabled"))
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "hello"))

			// Create a delete marker.
			delW := httptest.NewRecorder()
			ro.ServeHTTP(
				delW,
				httptest.NewRequest(http.MethodDelete, "/my-bucket/obj.txt", nil),
			)
			markerVID := delW.Header().Get(amzVersionID)
			require.NotEmpty(t, markerVID)

			// Permanently delete the delete marker itself.
			req := httptest.NewRequest(
				http.MethodDelete,
				"/my-bucket/obj.txt?versionId="+markerVID,
				nil,
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNoContent, w.Code)
			assert.Equal(t, "true", w.Header().Get(amzDeleteMarker))
			assert.Equal(t, markerVID, w.Header().Get(amzVersionID))
		},
	)
}

func TestRouterGetObjectVersion(t *testing.T) {
	t.Run("GET with versionId returns specific version body", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("my-bucket", "Enabled"))

		w1 := httptest.NewRecorder()
		ro.ServeHTTP(w1, putRequest("/my-bucket/obj.txt", "v1"))
		vid1 := w1.Header().Get(amzVersionID)
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "v2"))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt?versionId="+vid1, nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "v1", w.Body.String())
		assert.Equal(t, vid1, w.Header().Get(amzVersionID))
	})

	t.Run("GET with nonexistent versionId returns 404 NoSuchVersion", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("my-bucket", "Enabled"))
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "v1"))

		req := httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt?versionId=nonexistent", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchVersion")
	})
}

func TestRouterHeadObjectVersion(t *testing.T) {
	t.Run("HEAD with versionId returns 200 with x-amz-version-id header", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/my-bucket", nil))
		require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("my-bucket", "Enabled"))

		w1 := httptest.NewRecorder()
		ro.ServeHTTP(w1, putRequest("/my-bucket/obj.txt", "hello"))
		vid1 := w1.Header().Get(amzVersionID)
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "updated"))

		req := httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt?versionId="+vid1, nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, vid1, w.Header().Get(amzVersionID))
	})
}

func TestRouterCopyObjectVersionId(t *testing.T) {
	t.Run("CopyObject with ?versionId in copy-source copies specific version", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/src-bucket", nil),
		)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/dst-bucket", nil),
		)
		require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("src-bucket", "Enabled"))

		w1 := httptest.NewRecorder()
		ro.ServeHTTP(w1, putRequest("/src-bucket/obj.txt", "v1"))
		vid1 := w1.Header().Get(amzVersionID)
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/src-bucket/obj.txt", "v2"))

		req := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		req.Header.Set("x-amz-copy-source", "/src-bucket/obj.txt?versionId="+vid1)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify the copy contains v1 content.
		getReq := httptest.NewRequest(http.MethodGet, "/dst-bucket/copy.txt", nil)
		getW := httptest.NewRecorder()
		ro.ServeHTTP(getW, getReq)
		assert.Equal(t, "v1", getW.Body.String())
	})

	t.Run("CopyObject to versioned destination sets x-amz-version-id header", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/src-bucket", nil),
		)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/dst-bucket", nil),
		)
		require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("dst-bucket", "Enabled"))
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/src-bucket/obj.txt", "content"))

		req := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		req.Header.Set("x-amz-copy-source", "/src-bucket/obj.txt")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NotEmpty(t, w.Header().Get(amzVersionID))
	})
}

func TestRouterGetObjectDeleteMarker(t *testing.T) {
	t.Run(
		"GET without versionId on delete marker returns 404 with x-amz-delete-marker",
		func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil),
			)
			require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("my-bucket", "Enabled"))
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "v1"))
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodDelete, "/my-bucket/obj.txt", nil),
			)

			w := httptest.NewRecorder()
			ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt", nil))

			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Equal(t, "true", w.Header().Get(amzDeleteMarker))
		},
	)

	t.Run(
		"GET with versionId pointing to delete marker returns 405 with headers",
		func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil),
			)
			require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("my-bucket", "Enabled"))
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "v1"))

			delW := httptest.NewRecorder()
			ro.ServeHTTP(delW, httptest.NewRequest(http.MethodDelete, "/my-bucket/obj.txt", nil))
			markerVID := delW.Header().Get(amzVersionID)
			require.NotEmpty(t, markerVID)

			w := httptest.NewRecorder()
			ro.ServeHTTP(
				w,
				httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt?versionId="+markerVID, nil),
			)

			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
			assert.Equal(t, "true", w.Header().Get(amzDeleteMarker))
			assert.Equal(t, markerVID, w.Header().Get(amzVersionID))
		},
	)
}

func TestRouterHeadObjectDeleteMarker(t *testing.T) {
	t.Run(
		"HEAD without versionId on delete marker returns 404 with x-amz-delete-marker",
		func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil),
			)
			require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("my-bucket", "Enabled"))
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "v1"))
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodDelete, "/my-bucket/obj.txt", nil),
			)

			w := httptest.NewRecorder()
			ro.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/my-bucket/obj.txt", nil))

			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Equal(t, "true", w.Header().Get(amzDeleteMarker))
		},
	)

	t.Run(
		"HEAD with versionId pointing to delete marker returns 405 with headers",
		func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/my-bucket", nil),
			)
			require.NoError(t, ro.storage.(*Storage).PutBucketVersioning("my-bucket", "Enabled"))
			ro.ServeHTTP(httptest.NewRecorder(), putRequest("/my-bucket/obj.txt", "v1"))

			delW := httptest.NewRecorder()
			ro.ServeHTTP(delW, httptest.NewRequest(http.MethodDelete, "/my-bucket/obj.txt", nil))
			markerVID := delW.Header().Get(amzVersionID)
			require.NotEmpty(t, markerVID)

			w := httptest.NewRecorder()
			ro.ServeHTTP(
				w,
				httptest.NewRequest(
					http.MethodHead,
					"/my-bucket/obj.txt?versionId="+markerVID,
					nil,
				),
			)

			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
			assert.Equal(t, "true", w.Header().Get(amzDeleteMarker))
			assert.Equal(t, markerVID, w.Header().Get(amzVersionID))
		},
	)
}

// TestBucketConfigHandlers covers PUT/GET/DELETE for all 10 raw-XML bucket config routes.
func TestBucketConfigHandlers(t *testing.T) {
	const validXML = `<Config><X>1</X></Config>`

	ms := func(fields func(m *mockStore)) *mockStore {
		m := &mockStore{}
		fields(m)
		return m
	}

	t.Run("PUT returns 200 on valid XML", func(t *testing.T) {
		for _, q := range []string{
			"publicAccessBlock", "encryption", "ownershipControls", "notification",
			"lifecycle", "website", "logging", "accelerate", "replication", "requestPayment", "acl",
			"object-lock",
		} {
			q := q
			t.Run(q, func(t *testing.T) {
				ro := newRouterWithMock(&mockStore{bucketExists: true})
				req := httptest.NewRequest(http.MethodPut, "/b?"+q, strings.NewReader(validXML))
				w := httptest.NewRecorder()
				ro.ServeHTTP(w, req)
				assert.Equal(t, http.StatusOK, w.Code)
			})
		}
	})

	t.Run("PUT returns 400 on malformed XML", func(t *testing.T) {
		for _, q := range []string{
			"publicAccessBlock", "encryption", "ownershipControls", "notification",
			"lifecycle", "website", "logging", "accelerate", "replication", "requestPayment",
			"object-lock",
		} {
			q := q
			t.Run(q, func(t *testing.T) {
				ro := newRouterWithMock(&mockStore{})
				req := httptest.NewRequest(http.MethodPut, "/b?"+q, strings.NewReader("not-xml"))
				w := httptest.NewRecorder()
				ro.ServeHTTP(w, req)
				assert.Equal(t, http.StatusBadRequest, w.Code)
				assert.Contains(t, w.Body.String(), "MalformedXML")
			})
		}
	})

	t.Run("PUT returns 400 on body read error", func(t *testing.T) {
		for _, q := range []string{
			"publicAccessBlock", "encryption", "ownershipControls",
			"lifecycle", "website", "logging", "replication",
		} {
			q := q
			t.Run(q, func(t *testing.T) {
				ro := newRouterWithMock(&mockStore{})
				req := httptest.NewRequest(http.MethodPut, "/b?"+q, errReader{})
				w := httptest.NewRecorder()
				ro.ServeHTTP(w, req)
				assert.Equal(t, http.StatusBadRequest, w.Code)
				assert.Contains(t, w.Body.String(), "MalformedXML")
			})
		}
	})

	t.Run("PUT publicAccessBlock returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putPublicAccessBlockErr = ErrBucketNotFound }),
		)
		req := httptest.NewRequest(
			http.MethodPut,
			"/b?publicAccessBlock",
			strings.NewReader(validXML),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("PUT publicAccessBlock returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putPublicAccessBlockErr = errors.New("disk full") }),
		)
		req := httptest.NewRequest(
			http.MethodPut,
			"/b?publicAccessBlock",
			strings.NewReader(validXML),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PUT encryption returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketEncryptionErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?encryption", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("PUT encryption returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketEncryptionErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?encryption", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PUT ownershipControls returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketOwnershipControlsErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(
				http.MethodPut,
				"/b?ownershipControls",
				strings.NewReader(validXML),
			),
		)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("PUT ownershipControls returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketOwnershipControlsErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(
				http.MethodPut,
				"/b?ownershipControls",
				strings.NewReader(validXML),
			),
		)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PUT notification returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketNotificationErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?notification", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("PUT notification returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketNotificationErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?notification", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PUT lifecycle returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketLifecycleErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?lifecycle", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("PUT lifecycle returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketLifecycleErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?lifecycle", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PUT website returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketWebsiteErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?website", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("PUT website returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketWebsiteErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?website", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PUT logging returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketLoggingErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?logging", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("PUT logging returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketLoggingErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?logging", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PUT accelerate returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketAccelerateErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?accelerate", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("PUT accelerate returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketAccelerateErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?accelerate", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PUT replication returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketReplicationErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?replication", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("PUT replication returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketReplicationErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?replication", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PUT requestPayment returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketRequestPaymentErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?requestPayment", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("PUT requestPayment returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.putBucketRequestPaymentErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodPut, "/b?requestPayment", strings.NewReader(validXML)),
		)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET publicAccessBlock returns 200 with stored body", func(t *testing.T) {
		ro := newRouterWithMock(ms(func(m *mockStore) { m.getPublicAccessBlockResult = validXML }))
		req := httptest.NewRequest(http.MethodGet, "/b?publicAccessBlock", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "Config")
	})

	t.Run("GET returns 404 when not configured for applicable routes", func(t *testing.T) {
		for _, q := range []string{"publicAccessBlock", "encryption", "ownershipControls", "lifecycle", "website", "replication"} {
			q := q
			t.Run(q, func(t *testing.T) {
				ro := newRouterWithMock(&mockStore{})
				w := httptest.NewRecorder()
				ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?"+q, nil))
				assert.Equal(t, http.StatusNotFound, w.Code)
			})
		}
	})

	t.Run("GET returns default body when not configured", func(t *testing.T) {
		tests := []struct {
			query       string
			defaultFrag string
		}{
			{"notification", "NotificationConfiguration"},
			{"logging", "BucketLoggingStatus"},
			{"accelerate", "AccelerateConfiguration"},
			{"requestPayment", "BucketOwner"},
		}
		for _, tt := range tests {
			tt := tt
			t.Run(tt.query, func(t *testing.T) {
				ro := newRouterWithMock(&mockStore{})
				w := httptest.NewRecorder()
				ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?"+tt.query, nil))
				assert.Equal(t, http.StatusOK, w.Code)
				assert.Contains(t, w.Body.String(), tt.defaultFrag)
			})
		}
	})

	t.Run("GET publicAccessBlock returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getPublicAccessBlockErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?publicAccessBlock", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("GET publicAccessBlock returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getPublicAccessBlockErr = errors.New("disk full") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?publicAccessBlock", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET encryption 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketEncryptionErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?encryption", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("GET encryption 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketEncryptionErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?encryption", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET ownershipControls 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketOwnershipControlsErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?ownershipControls", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("GET ownershipControls 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketOwnershipControlsErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?ownershipControls", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET notification 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketNotificationErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?notification", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("GET notification 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketNotificationErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?notification", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET lifecycle 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketLifecycleErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?lifecycle", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("GET lifecycle 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketLifecycleErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?lifecycle", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET website 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketWebsiteErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?website", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("GET website 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketWebsiteErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?website", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET logging 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketLoggingErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?logging", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("GET logging 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketLoggingErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?logging", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET accelerate 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketAccelerateErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?accelerate", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("GET accelerate 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketAccelerateErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?accelerate", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET replication 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketReplicationErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?replication", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("GET replication 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketReplicationErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?replication", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET requestPayment 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketRequestPaymentErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?requestPayment", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("GET requestPayment 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.getBucketRequestPaymentErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?requestPayment", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("DELETE publicAccessBlock returns 204 on success", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?publicAccessBlock", nil))
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("DELETE returns 204 on success", func(t *testing.T) {
		for _, q := range []string{"encryption", "ownershipControls", "lifecycle", "website", "replication"} {
			q := q
			t.Run(q, func(t *testing.T) {
				ro := newRouterWithMock(&mockStore{})
				w := httptest.NewRecorder()
				ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?"+q, nil))
				assert.Equal(t, http.StatusNoContent, w.Code)
			})
		}
	})

	t.Run("DELETE publicAccessBlock 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.deletePublicAccessBlockErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?publicAccessBlock", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("DELETE publicAccessBlock 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.deletePublicAccessBlockErr = errors.New("disk full") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?publicAccessBlock", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("DELETE encryption 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.deleteBucketEncryptionErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?encryption", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("DELETE encryption 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.deleteBucketEncryptionErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?encryption", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("DELETE ownershipControls 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.deleteBucketOwnershipControlsErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?ownershipControls", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("DELETE ownershipControls 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.deleteBucketOwnershipControlsErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?ownershipControls", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("DELETE lifecycle 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.deleteBucketLifecycleErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?lifecycle", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("DELETE lifecycle 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.deleteBucketLifecycleErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?lifecycle", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("DELETE website 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.deleteBucketWebsiteErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?website", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("DELETE website 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.deleteBucketWebsiteErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?website", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("DELETE replication 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.deleteBucketReplicationErr = ErrBucketNotFound }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?replication", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("DELETE replication 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(
			ms(func(m *mockStore) { m.deleteBucketReplicationErr = errors.New("fail") }),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b?replication", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestStripXMLDecl(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no declaration", `<Config/>`, `<Config/>`},
		{
			"standard xml.Header",
			"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Config/>",
			"<Config/>",
		},
		{"declaration without newline", `<?xml version="1.0"?><Config/>`, `<Config/>`},
		{"declaration with whitespace", "  <?xml version=\"1.0\"?>  <Config/>", "<Config/>"},
		{"no closing ?>", `<?xml version="1.0" <Config/>`, `<?xml version="1.0" <Config/>`},
		{
			"xml-stylesheet PI not stripped",
			`<?xml-stylesheet type="text/xsl" href="s.xsl"?><Config/>`,
			`<?xml-stylesheet type="text/xsl" href="s.xsl"?><Config/>`,
		},
		{"empty string", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, stripXMLDecl(tc.input))
		})
	}
}

func TestBucketACLHandlers(t *testing.T) {
	t.Run("GET returns 200 with default ACL when bucket exists", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: true})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/my-bucket?acl", nil))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "AccessControlPolicy")
		assert.Contains(t, w.Body.String(), "FULL_CONTROL")
	})

	t.Run("GET returns 404 when bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: false})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/my-bucket?acl", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("PUT returns 200 when bucket exists (stub ignores body)", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: true})
		req := httptest.NewRequest(http.MethodPut, "/my-bucket?acl",
			strings.NewReader(`<AccessControlPolicy/>`))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("PUT returns 404 when bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: false})
		req := httptest.NewRequest(http.MethodPut, "/my-bucket?acl",
			strings.NewReader(`<AccessControlPolicy/>`))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})
}

func TestObjectACLHandlers(t *testing.T) {
	t.Run("GET returns 200 with default ACL when object exists", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: true})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt?acl", nil))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "AccessControlPolicy")
	})

	t.Run("GET returns 404 when bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: false})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt?acl", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("GET returns 404 when object not found", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: true, headObjectErr: ErrObjectNotFound})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt?acl", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchKey")
	})

	t.Run("GET returns 500 on HeadObject error", func(t *testing.T) {
		ro := newRouterWithMock(
			&mockStore{bucketExists: true, headObjectErr: errors.New("disk fail")},
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/my-bucket/obj.txt?acl", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PUT returns 200 when bucket exists (stub ignores body)", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: true})
		req := httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt?acl",
			strings.NewReader(`<AccessControlPolicy/>`))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("PUT returns 404 when bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: false})
		req := httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt?acl",
			strings.NewReader(`<AccessControlPolicy/>`))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("PUT returns 404 when object not found", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: true, headObjectErr: ErrObjectNotFound})
		req := httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt?acl",
			strings.NewReader(`<AccessControlPolicy/>`))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchKey")
	})

	t.Run("PUT returns 500 on HeadObject error", func(t *testing.T) {
		ro := newRouterWithMock(
			&mockStore{bucketExists: true, headObjectErr: errors.New("disk fail")},
		)
		req := httptest.NewRequest(http.MethodPut, "/my-bucket/obj.txt?acl",
			strings.NewReader(`<AccessControlPolicy/>`))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestRestoreObject(t *testing.T) {
	t.Run("POST returns 202 when object exists", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: true})
		req := httptest.NewRequest(http.MethodPost, "/my-bucket/obj.txt?restore",
			strings.NewReader(`<RestoreRequest/>`))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusAccepted, w.Code)
	})

	t.Run("POST returns 404 when bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: false})
		req := httptest.NewRequest(http.MethodPost, "/my-bucket/obj.txt?restore",
			strings.NewReader(`<RestoreRequest/>`))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("POST returns 404 when object not found", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: true, headObjectErr: ErrObjectNotFound})
		req := httptest.NewRequest(http.MethodPost, "/my-bucket/obj.txt?restore",
			strings.NewReader(`<RestoreRequest/>`))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchKey")
	})

	t.Run("POST returns 500 on HeadObject error", func(t *testing.T) {
		ro := newRouterWithMock(
			&mockStore{bucketExists: true, headObjectErr: errors.New("disk fail")},
		)
		req := httptest.NewRequest(http.MethodPost, "/my-bucket/obj.txt?restore",
			strings.NewReader(`<RestoreRequest/>`))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("POST returns 200 when restore already initiated", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:   true,
			headObjectMeta: ObjectMetadata{RestoreInitiated: true},
		})
		req := httptest.NewRequest(http.MethodPost, "/my-bucket/obj.txt?restore",
			strings.NewReader(`<RestoreRequest/>`))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("POST returns 500 when SetObjectRestoreInitiated fails", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:                 true,
			setObjectRestoreInitiatedErr: errors.New("write fail"),
		})
		req := httptest.NewRequest(http.MethodPost, "/my-bucket/obj.txt?restore",
			strings.NewReader(`<RestoreRequest/>`))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestSSEResponseHeaders(t *testing.T) {
	t.Run("PutObject echoes SSE headers from metadata", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			putObjectMeta: ObjectMetadata{SSEAlgorithm: "aws:kms", SSEKMSKeyID: "my-key"},
		})
		req := httptest.NewRequest(http.MethodPut, "/b/k", strings.NewReader("data"))
		req.Header.Set(amzSSE, "aws:kms")
		req.Header.Set(amzSSEKMSKeyID, "my-key")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "aws:kms", w.Header().Get(amzSSE))
		assert.Equal(t, "my-key", w.Header().Get(amzSSEKMSKeyID))
	})

	t.Run("GetObject echoes SSE headers from metadata", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			getObjectMeta: ObjectMetadata{SSEAlgorithm: "aws:kms", SSEKMSKeyID: "my-key"},
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b/k", nil))
		assert.Equal(t, "aws:kms", w.Header().Get(amzSSE))
		assert.Equal(t, "my-key", w.Header().Get(amzSSEKMSKeyID))
	})

	t.Run("HeadObject echoes SSE headers from metadata", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			headObjectMeta: ObjectMetadata{SSEAlgorithm: "AES256"},
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/b/k", nil))
		assert.Equal(t, "AES256", w.Header().Get(amzSSE))
	})

	t.Run("CopyObject echoes SSE headers from metadata", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			copyObjectMeta: ObjectMetadata{SSEAlgorithm: "aws:kms", SSEKMSKeyID: "my-key"},
		})
		req := httptest.NewRequest(http.MethodPut, "/dst-bucket/copy.txt", nil)
		req.Header.Set(amzCopySource, "/src-bucket/orig.txt")
		req.Header.Set(amzSSE, "aws:kms")
		req.Header.Set(amzSSEKMSKeyID, "my-key")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "aws:kms", w.Header().Get(amzSSE))
		assert.Equal(t, "my-key", w.Header().Get(amzSSEKMSKeyID))
	})

	t.Run("CreateMultipartUpload echoes SSE headers from request", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			createMultipartUploadID: "uid-sse",
		})
		req := httptest.NewRequest(http.MethodPost, "/b/k?uploads", nil)
		req.Header.Set(amzSSE, "AES256")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "AES256", w.Header().Get(amzSSE))
	})

	t.Run("CreateMultipartUpload echoes aws:kms SSE headers from request", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			createMultipartUploadID: "uid-kms",
		})
		req := httptest.NewRequest(http.MethodPost, "/b/k?uploads", nil)
		req.Header.Set(amzSSE, "aws:kms")
		req.Header.Set(amzSSEKMSKeyID, "my-key")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "aws:kms", w.Header().Get(amzSSE))
		assert.Equal(t, "my-key", w.Header().Get(amzSSEKMSKeyID))
	})

	t.Run("CompleteMultipartUpload echoes SSE headers from metadata", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			completeMultipartUploadMeta: ObjectMetadata{SSEAlgorithm: "AES256"},
		})
		body := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"abc"</ETag></Part></CompleteMultipartUpload>`
		req := httptest.NewRequest(http.MethodPost, "/b/k?uploadId=uid123", strings.NewReader(body))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, "AES256", w.Header().Get(amzSSE))
	})
}

func TestObjectLockConfigHandlers(t *testing.T) {
	const validXML = `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`

	t.Run("PUT returns 200 on valid XML when versioning enabled", func(t *testing.T) {
		ro := newTestRouter(t)
		putReq := httptest.NewRequest(http.MethodPut, "/b", nil)
		putW := httptest.NewRecorder()
		ro.ServeHTTP(putW, putReq)
		require.Equal(t, http.StatusOK, putW.Code)

		// Enable versioning (required for Object Lock).
		verReq := httptest.NewRequest(
			http.MethodPut,
			"/b?versioning",
			strings.NewReader(
				`<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`,
			),
		)
		ro.ServeHTTP(httptest.NewRecorder(), verReq)

		req := httptest.NewRequest(http.MethodPut, "/b?object-lock", strings.NewReader(validXML))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("PUT returns 400 InvalidBucketState when versioning not enabled", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.putBucketObjectLockErr = ErrInvalidBucketState }),
		)
		req := httptest.NewRequest(http.MethodPut, "/b?object-lock", strings.NewReader(validXML))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidBucketState")
	})

	t.Run("PUT returns 500 on body read error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		req := httptest.NewRequest(http.MethodPut, "/b?object-lock", errReader{})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "InternalError")
	})

	t.Run("PUT returns 400 on malformed XML", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		req := httptest.NewRequest(http.MethodPut, "/b?object-lock", strings.NewReader("not-xml"))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "MalformedXML")
	})

	t.Run("PUT returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.putBucketObjectLockErr = ErrBucketNotFound }),
		)
		req := httptest.NewRequest(http.MethodPut, "/b?object-lock", strings.NewReader(validXML))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("PUT returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(newMockStore(func(m *mockStore) {
			m.putBucketObjectLockErr = errors.New("disk full")
		}))
		req := httptest.NewRequest(http.MethodPut, "/b?object-lock", strings.NewReader(validXML))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET returns stored configuration", func(t *testing.T) {
		ro := newRouterWithMock(newMockStore(func(m *mockStore) {
			m.getBucketObjectLockResult = validXML
		}))
		req := httptest.NewRequest(http.MethodGet, "/b?object-lock", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "ObjectLockEnabled")
	})

	t.Run("GET returns 404 when not configured", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		req := httptest.NewRequest(http.MethodGet, "/b?object-lock", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "ObjectLockConfigurationNotFoundError")
	})

	t.Run("GET returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.getBucketObjectLockErr = ErrBucketNotFound }),
		)
		req := httptest.NewRequest(http.MethodGet, "/b?object-lock", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("GET returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(newMockStore(func(m *mockStore) {
			m.getBucketObjectLockErr = errors.New("disk full")
		}))
		req := httptest.NewRequest(http.MethodGet, "/b?object-lock", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestObjectRetentionHandlers(t *testing.T) {
	const validBody = `<Retention xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Mode>GOVERNANCE</Mode><RetainUntilDate>2030-01-01T00:00:00Z</RetainUntilDate></Retention>`

	t.Run("PUT/GET roundtrip via real storage", func(t *testing.T) {
		ro := newTestRouter(t)
		// Create bucket and object.
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/b", nil))
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/b/obj.txt", strings.NewReader("data")),
		)

		// PUT retention.
		putW := httptest.NewRecorder()
		ro.ServeHTTP(
			putW,
			httptest.NewRequest(
				http.MethodPut,
				"/b/obj.txt?retention",
				strings.NewReader(validBody),
			),
		)
		assert.Equal(t, http.StatusOK, putW.Code)

		// GET retention.
		getW := httptest.NewRecorder()
		ro.ServeHTTP(getW, httptest.NewRequest(http.MethodGet, "/b/obj.txt?retention", nil))
		assert.Equal(t, http.StatusOK, getW.Code)
		assert.Contains(t, getW.Body.String(), "GOVERNANCE")
		assert.Contains(t, getW.Body.String(), "2030-01-01")
	})

	t.Run("PUT returns 400 on malformed XML", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		req := httptest.NewRequest(http.MethodPut, "/b/k?retention", strings.NewReader("not-xml"))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "MalformedXML")
	})

	t.Run("PUT returns 400 on invalid mode", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		body := `<Retention xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Mode>INVALID</Mode><RetainUntilDate>2030-01-01T00:00:00Z</RetainUntilDate></Retention>`
		req := httptest.NewRequest(http.MethodPut, "/b/k?retention", strings.NewReader(body))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("PUT returns 400 when RetainUntilDate is in the past", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		ro.now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
		body := `<Retention xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Mode>GOVERNANCE</Mode><RetainUntilDate>2020-01-01T00:00:00Z</RetainUntilDate></Retention>`
		req := httptest.NewRequest(http.MethodPut, "/b/k?retention", strings.NewReader(body))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("PUT returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.putObjectRetentionErr = ErrBucketNotFound }),
		)
		req := httptest.NewRequest(http.MethodPut, "/b/k?retention", strings.NewReader(validBody))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("PUT returns 404 on object not found", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.putObjectRetentionErr = ErrObjectNotFound }),
		)
		req := httptest.NewRequest(http.MethodPut, "/b/k?retention", strings.NewReader(validBody))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchKey")
	})

	t.Run("PUT returns 405 for delete marker", func(t *testing.T) {
		ro := newRouterWithMock(newMockStore(func(m *mockStore) {
			m.putObjectRetentionErr = &DeleteMarkerError{VersionID: "vid"}
		}))
		req := httptest.NewRequest(http.MethodPut, "/b/k?retention", strings.NewReader(validBody))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("PUT returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(newMockStore(func(m *mockStore) {
			m.putObjectRetentionErr = errors.New("disk full")
		}))
		req := httptest.NewRequest(http.MethodPut, "/b/k?retention", strings.NewReader(validBody))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET returns 404 when retention not set", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.getObjectRetentionErr = ErrNoObjectRetention }),
		)
		req := httptest.NewRequest(http.MethodGet, "/b/k?retention", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchObjectLockConfiguration")
	})

	t.Run("GET returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.getObjectRetentionErr = ErrBucketNotFound }),
		)
		req := httptest.NewRequest(http.MethodGet, "/b/k?retention", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("GET returns 404 on object not found", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.getObjectRetentionErr = ErrObjectNotFound }),
		)
		req := httptest.NewRequest(http.MethodGet, "/b/k?retention", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchKey")
	})

	t.Run("GET returns 405 for delete marker", func(t *testing.T) {
		ro := newRouterWithMock(newMockStore(func(m *mockStore) {
			m.getObjectRetentionErr = &DeleteMarkerError{VersionID: "vid"}
		}))
		req := httptest.NewRequest(http.MethodGet, "/b/k?retention", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("GET returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(newMockStore(func(m *mockStore) {
			m.getObjectRetentionErr = errors.New("disk full")
		}))
		req := httptest.NewRequest(http.MethodGet, "/b/k?retention", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PUT passes versionId to storage", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/b", nil))
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/b/obj.txt", strings.NewReader("data")),
		)
		putW := httptest.NewRecorder()
		ro.ServeHTTP(
			putW,
			httptest.NewRequest(
				http.MethodPut,
				"/b/obj.txt?retention&versionId=nonexistent",
				strings.NewReader(validBody),
			),
		)
		// nonexistent versionId → 404
		assert.Equal(t, http.StatusNotFound, putW.Code)
	})
}

func TestObjectLegalHoldHandlers(t *testing.T) {
	const validBody = `<LegalHold xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>ON</Status></LegalHold>`

	t.Run("PUT/GET roundtrip via real storage", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/b", nil))
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/b/obj.txt", strings.NewReader("data")),
		)

		putW := httptest.NewRecorder()
		ro.ServeHTTP(
			putW,
			httptest.NewRequest(
				http.MethodPut,
				"/b/obj.txt?legal-hold",
				strings.NewReader(validBody),
			),
		)
		assert.Equal(t, http.StatusOK, putW.Code)

		getW := httptest.NewRecorder()
		ro.ServeHTTP(getW, httptest.NewRequest(http.MethodGet, "/b/obj.txt?legal-hold", nil))
		assert.Equal(t, http.StatusOK, getW.Code)
		assert.Contains(t, getW.Body.String(), "ON")
	})

	t.Run("PUT returns 400 on malformed XML", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		req := httptest.NewRequest(http.MethodPut, "/b/k?legal-hold", strings.NewReader("not-xml"))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "MalformedXML")
	})

	t.Run("PUT returns 400 on invalid status", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{})
		body := `<LegalHold xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>INVALID</Status></LegalHold>`
		req := httptest.NewRequest(http.MethodPut, "/b/k?legal-hold", strings.NewReader(body))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("PUT returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.putObjectLegalHoldErr = ErrBucketNotFound }),
		)
		req := httptest.NewRequest(http.MethodPut, "/b/k?legal-hold", strings.NewReader(validBody))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("PUT returns 404 on object not found", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.putObjectLegalHoldErr = ErrObjectNotFound }),
		)
		req := httptest.NewRequest(http.MethodPut, "/b/k?legal-hold", strings.NewReader(validBody))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchKey")
	})

	t.Run("PUT returns 405 for delete marker", func(t *testing.T) {
		ro := newRouterWithMock(newMockStore(func(m *mockStore) {
			m.putObjectLegalHoldErr = &DeleteMarkerError{VersionID: "vid"}
		}))
		req := httptest.NewRequest(http.MethodPut, "/b/k?legal-hold", strings.NewReader(validBody))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("PUT returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(newMockStore(func(m *mockStore) {
			m.putObjectLegalHoldErr = errors.New("disk full")
		}))
		req := httptest.NewRequest(http.MethodPut, "/b/k?legal-hold", strings.NewReader(validBody))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GET returns 404 when legal hold not set", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.getObjectLegalHoldErr = ErrNoObjectLegalHold }),
		)
		req := httptest.NewRequest(http.MethodGet, "/b/k?legal-hold", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchObjectLockConfiguration")
	})

	t.Run("GET returns 404 on bucket not found", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.getObjectLegalHoldErr = ErrBucketNotFound }),
		)
		req := httptest.NewRequest(http.MethodGet, "/b/k?legal-hold", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("GET returns 404 on object not found", func(t *testing.T) {
		ro := newRouterWithMock(
			newMockStore(func(m *mockStore) { m.getObjectLegalHoldErr = ErrObjectNotFound }),
		)
		req := httptest.NewRequest(http.MethodGet, "/b/k?legal-hold", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchKey")
	})

	t.Run("GET returns 405 for delete marker", func(t *testing.T) {
		ro := newRouterWithMock(newMockStore(func(m *mockStore) {
			m.getObjectLegalHoldErr = &DeleteMarkerError{VersionID: "vid"}
		}))
		req := httptest.NewRequest(http.MethodGet, "/b/k?legal-hold", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("GET returns 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(newMockStore(func(m *mockStore) {
			m.getObjectLegalHoldErr = errors.New("disk full")
		}))
		req := httptest.NewRequest(http.MethodGet, "/b/k?legal-hold", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestObjectLockDeleteEnforcement(t *testing.T) {
	const (
		bucket          = "my-bucket"
		key             = "obj.txt"
		futureRetention = `<Retention xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Mode>COMPLIANCE</Mode><RetainUntilDate>2099-01-01T00:00:00Z</RetainUntilDate></Retention>`
		govRetention    = `<Retention xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Mode>GOVERNANCE</Mode><RetainUntilDate>2099-01-01T00:00:00Z</RetainUntilDate></Retention>`
		legalHoldOn     = `<LegalHold xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>ON</Status></LegalHold>`
		legalHoldOff    = `<LegalHold xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>OFF</Status></LegalHold>`
	)

	setup := func(t *testing.T) *Router {
		t.Helper()
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/"+bucket, nil))
		ro.ServeHTTP(httptest.NewRecorder(), putRequest("/"+bucket+"/"+key, "data"))
		return ro
	}

	t.Run("DELETE blocked by legal hold ON", func(t *testing.T) {
		ro := setup(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(
			http.MethodPut, "/"+bucket+"/"+key+"?legal-hold", strings.NewReader(legalHoldOn),
		))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/"+bucket+"/"+key, nil))
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "AccessDenied")
	})

	t.Run("DELETE allowed after legal hold cleared", func(t *testing.T) {
		ro := setup(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(
			http.MethodPut, "/"+bucket+"/"+key+"?legal-hold", strings.NewReader(legalHoldOn),
		))
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(
			http.MethodPut, "/"+bucket+"/"+key+"?legal-hold", strings.NewReader(legalHoldOff),
		))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/"+bucket+"/"+key, nil))
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("DELETE blocked by COMPLIANCE retention", func(t *testing.T) {
		ro := setup(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(
			http.MethodPut, "/"+bucket+"/"+key+"?retention", strings.NewReader(futureRetention),
		))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/"+bucket+"/"+key, nil))
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "AccessDenied")
	})

	t.Run("DELETE blocked by COMPLIANCE retention even with bypass header", func(t *testing.T) {
		ro := setup(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(
			http.MethodPut, "/"+bucket+"/"+key+"?retention", strings.NewReader(futureRetention),
		))
		req := httptest.NewRequest(http.MethodDelete, "/"+bucket+"/"+key, nil)
		req.Header.Set("x-amz-bypass-governance-retention", "true")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "AccessDenied")
	})

	t.Run("DELETE blocked by GOVERNANCE retention without bypass header", func(t *testing.T) {
		ro := setup(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(
			http.MethodPut, "/"+bucket+"/"+key+"?retention", strings.NewReader(govRetention),
		))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/"+bucket+"/"+key, nil))
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "AccessDenied")
	})

	t.Run("DELETE allowed for GOVERNANCE with bypass header", func(t *testing.T) {
		ro := setup(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(
			http.MethodPut, "/"+bucket+"/"+key+"?retention", strings.NewReader(govRetention),
		))
		req := httptest.NewRequest(http.MethodDelete, "/"+bucket+"/"+key, nil)
		req.Header.Set("x-amz-bypass-governance-retention", "true")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("DELETE with versionId blocked by legal hold on specific version", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/"+bucket, nil))
		require.NoError(t, ro.storage.(*Storage).PutBucketVersioning(bucket, "Enabled"))
		putW := httptest.NewRecorder()
		ro.ServeHTTP(putW, putRequest("/"+bucket+"/"+key, "data"))
		vid := putW.Header().Get(amzVersionID)
		require.NotEmpty(t, vid)

		// Set legal hold on the specific version.
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(
			http.MethodPut,
			"/"+bucket+"/"+key+"?legal-hold&versionId="+vid,
			strings.NewReader(legalHoldOn),
		))

		req := httptest.NewRequest(http.MethodDelete, "/"+bucket+"/"+key+"?versionId="+vid, nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "AccessDenied")
	})

	t.Run(
		"DELETE with versionId blocked by COMPLIANCE retention on specific version",
		func(t *testing.T) {
			ro := newTestRouter(t)
			ro.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPut, "/"+bucket, nil),
			)
			require.NoError(t, ro.storage.(*Storage).PutBucketVersioning(bucket, "Enabled"))
			putW := httptest.NewRecorder()
			ro.ServeHTTP(putW, putRequest("/"+bucket+"/"+key, "data"))
			vid := putW.Header().Get(amzVersionID)
			require.NotEmpty(t, vid)

			complianceRetention := `<Retention xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Mode>COMPLIANCE</Mode><RetainUntilDate>2099-01-01T00:00:00Z</RetainUntilDate></Retention>`
			ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(
				http.MethodPut,
				"/"+bucket+"/"+key+"?retention&versionId="+vid,
				strings.NewReader(complianceRetention),
			))

			req := httptest.NewRequest(http.MethodDelete, "/"+bucket+"/"+key+"?versionId="+vid, nil)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, w.Body.String(), "AccessDenied")
		},
	)

	t.Run("DELETE with versionId allowed for GOVERNANCE with bypass header", func(t *testing.T) {
		ro := newTestRouter(t)
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/"+bucket, nil))
		require.NoError(t, ro.storage.(*Storage).PutBucketVersioning(bucket, "Enabled"))
		putW := httptest.NewRecorder()
		ro.ServeHTTP(putW, putRequest("/"+bucket+"/"+key, "data"))
		vid := putW.Header().Get(amzVersionID)
		require.NotEmpty(t, vid)

		governanceRetention := `<Retention xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Mode>GOVERNANCE</Mode><RetainUntilDate>2099-01-01T00:00:00Z</RetainUntilDate></Retention>`
		ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(
			http.MethodPut,
			"/"+bucket+"/"+key+"?retention&versionId="+vid,
			strings.NewReader(governanceRetention),
		))

		req := httptest.NewRequest(http.MethodDelete, "/"+bucket+"/"+key+"?versionId="+vid, nil)
		req.Header.Set("x-amz-bypass-governance-retention", "true")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run(
		"DeleteObjects blocked by legal hold returns AccessDenied in error element",
		func(t *testing.T) {
			ro := setup(t)
			ro.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(
				http.MethodPut, "/"+bucket+"/"+key+"?legal-hold", strings.NewReader(legalHoldOn),
			))
			req := httptest.NewRequest(
				http.MethodPost,
				"/"+bucket+"?delete",
				strings.NewReader(`<Delete><Object><Key>`+key+`</Key></Object></Delete>`),
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()
			assert.Contains(t, body, "<Error>")
			assert.Contains(t, body, "AccessDenied")
		},
	)
}
