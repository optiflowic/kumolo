package s3

import (
	"bytes"
	"crypto/md5" //nolint:gosec // MD5 used for data-integrity checking per S3 spec, not cryptographic security
	"encoding/xml"
	"errors"
	"hash"
	"io"
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
	sseAlgorithm, sseKMSKeyID, ssecKeyMD5, sseBucketKeyEnabled, ok := ro.resolveSSEWithSSEC(
		w,
		r,
		bucket,
	)
	if !ok {
		return
	}
	retention, legalHold, ok := parseObjectLockHeaders(w, r)
	if !ok {
		return
	}
	storageClass := r.Header.Get(amzStorageClass)
	uploadID, err := ro.storage.CreateMultipartUpload(
		bucket,
		key,
		contentType,
		sseAlgorithm,
		sseKMSKeyID,
		sseBucketKeyEnabled,
		ssecKeyMD5,
		retention,
		legalHold,
		storageClass,
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
	setSSEHeaders(
		w,
		ObjectMetadata{
			SSEAlgorithm:        sseAlgorithm,
			SSEKMSKeyID:         sseKMSKeyID,
			SSEBucketKeyEnabled: sseBucketKeyEnabled,
			SSECKeyMD5:          ssecKeyMD5,
		},
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
	if err != nil || partNumber < 1 || partNumber > maxPartNumber {
		writeError(
			w,
			r,
			http.StatusBadRequest,
			"InvalidArgument",
			"partNumber must be an integer between 1 and 10000.",
		)
		return
	}
	ssecKeyMD5, ok := parseSSECHeaders(w, r, amzSSECAlgorithm, amzSSECKey, amzSSECKeyMD5)
	if !ok {
		return
	}
	umeta, err := ro.storage.GetUploadMeta(uploadID)
	if err != nil {
		if errors.Is(err, ErrUploadNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchUpload",
				"The specified upload does not exist.")
		} else {
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	if !validateSSECOnRead(w, r, ObjectMetadata{SSECKeyMD5: umeta.SSECKeyMD5}, ssecKeyMD5) {
		return
	}
	expected, ok := parseContentMD5Header(w, r)
	if !ok {
		return
	}
	checksumH, checksumExpected, ok := parseChecksumHeaders(w, r)
	if !ok {
		return
	}
	var body io.Reader = r.Body
	var md5Hash hash.Hash
	if expected != nil {
		md5Hash = md5.New() //nolint:gosec // MD5 used for data-integrity checking per S3 spec
		body = io.TeeReader(body, md5Hash)
	}
	if checksumH != nil {
		body = io.TeeReader(body, checksumH)
	}
	etag, err := ro.storage.UploadPart(uploadID, partNumber, body)
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
	if md5Hash != nil && !bytes.Equal(md5Hash.Sum(nil), expected) {
		if err := ro.storage.DeletePart(uploadID, partNumber); err != nil {
			slog.Warn( // #nosec G706 -- uploadId comes from URL query; log injection risk accepted for a local dev emulator
				"failed to roll back part after Content-MD5 mismatch",
				"uploadId",
				uploadID,
				"partNumber",
				partNumber,
				"err",
				err,
			)
		}
		writeError(w, r, http.StatusBadRequest, "BadDigest",
			"The Content-MD5 you specified did not match what we received.")
		return
	}
	if checksumH != nil && !bytes.Equal(checksumH.Sum(nil), checksumExpected) {
		if err := ro.storage.DeletePart(uploadID, partNumber); err != nil {
			slog.Warn( // #nosec G706 -- uploadId comes from URL query; log injection risk accepted for a local dev emulator
				"failed to roll back part after checksum mismatch",
				"uploadId",
				uploadID,
				"partNumber",
				partNumber,
				"err",
				err,
			)
		}
		writeError(w, r, http.StatusBadRequest, "BadDigest",
			"The checksum you specified did not match what we received.")
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
	if err != nil || partNumber < 1 || partNumber > maxPartNumber {
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
	if rangeHdr := r.Header.Get(amzCopySourceRange); rangeHdr != "" {
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

	srcSSECKeyMD5, ok := parseSSECHeaders(
		w,
		r,
		amzCopySourceSSECAlgorithm,
		amzCopySourceSSECKey,
		amzCopySourceSSECKeyMD5,
	)
	if !ok {
		return
	}

	// Head the source to validate SSE-C key and/or evaluate copy-source-if-* conditions.
	// Always performed so SSE-C objects require the key even when no conditions are set.
	// ErrObjectNotFound is left for UploadPartCopy to report as NoSuchKey.
	{
		var srcMeta ObjectMetadata
		var headErr error
		if srcVersionID != "" {
			srcMeta, headErr = ro.storage.HeadObjectVersion(srcBucket, srcKey, srcVersionID)
		} else {
			srcMeta, headErr = ro.storage.HeadObject(srcBucket, srcKey)
		}
		if headErr != nil && !errors.Is(headErr, ErrObjectNotFound) {
			slog.Error( // #nosec G706 -- srcBucket/srcKey come from header; log injection risk accepted for a local dev emulator
				"failed to head source object",
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
		if headErr == nil {
			if !validateSSECOnRead(w, r, srcMeta, srcSSECKeyMD5) {
				return
			}
			if hasCopySourceConditions(r) && !checkCopySourceConditions(r, srcMeta) {
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
		}
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
	// A negative start is unreachable: a leading '-' causes SplitN to produce an
	// empty parts[0], so ParseInt always errors before start < 0 could be true.
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
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
		case errors.Is(err, ErrEntityTooSmall):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"part too small",
				"uploadId",
				uploadID,
			)
			writeError(
				w,
				r,
				http.StatusBadRequest,
				"EntityTooSmall",
				"Your proposed upload is smaller than the minimum allowed size.",
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
	q := r.URL.Query()
	uploadID := q.Get("uploadId")
	if uploadID == "" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument", "uploadId is required.")
		return
	}
	maxParts := 1000
	if s := q.Get("max-parts"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			if n > 1000 {
				n = 1000
			}
			maxParts = n
		}
	}
	partNumberMarker := 0
	if s := q.Get("part-number-marker"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			partNumberMarker = n
		}
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

	var xmlParts []xmlPart
	var isTruncated bool
	var nextPartNumberMarker int

	for _, p := range parts {
		if p.PartNumber <= partNumberMarker {
			continue
		}
		if len(xmlParts) >= maxParts {
			isTruncated = true
			break
		}
		xmlParts = append(xmlParts, xmlPart{
			PartNumber:   p.PartNumber,
			ETag:         p.ETag,
			Size:         p.Size,
			LastModified: p.LastModified.UTC(),
		})
		nextPartNumberMarker = p.PartNumber
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
		len(xmlParts),
	)

	result := listPartsResult{
		Bucket:           umeta.Bucket,
		Key:              umeta.Key,
		UploadID:         uploadID,
		StorageClass:     "STANDARD",
		PartNumberMarker: partNumberMarker,
		MaxParts:         maxParts,
		IsTruncated:      isTruncated,
		Parts:            xmlParts,
	}
	if isTruncated {
		result.NextPartNumberMarker = nextPartNumberMarker
	}
	writeXML(w, http.StatusOK, result)
}
