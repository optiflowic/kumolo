package s3

import (
	"encoding/xml"
	"errors"
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

// Router handles S3 API requests using path-style URLs: /<bucket>/<key>
type Router struct {
	storage bucketStore
}

func NewRouter(storage *Storage) *Router {
	return &Router{storage: storage}
}

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket, key := parsePath(r.URL.Path)

	if bucket == "" {
		if r.Method == http.MethodGet {
			ro.handleListBuckets(w, r)
			return
		}
		writeError(
			w,
			r,
			http.StatusMethodNotAllowed,
			"MethodNotAllowed",
			"The specified method is not allowed.",
		)
		return
	}

	if key == "" {
		ro.routeBucket(w, r, bucket)
		return
	}

	ro.routeObject(w, r, bucket, key)
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

func (ro *Router) routeObject(w http.ResponseWriter, r *http.Request, _, _ string) {
	switch r.Method {
	case http.MethodPut:
		writeNotImplemented(w, r)
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

func (ro *Router) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := ro.storage.ListBuckets()
	if err != nil {
		slog.Error("ListBuckets failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info("ListBuckets", "count", len(buckets))
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
			slog.Warn("CreateBucket: already exists", "bucket", bucket)
			writeError(
				w,
				r,
				http.StatusConflict,
				"BucketAlreadyOwnedByYou",
				"Your previous request to create the named bucket succeeded and you already own it.",
			)
			return
		}
		slog.Error("CreateBucket failed", "bucket", bucket, "err", err)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info("CreateBucket", "bucket", bucket)
	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := ro.storage.DeleteBucket(bucket); err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			slog.Warn("DeleteBucket: not found", "bucket", bucket)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		case errors.Is(err, ErrBucketNotEmpty):
			slog.Warn("DeleteBucket: not empty", "bucket", bucket)
			writeError(w, r, http.StatusConflict, "BucketNotEmpty",
				"The bucket you tried to delete is not empty.")
		default:
			slog.Error("DeleteBucket failed", "bucket", bucket, "err", err)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	slog.Info("DeleteBucket", "bucket", bucket)
	w.WriteHeader(http.StatusNoContent)
}

func (ro *Router) handleHeadBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	w.Header().Set("Content-Length", "0")
	if !ro.storage.BucketExists(bucket) {
		slog.Debug("HeadBucket: not found", "bucket", bucket)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	slog.Debug("HeadBucket: found", "bucket", bucket)
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
