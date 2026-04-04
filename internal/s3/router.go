package s3

import (
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
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

// Router handles S3 API requests using path-style URLs: /<bucket>/<key>
type Router struct {
	storage interface {
		bucketStore
		objectStore
	}
}

func NewRouter(storage *Storage) *Router {
	return &Router{storage: storage}
}

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

func (ro *Router) handleListObjectsV2(w http.ResponseWriter, r *http.Request, bucket string) {
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
	slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"listed objects",
		"bucket",
		bucket,
		"count",
		len(objects),
	)
	contents := make([]xmlObjectContent, len(objects))
	for i, obj := range objects {
		contents[i] = xmlObjectContent{
			Key:          obj.Key,
			LastModified: obj.Metadata.LastModified.UTC(),
			ETag:         obj.Metadata.ETag,
			Size:         obj.Metadata.Size,
			StorageClass: "STANDARD",
		}
	}
	writeXML(w, http.StatusOK, listObjectsV2Result{
		Name:        bucket,
		KeyCount:    len(objects),
		MaxKeys:     1000,
		IsTruncated: false,
		Contents:    contents,
	})
}

func (ro *Router) routeObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	switch r.Method {
	case http.MethodPut:
		if r.Header.Get("x-amz-copy-source") != "" {
			ro.handleCopyObject(w, r, bucket, key)
		} else {
			ro.handlePutObject(w, r, bucket, key)
		}
	case http.MethodGet:
		ro.handleGetObject(w, r, bucket, key)
	case http.MethodDelete:
		ro.handleDeleteObject(w, r, bucket, key)
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
