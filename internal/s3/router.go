package s3

import (
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// bucketStore is the subset of Storage used by the Router for bucket operations.
type bucketStore interface {
	ListBuckets() ([]BucketInfo, error)
	CreateBucket(bucket, region string) error
	DeleteBucket(bucket string) error
	BucketExists(bucket string) bool
	GetBucketRegion(bucket string) (string, error)
}

// objectStore is the subset of Storage used by the Router for object operations.
type objectStore interface {
	PutObject(bucket, key string, r io.Reader, contentType string) (ObjectMetadata, error)
	GetObject(bucket, key string) (*os.File, ObjectMetadata, error)
	CopyObject(srcBucket, srcKey, dstBucket, dstKey string) (ObjectMetadata, error)
	DeleteObject(bucket, key string) error
	HeadObject(bucket, key string) (ObjectMetadata, error)
	ListObjects(bucket string) ([]ObjectInfo, error)
}

// multipartStore is the subset of Storage used by the Router for multipart upload operations.
type multipartStore interface {
	CreateMultipartUpload(bucket, key, contentType string) (uploadID string, err error)
	UploadPart(uploadID string, partNumber int, r io.Reader) (etag string, err error)
	CompleteMultipartUpload(uploadID string, parts []CompletePart) (ObjectMetadata, error)
	AbortMultipartUpload(uploadID string) error
}

// Router handles S3 API requests using path-style URLs: /<bucket>/<key>
type Router struct {
	storage interface {
		bucketStore
		objectStore
		multipartStore
	}
	now func() time.Time // injectable for testing; defaults to time.Now
}

func NewRouter(storage *Storage) *Router {
	return &Router{storage: storage, now: time.Now}
}

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("X-Amz-Signature") {
		if status, code, msg := checkPresigned(r, ro.now()); status != 0 {
			slog.Debug( // #nosec G706 -- path comes from URL; log injection risk accepted for a local dev emulator
				"presigned request rejected",
				"path",
				r.URL.Path,
				"code",
				code,
			)
			writeError(w, r, status, code, msg)
			return
		}
	}
	bucket, key := parsePath(r.URL.Path)
	switch {
	case bucket == "":
		ro.routeRoot(w, r)
	case key == "":
		ro.routeBucket(w, r, bucket)
	default:
		ro.routeObject(w, r, bucket, key)
	}
}

// checkPresigned validates a presigned request.
// Returns (0, "", "") if valid. Returns (status, code, message) if invalid.
func checkPresigned(r *http.Request, now time.Time) (int, string, string) {
	q := r.URL.Query()

	if algo := q.Get("X-Amz-Algorithm"); algo != "" && algo != "AWS4-HMAC-SHA256" {
		return http.StatusBadRequest,
			"AuthorizationQueryParametersError",
			`X-Amz-Algorithm only supports "AWS4-HMAC-SHA256".`
	}

	amzDate := q.Get("X-Amz-Date")
	amzExpires := q.Get("X-Amz-Expires")
	if amzDate == "" || amzExpires == "" {
		return 0, "", ""
	}

	expires, err := strconv.ParseInt(amzExpires, 10, 64)
	if err != nil || expires < 1 || expires > 604800 {
		return http.StatusBadRequest,
			"AuthorizationQueryParametersError",
			"X-Amz-Expires must be between 1 and 604800 seconds."
	}

	t, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil {
		return 0, "", ""
	}

	if !now.Before(t.Add(time.Duration(expires) * time.Second)) {
		return http.StatusForbidden, "AccessDenied", "Request has expired."
	}

	return 0, "", ""
}

func (ro *Router) routeRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ro.handleListBuckets(w, r)
	default:
		writeError(
			w,
			r,
			http.StatusMethodNotAllowed,
			"MethodNotAllowed",
			"The specified method is not allowed.",
		)
	}
}

func (ro *Router) routeBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodPut:
		ro.handleCreateBucket(w, r, bucket)
	case http.MethodDelete:
		ro.handleDeleteBucket(w, r, bucket)
	case http.MethodHead:
		ro.handleHeadBucket(w, r, bucket)
	case http.MethodGet:
		switch {
		case r.URL.Query().Has("location"):
			ro.handleGetBucketLocation(w, r, bucket)
		case r.URL.Query().Get("list-type") == "2":
			ro.handleListObjectsV2(w, r, bucket)
		default:
			ro.handleListObjects(w, r, bucket)
		}
	case http.MethodPost:
		if r.URL.Query().Has("delete") {
			ro.handleDeleteObjects(w, r, bucket)
		} else {
			writeNotImplemented(w, r)
		}
	default:
		writeError(
			w,
			r,
			http.StatusMethodNotAllowed,
			"MethodNotAllowed",
			"The specified method is not allowed.",
		)
	}
}

func (ro *Router) handleListObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	q := r.URL.Query()
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	marker := q.Get("marker")
	maxKeys := 1000
	if s := q.Get("max-keys"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			maxKeys = n
		}
	}

	objects, err := ro.storage.ListObjects(bucket)
	if err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		slog.Error( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"failed to list objects",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	var contents []xmlObjectContent
	commonPrefixes := make(map[string]struct{})
	var nextMarker string
	isTruncated := false

	for _, obj := range objects {
		if !strings.HasPrefix(obj.Key, prefix) {
			continue
		}
		if obj.Key <= marker {
			continue
		}
		// Apply delimiter: group keys that share a common prefix up to the delimiter.
		if delimiter != "" {
			rest := strings.TrimPrefix(obj.Key, prefix)
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				cp := prefix + rest[:idx+len(delimiter)]
				commonPrefixes[cp] = struct{}{}
				continue
			}
		}
		if len(contents) >= maxKeys {
			isTruncated = true
			break
		}
		contents = append(contents, xmlObjectContent{
			Key:          obj.Key,
			LastModified: obj.Metadata.LastModified.UTC(),
			ETag:         obj.Metadata.ETag,
			Size:         obj.Metadata.Size,
			StorageClass: "STANDARD",
		})
		nextMarker = obj.Key
	}

	cps := make([]xmlCommonPrefix, 0, len(commonPrefixes))
	for cp := range commonPrefixes {
		cps = append(cps, xmlCommonPrefix{Prefix: cp})
	}
	slices.SortFunc(cps, func(a, b xmlCommonPrefix) int {
		return strings.Compare(a.Prefix, b.Prefix)
	})

	slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"listed objects",
		"bucket",
		bucket,
		"count",
		len(contents),
	)

	result := listObjectsResult{
		Name:           bucket,
		Prefix:         prefix,
		Marker:         marker,
		MaxKeys:        maxKeys,
		IsTruncated:    isTruncated,
		Contents:       contents,
		CommonPrefixes: cps,
	}
	if isTruncated {
		result.NextMarker = nextMarker
	}
	writeXML(w, http.StatusOK, result)
}

func (ro *Router) handleListObjectsV2(w http.ResponseWriter, r *http.Request, bucket string) {
	prefix := r.URL.Query().Get("prefix")
	objects, err := ro.storage.ListObjects(bucket)
	if err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		slog.Error( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"failed to list objects",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	contents := make([]xmlObjectContent, 0, len(objects))
	for _, obj := range objects {
		if !strings.HasPrefix(obj.Key, prefix) {
			continue
		}
		contents = append(contents, xmlObjectContent{
			Key:          obj.Key,
			LastModified: obj.Metadata.LastModified.UTC(),
			ETag:         obj.Metadata.ETag,
			Size:         obj.Metadata.Size,
			StorageClass: "STANDARD",
		})
	}
	slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"listed objects",
		"bucket",
		bucket,
		"count",
		len(contents),
	)
	writeXML(w, http.StatusOK, listObjectsV2Result{
		Name:        bucket,
		Prefix:      prefix,
		KeyCount:    len(contents),
		MaxKeys:     1000,
		IsTruncated: false,
		Contents:    contents,
	})
}

func (ro *Router) routeObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	q := r.URL.Query()
	switch r.Method {
	case http.MethodPost:
		switch {
		case q.Has("uploads"):
			ro.handleCreateMultipartUpload(w, r, bucket, key)
		case q.Has("uploadId"):
			ro.handleCompleteMultipartUpload(w, r, bucket, key)
		default:
			writeNotImplemented(w, r)
		}
	case http.MethodPut:
		switch {
		case q.Has("partNumber") && q.Has("uploadId"):
			ro.handleUploadPart(w, r, bucket, key)
		case r.Header.Get("x-amz-copy-source") != "":
			ro.handleCopyObject(w, r, bucket, key)
		default:
			ro.handlePutObject(w, r, bucket, key)
		}
	case http.MethodGet:
		ro.handleGetObject(w, r, bucket, key)
	case http.MethodDelete:
		if q.Has("uploadId") {
			ro.handleAbortMultipartUpload(w, r, bucket, key)
		} else {
			ro.handleDeleteObject(w, r, bucket, key)
		}
	case http.MethodHead:
		ro.handleHeadObject(w, r, bucket, key)
	default:
		writeError(
			w,
			r,
			http.StatusMethodNotAllowed,
			"MethodNotAllowed",
			"The specified method is not allowed.",
		)
	}
}

func (ro *Router) handleCreateMultipartUpload(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	uploadID, err := ro.storage.CreateMultipartUpload(bucket, key, contentType)
	if err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(
				w,
				r,
				http.StatusNotFound,
				"NoSuchBucket",
				"The specified bucket does not exist.",
			)
		default:
			slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"failed to create multipart upload",
				"bucket",
				bucket,
				"key",
				key,
				"err",
				err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
		"multipart upload initiated",
		"bucket",
		bucket,
		"key",
		key,
		"uploadId",
		uploadID,
	)
	writeXML(w, http.StatusOK, initiateMultipartUploadResult{
		Bucket:   bucket,
		Key:      key,
		UploadID: uploadID,
	})
}

func (ro *Router) handleUploadPart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	q := r.URL.Query()
	uploadID := q.Get("uploadId")
	if uploadID == "" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument", "uploadId is required.")
		return
	}
	partNumberStr := q.Get("partNumber")
	partNumber, err := strconv.Atoi(partNumberStr)
	if err != nil || partNumber < 1 || partNumber > 10000 {
		writeError(
			w,
			r,
			http.StatusBadRequest,
			"InvalidArgument",
			"partNumber must be an integer between 1 and 10000.",
		)
		return
	}
	etag, err := ro.storage.UploadPart(uploadID, partNumber, r.Body)
	if err != nil {
		switch {
		case errors.Is(err, ErrUploadNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"upload not found",
				"uploadId",
				uploadID,
			)
			writeError(
				w,
				r,
				http.StatusNotFound,
				"NoSuchUpload",
				"The specified upload does not exist.",
			)
		default:
			slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"failed to upload part",
				"bucket",
				bucket,
				"key",
				key,
				"uploadId",
				uploadID,
				"partNumber",
				partNumber,
				"err",
				err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
		"part uploaded",
		"bucket",
		bucket,
		"key",
		key,
		"uploadId",
		uploadID,
		"partNumber",
		partNumber,
	)
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleCompleteMultipartUpload(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	uploadID := r.URL.Query().Get("uploadId")
	if uploadID == "" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument", "uploadId is required.")
		return
	}
	var req completeMultipartUploadRequest
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(
			w,
			r,
			http.StatusBadRequest,
			"MalformedXML",
			"The XML you provided was not well-formed.",
		)
		return
	}
	parts := make([]CompletePart, len(req.Parts))
	for i, p := range req.Parts {
		parts[i] = CompletePart(p)
	}
	meta, err := ro.storage.CompleteMultipartUpload(uploadID, parts)
	if err != nil {
		switch {
		case errors.Is(err, ErrUploadNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"upload not found",
				"uploadId",
				uploadID,
			)
			writeError(
				w,
				r,
				http.StatusNotFound,
				"NoSuchUpload",
				"The specified upload does not exist.",
			)
		case errors.Is(err, ErrInvalidPart):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"invalid part",
				"uploadId",
				uploadID,
			)
			writeError(
				w,
				r,
				http.StatusBadRequest,
				"InvalidPart",
				"One or more of the specified parts could not be found.",
			)
		case errors.Is(err, ErrInvalidPartOrder):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"parts not in order",
				"uploadId",
				uploadID,
			)
			writeError(
				w,
				r,
				http.StatusBadRequest,
				"InvalidPartOrder",
				"The list of parts was not in ascending order.",
			)
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(
				w,
				r,
				http.StatusNotFound,
				"NoSuchBucket",
				"The specified bucket does not exist.",
			)
		default:
			slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"failed to complete multipart upload",
				"bucket",
				bucket,
				"key",
				key,
				"uploadId",
				uploadID,
				"err",
				err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
		"multipart upload completed",
		"bucket",
		bucket,
		"key",
		key,
		"uploadId",
		uploadID,
	)
	writeXML(w, http.StatusOK, completeMultipartUploadResult{
		Location: "/" + bucket + "/" + key,
		Bucket:   bucket,
		Key:      key,
		ETag:     meta.ETag,
	})
}

func (ro *Router) handleAbortMultipartUpload(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	uploadID := r.URL.Query().Get("uploadId")
	if uploadID == "" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument", "uploadId is required.")
		return
	}
	if err := ro.storage.AbortMultipartUpload(uploadID); err != nil {
		switch {
		case errors.Is(err, ErrUploadNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"upload not found",
				"uploadId",
				uploadID,
			)
			writeError(
				w,
				r,
				http.StatusNotFound,
				"NoSuchUpload",
				"The specified upload does not exist.",
			)
		default:
			slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"failed to abort multipart upload",
				"bucket",
				bucket,
				"key",
				key,
				"uploadId",
				uploadID,
				"err",
				err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
		"multipart upload aborted",
		"bucket",
		bucket,
		"key",
		key,
		"uploadId",
		uploadID,
	)
	w.WriteHeader(http.StatusNoContent)
}

func (ro *Router) handleCopyObject(
	w http.ResponseWriter,
	r *http.Request,
	dstBucket, dstKey string,
) {
	copySource, err := url.PathUnescape(r.Header.Get("x-amz-copy-source"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument", "x-amz-copy-source is invalid.")
		return
	}
	srcBucket, srcKey := parsePath(copySource)
	if srcBucket == "" || srcKey == "" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"x-amz-copy-source must be in the form /<bucket>/<key>.")
		return
	}
	meta, err := ro.storage.CopyObject(srcBucket, srcKey, dstBucket, dstKey)
	if err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"bucket not found",
				"srcBucket",
				srcBucket,
				"dstBucket",
				dstBucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		case errors.Is(err, ErrObjectNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"source object not found",
				"srcBucket",
				srcBucket,
				"srcKey",
				srcKey,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchKey",
				"The specified key does not exist.")
		default:
			slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"failed to copy object",
				"srcBucket",
				srcBucket,
				"srcKey",
				srcKey,
				"dstBucket",
				dstBucket,
				"dstKey",
				dstKey,
				"err",
				err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
		"object copied",
		"srcBucket",
		srcBucket,
		"srcKey",
		srcKey,
		"dstBucket",
		dstBucket,
		"dstKey",
		dstKey,
	)
	writeXML(w, http.StatusOK, copyObjectResult{
		ETag:         meta.ETag,
		LastModified: meta.LastModified,
	})
}

func (ro *Router) handlePutObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	meta, err := ro.storage.PutObject(bucket, key, r.Body, contentType)
	if err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"failed to put object",
			"bucket",
			bucket,
			"key",
			key,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
		"object created",
		"bucket",
		bucket,
		"key",
		key,
	)
	w.Header().Set("ETag", meta.ETag)
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleHeadObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	meta, err := ro.storage.HeadObject(bucket, key)
	if err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound), errors.Is(err, ErrObjectNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"object not found",
				"bucket",
				bucket,
				"key",
				key,
			)
			w.WriteHeader(http.StatusNotFound)
		default:
			slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"failed to head object",
				"bucket",
				bucket,
				"key",
				key,
				"err",
				err,
			)
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", meta.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	w.Header().Set("ETag", meta.ETag)
	w.Header().Set("Last-Modified", meta.LastModified.UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleDeleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	err := ro.storage.DeleteObject(bucket, key)
	switch {
	case err == nil:
		slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"object deleted",
			"bucket",
			bucket,
			"key",
			key,
		)
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrObjectNotFound):
		// S3 returns 204 regardless of whether the object existed.
		slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"delete skipped: object not found",
			"bucket",
			bucket,
			"key",
			key,
		)
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrBucketNotFound):
		slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"bucket not found",
			"bucket",
			bucket,
		)
		writeError(w, r, http.StatusNotFound, "NoSuchBucket",
			"The specified bucket does not exist.")
	default:
		slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"failed to delete object",
			"bucket",
			bucket,
			"key",
			key,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
	}
}

func (ro *Router) handleGetObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	f, meta, err := ro.storage.GetObject(bucket, key)
	if err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		case errors.Is(err, ErrObjectNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"object not found",
				"bucket",
				bucket,
				"key",
				key,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchKey",
				"The specified key does not exist.")
		default:
			slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"failed to get object",
				"bucket",
				bucket,
				"key",
				key,
				"err",
				err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	defer func() { _ = f.Close() }()
	slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
		"serving object",
		"bucket",
		bucket,
		"key",
		key,
	)
	w.Header().Set("Content-Type", meta.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	w.Header().Set("ETag", meta.ETag)
	w.Header().Set("Last-Modified", meta.LastModified.UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"failed to stream object body",
			"bucket",
			bucket,
			"key",
			key,
			"err",
			err,
		)
	}
}

func (ro *Router) handleGetBucketLocation(w http.ResponseWriter, r *http.Request, bucket string) {
	region, err := ro.storage.GetBucketRegion(bucket)
	if err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		slog.Error( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"failed to get bucket region",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"get bucket location",
		"bucket",
		bucket,
	)
	writeXML(w, http.StatusOK, locationConstraint{Location: region})
}

func (ro *Router) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := ro.storage.ListBuckets()
	if err != nil {
		slog.Error("failed to list buckets", "err", err)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Debug("listed buckets", "count", len(buckets))
	xmlBuckets := make([]xmlBucket, len(buckets))
	for i, b := range buckets {
		xmlBuckets[i] = xmlBucket{Name: b.Name, CreationDate: b.CreationDate.UTC()}
	}
	writeXML(w, http.StatusOK, listBucketsResult{
		Owner:   xmlOwner{ID: "owner", DisplayName: "owner"},
		Buckets: xmlBuckets,
	})
}

func (ro *Router) handleCreateBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	region := ParseSigV4(r).Region
	if err := ro.storage.CreateBucket(bucket, region); err != nil {
		if errors.Is(err, os.ErrExist) {
			slog.Debug( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
				"bucket already exists",
				"bucket",
				bucket,
			)
			writeError(
				w,
				r,
				http.StatusConflict,
				"BucketAlreadyOwnedByYou",
				"Your previous request to create the named bucket succeeded and you already own it.",
			)
			return
		}
		slog.Error( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
			"failed to create bucket",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
		"bucket created",
		"bucket",
		bucket,
	)
	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := ro.storage.DeleteBucket(bucket); err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		case errors.Is(err, ErrBucketNotEmpty):
			slog.Debug( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
				"bucket not empty",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusConflict, "BucketNotEmpty",
				"The bucket you tried to delete is not empty.")
		default:
			slog.Error( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
				"failed to delete bucket",
				"bucket",
				bucket,
				"err",
				err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	slog.Info( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
		"bucket deleted",
		"bucket",
		bucket,
	)
	w.WriteHeader(http.StatusNoContent)
}

func (ro *Router) handleDeleteObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	if !ro.storage.BucketExists(bucket) {
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"bucket not found",
			"bucket",
			bucket,
		)
		writeError(w, r, http.StatusNotFound, "NoSuchBucket",
			"The specified bucket does not exist.")
		return
	}
	var req deleteObjectsRequest
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	result := deleteObjectsResult{}
	for _, obj := range req.Objects {
		if err := ro.storage.DeleteObject(bucket, obj.Key); err != nil &&
			!errors.Is(err, ErrObjectNotFound) {
			slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"failed to delete object",
				"bucket",
				bucket,
				"key",
				obj.Key,
				"err",
				err,
			)
			result.Errors = append(result.Errors, xmlDeleteError{
				Key:     obj.Key,
				Code:    "InternalError",
				Message: err.Error(),
			})
			continue
		}
		if !req.Quiet {
			result.Deleted = append(result.Deleted, xmlDeletedObject(obj))
		}
	}
	slog.Info( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"objects deleted",
		"bucket",
		bucket,
		"deleted",
		len(result.Deleted),
		"errors",
		len(result.Errors),
	)
	writeXML(w, http.StatusOK, result)
}

func (ro *Router) handleHeadBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	w.Header().Set("Content-Length", "0")
	if !ro.storage.BucketExists(bucket) {
		slog.Debug( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
			"bucket not found",
			"bucket",
			bucket,
		)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	slog.Debug( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
		"bucket found",
		"bucket",
		bucket,
	)
	w.WriteHeader(http.StatusOK)
}

// parsePath splits a path-style S3 URL into bucket and key:
// "/my-bucket/path/to/object" → ("my-bucket", "path/to/object")
func parsePath(path string) (bucket, key string) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", ""
	}
	parts := strings.SplitN(path, "/", 2)
	bucket = parts[0]
	if len(parts) == 2 {
		key = parts[1]
	}
	return bucket, key
}

// XML response types for S3 operations.

type listBucketsResult struct {
	XMLName xml.Name    `xml:"ListAllMyBucketsResult"`
	Owner   xmlOwner    `xml:"Owner"`
	Buckets []xmlBucket `xml:"Buckets>Bucket"`
}

type xmlOwner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type xmlBucket struct {
	Name         string    `xml:"Name"`
	CreationDate time.Time `xml:"CreationDate"`
}

type listObjectsResult struct {
	XMLName        xml.Name           `xml:"ListBucketResult"`
	Name           string             `xml:"Name"`
	Prefix         string             `xml:"Prefix"`
	Marker         string             `xml:"Marker"`
	NextMarker     string             `xml:"NextMarker,omitempty"`
	MaxKeys        int                `xml:"MaxKeys"`
	IsTruncated    bool               `xml:"IsTruncated"`
	Contents       []xmlObjectContent `xml:"Contents"`
	CommonPrefixes []xmlCommonPrefix  `xml:"CommonPrefixes"`
}

type xmlCommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

type listObjectsV2Result struct {
	XMLName     xml.Name           `xml:"ListBucketResult"`
	Name        string             `xml:"Name"`
	Prefix      string             `xml:"Prefix"`
	KeyCount    int                `xml:"KeyCount"`
	MaxKeys     int                `xml:"MaxKeys"`
	IsTruncated bool               `xml:"IsTruncated"`
	Contents    []xmlObjectContent `xml:"Contents"`
}

type xmlObjectContent struct {
	Key          string    `xml:"Key"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
}

// locationConstraint represents the GetBucketLocation response.
// An empty Location means us-east-1 per the S3 specification.
type locationConstraint struct {
	XMLName  xml.Name `xml:"LocationConstraint"`
	Location string   `xml:",chardata"`
}

type copyObjectResult struct {
	XMLName      xml.Name  `xml:"CopyObjectResult"`
	ETag         string    `xml:"ETag"`
	LastModified time.Time `xml:"LastModified"`
}

type initiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

type completeMultipartUploadRequest struct {
	XMLName xml.Name          `xml:"CompleteMultipartUpload"`
	Parts   []xmlCompletePart `xml:"Part"`
}

type xmlCompletePart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type completeMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

type deleteObjectsRequest struct {
	XMLName xml.Name          `xml:"Delete"`
	Quiet   bool              `xml:"Quiet"`
	Objects []xmlDeleteObject `xml:"Object"`
}

type xmlDeleteObject struct {
	Key string `xml:"Key"`
}

type deleteObjectsResult struct {
	XMLName xml.Name           `xml:"DeleteResult"`
	Deleted []xmlDeletedObject `xml:"Deleted"`
	Errors  []xmlDeleteError   `xml:"Error"`
}

type xmlDeletedObject struct {
	Key string `xml:"Key"`
}

type xmlDeleteError struct {
	Key     string `xml:"Key"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}
