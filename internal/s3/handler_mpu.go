package s3

import (
	"encoding/xml"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
		if qs, qErr := url.ParseQuery(rawCopySource[idx+1:]); qErr == nil {
			srcVersionID = qs.Get("versionId")
		}
	}
	srcBucket, srcKey := parsePath(copySource)
	if srcBucket == "" || srcKey == "" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"x-amz-copy-source must be in the form /<bucket>/<key>.")
		return
	}

	var byteRange *ByteRange
	if rangeHdr := r.Header.Get("x-amz-copy-source-range"); rangeHdr != "" {
		br, parseErr := parseCopySourceRange(rangeHdr)
		if parseErr != nil {
			writeError(w, r, http.StatusBadRequest, "InvalidArgument",
				"x-amz-copy-source-range value must be of the form bytes=first-last where first and last are byte offsets.")
			return
		}
		byteRange = br
	}

	etag, lastModified, err := ro.storage.UploadPartCopy(
		uploadID, partNumber, srcBucket, srcKey, srcVersionID, byteRange,
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrUploadNotFound):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"upload not found",
				"uploadId",
				uploadID,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchUpload",
				"The specified upload does not exist.")
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
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
		"part copy uploaded",
		"bucket",
		bucket,
		"key",
		key,
		"uploadId",
		uploadID,
		"partNumber",
		partNumber,
	)
	writeXML(w, http.StatusOK, copyPartResult{
		ETag:         etag,
		LastModified: lastModified,
	})
}

// parseCopySourceRange parses a "bytes=first-last" range header value.
func parseCopySourceRange(s string) (*ByteRange, error) {
	const prefix = "bytes="
	if !strings.HasPrefix(s, prefix) {
		return nil, errors.New("invalid range")
	}
	parts := strings.SplitN(s[len(prefix):], "-", 2)
	if len(parts) != 2 {
		return nil, errors.New("invalid range")
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 {
		return nil, errors.New("invalid range start")
	}
	end, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || end < start {
		return nil, errors.New("invalid range end")
	}
	return &ByteRange{Start: start, End: end}, nil
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
