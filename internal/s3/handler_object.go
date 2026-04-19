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
	"unicode/utf8"
)

// extractUserMetadata collects all x-amz-meta-* headers from h and returns
// them as a map keyed by the suffix after the prefix (lowercased). Returns nil
// if no such headers are present.
func extractUserMetadata(h http.Header) map[string]string {
	var m map[string]string
	for k, vs := range h {
		if strings.HasPrefix(k, amzMetaPrefix) {
			if m == nil {
				m = make(map[string]string)
			}
			m[strings.ToLower(k[len(amzMetaPrefix):])] = vs[0]
		}
	}
	return m
}

func (ro *Router) handleCopyObject(
	w http.ResponseWriter,
	r *http.Request,
	dstBucket, dstKey string,
) {
	rawCopySource, err := url.PathUnescape(r.Header.Get(amzCopySource))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument", "x-amz-copy-source is invalid.")
		return
	}
	// Copy source may include a ?versionId=<id> query string.
	var srcVersionID string
	copySource := rawCopySource
	if idx := strings.IndexByte(rawCopySource, '?'); idx != -1 {
		copySource = rawCopySource[:idx]
		if q, qErr := url.ParseQuery(rawCopySource[idx+1:]); qErr == nil {
			srcVersionID = q.Get("versionId")
		}
	}
	srcBucket, srcKey := parsePath(copySource)
	if srcBucket == "" || srcKey == "" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"x-amz-copy-source must be in the form /<bucket>/<key>.")
		return
	}
	// REPLACE directive: use Content-Type and x-amz-meta-* from the request.
	// COPY directive (default): pass empty/nil so CopyObject inherits from source.
	var (
		contentType  string
		userMetadata map[string]string
	)
	if strings.ToUpper(r.Header.Get("x-amz-metadata-directive")) == "REPLACE" {
		contentType = r.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		// Use empty non-nil map when no x-amz-meta-* headers are present so that
		// CopyObject can distinguish REPLACE-with-no-metadata from COPY (nil).
		userMetadata = extractUserMetadata(r.Header)
		if userMetadata == nil {
			userMetadata = map[string]string{}
		}
	}
	sseAlgorithm := r.Header.Get(amzSSE)
	sseKMSKeyID := r.Header.Get(amzSSEKMSKeyID)
	meta, err := ro.storage.CopyObject(
		srcBucket,
		srcKey,
		srcVersionID,
		dstBucket,
		dstKey,
		contentType,
		userMetadata,
		sseAlgorithm,
		sseKMSKeyID,
	)
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
	if meta.VersionID != "" {
		w.Header().Set(amzVersionID, meta.VersionID)
	}
	setSSEHeaders(w, meta)
	writeXML(w, http.StatusOK, copyObjectResult{
		ETag:         meta.ETag,
		LastModified: meta.LastModified,
	})
}

// setSSEHeaders writes SSE response headers derived from object metadata.
func setSSEHeaders(w http.ResponseWriter, meta ObjectMetadata) {
	if meta.SSEAlgorithm != "" {
		w.Header().Set(amzSSE, meta.SSEAlgorithm)
	}
	if meta.SSEKMSKeyID != "" {
		w.Header().Set(amzSSEKMSKeyID, meta.SSEKMSKeyID)
	}
}

func (ro *Router) handlePutObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	userMetadata := extractUserMetadata(r.Header)
	sseAlgorithm := r.Header.Get(amzSSE)
	sseKMSKeyID := r.Header.Get(amzSSEKMSKeyID)
	meta, err := ro.storage.PutObject(
		bucket,
		key,
		r.Body,
		contentType,
		userMetadata,
		sseAlgorithm,
		sseKMSKeyID,
	)
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
	if meta.VersionID != "" {
		w.Header().Set(amzVersionID, meta.VersionID)
	}
	setSSEHeaders(w, meta)
	w.WriteHeader(http.StatusOK)
}

// checkConditionals evaluates conditional request headers per RFC 7232.
// Response headers on w (ETag, Content-Type, etc.) must be set before calling,
// so that any 304/412 short-circuit response includes them.
// Returns 0 to proceed normally, or the written status code (304/412) to short-circuit.
func checkConditionals(
	w http.ResponseWriter,
	r *http.Request,
	etag string,
	lastModified time.Time,
) int {
	modtime := lastModified.Truncate(time.Second)

	// If-Match: 412 if no listed ETag matches.
	if im := r.Header.Get("If-Match"); im != "" {
		if !etagListContains(im, etag) {
			w.WriteHeader(http.StatusPreconditionFailed)
			return http.StatusPreconditionFailed
		}
	} else if ius := r.Header.Get("If-Unmodified-Since"); ius != "" {
		// If-Unmodified-Since (only when If-Match absent): 412 if modified after.
		if t, err := http.ParseTime(ius); err == nil && modtime.After(t) {
			w.WriteHeader(http.StatusPreconditionFailed)
			return http.StatusPreconditionFailed
		}
	}

	// If-None-Match: 304 if any listed ETag matches.
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if etagListContains(inm, etag) {
			w.WriteHeader(http.StatusNotModified)
			return http.StatusNotModified
		}
	} else if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		// If-Modified-Since (only when If-None-Match absent): 304 if not modified.
		if t, err := http.ParseTime(ims); err == nil && !modtime.After(t) {
			w.WriteHeader(http.StatusNotModified)
			return http.StatusNotModified
		}
	}

	return 0
}

// etagListContains reports whether etag appears in a comma-separated ETag list header value.
// A wildcard "*" matches any etag.
func etagListContains(headerVal, etag string) bool {
	if strings.TrimSpace(headerVal) == "*" {
		return true
	}
	for _, s := range strings.Split(headerVal, ",") {
		if strings.TrimSpace(s) == etag {
			return true
		}
	}
	return false
}

// isRangeSatisfiable reports whether at least one range in a "bytes=..." Range
// header is satisfiable for an object of the given size.
func isRangeSatisfiable(rangeHeader string, size int64) bool {
	const prefix = "bytes="
	if !strings.HasPrefix(rangeHeader, prefix) {
		return true // non-bytes range: let ServeContent handle
	}
	for _, spec := range strings.Split(rangeHeader[len(prefix):], ",") {
		spec = strings.TrimSpace(spec)
		dash := strings.IndexByte(spec, '-')
		if dash < 0 {
			return true // malformed: let ServeContent handle
		}
		if dash == 0 {
			return true // suffix range (e.g. bytes=-500): always satisfiable
		}
		start, err := strconv.ParseInt(spec[:dash], 10, 64)
		if err != nil || start < 0 {
			return true // malformed: let ServeContent handle
		}
		if start < size {
			return true
		}
	}
	return false
}

func (ro *Router) handleHeadObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	var meta ObjectMetadata
	var err error
	if versionID := r.URL.Query().Get("versionId"); versionID != "" {
		meta, err = ro.storage.HeadObjectVersion(bucket, key, versionID)
	} else {
		meta, err = ro.storage.HeadObject(bucket, key)
	}
	if err != nil {
		var dme *DeleteMarkerError
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
		case errors.As(err, &dme):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"object is a delete marker",
				"bucket",
				bucket,
				"key",
				key,
			)
			w.Header().Set(amzDeleteMarker, "true")
			if dme.VersionID != "" {
				w.Header().Set(amzVersionID, dme.VersionID)
			}
			if r.URL.Query().Get("versionId") != "" {
				w.WriteHeader(http.StatusMethodNotAllowed)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
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
	w.Header().Set("Accept-Ranges", "bytes")
	if meta.VersionID != "" {
		w.Header().Set(amzVersionID, meta.VersionID)
	}
	for k, v := range meta.UserMetadata {
		w.Header().Set(amzMetaPrefix+k, v)
	}
	setSSEHeaders(w, meta)
	// tagging count is best-effort; errors are intentionally ignored so that a
	// missing or unreadable tags file never prevents a successful object response.
	if tags, err := ro.storage.GetObjectTagging(bucket, key); err == nil && len(tags) > 0 {
		w.Header().Set(amzTaggingCount, strconv.Itoa(len(tags)))
	}
	if status := checkConditionals(w, r, meta.ETag, meta.LastModified); status != 0 {
		slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"conditional check short-circuited",
			"bucket",
			bucket,
			"key",
			key,
			"status",
			status,
		)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleDeleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if versionID := r.URL.Query().Get("versionId"); versionID != "" {
		// Permanently delete a specific version.
		isMarker, err := ro.storage.DeleteObjectVersion(bucket, key, versionID)
		switch {
		case err == nil:
			slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"object version deleted",
				"bucket",
				bucket,
				"key",
				key,
				"versionId",
				versionID,
			)
			w.Header().Set(amzVersionID, versionID)
			if isMarker {
				w.Header().Set(amzDeleteMarker, "true")
			}
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, ErrObjectNotFound):
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		default:
			slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"failed to delete object version",
				"bucket",
				bucket,
				"key",
				key,
				"versionId",
				versionID,
				"err",
				err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}

	// Versioning-aware delete (may create a delete marker).
	versionID, isMarker, err := ro.storage.DeleteObjectVersioned(bucket, key)
	switch {
	case err == nil:
		slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"object deleted",
			"bucket",
			bucket,
			"key",
			key,
		)
		if versionID != "" {
			w.Header().Set(amzVersionID, versionID)
		}
		if isMarker {
			w.Header().Set(amzDeleteMarker, "true")
		}
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrBucketNotFound):
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
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
	var f *os.File
	var meta ObjectMetadata
	var err error
	if versionID := r.URL.Query().Get("versionId"); versionID != "" {
		f, meta, err = ro.storage.GetObjectVersion(bucket, key, versionID)
	} else {
		f, meta, err = ro.storage.GetObject(bucket, key)
	}
	if err != nil {
		var dme *DeleteMarkerError
		switch {
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		case errors.As(err, &dme):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"object is a delete marker",
				"bucket",
				bucket,
				"key",
				key,
			)
			w.Header().Set(amzDeleteMarker, "true")
			if dme.VersionID != "" {
				w.Header().Set(amzVersionID, dme.VersionID)
			}
			if r.URL.Query().Get("versionId") != "" {
				writeError(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed",
					"The specified method is not allowed against this resource.")
			} else {
				writeError(w, r, http.StatusNotFound, "NoSuchKey",
					"The specified key does not exist.")
			}
		case errors.Is(err, ErrObjectNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"object not found",
				"bucket",
				bucket,
				"key",
				key,
			)
			if r.URL.Query().Get("versionId") != "" {
				writeError(w, r, http.StatusNotFound, "NoSuchVersion",
					"The specified version does not exist.")
			} else {
				writeError(w, r, http.StatusNotFound, "NoSuchKey",
					"The specified key does not exist.")
			}
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
	// Pre-check If-Match / If-Unmodified-Since before setting response headers.
	// http.ServeContent returns an empty 412; AWS S3 requires an XML error body.
	if im := r.Header.Get("If-Match"); im != "" {
		if !etagListContains(im, meta.ETag) {
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"precondition failed: If-Match",
				"bucket",
				bucket,
				"key",
				key,
			)
			writeError(w, r, http.StatusPreconditionFailed, "PreconditionFailed",
				"At least one of the pre-conditions you specified did not hold")
			return
		}
	} else if ius := r.Header.Get("If-Unmodified-Since"); ius != "" {
		if t, err := http.ParseTime(ius); err == nil &&
			meta.LastModified.Truncate(time.Second).After(t) {
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"precondition failed: If-Unmodified-Since",
				"bucket",
				bucket,
				"key",
				key,
			)
			writeError(w, r, http.StatusPreconditionFailed, "PreconditionFailed",
				"At least one of the pre-conditions you specified did not hold")
			return
		}
	}

	// Pre-check Range satisfiability.
	// http.ServeContent returns a text/plain 416 without Content-Range;
	// AWS S3 requires an XML body and Content-Range: bytes */size.
	if rng := r.Header.Get("Range"); rng != "" && !isRangeSatisfiable(rng, meta.Size) {
		slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"range not satisfiable",
			"bucket",
			bucket,
			"key",
			key,
		)
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(meta.Size, 10))
		writeError(w, r, http.StatusRequestedRangeNotSatisfiable, "InvalidRange",
			"The requested range is not satisfiable")
		return
	}

	slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
		"serving object",
		"bucket",
		bucket,
		"key",
		key,
	)
	// Set custom headers before ServeContent so that:
	//   - Content-Type is preserved (ServeContent skips sniffing when already set)
	//   - ETag is available for If-Match / If-None-Match evaluation
	w.Header().Set("Content-Type", meta.ContentType)
	w.Header().Set("ETag", meta.ETag)
	if meta.VersionID != "" {
		w.Header().Set(amzVersionID, meta.VersionID)
	}
	for k, v := range meta.UserMetadata {
		w.Header().Set(amzMetaPrefix+k, v)
	}
	setSSEHeaders(w, meta)
	// tagging count is best-effort; errors are intentionally ignored so that a
	// missing or unreadable tags file never prevents a successful object response.
	if tags, err := ro.storage.GetObjectTagging(bucket, key); err == nil && len(tags) > 0 {
		w.Header().Set(amzTaggingCount, strconv.Itoa(len(tags)))
	}
	http.ServeContent(w, r, "", meta.LastModified, f)
}

func (ro *Router) handlePutObjectTagging(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	var req xmlTagging
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"malformed tagging XML",
			"bucket",
			bucket,
			"key",
			key,
		)
		writeError(
			w,
			r,
			http.StatusBadRequest,
			"MalformedXML",
			"The XML you provided was not well-formed.",
		)
		return
	}
	if len(req.TagSet) > 10 {
		slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"too many tags",
			"bucket",
			bucket,
			"key",
			key,
			"count",
			len(req.TagSet),
		)
		writeError(w, r, http.StatusBadRequest, "InvalidTag",
			"Object tags cannot be greater than 10")
		return
	}
	seen := make(map[string]struct{}, len(req.TagSet))
	for _, t := range req.TagSet {
		if utf8.RuneCountInString(t.Key) > 128 {
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"tag key too long",
				"bucket",
				bucket,
				"key",
				key,
			)
			writeError(w, r, http.StatusBadRequest, "InvalidTag",
				"The TagKey you have provided is invalid")
			return
		}
		if utf8.RuneCountInString(t.Value) > 256 {
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"tag value too long",
				"bucket",
				bucket,
				"key",
				key,
			)
			writeError(w, r, http.StatusBadRequest, "InvalidTag",
				"The TagValue you have provided is invalid")
			return
		}
		if _, dup := seen[t.Key]; dup {
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"duplicate tag key",
				"bucket",
				bucket,
				"key",
				key,
			)
			writeError(w, r, http.StatusBadRequest, "InvalidTag",
				"Cannot provide multiple Tags with the same key")
			return
		}
		seen[t.Key] = struct{}{}
	}
	tags := make([]Tag, len(req.TagSet))
	for i, t := range req.TagSet {
		tags[i] = Tag(t)
	}
	if err := ro.storage.PutObjectTagging(bucket, key, tags); err != nil {
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
				"failed to put object tagging",
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
		"object tagging updated",
		"bucket",
		bucket,
		"key",
		key,
	)
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleGetObjectTagging(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	tags, err := ro.storage.GetObjectTagging(bucket, key)
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
				"failed to get object tagging",
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
	slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
		"get object tagging",
		"bucket",
		bucket,
		"key",
		key,
	)
	xmlTags := make([]xmlTag, len(tags))
	for i, t := range tags {
		xmlTags[i] = xmlTag(t)
	}
	writeXML(w, http.StatusOK, xmlTagging{TagSet: xmlTags})
}

// handleRestoreObject handles RestoreObject (#95).
// Returns 202 Accepted on first restore request, 200 OK if restore was already initiated.
func (ro *Router) handleRestoreObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	_, _ = io.Copy(io.Discard, r.Body)
	if !ro.storage.BucketExists(bucket) {
		slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"bucket not found",
			"bucket",
			bucket,
		)
		writeError(w, r, http.StatusNotFound, "NoSuchBucket",
			"The specified bucket does not exist.")
		return
	}
	meta, err := ro.storage.HeadObject(bucket, key)
	if err != nil {
		if errors.Is(err, ErrObjectNotFound) {
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"object not found",
				"bucket",
				bucket,
				"key",
				key,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchKey",
				"The specified key does not exist.")
			return
		}
		slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"failed to head object for restore",
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
	if meta.RestoreInitiated {
		slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"restore already initiated",
			"bucket",
			bucket,
			"key",
			key,
		)
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := ro.storage.SetObjectRestoreInitiated(bucket, key); err != nil {
		slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"failed to set restore initiated",
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
		"restore object accepted",
		"bucket",
		bucket,
		"key",
		key,
	)
	w.WriteHeader(http.StatusAccepted)
}

func (ro *Router) handleDeleteObjectTagging(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	if err := ro.storage.DeleteObjectTagging(bucket, key); err != nil {
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
				"failed to delete object tagging",
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
		"object tagging deleted",
		"bucket",
		bucket,
		"key",
		key,
	)
	w.WriteHeader(http.StatusNoContent)
}
