package s3

import (
	"bytes"
	"crypto/md5" //nolint:gosec // MD5 used for data-integrity checking per S3 spec, not cryptographic security
	"encoding/base64"
	"encoding/xml"
	"errors"
	"hash"
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

// parseSSECHeaders validates an SSE-C header triple (algorithm, key, key-MD5) and
// returns the base64-encoded key MD5. Returns ("", true) when all three headers are
// absent (not an SSE-C request). Writes a 400 and returns ("", false) on any error.
func parseSSECHeaders(
	w http.ResponseWriter,
	r *http.Request,
	algHeader, keyHeader, keyMD5Header string,
) (keyMD5 string, ok bool) {
	alg := r.Header.Get(algHeader)
	key := r.Header.Get(keyHeader)
	md5val := r.Header.Get(keyMD5Header)
	if alg == "" && key == "" && md5val == "" {
		return "", true
	}
	if alg == "" || key == "" || md5val == "" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"All three SSE-C headers (algorithm, key, and key MD5) must be provided together.")
		return "", false
	}
	if alg != "AES256" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"The encryption algorithm specified is not supported.")
		return "", false
	}
	keyBytes, decErr := base64.StdEncoding.DecodeString(key)
	if decErr != nil || len(keyBytes) != 32 {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"The secret key was invalid for the specified algorithm.")
		return "", false
	}
	h := md5.New() //nolint:gosec // MD5 required by S3 spec for SSE-C key validation
	_, _ = h.Write(keyBytes)
	if base64.StdEncoding.EncodeToString(h.Sum(nil)) != md5val {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"The calculated MD5 hash of the key did not match the hash that was provided.")
		return "", false
	}
	return md5val, true
}

// resolveSSEWithSSEC resolves SSE settings for write operations.
// If SSE-C headers are present, returns (ssecKeyMD5, "", "", false, true).
// If X-Amz-Server-Side-Encryption is present, delegates to resolveSSE.
// Returns an error if both SSE-C and SSE-S3/KMS headers coexist.
func (ro *Router) resolveSSEWithSSEC(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) (sseAlg, sseKMSKeyID, ssecKeyMD5 string, sseBucketKeyEnabled, ok bool) {
	ssecKeyMD5, ok = parseSSECHeaders(w, r, amzSSECAlgorithm, amzSSECKey, amzSSECKeyMD5)
	if !ok {
		return
	}
	if ssecKeyMD5 != "" {
		if r.Header.Get(amzSSE) != "" {
			writeError(w, r, http.StatusBadRequest, "InvalidArgument",
				"Server-side encryption headers are mutually exclusive.")
			ok = false
			return
		}
		ok = true
		return
	}
	sseAlg, sseKMSKeyID, sseBucketKeyEnabled, ok = ro.resolveSSE(w, r, bucket)
	return
}

// validateSSECOnRead checks SSE-C key consistency for read operations (GetObject, HeadObject).
// requestKeyMD5 is empty when no SSE-C headers were provided.
// Returns false after writing an error when access must be denied.
func validateSSECOnRead(
	w http.ResponseWriter,
	r *http.Request,
	meta ObjectMetadata,
	requestKeyMD5 string,
) bool {
	if meta.SSECKeyMD5 != "" && requestKeyMD5 == "" {
		writeError(
			w,
			r,
			http.StatusBadRequest,
			"InvalidRequest",
			"The object was stored using a form of Server Side Encryption. The correct parameters must be provided to retrieve the object.",
		)
		return false
	}
	if requestKeyMD5 != "" && requestKeyMD5 != meta.SSECKeyMD5 {
		writeError(w, r, http.StatusForbidden, "AccessDenied", "Access Denied")
		return false
	}
	return true
}

// resolveSSEAlgorithm returns the SSE algorithm header value; empty (absent) is valid, unknown values write 400 and return false.
func resolveSSEAlgorithm(w http.ResponseWriter, r *http.Request) (string, bool) {
	alg := r.Header.Get(amzSSE)
	switch alg {
	case "", "AES256", "aws:kms", "aws:kms:dsse":
		return alg, true
	default:
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"The encryption method specified is not supported.")
		return "", false
	}
}

// resolveSSE returns the SSE algorithm, KMS key ID, and bucket-key-enabled flag.
// Explicit request headers take priority; when the X-Amz-Server-Side-Encryption
// header is absent the bucket's stored default encryption config is applied.
// For aws:kms / aws:kms:dsse, the key ID is resolved to a canonical ARN via the
// KMS service (if wired). Returns ok=false (after writing an error) on failure.
func (ro *Router) resolveSSE(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) (alg, keyID string, bucketKeyEnabled, ok bool) {
	if r.Header.Get(amzSSE) != "" {
		alg, ok = resolveSSEAlgorithm(w, r)
		if !ok {
			return
		}
		keyID = r.Header.Get(amzSSEKMSKeyID)
		bucketKeyEnabled, ok = parseBucketKeyEnabled(w, r, alg)
		if !ok {
			return
		}
		if isKMSAlgorithm(alg) && ro.kms != nil {
			keyID, ok = ro.resolveKMSKey(w, r, keyID)
		}
		return
	}

	xmlBody, err := ro.storage.GetBucketEncryption(bucket)
	if err != nil || xmlBody == "" {
		return "", "", false, true
	}

	var conf xmlSSEConfiguration
	xmlErr := xml.Unmarshal( //nolint:gosec // G709: data from kumolo internal storage, not user input
		[]byte(xmlBody),
		&conf,
	)
	if xmlErr != nil || len(conf.Rules) == 0 {
		return "", "", false, true
	}

	rule := conf.Rules[0]
	alg = rule.Apply.SSEAlgorithm
	keyID = rule.Apply.KMSMasterKeyID
	bucketKeyEnabled = rule.BucketKeyEnabled && isKMSAlgorithm(alg)
	if isKMSAlgorithm(alg) && ro.kms != nil {
		keyID, ok = ro.resolveKMSKey(w, r, keyID)
		return
	}
	ok = true
	return
}

// resolveKMSKey calls the KMS service to resolve keyRef to a canonical ARN.
// On failure it writes the appropriate S3 KMS error and returns ok=false.
func (ro *Router) resolveKMSKey(
	w http.ResponseWriter,
	r *http.Request,
	keyRef string,
) (string, bool) {
	arn, err := ro.kms.ResolveKeyForEncryption(keyRef)
	if err != nil {
		writeKMSError(w, r, err)
		return "", false
	}
	return arn, true
}

// writeKMSError maps a KMS service error to the appropriate S3 KMS error response.
func writeKMSError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrKMSKeyNotFound):
		writeError(w, r, http.StatusBadRequest, "KMS.NotFoundException",
			"The specified KMS key does not exist.")
	case errors.Is(err, ErrKMSKeyDisabled):
		writeError(w, r, http.StatusBadRequest, "KMS.DisabledException",
			"The specified KMS key is disabled.")
	case errors.Is(err, ErrKMSKeyPendingDeletion):
		writeError(w, r, http.StatusBadRequest, "KMS.InvalidStateException",
			"The specified KMS key is pending deletion.")
	default:
		slog.Error("S3 SSE-KMS: KMS service error", "err", err)
		writeError(w, r, http.StatusInternalServerError, "KMS.KMSInternalException",
			"An internal error occurred in the KMS service.")
	}
}

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
	if strings.ToUpper(r.Header.Get(amzMetadataDirective)) == "REPLACE" {
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
	sseAlgorithm, sseKMSKeyID, dstSSECKeyMD5, sseBucketKeyEnabled, ok := ro.resolveSSEWithSSEC(
		w,
		r,
		dstBucket,
	)
	if !ok {
		return
	}
	// Validate source SSE-C: head the source to check its stored key MD5.
	// Only short-circuit on validation failure; not-found errors are left to CopyObject.
	{
		var srcMeta ObjectMetadata
		var headErr error
		if srcVersionID != "" {
			srcMeta, headErr = ro.storage.HeadObjectVersion(srcBucket, srcKey, srcVersionID)
		} else {
			srcMeta, headErr = ro.storage.HeadObject(srcBucket, srcKey)
		}
		if headErr == nil && !validateSSECOnRead(w, r, srcMeta, srcSSECKeyMD5) {
			return
		}
	}
	retention, legalHold, ok := parseObjectLockHeaders(w, r)
	if !ok {
		return
	}
	storageClass := r.Header.Get(amzStorageClass)
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
		sseBucketKeyEnabled,
		dstSSECKeyMD5,
		retention,
		legalHold,
		storageClass,
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
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
	ro.replicateObject(dstBucket, dstKey, meta)
}

// setSSEHeaders writes SSE response headers derived from object metadata.
func setSSEHeaders(w http.ResponseWriter, meta ObjectMetadata) {
	if meta.SSECKeyMD5 != "" {
		w.Header().Set(amzSSECAlgorithm, "AES256")
		w.Header().Set(amzSSECKeyMD5, meta.SSECKeyMD5)
		return
	}
	if meta.SSEAlgorithm != "" {
		w.Header().Set(amzSSE, meta.SSEAlgorithm)
	}
	if meta.SSEKMSKeyID != "" {
		w.Header().Set(amzSSEKMSKeyID, meta.SSEKMSKeyID)
	}
	if meta.SSEBucketKeyEnabled && isKMSAlgorithm(meta.SSEAlgorithm) {
		w.Header().Set(amzSSEBucketKeyEnabled, "true")
	}
}

// parseBucketKeyEnabled reads X-Amz-Server-Side-Encryption-Bucket-Key-Enabled.
// Only meaningful for aws:kms / aws:kms:dsse; other algorithms ignore the header.
// Returns (value, ok); ok is false when the header contains an invalid value and a
// 400 response has already been written.
func parseBucketKeyEnabled(
	w http.ResponseWriter,
	r *http.Request,
	sseAlgorithm string,
) (bool, bool) {
	if !isKMSAlgorithm(sseAlgorithm) {
		return false, true
	}
	switch r.Header.Get(amzSSEBucketKeyEnabled) {
	case "", "false":
		return false, true
	case "true":
		return true, true
	default:
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			`X-Amz-Server-Side-Encryption-Bucket-Key-Enabled must be "true" or "false".`)
		return false, false
	}
}

func isKMSAlgorithm(alg string) bool {
	return alg == "aws:kms" || alg == "aws:kms:dsse"
}

func (ro *Router) handlePutObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if cannedACL := r.Header.Get(amzACL); cannedACL != "" {
		if _, err := buildCannedACL(cannedACL); err != nil {
			writeError(w, r, http.StatusBadRequest, "InvalidArgument",
				"The canned ACL you provided is not valid.")
			return
		}
	}
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	userMetadata := extractUserMetadata(r.Header)
	sseAlgorithm, sseKMSKeyID, ssecKeyMD5, sseBucketKeyEnabled, ok := ro.resolveSSEWithSSEC(
		w,
		r,
		bucket,
	)
	if !ok {
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
	retention, legalHold, ok := parseObjectLockHeaders(w, r)
	if !ok {
		return
	}
	storageClass := r.Header.Get(amzStorageClass)
	if isAnonymousRequest(r) {
		bucketACL, err := ro.storage.GetBucketACL(bucket)
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
				"failed to get bucket ACL",
				"bucket",
				bucket,
				"err",
				err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if !aclAllowsAnonymous(bucketACL, aclPermWrite) {
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"put object denied: ACL",
				"bucket",
				bucket,
				"key",
				key,
			)
			writeError(w, r, http.StatusForbidden, "AccessDenied", "Access Denied")
			return
		}
	}
	putFn := ro.storage.PutObject
	if r.Header.Get("If-None-Match") == "*" {
		putFn = ro.storage.PutObjectIfNotExists
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
	meta, err := putFn(
		bucket,
		key,
		body,
		contentType,
		userMetadata,
		sseAlgorithm,
		sseKMSKeyID,
		sseBucketKeyEnabled,
		ssecKeyMD5,
		retention,
		legalHold,
		storageClass,
	)
	if err != nil {
		var oae *ObjectAlreadyExistsError
		if errors.As(err, &oae) {
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"precondition failed: object already exists",
				"bucket",
				bucket,
				"key",
				key,
			)
			if oae.ETag != "" {
				w.Header().Set("ETag", oae.ETag)
			}
			writeError(w, r, http.StatusPreconditionFailed, "PreconditionFailed",
				"At least one of the pre-conditions you specified did not hold.")
			return
		}
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
	if md5Hash != nil && !bytes.Equal(md5Hash.Sum(nil), expected) {
		if meta.VersionID != "" {
			if _, err := ro.storage.DeleteObjectVersion(bucket, key, meta.VersionID, false); err != nil {
				slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
					"failed to roll back object version after Content-MD5 mismatch",
					"bucket",
					bucket,
					"key",
					key,
					"versionID",
					meta.VersionID,
					"err",
					err,
				)
			}
		} else {
			if err := ro.storage.DeleteObject(bucket, key, false); err != nil {
				slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
					"failed to roll back object after Content-MD5 mismatch",
					"bucket",
					bucket,
					"key",
					key,
					"err",
					err,
				)
			}
		}
		writeError(w, r, http.StatusBadRequest, "BadDigest",
			"The Content-MD5 you specified did not match what we received.")
		return
	}
	if checksumH != nil && !bytes.Equal(checksumH.Sum(nil), checksumExpected) {
		// meta.VersionID is non-empty iff versioning is enabled on the bucket;
		// storage.PutObject always sets it when versioning is active.
		if meta.VersionID != "" {
			if _, err := ro.storage.DeleteObjectVersion(bucket, key, meta.VersionID, false); err != nil {
				slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
					"failed to roll back object version after checksum mismatch",
					"bucket",
					bucket,
					"key",
					key,
					"versionID",
					meta.VersionID,
					"err",
					err,
				)
			}
		} else {
			if err := ro.storage.DeleteObject(bucket, key, false); err != nil {
				slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
					"failed to roll back object after checksum mismatch",
					"bucket",
					bucket,
					"key",
					key,
					"err",
					err,
				)
			}
		}
		writeError(w, r, http.StatusBadRequest, "BadDigest",
			"The checksum you specified did not match what we received.")
		return
	}
	if cannedACL := r.Header.Get(amzACL); cannedACL != "" {
		if aclXML, aclErr := buildCannedACL(cannedACL); aclErr == nil {
			if storeErr := ro.storage.PutObjectACL(bucket, key, aclXML); storeErr != nil {
				if meta.VersionID != "" {
					if _, err := ro.storage.DeleteObjectVersion(bucket, key, meta.VersionID, false); err != nil {
						slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
							"failed to roll back object version after ACL persistence failure",
							"bucket",
							bucket,
							"key",
							key,
							"versionID",
							meta.VersionID,
							"err",
							err,
						)
					}
				} else {
					if err := ro.storage.DeleteObject(bucket, key, false); err != nil {
						slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
							"failed to roll back object after ACL persistence failure",
							"bucket",
							bucket,
							"key",
							key,
							"err",
							err,
						)
					}
				}
				writeError(w, r, http.StatusInternalServerError, "InternalError", storeErr.Error())
				return
			}
		}
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
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
	ro.replicateObject(bucket, key, meta)
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
	if isAnonymousRequest(r) && !aclAllowsAnonymous(meta.ACL, aclPermRead) {
		slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"head object denied: ACL",
			"bucket",
			bucket,
			"key",
			key,
		)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	ssecKeyMD5, ok := parseSSECHeaders(w, r, amzSSECAlgorithm, amzSSECKey, amzSSECKeyMD5)
	if !ok {
		return
	}
	if !validateSSECOnRead(w, r, meta, ssecKeyMD5) {
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
	if meta.StorageClass != "" {
		w.Header().Set(amzStorageClass, meta.StorageClass)
	}
	if meta.ReplicationStatus != "" {
		w.Header().Set(amzReplicationStatus, meta.ReplicationStatus)
	}
	// tagging count is best-effort; errors are intentionally ignored so that a
	// missing or unreadable tags file never prevents a successful object response.
	if tags, err := ro.storage.GetObjectTagging(bucket, key); err == nil && len(tags) > 0 {
		w.Header().Set(amzTaggingCount, strconv.Itoa(len(tags)))
	}
	if meta.Retention != nil {
		w.Header().Set(amzObjectLockMode, meta.Retention.Mode)
		w.Header().
			Set(amzObjectLockRetainUntilDate, meta.Retention.RetainUntilDate.UTC().Format(time.RFC3339))
	}
	if meta.LegalHold != nil {
		w.Header().Set(amzObjectLockLegalHold, meta.LegalHold.Status)
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
	if isAnonymousRequest(r) {
		bucketACL, err := ro.storage.GetBucketACL(bucket)
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
				"failed to get bucket ACL",
				"bucket",
				bucket,
				"err",
				err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if !aclAllowsAnonymous(bucketACL, aclPermWrite) {
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"delete object denied: ACL",
				"bucket",
				bucket,
				"key",
				key,
			)
			writeError(w, r, http.StatusForbidden, "AccessDenied", "Access Denied")
			return
		}
	}
	bypassGovernance := r.Header.Get(amzBypassGovernanceRetention) == "true"

	if versionID := r.URL.Query().Get("versionId"); versionID != "" {
		// Permanently delete a specific version.
		isMarker, err := ro.storage.DeleteObjectVersion(bucket, key, versionID, bypassGovernance)
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
		case errors.Is(err, ErrObjectLocked):
			slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"delete rejected: object locked",
				"bucket",
				bucket,
				"key",
				key,
				"versionId",
				versionID,
			)
			writeError(w, r, http.StatusForbidden, "AccessDenied",
				"Access Denied because the object is protected by Object Lock.")
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
	versionID, isMarker, err := ro.storage.DeleteObjectVersioned(bucket, key, bypassGovernance)
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
	case errors.Is(err, ErrObjectLocked):
		slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"delete rejected: object locked",
			"bucket",
			bucket,
			"key",
			key,
		)
		writeError(w, r, http.StatusForbidden, "AccessDenied",
			"Access Denied because the object is protected by Object Lock.")
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
	if isAnonymousRequest(r) && !aclAllowsAnonymous(meta.ACL, aclPermRead) {
		slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"get object denied: ACL",
			"bucket",
			bucket,
			"key",
			key,
		)
		writeError(w, r, http.StatusForbidden, "AccessDenied", "Access Denied")
		return
	}
	ssecKeyMD5, ok := parseSSECHeaders(w, r, amzSSECAlgorithm, amzSSECKey, amzSSECKeyMD5)
	if !ok {
		return
	}
	if !validateSSECOnRead(w, r, meta, ssecKeyMD5) {
		return
	}
	if isArchiveStorageClass(meta.StorageClass) && !meta.RestoreInitiated {
		slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"object not restored",
			"bucket",
			bucket,
			"key",
			key,
		)
		writeError(w, r, http.StatusForbidden, "InvalidObjectState",
			"The operation is not valid for the object's storage class.")
		return
	}
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
	if meta.StorageClass != "" {
		w.Header().Set(amzStorageClass, meta.StorageClass)
	}
	if meta.ReplicationStatus != "" {
		w.Header().Set(amzReplicationStatus, meta.ReplicationStatus)
	}
	// tagging count is best-effort; errors are intentionally ignored so that a
	// missing or unreadable tags file never prevents a successful object response.
	if tags, err := ro.storage.GetObjectTagging(bucket, key); err == nil && len(tags) > 0 {
		w.Header().Set(amzTaggingCount, strconv.Itoa(len(tags)))
	}
	if meta.Retention != nil {
		w.Header().Set(amzObjectLockMode, meta.Retention.Mode)
		w.Header().
			Set(amzObjectLockRetainUntilDate, meta.Retention.RetainUntilDate.UTC().Format(time.RFC3339))
	}
	if meta.LegalHold != nil {
		w.Header().Set(amzObjectLockLegalHold, meta.LegalHold.Status)
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
	if !isArchiveStorageClass(meta.StorageClass) {
		slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"restore rejected: object is not in archive storage class",
			"bucket",
			bucket,
			"key",
			key,
			"storageClass",
			meta.StorageClass,
		)
		writeError(w, r, http.StatusConflict, "InvalidObjectState",
			"The operation is not valid for the object's storage class.")
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

// parseContentMD5Header decodes the Content-MD5 request header.
// Returns (nil, true) when absent, (expected, true) when valid,
// or (nil, false) after writing 400 InvalidDigest when malformed.
func parseContentMD5Header(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	encoded := r.Header.Get("Content-MD5")
	if encoded == "" {
		return nil, true
	}
	expected, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(expected) != md5.Size {
		writeError(w, r, http.StatusBadRequest, "InvalidDigest",
			"The Content-MD5 you specified was invalid.")
		return nil, false
	}
	return expected, true
}

// parseObjectLockHeaders reads x-amz-object-lock-* request headers and returns
// the parsed retention and legal hold values. Returns false and writes an error
// response if any header value is invalid. Either or both pointers may be nil
// when the corresponding header is absent.
func parseObjectLockHeaders(
	w http.ResponseWriter,
	r *http.Request,
) (retention *ObjectRetention, legalHold *ObjectLegalHold, ok bool) {
	modeStr := r.Header.Get(amzObjectLockMode)
	dateStr := r.Header.Get(amzObjectLockRetainUntilDate)
	holdStr := r.Header.Get(amzObjectLockLegalHold)

	if modeStr != "" || dateStr != "" {
		if modeStr == "" || dateStr == "" {
			writeError(
				w,
				r,
				http.StatusBadRequest,
				"InvalidArgument",
				"Both x-amz-object-lock-mode and x-amz-object-lock-retain-until-date must be supplied together.",
			)
			return nil, nil, false
		}
		if modeStr != "GOVERNANCE" && modeStr != "COMPLIANCE" {
			writeError(w, r, http.StatusBadRequest, "InvalidArgument",
				"x-amz-object-lock-mode must be GOVERNANCE or COMPLIANCE.")
			return nil, nil, false
		}
		retainUntil, err := time.Parse(time.RFC3339Nano, dateStr)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "InvalidArgument",
				"x-amz-object-lock-retain-until-date must be an RFC3339 timestamp.")
			return nil, nil, false
		}
		retention = &ObjectRetention{Mode: modeStr, RetainUntilDate: retainUntil}
	}

	if holdStr != "" {
		if holdStr != "ON" && holdStr != "OFF" {
			writeError(w, r, http.StatusBadRequest, "InvalidArgument",
				"x-amz-object-lock-legal-hold must be ON or OFF.")
			return nil, nil, false
		}
		legalHold = &ObjectLegalHold{Status: holdStr}
	}

	return retention, legalHold, true
}

func isArchiveStorageClass(sc string) bool {
	return sc == "GLACIER" || sc == "DEEP_ARCHIVE"
}

// validObjectAttributes is the set of attribute names accepted by GetObjectAttributes.
var validObjectAttributes = map[string]struct{}{
	"ETag": {}, "Checksum": {}, "ObjectParts": {}, "StorageClass": {}, "ObjectSize": {},
}

func (ro *Router) handleGetObjectAttributes(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	attrHeader := r.Header.Get(amzObjectAttributes)
	if attrHeader == "" {
		slog.Debug("get object attributes: missing x-amz-object-attributes header",
			"bucket", bucket, "key", key) // #nosec G706
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"x-amz-object-attributes header is required.")
		return
	}
	requested := map[string]struct{}{}
	for _, a := range strings.Split(attrHeader, ",") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if _, ok := validObjectAttributes[a]; !ok {
			slog.Debug("get object attributes: unknown attribute",
				"bucket", bucket, "key", key, "attr", a) // #nosec G706
			writeError(w, r, http.StatusBadRequest, "InvalidArgument",
				"Invalid attribute: "+a)
			return
		}
		requested[a] = struct{}{}
	}
	if len(requested) == 0 {
		slog.Debug("get object attributes: no valid attributes in header",
			"bucket", bucket, "key", key) // #nosec G706
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"x-amz-object-attributes header is required.")
		return
	}

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
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug("get object attributes: bucket not found",
				"bucket", bucket, "key", key) // #nosec G706
			writeError(w, r, http.StatusNotFound, "NoSuchKey",
				"The specified key does not exist.")
		case errors.Is(err, ErrObjectNotFound):
			slog.Debug("get object attributes: object not found",
				"bucket", bucket, "key", key) // #nosec G706
			writeError(w, r, http.StatusNotFound, "NoSuchKey",
				"The specified key does not exist.")
		case errors.As(err, &dme):
			slog.Debug("get object attributes: object is a delete marker",
				"bucket", bucket, "key", key) // #nosec G706
			w.Header().Set(amzDeleteMarker, "true")
			if dme.VersionID != "" {
				w.Header().Set(amzVersionID, dme.VersionID)
			}
			writeError(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed",
				"The specified method is not allowed against this resource.")
		default:
			slog.Error("get object attributes: storage error",
				"bucket", bucket, "key", key, "err", err) // #nosec G706
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	if isAnonymousRequest(r) && !aclAllowsAnonymous(meta.ACL, aclPermRead) {
		slog.Debug("get object attributes denied: ACL",
			"bucket", bucket, "key", key) // #nosec G706
		writeError(w, r, http.StatusForbidden, "AccessDenied", "Access Denied")
		return
	}

	resp := getObjectAttributesResponse{}
	if _, ok := requested["ETag"]; ok {
		resp.ETag = strings.Trim(meta.ETag, `"`)
	}
	if _, ok := requested["StorageClass"]; ok {
		resp.StorageClass = meta.StorageClass
		if resp.StorageClass == "" {
			resp.StorageClass = "STANDARD"
		}
	}
	if _, ok := requested["ObjectSize"]; ok {
		size := meta.Size
		resp.ObjectSize = &size
	}
	if _, ok := requested["ObjectParts"]; ok {
		if n := parseMultipartPartCount(meta.ETag); n > 0 {
			resp.ObjectParts = &xmlObjectParts{TotalPartsCount: n}
		}
	}

	w.Header().Set("Last-Modified", meta.LastModified.UTC().Format(http.TimeFormat))
	if meta.VersionID != "" {
		w.Header().Set(amzVersionID, meta.VersionID)
	}
	slog.Debug("get object attributes",
		"bucket", bucket, "key", key) // #nosec G706
	writeXML(w, http.StatusOK, resp)
}

// parseMultipartPartCount returns the part count encoded in a multipart ETag
// (e.g. `"abc123-5"` → 5). Returns 0 for single-part (non-multipart) ETags.
func parseMultipartPartCount(etag string) int {
	s := strings.Trim(etag, `"`)
	idx := strings.LastIndex(s, "-")
	if idx < 0 {
		return 0
	}
	n, err := strconv.Atoi(s[idx+1:])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
