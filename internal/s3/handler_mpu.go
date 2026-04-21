package s3

import (
	"encoding/xml"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (ro *Router) handleCreateMultipartUpload(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	sseAlgorithm := r.Header.Get(amzSSE)
	sseKMSKeyID := r.Header.Get(amzSSEKMSKeyID)
	uploadID, err := ro.storage.CreateMultipartUpload(
		bucket,
		key,
		contentType,
		sseAlgorithm,
		sseKMSKeyID,
	)
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
	setSSEHeaders(w, ObjectMetadata{SSEAlgorithm: sseAlgorithm, SSEKMSKeyID: sseKMSKeyID})
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

func (ro *Router) handleUploadPartCopy(w http.ResponseWriter, r *http.Request, bucket, key string) {
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

	rawCopySource, err := url.PathUnescape(r.Header.Get(amzCopySource))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument", "x-amz-copy-source is invalid.")
		return
	}
	var srcVersionID string
	copySource := rawCopySource
	if idx := strings.IndexByte(rawCopySource, '?'); idx != -1 {
		copySource = rawCopySource[:idx]
		qs, qErr := url.ParseQuery(rawCopySource[idx+1:])
		if qErr != nil {
			writeError(
				w,
				r,
				http.StatusBadRequest,
				"InvalidArgument",
				"x-amz-copy-source query string is invalid.",
			)
			return
		}
		srcVersionID = qs.Get("versionId")
	}
	srcBucket, srcKey := parsePath(copySource)
	if srcBucket == "" || srcKey == "" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"x-amz-copy-source must be in the form bucket/key.")
		return
	}

	var br *byteRange
	if rangeHdr := r.Header.Get("x-amz-copy-source-range"); rangeHdr != "" {
		var parseErr error
		br, parseErr = parseCopySourceRange(rangeHdr)
		if parseErr != nil {
			writeError(
				w,
				r,
				http.StatusBadRequest,
				"InvalidArgument",
				"x-amz-copy-source-range value must be of the form bytes=first-last where first and last are byte offsets.",
			)
			return
		}
	}

	// Evaluate x-amz-copy-source-if-* preconditions against source object metadata.
	if hasCopySourceConditions(r) {
		var srcMeta ObjectMetadata
		var headErr error
		if srcVersionID != "" {
			srcMeta, headErr = ro.storage.HeadObjectVersion(srcBucket, srcKey, srcVersionID)
		} else {
			srcMeta, headErr = ro.storage.HeadObject(srcBucket, srcKey)
		}
		if headErr != nil && !errors.Is(headErr, ErrObjectNotFound) {
			slog.Error( // #nosec G706 -- srcBucket/srcKey come from header; log injection risk accepted for a local dev emulator
				"failed to head source object for precondition check",
				"srcBucket",
				srcBucket,
				"srcKey",
				srcKey,
				"err",
				headErr,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError",
				"We encountered an internal error. Please try again.")
			return
		}
		if headErr == nil && !checkCopySourceConditions(r, srcMeta) {
			slog.Debug( // #nosec G706 -- srcBucket/srcKey come from header; log injection risk accepted for a local dev emulator
				"copy source precondition failed",
				"srcBucket",
				srcBucket,
				"srcKey",
				srcKey,
			)
			writeError(w, r, http.StatusPreconditionFailed, "PreconditionFailed",
				"At least one of the pre-conditions you specified did not hold")
			return
		}
		// headErr is ErrObjectNotFound: let UploadPartCopy return the canonical NoSuchKey error.
	}

	etag, lastModified, copySourceVersionID, err := ro.storage.UploadPartCopy(
		uploadID, partNumber, srcBucket, srcKey, srcVersionID, br,
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrUploadNotFound):
			slog.Debug( // #nosec G706 -- uploadId comes from URL query; log injection risk accepted for a local dev emulator
				"upload not found",
				"uploadId",
				uploadID,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchUpload",
				"The specified upload does not exist.")
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug( // #nosec G706 -- srcBucket comes from header; log injection risk accepted for a local dev emulator
				"source bucket not found",
				"srcBucket",
				srcBucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		case errors.Is(err, ErrObjectNotFound):
			slog.Debug( // #nosec G706 -- srcBucket/srcKey come from header; log injection risk accepted for a local dev emulator
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
				"failed to upload part copy",
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
			writeError(w, r, http.StatusInternalServerError, "InternalError",
				"We encountered an internal error. Please try again.")
		}
		return
	}
	slog.Info( // #nosec G706 -- bucket/key/srcBucket/srcKey come from URL path/header; log injection risk accepted for a local dev emulator
		"part copy uploaded",
		"bucket",
		bucket,
		"key",
		key,
		"uploadId",
		uploadID,
		"partNumber",
		partNumber,
		"srcBucket",
		srcBucket,
		"srcKey",
		srcKey,
	)
	if copySourceVersionID != "" {
		w.Header().Set(amzCopySourceVersionID, copySourceVersionID)
	}
	writeXML(w, http.StatusOK, copyPartResult{
		ETag:         etag,
		LastModified: lastModified,
	})
}

// hasCopySourceConditions reports whether any x-amz-copy-source-if-* header is present.
func hasCopySourceConditions(r *http.Request) bool {
	return r.Header.Get(amzCopySourceIfMatch) != "" ||
		r.Header.Get(amzCopySourceIfNoneMatch) != "" ||
		r.Header.Get(amzCopySourceIfModifiedSince) != "" ||
		r.Header.Get(amzCopySourceIfUnmodifiedSince) != ""
}

// checkCopySourceConditions evaluates x-amz-copy-source-if-* headers against
// the given source object metadata. Returns false if any condition fails (412).
// Precedence: if-match takes precedence over if-unmodified-since;
// if-none-match takes precedence over if-modified-since.
func checkCopySourceConditions(r *http.Request, meta ObjectMetadata) bool {
	if im := r.Header.Get(amzCopySourceIfMatch); im != "" {
		if !etagListContains(im, meta.ETag) {
			return false
		}
	} else if ius := r.Header.Get(amzCopySourceIfUnmodifiedSince); ius != "" {
		if t, parseErr := http.ParseTime(ius); parseErr == nil &&
			meta.LastModified.Truncate(time.Second).After(t) {
			return false
		}
	}
	if inm := r.Header.Get(amzCopySourceIfNoneMatch); inm != "" {
		if etagListContains(inm, meta.ETag) {
			return false
		}
	} else if ims := r.Header.Get(amzCopySourceIfModifiedSince); ims != "" {
		if t, parseErr := http.ParseTime(ims); parseErr == nil &&
			!meta.LastModified.Truncate(time.Second).After(t) {
			return false
		}
	}
	return true
}

// parseCopySourceRange parses a "bytes=first-last" range header value.
func parseCopySourceRange(s string) (*byteRange, error) {
	const prefix = "bytes="
	if !strings.HasPrefix(s, prefix) {
		return nil, errors.New("invalid range")
	}
	parts := strings.SplitN(s[len(prefix):], "-", 2)
	if len(parts) != 2 {
		return nil, errors.New("invalid range")
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil ||
		start < 0 { // start < 0 is untestable: a leading '-' in the value causes SplitN to produce an empty parts[0], so ParseInt always errors first
		return nil, errors.New("invalid range start")
	}
	end, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || end < start {
		return nil, errors.New("invalid range end")
	}
	return &byteRange{Start: start, End: end}, nil
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
	setSSEHeaders(w, meta)
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

func (ro *Router) handleListParts(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	uploadID := r.URL.Query().Get("uploadId")
	if uploadID == "" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument", "uploadId is required.")
		return
	}
	umeta, parts, err := ro.storage.ListParts(uploadID)
	if err != nil {
		if errors.Is(err, ErrUploadNotFound) {
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"upload not found",
				"uploadId",
				uploadID,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchUpload",
				"The specified upload does not exist.")
			return
		}
		slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"failed to list parts",
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
		return
	}
	slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
		"listed parts",
		"bucket",
		bucket,
		"key",
		key,
		"uploadId",
		uploadID,
		"count",
		len(parts),
	)
	xmlParts := make([]xmlPart, len(parts))
	for i, p := range parts {
		xmlParts[i] = xmlPart{
			PartNumber:   p.PartNumber,
			ETag:         p.ETag,
			Size:         p.Size,
			LastModified: p.LastModified.UTC(),
		}
	}
	writeXML(w, http.StatusOK, listPartsResult{
		Bucket:       umeta.Bucket,
		Key:          umeta.Key,
		UploadID:     uploadID,
		StorageClass: "STANDARD",
		MaxParts:     1000,
		IsTruncated:  false,
		Parts:        xmlParts,
	})
}
