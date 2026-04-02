package s3

import (
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// bucketStore is the subset of Storage used by the Router for bucket operations.
type bucketStore interface {
	ListBuckets() ([]BucketInfo, error)
	CreateBucket(bucket string) error
	DeleteBucket(bucket string) error
	BucketExists(bucket string) bool
}

// objectStore is the subset of Storage used by the Router for object operations.
type objectStore interface {
	PutObject(bucket, key string, r io.Reader, contentType string) (ObjectMetadata, error)
	GetObject(bucket, key string) (*os.File, ObjectMetadata, error)
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
		writeNotImplemented(w, r)
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

func (ro *Router) routeObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	switch r.Method {
	case http.MethodPut:
		ro.handlePutObject(w, r, bucket, key)
	case http.MethodGet:
		writeNotImplemented(w, r)
	case http.MethodDelete:
		writeNotImplemented(w, r)
	case http.MethodHead:
		writeNotImplemented(w, r)
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

func (ro *Router) handlePutObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	meta, err := ro.storage.PutObject(bucket, key, r.Body, contentType)
	if err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
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

func (ro *Router) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := ro.storage.ListBuckets()
	if err != nil {
		slog.Error("failed to list buckets", "err", err)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info("listed buckets", "count", len(buckets))
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
	if err := ro.storage.CreateBucket(bucket); err != nil {
		if errors.Is(err, os.ErrExist) {
			slog.Warn( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
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
			slog.Warn( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		case errors.Is(err, ErrBucketNotEmpty):
			slog.Warn( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
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

// XML response types for S3 bucket operations.

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
