package s3

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	amzCopySource                  = "X-Amz-Copy-Source"
	amzCopySourceVersionID         = "X-Amz-Copy-Source-Version-Id"
	amzCopySourceIfMatch           = "X-Amz-Copy-Source-If-Match"
	amzCopySourceIfNoneMatch       = "X-Amz-Copy-Source-If-None-Match"
	amzCopySourceIfModifiedSince   = "X-Amz-Copy-Source-If-Modified-Since"
	amzCopySourceIfUnmodifiedSince = "X-Amz-Copy-Source-If-Unmodified-Since"
	amzMetaPrefix                  = "X-Amz-Meta-"
	amzTaggingCount                = "X-Amz-Tagging-Count"
	amzVersionID                   = "X-Amz-Version-Id"
	amzDeleteMarker                = "X-Amz-Delete-Marker"
	amzSSE                         = "X-Amz-Server-Side-Encryption"
	amzSSEKMSKeyID                 = "X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id"
	amzSSEBucketKeyEnabled         = "X-Amz-Server-Side-Encryption-Bucket-Key-Enabled"
	amzSSECAlgorithm               = "X-Amz-Server-Side-Encryption-Customer-Algorithm"
	amzSSECKey                     = "X-Amz-Server-Side-Encryption-Customer-Key" //nolint:gosec // HTTP header name, not a credential
	amzSSECKeyMD5                  = "X-Amz-Server-Side-Encryption-Customer-Key-Md5"
	amzCopySourceSSECAlgorithm     = "X-Amz-Copy-Source-Server-Side-Encryption-Customer-Algorithm"
	amzCopySourceSSECKey           = "X-Amz-Copy-Source-Server-Side-Encryption-Customer-Key" //nolint:gosec // HTTP header name, not a credential
	amzCopySourceSSECKeyMD5        = "X-Amz-Copy-Source-Server-Side-Encryption-Customer-Key-Md5"
	amzStorageClass                = "X-Amz-Storage-Class"
	amzMetadataDirective           = "X-Amz-Metadata-Directive"
	amzCopySourceRange             = "X-Amz-Copy-Source-Range"
	amzBypassGovernanceRetention   = "X-Amz-Bypass-Governance-Retention" // #nosec G101 -- HTTP header name, not a credential
	amzReplicationStatus           = "X-Amz-Replication-Status"
	amzBucketRegion                = "X-Amz-Bucket-Region"
	amzObjectLockEnabled           = "X-Amz-Object-Lock-Enabled"
	amzObjectLockMode              = "X-Amz-Object-Lock-Mode"
	amzObjectLockRetainUntilDate   = "X-Amz-Object-Lock-Retain-Until-Date"
	amzObjectLockLegalHold         = "X-Amz-Object-Lock-Legal-Hold"
	amzSdkChecksumAlgorithm        = "X-Amz-Sdk-Checksum-Algorithm"
	amzChecksumCRC32               = "X-Amz-Checksum-Crc32"
	amzChecksumCRC32C              = "X-Amz-Checksum-Crc32c"
	amzChecksumSHA1                = "X-Amz-Checksum-Sha1"
	amzChecksumSHA256              = "X-Amz-Checksum-Sha256"
	amzChecksumCRC64NVME           = "X-Amz-Checksum-Crc64nvme"
	amzObjectAttributes            = "X-Amz-Object-Attributes"

	// Presigned URL query parameter names.
	amzQSignature  = "X-Amz-Signature"
	amzQAlgorithm  = "X-Amz-Algorithm"
	amzQDate       = "X-Amz-Date"
	amzQExpires    = "X-Amz-Expires"
	amzQCredential = "X-Amz-Credential" // #nosec G101 -- presigned URL query parameter name, not a credential value

	presignedURLMaxExpiry = 7 * 24 * 60 * 60 // 604800 seconds; AWS S3 maximum
	maxPartNumber         = 10000            // AWS S3 maximum part number
	minPartSize           = 5 * 1024 * 1024  // 5 MiB; AWS S3 minimum non-final part size
)

// Router handles S3 API requests using path-style URLs: /<bucket>/<key>
type Router struct {
	storage interface {
		bucketStore
		objectStore
		multipartStore
		objectTaggingStore
		bucketTaggingStore
		bucketVersioningStore
		bucketCORSStore
		bucketPolicyStore
		bucketPublicAccessBlockStore
		bucketEncryptionStore
		bucketOwnershipControlsStore
		bucketNotificationStore
		bucketLifecycleStore
		bucketWebsiteStore
		bucketLoggingStore
		bucketAccelerateStore
		bucketReplicationStore
		bucketRequestPaymentStore
		bucketObjectLockStore
		objectRetentionStore
		objectLegalHoldStore
	}
	kms KMSService       // nil means SSE-KMS key validation is skipped
	now func() time.Time // injectable for testing; defaults to time.Now
}

// NewRouter creates a new S3 router. kms may be nil, in which case SSE-KMS
// key validation is skipped and key IDs are stored verbatim.
func NewRouter(storage *Storage, kms KMSService) *Router {
	return &Router{storage: storage, kms: kms, now: time.Now}
}

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := newResponseRecorder(w)
	start := ro.now()
	ro.serveHTTP(rec, r)
	ro.appendAccessLog(r, rec, start)
}

func (ro *Router) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has(amzQSignature) {
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

	if algo := q.Get(amzQAlgorithm); algo != "" && algo != "AWS4-HMAC-SHA256" {
		return http.StatusBadRequest,
			"AuthorizationQueryParametersError",
			`X-Amz-Algorithm only supports "AWS4-HMAC-SHA256".`
	}

	amzDate := q.Get(amzQDate)
	amzExpires := q.Get(amzQExpires)
	if amzDate == "" || amzExpires == "" {
		return 0, "", ""
	}

	expires, err := strconv.ParseInt(amzExpires, 10, 64)
	if err != nil || expires < 1 || expires > presignedURLMaxExpiry {
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
	q := r.URL.Query()
	// Inject CORS headers only for data-access operations, not for bucket management
	// sub-resources (which are typically called by server-side code, not browsers).
	isMgmtSubresource := slices.ContainsFunc([]string{
		"cors", "policy", "tagging", "versioning", "publicAccessBlock",
		"encryption", "ownershipControls", "notification", "lifecycle",
		"website", "logging", "accelerate", "replication", "requestPayment",
		"object-lock",
	}, q.Has)
	if r.Method != http.MethodOptions && !isMgmtSubresource {
		ro.injectCORSHeaders(w, r, bucket)
	}
	switch r.Method {
	case http.MethodOptions:
		ro.handleCORSPreflight(w, r, bucket)
		return
	case http.MethodPut:
		switch {
		case q.Has("cors"):
			ro.handlePutBucketCors(w, r, bucket)
		case q.Has("policy"):
			ro.handlePutBucketPolicy(w, r, bucket)
		case q.Has("tagging"):
			ro.handlePutBucketTagging(w, r, bucket)
		case q.Has("versioning"):
			ro.handlePutBucketVersioning(w, r, bucket)
		case q.Has("publicAccessBlock"):
			ro.handlePutPublicAccessBlock(w, r, bucket)
		case q.Has("encryption"):
			ro.handlePutBucketEncryption(w, r, bucket)
		case q.Has("ownershipControls"):
			ro.handlePutBucketOwnershipControls(w, r, bucket)
		case q.Has("notification"):
			ro.handlePutBucketNotification(w, r, bucket)
		case q.Has("lifecycle"):
			ro.handlePutBucketLifecycle(w, r, bucket)
		case q.Has("website"):
			ro.handlePutBucketWebsite(w, r, bucket)
		case q.Has("logging"):
			ro.handlePutBucketLogging(w, r, bucket)
		case q.Has("accelerate"):
			ro.handlePutBucketAccelerate(w, r, bucket)
		case q.Has("replication"):
			ro.handlePutBucketReplication(w, r, bucket)
		case q.Has("requestPayment"):
			ro.handlePutBucketRequestPayment(w, r, bucket)
		case q.Has("object-lock"):
			ro.handlePutObjectLockConfiguration(w, r, bucket)
		case q.Has("acl"):
			ro.handlePutBucketACL(w, r, bucket)
		default:
			ro.handleCreateBucket(w, r, bucket)
		}
	case http.MethodDelete:
		switch {
		case q.Has("cors"):
			ro.handleDeleteBucketCors(w, r, bucket)
		case q.Has("policy"):
			ro.handleDeleteBucketPolicy(w, r, bucket)
		case q.Has("tagging"):
			ro.handleDeleteBucketTagging(w, r, bucket)
		case q.Has("publicAccessBlock"):
			ro.handleDeletePublicAccessBlock(w, r, bucket)
		case q.Has("encryption"):
			ro.handleDeleteBucketEncryption(w, r, bucket)
		case q.Has("ownershipControls"):
			ro.handleDeleteBucketOwnershipControls(w, r, bucket)
		case q.Has("lifecycle"):
			ro.handleDeleteBucketLifecycle(w, r, bucket)
		case q.Has("website"):
			ro.handleDeleteBucketWebsite(w, r, bucket)
		case q.Has("replication"):
			ro.handleDeleteBucketReplication(w, r, bucket)
		default:
			ro.handleDeleteBucket(w, r, bucket)
		}
	case http.MethodHead:
		ro.handleHeadBucket(w, r, bucket)
	case http.MethodGet:
		switch {
		case q.Has("cors"):
			ro.handleGetBucketCors(w, r, bucket)
		case q.Has("policy"):
			ro.handleGetBucketPolicy(w, r, bucket)
		case q.Has("tagging"):
			ro.handleGetBucketTagging(w, r, bucket)
		case q.Has("versioning"):
			ro.handleGetBucketVersioning(w, r, bucket)
		case q.Has("versions"):
			ro.handleListObjectVersions(w, r, bucket)
		case q.Has("location"):
			ro.handleGetBucketLocation(w, r, bucket)
		case q.Has("uploads"):
			ro.handleListMultipartUploads(w, r, bucket)
		case q.Has("publicAccessBlock"):
			ro.handleGetPublicAccessBlock(w, r, bucket)
		case q.Has("encryption"):
			ro.handleGetBucketEncryption(w, r, bucket)
		case q.Has("ownershipControls"):
			ro.handleGetBucketOwnershipControls(w, r, bucket)
		case q.Has("notification"):
			ro.handleGetBucketNotification(w, r, bucket)
		case q.Has("lifecycle"):
			ro.handleGetBucketLifecycle(w, r, bucket)
		case q.Has("website"):
			ro.handleGetBucketWebsite(w, r, bucket)
		case q.Has("logging"):
			ro.handleGetBucketLogging(w, r, bucket)
		case q.Has("accelerate"):
			ro.handleGetBucketAccelerate(w, r, bucket)
		case q.Has("replication"):
			ro.handleGetBucketReplication(w, r, bucket)
		case q.Has("requestPayment"):
			ro.handleGetBucketRequestPayment(w, r, bucket)
		case q.Has("object-lock"):
			ro.handleGetObjectLockConfiguration(w, r, bucket)
		case q.Has("acl"):
			ro.handleGetBucketACL(w, r, bucket)
		case q.Get("list-type") == "2":
			ro.handleListObjectsV2(w, r, bucket)
		default:
			ro.handleListObjects(w, r, bucket)
		}
	case http.MethodPost:
		if q.Has("delete") {
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
	var isTruncated bool

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
		sc := obj.Metadata.StorageClass
		if sc == "" {
			sc = "STANDARD"
		}
		contents = append(contents, xmlObjectContent{
			Key:          obj.Key,
			LastModified: obj.Metadata.LastModified.UTC(),
			ETag:         obj.Metadata.ETag,
			Size:         obj.Metadata.Size,
			StorageClass: sc,
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
		Delimiter:      delimiter,
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
	q := r.URL.Query()
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	continuationToken := q.Get("continuation-token")
	startAfter := q.Get("start-after")
	fetchOwner := q.Get("fetch-owner") == "true"
	maxKeys := 1000
	if s := q.Get("max-keys"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			maxKeys = n
		}
	}

	// continuation-token encodes the last key returned in the previous page;
	// use it as the effective cursor (overrides start-after if it sorts later).
	effectiveStartAfter := startAfter
	if continuationToken != "" {
		if decoded, err := base64.URLEncoding.DecodeString(continuationToken); err == nil {
			if tokenKey := string(decoded); tokenKey > effectiveStartAfter {
				effectiveStartAfter = tokenKey
			}
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

	// maxKeys=0: return empty result without iterating (bucket existence already validated above).
	if maxKeys == 0 {
		writeXML(w, http.StatusOK, listObjectsV2Result{
			Name:              bucket,
			Prefix:            prefix,
			Delimiter:         delimiter,
			MaxKeys:           0,
			KeyCount:          0,
			IsTruncated:       false,
			ContinuationToken: continuationToken,
			StartAfter:        startAfter,
		})
		return
	}

	var contents []xmlObjectContent
	seenPrefixes := make(map[string]struct{})
	var lastCPAdded string
	var isTruncated bool
	count := 0

	for _, obj := range objects {
		if !strings.HasPrefix(obj.Key, prefix) {
			continue
		}
		if obj.Key <= effectiveStartAfter {
			continue
		}
		// When the cursor is a common prefix (e.g. "a/"), skip all keys that
		// belong to that prefix group (e.g. "a/x.txt"), since they were already
		// counted in the previous page.
		if delimiter != "" && strings.HasSuffix(effectiveStartAfter, delimiter) &&
			strings.HasPrefix(obj.Key, effectiveStartAfter) {
			continue
		}
		// Group keys sharing a common prefix up to the delimiter.
		// Each unique common prefix counts as one entry toward maxKeys.
		if delimiter != "" {
			rest := strings.TrimPrefix(obj.Key, prefix)
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				cp := prefix + rest[:idx+len(delimiter)]
				if _, seen := seenPrefixes[cp]; !seen {
					if count >= maxKeys {
						isTruncated = true
						break
					}
					seenPrefixes[cp] = struct{}{}
					lastCPAdded = cp
					count++
				}
				continue
			}
		}
		if count >= maxKeys {
			isTruncated = true
			break
		}
		sc := obj.Metadata.StorageClass
		if sc == "" {
			sc = "STANDARD"
		}
		content := xmlObjectContent{
			Key:          obj.Key,
			LastModified: obj.Metadata.LastModified.UTC(),
			ETag:         obj.Metadata.ETag,
			Size:         obj.Metadata.Size,
			StorageClass: sc,
		}
		if fetchOwner {
			content.Owner = &xmlOwner{ID: "owner", DisplayName: "owner"}
		}
		contents = append(contents, content)
		count++
	}

	cps := make([]xmlCommonPrefix, 0, len(seenPrefixes))
	for cp := range seenPrefixes {
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

	result := listObjectsV2Result{
		Name:              bucket,
		Prefix:            prefix,
		Delimiter:         delimiter,
		MaxKeys:           maxKeys,
		KeyCount:          len(contents) + len(cps),
		IsTruncated:       isTruncated,
		ContinuationToken: continuationToken,
		StartAfter:        startAfter,
		Contents:          contents,
		CommonPrefixes:    cps,
	}
	if isTruncated {
		// Use the last content key as cursor; fall back to the last common prefix
		// when the page consisted entirely of common prefixes.
		if len(contents) > 0 {
			result.NextContinuationToken = base64.URLEncoding.EncodeToString(
				[]byte(contents[len(contents)-1].Key),
			)
		} else if lastCPAdded != "" {
			result.NextContinuationToken = base64.URLEncoding.EncodeToString(
				[]byte(lastCPAdded),
			)
		}
	}
	writeXML(w, http.StatusOK, result)
}

func (ro *Router) routeObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	q := r.URL.Query()
	if r.Method != http.MethodOptions {
		ro.injectCORSHeaders(w, r, bucket)
	}
	switch r.Method {
	case http.MethodOptions:
		ro.handleCORSPreflight(w, r, bucket)
		return
	case http.MethodPost:
		switch {
		case q.Has("uploads"):
			ro.handleCreateMultipartUpload(w, r, bucket, key)
		case q.Has("uploadId"):
			ro.handleCompleteMultipartUpload(w, r, bucket, key)
		case q.Has("restore"):
			ro.handleRestoreObject(w, r, bucket, key)
		default:
			writeNotImplemented(w, r)
		}
	case http.MethodPut:
		switch {
		case q.Has("tagging"):
			ro.handlePutObjectTagging(w, r, bucket, key)
		case q.Has("retention"):
			ro.handlePutObjectRetention(w, r, bucket, key)
		case q.Has("legal-hold"):
			ro.handlePutObjectLegalHold(w, r, bucket, key)
		case q.Has("partNumber") && q.Has("uploadId") && r.Header.Get(amzCopySource) != "":
			ro.handleUploadPartCopy(w, r, bucket, key)
		case q.Has("partNumber") && q.Has("uploadId"):
			ro.handleUploadPart(w, r, bucket, key)
		case r.Header.Get(amzCopySource) != "":
			ro.handleCopyObject(w, r, bucket, key)
		case q.Has("acl"):
			ro.handlePutObjectACL(w, r, bucket, key)
		default:
			ro.handlePutObject(w, r, bucket, key)
		}
	case http.MethodGet:
		switch {
		case q.Has("tagging"):
			ro.handleGetObjectTagging(w, r, bucket, key)
		case q.Has("retention"):
			ro.handleGetObjectRetention(w, r, bucket, key)
		case q.Has("legal-hold"):
			ro.handleGetObjectLegalHold(w, r, bucket, key)
		case q.Has("uploadId"):
			ro.handleListParts(w, r, bucket, key)
		case q.Has("acl"):
			ro.handleGetObjectACL(w, r, bucket, key)
		case q.Has("attributes"):
			ro.handleGetObjectAttributes(w, r, bucket, key)
		default:
			ro.handleGetObject(w, r, bucket, key)
		}
	case http.MethodDelete:
		switch {
		case q.Has("tagging"):
			ro.handleDeleteObjectTagging(w, r, bucket, key)
		case q.Has("uploadId"):
			ro.handleAbortMultipartUpload(w, r, bucket, key)
		default:
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

func (ro *Router) handlePutBucketTagging(w http.ResponseWriter, r *http.Request, bucket string) {
	var req xmlTagging
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"malformed tagging XML",
			"bucket",
			bucket,
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
	if len(req.TagSet) > 50 {
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"too many tags",
			"bucket",
			bucket,
			"count",
			len(req.TagSet),
		)
		writeError(w, r, http.StatusBadRequest, "InvalidTag",
			"Bucket tag cannot be greater than 50")
		return
	}
	seen := make(map[string]struct{}, len(req.TagSet))
	for _, t := range req.TagSet {
		if utf8.RuneCountInString(t.Key) > 128 {
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"tag key too long",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusBadRequest, "InvalidTag",
				"The TagKey you have provided is invalid")
			return
		}
		if utf8.RuneCountInString(t.Value) > 256 {
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"tag value too long",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusBadRequest, "InvalidTag",
				"The TagValue you have provided is invalid")
			return
		}
		if _, dup := seen[t.Key]; dup {
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"duplicate tag key",
				"bucket",
				bucket,
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
	if err := ro.storage.PutBucketTagging(bucket, tags); err != nil {
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
			"failed to put bucket tagging",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"bucket tagging updated",
		"bucket",
		bucket,
	)
}

func (ro *Router) handleGetBucketTagging(w http.ResponseWriter, r *http.Request, bucket string) {
	tags, err := ro.storage.GetBucketTagging(bucket)
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
			"failed to get bucket tagging",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if len(tags) == 0 {
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"no tag set on bucket",
			"bucket",
			bucket,
		)
		writeError(w, r, http.StatusNotFound, "NoSuchTagSet",
			"There is no tag set associated with the bucket.")
		return
	}
	slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"get bucket tagging",
		"bucket",
		bucket,
	)
	xmlTags := make([]xmlTag, len(tags))
	for i, t := range tags {
		xmlTags[i] = xmlTag(t)
	}
	writeXML(w, http.StatusOK, xmlTagging{TagSet: xmlTags})
}

func (ro *Router) handleDeleteBucketTagging(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := ro.storage.DeleteBucketTagging(bucket); err != nil {
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
			"failed to delete bucket tagging",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"bucket tagging deleted",
		"bucket",
		bucket,
	)
	w.WriteHeader(http.StatusNoContent)
}

func (ro *Router) handlePutBucketCors(w http.ResponseWriter, r *http.Request, bucket string) {
	var req xmlCORSConfiguration
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"malformed cors XML",
			"bucket",
			bucket,
		)
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	if len(req.CORSRules) == 0 {
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"cors configuration has no rules",
			"bucket",
			bucket,
		)
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	rules := make([]CORSRule, len(req.CORSRules))
	for i, rule := range req.CORSRules {
		if len(rule.AllowedOrigins) == 0 || len(rule.AllowedMethods) == 0 {
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"cors rule missing required fields",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusBadRequest, "MalformedXML",
				"The XML you provided was not well-formed.")
			return
		}
		for _, method := range rule.AllowedMethods {
			if !validCORSMethod(method) {
				slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
					"invalid cors method",
					"bucket",
					bucket,
					"method",
					method,
				)
				writeError(w, r, http.StatusBadRequest, "InvalidArgument",
					"Found invalid method in CORS rule.")
				return
			}
		}
		rules[i] = CORSRule(rule)
	}
	if err := ro.storage.PutBucketCors(bucket, rules); err != nil {
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
			"failed to put bucket cors",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"bucket cors updated",
		"bucket",
		bucket,
		"rules",
		len(rules),
	)
}

func (ro *Router) handleGetBucketCors(w http.ResponseWriter, r *http.Request, bucket string) {
	rules, err := ro.storage.GetBucketCors(bucket)
	if err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		case errors.Is(err, ErrNoCORSConfiguration):
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"no cors configuration",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchCORSConfiguration",
				"The CORS configuration does not exist.")
		default:
			slog.Error( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"failed to get bucket cors",
				"bucket",
				bucket,
				"err",
				err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"get bucket cors",
		"bucket",
		bucket,
		"rules",
		len(rules),
	)
	xmlRules := make([]xmlCORSRule, len(rules))
	for i, rule := range rules {
		xmlRules[i] = xmlCORSRule(rule)
	}
	writeXML(w, http.StatusOK, xmlCORSConfiguration{CORSRules: xmlRules})
}

// validCORSMethod reports whether method is an AWS-allowed CORS HTTP method.
func validCORSMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPut, http.MethodPost, http.MethodDelete, http.MethodHead:
		return true
	}
	return false
}

func (ro *Router) handleDeleteBucketCors(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := ro.storage.DeleteBucketCors(bucket); err != nil {
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
			"failed to delete bucket cors",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"bucket cors deleted",
		"bucket",
		bucket,
	)
	w.WriteHeader(http.StatusNoContent)
}

// corsMatchOrigin reports whether origin matches any of the given patterns.
// Each pattern may contain at most one '*' wildcard character.
func corsMatchOrigin(patterns []string, origin string) bool {
	for _, p := range patterns {
		if p == "*" || corsWildcardMatch(p, origin) {
			return true
		}
	}
	return false
}

// corsWildcardMatch matches s against pattern where pattern may contain one '*'.
// The '*' must match at least one character (empty match is not allowed).
func corsWildcardMatch(pattern, s string) bool {
	idx := strings.Index(pattern, "*")
	if idx < 0 {
		return pattern == s
	}
	prefix, suffix := pattern[:idx], pattern[idx+1:]
	return len(s) >= len(prefix)+len(suffix)+1 &&
		strings.HasPrefix(s, prefix) &&
		strings.HasSuffix(s, suffix)
}

// corsMatchMethod reports whether method is in the allowed list (case-insensitive).
func corsMatchMethod(allowed []string, method string) bool {
	for _, m := range allowed {
		if strings.EqualFold(m, method) {
			return true
		}
	}
	return false
}

// corsMatchRequestedHeaders reports whether every header in the comma-separated
// requestedHeaders string is covered by allowed (supports "*" wildcard).
func corsMatchRequestedHeaders(allowed []string, requestedHeaders string) bool {
	if requestedHeaders == "" {
		return true
	}
	for _, a := range allowed {
		if a == "*" {
			return true
		}
	}
	for _, rh := range strings.Split(requestedHeaders, ",") {
		rh = strings.TrimSpace(strings.ToLower(rh))
		if rh == "" {
			continue
		}
		found := false
		for _, a := range allowed {
			if strings.EqualFold(a, rh) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// findMatchingCORSRule returns the first rule whose origin, method, and header
// constraints all match. requestedHeaders may be empty for non-preflight checks;
// when empty, header matching is intentionally skipped because actual (non-preflight)
// requests do not send Access-Control-Request-Headers and AWS does not enforce
// AllowedHeaders on simple responses.
func findMatchingCORSRule(rules []CORSRule, origin, method, requestedHeaders string) *CORSRule {
	for i := range rules {
		rule := &rules[i]
		if !corsMatchOrigin(rule.AllowedOrigins, origin) {
			continue
		}
		if !corsMatchMethod(rule.AllowedMethods, method) {
			continue
		}
		if requestedHeaders != "" &&
			!corsMatchRequestedHeaders(rule.AllowedHeaders, requestedHeaders) {
			continue
		}
		return rule
	}
	return nil
}

const corsAccessDeniedMsg = "CORSResponse: This CORS request is not allowed. This is usually because the " +
	"evaluation of Origin, request method / Access-Control-Request-Method or " +
	"Access-Control-Request-Headers are not whitelisted by the resource's CORS spec."

// handleCORSPreflight processes an HTTP OPTIONS preflight request for bucket/object endpoints.
func (ro *Router) handleCORSPreflight(w http.ResponseWriter, r *http.Request, bucket string) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		writeError(w, r, http.StatusForbidden, "AccessForbidden", corsAccessDeniedMsg)
		return
	}
	requestMethod := r.Header.Get("Access-Control-Request-Method")
	requestHeaders := r.Header.Get("Access-Control-Request-Headers")

	rules, err := ro.storage.GetBucketCors(bucket)
	if err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"cors preflight: bucket not found",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"cors preflight: no cors configuration",
			"bucket",
			bucket,
		)
		writeError(w, r, http.StatusForbidden, "AccessForbidden", corsAccessDeniedMsg)
		return
	}

	rule := findMatchingCORSRule(rules, origin, requestMethod, requestHeaders)
	if rule == nil {
		slog.Debug( // #nosec G706 -- bucket/origin come from request; log injection risk accepted for a local dev emulator
			"cors preflight: no matching rule",
			"bucket",
			bucket,
			"origin",
			origin,
			"method",
			requestMethod,
		)
		writeError(w, r, http.StatusForbidden, "AccessForbidden", corsAccessDeniedMsg)
		return
	}

	h := w.Header()
	// Literal "*" entry in AllowedOrigins → respond with wildcard (not the echoed origin).
	// A pattern like "http://*.example.com" is matched earlier but is not the literal "*",
	// so we echo back the actual origin value in that case.
	if slices.Contains(rule.AllowedOrigins, "*") {
		h.Set("Access-Control-Allow-Origin", "*")
	} else {
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Vary", "Origin, Access-Control-Request-Headers")
	}
	h.Set("Access-Control-Allow-Methods", strings.Join(rule.AllowedMethods, ", "))
	if len(rule.AllowedHeaders) > 0 {
		h.Set("Access-Control-Allow-Headers", strings.Join(rule.AllowedHeaders, ", "))
	}
	if rule.MaxAgeSeconds > 0 {
		h.Set("Access-Control-Max-Age", strconv.Itoa(rule.MaxAgeSeconds))
	}
	slog.Debug( // #nosec G706 -- bucket/origin come from request; log injection risk accepted for a local dev emulator
		"cors preflight: allowed",
		"bucket",
		bucket,
		"origin",
		origin,
		"method",
		requestMethod,
	)
	w.WriteHeader(http.StatusOK)
}

// injectCORSHeaders adds CORS response headers when the request carries an Origin
// header and the bucket has a matching CORS rule. Called for all non-OPTIONS requests.
func (ro *Router) injectCORSHeaders(w http.ResponseWriter, r *http.Request, bucket string) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	rules, err := ro.storage.GetBucketCors(bucket)
	if err != nil {
		return
	}
	rule := findMatchingCORSRule(rules, origin, r.Method, "")
	if rule == nil {
		return
	}
	h := w.Header()
	// Literal "*" entry → wildcard response; wildcard pattern match → echo origin.
	if slices.Contains(rule.AllowedOrigins, "*") {
		h.Set("Access-Control-Allow-Origin", "*")
	} else {
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Vary", "Origin")
	}
	if len(rule.ExposeHeaders) > 0 {
		h.Set("Access-Control-Expose-Headers", strings.Join(rule.ExposeHeaders, ", "))
	}
}

func (ro *Router) handlePutBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"failed to read policy body",
			"bucket",
			bucket,
		)
		writeError(w, r, http.StatusBadRequest, "MalformedPolicy",
			"Policies must be valid JSON and the first byte must be '{'.")
		return
	}
	trimmed := bytes.TrimSpace(body)
	if !json.Valid(trimmed) || trimmed[0] != '{' {
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"invalid policy JSON",
			"bucket",
			bucket,
		)
		writeError(w, r, http.StatusBadRequest, "MalformedPolicy",
			"Policies must be valid JSON and the first byte must be '{'.")
		return
	}
	if err := ro.storage.PutBucketPolicy(bucket, string(trimmed)); err != nil {
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
			"failed to put bucket policy",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"bucket policy updated",
		"bucket",
		bucket,
	)
	w.WriteHeader(http.StatusNoContent)
}

func (ro *Router) handleGetBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	policy, err := ro.storage.GetBucketPolicy(bucket)
	if err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"bucket not found",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		case errors.Is(err, ErrNoBucketPolicy):
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"no bucket policy",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, "NoSuchBucketPolicy",
				"The bucket policy does not exist.")
		default:
			slog.Error( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				"failed to get bucket policy",
				"bucket",
				bucket,
				"err",
				err,
			)
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"get bucket policy",
		"bucket",
		bucket,
	)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(
		w,
		policy,
	) // #nosec G705 -- policy is stored JSON from a prior validated PUT; XSS risk accepted for a local dev emulator
}

func (ro *Router) handleDeleteBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := ro.storage.DeleteBucketPolicy(bucket); err != nil {
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
			"failed to delete bucket policy",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"bucket policy deleted",
		"bucket",
		bucket,
	)
	w.WriteHeader(http.StatusNoContent)
}

func (ro *Router) handlePutBucketVersioning(w http.ResponseWriter, r *http.Request, bucket string) {
	var req xmlVersioningConfiguration
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"malformed versioning XML",
			"bucket",
			bucket,
		)
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	if req.Status != "Enabled" && req.Status != "Suspended" {
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"invalid versioning status",
			"bucket",
			bucket,
			"status",
			req.Status,
		)
		writeError(w, r, http.StatusBadRequest, "IllegalVersioningConfigurationException",
			"The versioning configuration specified is invalid.")
		return
	}
	if err := ro.storage.PutBucketVersioning(bucket, req.Status); err != nil {
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
			"failed to put bucket versioning",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"bucket versioning updated",
		"bucket",
		bucket,
		"status",
		req.Status,
	)
}

func (ro *Router) handleGetBucketVersioning(w http.ResponseWriter, r *http.Request, bucket string) {
	status, err := ro.storage.GetBucketVersioning(bucket)
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
			"failed to get bucket versioning",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"get bucket versioning",
		"bucket",
		bucket,
		"status",
		status,
	)
	writeXML(w, http.StatusOK, xmlVersioningConfiguration{Status: status})
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
		xmlBuckets[i] = xmlBucket{
			Name:         b.Name,
			CreationDate: b.CreationDate.UTC(),
			BucketRegion: b.Region,
		}
	}
	writeXML(w, http.StatusOK, listBucketsResult{
		Owner:   xmlOwner{ID: "owner", DisplayName: "owner"},
		Buckets: xmlBuckets,
	})
}

func (ro *Router) handleCreateBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	region := ParseSigV4(r).Region
	objectLockEnabled := strings.EqualFold(r.Header.Get(amzObjectLockEnabled), "true")
	if err := ro.storage.CreateBucket(bucket, region, objectLockEnabled); err != nil {
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

func (ro *Router) handleListMultipartUploads(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	q := r.URL.Query()
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	keyMarker := q.Get("key-marker")
	maxUploads := 1000
	if s := q.Get("max-uploads"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			if n > 1000 {
				n = 1000
			}
			maxUploads = n
		}
	}

	uploads, err := ro.storage.ListMultipartUploads(bucket)
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
			"failed to list multipart uploads",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	var xmlUploads []xmlMultipartUpload
	commonPrefixes := make(map[string]struct{})
	var nextKeyMarker, nextUploadIdMarker string
	var isTruncated bool

	for _, u := range uploads {
		if !strings.HasPrefix(u.Key, prefix) {
			continue
		}
		if u.Key <= keyMarker {
			continue
		}
		if delimiter != "" {
			rest := strings.TrimPrefix(u.Key, prefix)
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				commonPrefixes[prefix+rest[:idx+len(delimiter)]] = struct{}{}
				continue
			}
		}
		if len(xmlUploads) >= maxUploads {
			isTruncated = true
			break
		}
		sc := u.StorageClass
		if sc == "" {
			sc = "STANDARD"
		}
		xmlUploads = append(xmlUploads, xmlMultipartUpload{
			Key:          u.Key,
			UploadID:     u.UploadID,
			StorageClass: sc,
			Initiated:    u.Initiated.UTC(),
		})
		nextKeyMarker = u.Key
		nextUploadIdMarker = u.UploadID
	}

	cps := make([]xmlCommonPrefix, 0, len(commonPrefixes))
	for cp := range commonPrefixes {
		cps = append(cps, xmlCommonPrefix{Prefix: cp})
	}
	slices.SortFunc(cps, func(a, b xmlCommonPrefix) int {
		return strings.Compare(a.Prefix, b.Prefix)
	})

	slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"listed multipart uploads",
		"bucket",
		bucket,
		"count",
		len(xmlUploads),
	)

	result := listMultipartUploadsResult{
		Bucket:         bucket,
		KeyMarker:      keyMarker,
		Prefix:         prefix,
		Delimiter:      delimiter,
		MaxUploads:     maxUploads,
		IsTruncated:    isTruncated,
		Uploads:        xmlUploads,
		CommonPrefixes: cps,
	}
	if isTruncated {
		result.NextKeyMarker = nextKeyMarker
		result.NextUploadIdMarker = nextUploadIdMarker
	}
	writeXML(w, http.StatusOK, result)
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
	bypassGovernance := r.Header.Get(amzBypassGovernanceRetention) == "true"
	result := deleteObjectsResult{}
	for _, obj := range req.Objects {
		var (
			deleteMarker          bool
			deleteMarkerVersionID string
			deletedVersionID      string
			deleteErr             error
		)
		if obj.VersionId != "" {
			// Delete a specific version.
			// Always echo the requested VersionId in <Deleted>, even when the
			// version does not exist (ErrObjectNotFound is treated as success).
			deletedVersionID = obj.VersionId
			isMarker, err := ro.storage.DeleteObjectVersion(
				bucket,
				obj.Key,
				obj.VersionId,
				bypassGovernance,
			)
			deleteErr = err
			if err == nil {
				deleteMarker = isMarker
				if isMarker {
					deleteMarkerVersionID = obj.VersionId
				}
			}
		} else {
			// Versioning-aware delete: creates a delete marker when versioning is enabled.
			vid, isMarker, err := ro.storage.DeleteObjectVersioned(bucket, obj.Key, bypassGovernance)
			deleteErr = err
			if err == nil && isMarker {
				deleteMarker = true
				deleteMarkerVersionID = vid
			}
		}
		if deleteErr != nil && !errors.Is(deleteErr, ErrObjectNotFound) {
			var code, message string
			if errors.Is(deleteErr, ErrObjectLocked) {
				slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
					"delete rejected: object locked",
					"bucket",
					bucket,
					"key",
					obj.Key,
				)
				code = "AccessDenied"
				message = "Access Denied because the object is protected by Object Lock."
			} else {
				slog.Error( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
					"failed to delete object",
					"bucket",
					bucket,
					"key",
					obj.Key,
					"err",
					deleteErr,
				)
				code = "InternalError"
				message = deleteErr.Error()
			}
			result.Errors = append(result.Errors, xmlDeleteError{
				Key:       obj.Key,
				VersionId: obj.VersionId,
				Code:      code,
				Message:   message,
			})
			continue
		}
		if !req.Quiet {
			result.Deleted = append(result.Deleted, xmlDeletedObject{
				Key:                   obj.Key,
				VersionId:             deletedVersionID,
				DeleteMarker:          deleteMarker,
				DeleteMarkerVersionId: deleteMarkerVersionID,
			})
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

func (ro *Router) handleListObjectVersions(w http.ResponseWriter, r *http.Request, bucket string) {
	q := r.URL.Query()
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	keyMarker := q.Get("key-marker")
	maxKeys := 1000
	if s := q.Get("max-keys"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			maxKeys = n
		}
	}

	versions, deleteMarkers, err := ro.storage.ListObjectVersions(bucket)
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
			"failed to list object versions",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"listed object versions",
		"bucket",
		bucket,
		"versions",
		len(versions),
		"deleteMarkers",
		len(deleteMarkers),
	)

	result := xmlListVersionsResult{
		Xmlns:       "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:        bucket,
		Prefix:      prefix,
		KeyMarker:   keyMarker,
		Delimiter:   delimiter,
		MaxKeys:     maxKeys,
		IsTruncated: false,
	}

	commonPrefixes := make(map[string]struct{})
	count := 0
	var nextKeyMarker, nextVersionIdMarker string

	for _, v := range versions {
		if !strings.HasPrefix(v.Key, prefix) {
			continue
		}
		if v.Key <= keyMarker {
			continue
		}
		if delimiter != "" {
			rest := strings.TrimPrefix(v.Key, prefix)
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				cp := prefix + rest[:idx+len(delimiter)]
				if _, seen := commonPrefixes[cp]; !seen {
					if count >= maxKeys {
						result.IsTruncated = true
						break
					}
					commonPrefixes[cp] = struct{}{}
					count++
					nextKeyMarker = cp
					nextVersionIdMarker = ""
				}
				continue
			}
		}
		if count >= maxKeys {
			result.IsTruncated = true
			break
		}
		sc := v.StorageClass
		if sc == "" {
			sc = "STANDARD"
		}
		result.Versions = append(result.Versions, xmlObjectVersion{
			Key:          v.Key,
			VersionId:    v.VersionID,
			IsLatest:     v.IsLatest,
			LastModified: v.LastModified.UTC().Format(time.RFC3339),
			ETag:         v.ETag,
			Size:         v.Size,
			StorageClass: sc,
			Owner:        xmlOwner{ID: "owner", DisplayName: "owner"},
		})
		nextKeyMarker = v.Key
		nextVersionIdMarker = v.VersionID
		count++
	}

	if !result.IsTruncated {
		for _, dm := range deleteMarkers {
			if !strings.HasPrefix(dm.Key, prefix) {
				continue
			}
			if dm.Key <= keyMarker {
				continue
			}
			if delimiter != "" {
				rest := strings.TrimPrefix(dm.Key, prefix)
				if idx := strings.Index(rest, delimiter); idx >= 0 {
					cp := prefix + rest[:idx+len(delimiter)]
					if _, seen := commonPrefixes[cp]; !seen {
						if count >= maxKeys {
							result.IsTruncated = true
							break
						}
						commonPrefixes[cp] = struct{}{}
						count++
						nextKeyMarker = cp
						nextVersionIdMarker = ""
					}
					continue
				}
			}
			if count >= maxKeys {
				result.IsTruncated = true
				break
			}
			result.DeleteMarkers = append(result.DeleteMarkers, xmlDeleteMarker{
				Key:          dm.Key,
				VersionId:    dm.VersionID,
				IsLatest:     dm.IsLatest,
				LastModified: dm.LastModified.UTC().Format(time.RFC3339),
				Owner:        xmlOwner{ID: "owner", DisplayName: "owner"},
			})
			nextKeyMarker = dm.Key
			nextVersionIdMarker = dm.VersionID
			count++
		}
	}

	cps := make([]xmlCommonPrefix, 0, len(commonPrefixes))
	for cp := range commonPrefixes {
		cps = append(cps, xmlCommonPrefix{Prefix: cp})
	}
	slices.SortFunc(cps, func(a, b xmlCommonPrefix) int {
		return strings.Compare(a.Prefix, b.Prefix)
	})
	result.CommonPrefixes = cps

	if result.IsTruncated {
		result.NextKeyMarker = nextKeyMarker
		result.NextVersionIdMarker = nextVersionIdMarker
	}
	writeXML(w, http.StatusOK, result)
}

func (ro *Router) handleHeadBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	w.Header().Set("Content-Length", "0")
	region, err := ro.storage.GetBucketRegion(bucket)
	if err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			slog.Debug( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
				"bucket not found",
				"bucket",
				bucket,
			)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		slog.Error( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
			"failed to get bucket region",
			"bucket",
			bucket,
			"err",
			err,
		)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	slog.Debug( // #nosec G706 -- bucket name is validated by S3 naming rules before reaching this point
		"bucket found",
		"bucket",
		bucket,
	)
	if region == "" {
		region = "us-east-1"
	}
	w.Header().Set(amzBucketRegion, region)
	w.WriteHeader(http.StatusOK)
}

// extractUserMetadata collects all x-amz-meta-* headers from h and returns
// them as a map keyed by the suffix after the prefix (lowercased). Returns nil
// if no such headers are present.

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
