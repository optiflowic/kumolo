package s3

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
)

// isWellFormedXML reports whether data is well-formed XML with at least one element.
func isWellFormedXML(data []byte) bool {
	d := xml.NewDecoder(bytes.NewReader(data))
	found := false
	for {
		tok, err := d.Token()
		if err != nil {
			return errors.Is(err, io.EOF) && found
		}
		if _, ok := tok.(xml.StartElement); ok {
			found = true
		}
	}
}

// stripXMLDecl removes a leading <?xml ... ?> processing instruction, if present,
// so that writeRawXML can prepend xml.Header without producing a double prolog.
// The prefix check uses "<?xml " (with a trailing space) per the XML spec, so that
// other PIs such as <?xml-stylesheet ... ?> are left intact.
func stripXMLDecl(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "<?xml ") {
		return t
	}
	idx := strings.Index(t, "?>")
	if idx < 0 {
		return t
	}
	return strings.TrimSpace(t[idx+2:])
}

// writeRawXML writes a raw XML string with the appropriate Content-Type header.
func writeRawXML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(
		w,
		xml.Header+body,
	) // #nosec G705 -- body is stored XML from a prior validated PUT
}

// handlePutBucketRawXML is a shared handler for storing raw XML bucket configurations.
func (ro *Router) handlePutBucketRawXML(
	w http.ResponseWriter,
	r *http.Request,
	bucket, opName string,
	put func(bucket, xmlBody string) error,
) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	if !isWellFormedXML(body) {
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	// Strip any leading XML declaration so that writeRawXML does not emit a
	// double prolog when serving the stored body back to the client.
	if err := put(bucket, stripXMLDecl(string(body))); err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleDeleteBucketRawXML is a shared handler for deleting raw XML bucket configurations.
func (ro *Router) handleDeleteBucketRawXML(
	w http.ResponseWriter,
	r *http.Request,
	bucket, opName string,
	del func(bucket string) error,
) {
	if err := del(bucket); err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetBucketRawXML is a shared handler for retrieving raw XML bucket configurations.
// notFoundCode is the S3 error code returned when the config has not been set; if empty,
// a default body is returned instead of 404.
// defaultBody is returned when the config is empty and notFoundCode is empty.
func (ro *Router) handleGetBucketRawXML(
	w http.ResponseWriter,
	r *http.Request,
	bucket, opName string,
	get func(bucket string) (string, error),
	notFoundCode string,
	notFoundMsg string,
	defaultBody string,
) {
	body, err := get(bucket)
	if err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if body == "" {
		if notFoundCode != "" {
			writeError(w, r, http.StatusNotFound, notFoundCode, notFoundMsg)
			return
		}
		writeRawXML(w, http.StatusOK, defaultBody)
		return
	}
	writeRawXML(w, http.StatusOK, body)
}

// --- PublicAccessBlock (#76) ---

func (ro *Router) handlePutPublicAccessBlock(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handlePutBucketRawXML(w, r, bucket, "public access block", ro.storage.PutPublicAccessBlock)
}

func (ro *Router) handleGetPublicAccessBlock(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handleGetBucketRawXML(w, r, bucket, "public access block",
		ro.storage.GetPublicAccessBlock,
		"NoSuchPublicAccessBlockConfiguration",
		"The public access block configuration was not found.",
		"",
	)
}

func (ro *Router) handleDeletePublicAccessBlock(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handleDeleteBucketRawXML(
		w,
		r,
		bucket,
		"public access block",
		ro.storage.DeletePublicAccessBlock,
	)
}

// --- Encryption (#77) ---

func (ro *Router) handlePutBucketEncryption(w http.ResponseWriter, r *http.Request, bucket string) {
	ro.handlePutBucketRawXML(w, r, bucket, "encryption", ro.storage.PutBucketEncryption)
}

func (ro *Router) handleGetBucketEncryption(w http.ResponseWriter, r *http.Request, bucket string) {
	ro.handleGetBucketRawXML(w, r, bucket, "encryption",
		ro.storage.GetBucketEncryption,
		"ServerSideEncryptionConfigurationNotFoundError",
		"The server side encryption configuration was not found.",
		"",
	)
}

func (ro *Router) handleDeleteBucketEncryption(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handleDeleteBucketRawXML(w, r, bucket, "encryption", ro.storage.DeleteBucketEncryption)
}

// --- OwnershipControls (#78) ---

func (ro *Router) handlePutBucketOwnershipControls(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handlePutBucketRawXML(
		w,
		r,
		bucket,
		"ownership controls",
		ro.storage.PutBucketOwnershipControls,
	)
}

func (ro *Router) handleGetBucketOwnershipControls(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handleGetBucketRawXML(w, r, bucket, "ownership controls",
		ro.storage.GetBucketOwnershipControls,
		"OwnershipControlsNotFoundError",
		"The bucket ownership controls were not found.",
		"",
	)
}

func (ro *Router) handleDeleteBucketOwnershipControls(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handleDeleteBucketRawXML(
		w,
		r,
		bucket,
		"ownership controls",
		ro.storage.DeleteBucketOwnershipControls,
	)
}

// --- ACL (#211) ---

func (ro *Router) handleGetBucketACL(w http.ResponseWriter, r *http.Request, bucket string) {
	aclXML, err := ro.storage.GetBucketACL(bucket)
	if err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if aclXML == "" {
		aclXML = defaultACLXML()
	}
	writeRawXML(w, http.StatusOK, aclXML)
}

func (ro *Router) handlePutBucketACL(w http.ResponseWriter, r *http.Request, bucket string) {
	var body []byte
	if r.Header.Get(amzACL) == "" {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil { // untestable: httptest.NewRequest body never errors
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
	}

	aclXML, err := resolveACLFromRequest(r, body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument", err.Error())
		return
	}
	if aclXML == "" {
		writeError(
			w,
			r,
			http.StatusBadRequest,
			"MalformedXML",
			"The XML you provided was not well-formed or did not validate against our published schema.",
		)
		return
	}

	if err := ro.storage.PutBucketACL(bucket, stripXMLDecl(aclXML)); err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleGetObjectACL(w http.ResponseWriter, r *http.Request, bucket, key string) {
	aclXML, err := ro.storage.GetObjectACL(bucket, key)
	if err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		if errors.Is(err, ErrObjectNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchKey",
				"The specified key does not exist.")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if aclXML == "" {
		aclXML = defaultACLXML()
	}
	writeRawXML(w, http.StatusOK, aclXML)
}

func (ro *Router) handlePutObjectACL(w http.ResponseWriter, r *http.Request, bucket, key string) {
	var body []byte
	if r.Header.Get(amzACL) == "" {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil { // untestable: httptest.NewRequest body never errors
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
	}

	aclXML, err := resolveACLFromRequest(r, body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument", err.Error())
		return
	}
	if aclXML == "" {
		writeError(
			w,
			r,
			http.StatusBadRequest,
			"MalformedXML",
			"The XML you provided was not well-formed or did not validate against our published schema.",
		)
		return
	}

	if err := ro.storage.PutObjectACL(bucket, key, stripXMLDecl(aclXML)); err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		if errors.Is(err, ErrObjectNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchKey",
				"The specified key does not exist.")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// resolveACLFromRequest returns ACL XML from the x-amz-acl header (takes precedence)
// or the request body. Returns "" if neither is provided.
func resolveACLFromRequest(r *http.Request, body []byte) (string, error) {
	if canned := r.Header.Get(amzACL); canned != "" {
		return buildCannedACL(canned)
	}
	if len(body) > 0 {
		return parseACLBody(body)
	}
	return "", nil
}

// --- NotificationConfiguration (#80) ---

func (ro *Router) handlePutBucketNotification(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handlePutBucketRawXML(
		w,
		r,
		bucket,
		"notification configuration",
		ro.storage.PutBucketNotification,
	)
}

func (ro *Router) handleGetBucketNotification(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handleGetBucketRawXML(w, r, bucket, "notification configuration",
		ro.storage.GetBucketNotification,
		"",
		"",
		`<NotificationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"/>`,
	)
}

// --- LifecycleConfiguration (#81) ---

func (ro *Router) handlePutBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	transitionMinSize := r.Header.Get("x-amz-transition-default-minimum-object-size")
	switch transitionMinSize {
	case "", "all_storage_classes_128K":
		transitionMinSize = "all_storage_classes_128K"
	case "varies_by_storage_class":
		// valid; use as-is
	default:
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"Invalid value for x-amz-transition-default-minimum-object-size.")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	if !isWellFormedXML(body) {
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	if err := ro.storage.PutBucketLifecycleConfig(bucket, stripXMLDecl(string(body)), transitionMinSize); err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleGetBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	xmlBody, err := ro.storage.GetBucketLifecycle(bucket)
	if err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if xmlBody == "" {
		writeError(w, r, http.StatusNotFound, "NoSuchLifecycleConfiguration",
			"The lifecycle configuration does not exist.")
		return
	}
	transitionMinSize, err := ro.storage.GetBucketLifecycleTransitionMinSize(bucket)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if transitionMinSize == "" {
		transitionMinSize = "all_storage_classes_128K"
	}
	w.Header().Set("x-amz-transition-default-minimum-object-size", transitionMinSize)
	writeRawXML(w, http.StatusOK, xmlBody)
}

func (ro *Router) handleDeleteBucketLifecycle(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handleDeleteBucketRawXML(
		w,
		r,
		bucket,
		"lifecycle configuration",
		ro.storage.DeleteBucketLifecycle,
	)
}

// --- Website (#82) ---

func (ro *Router) handlePutBucketWebsite(w http.ResponseWriter, r *http.Request, bucket string) {
	ro.handlePutBucketRawXML(w, r, bucket, "website configuration", ro.storage.PutBucketWebsite)
}

func (ro *Router) handleGetBucketWebsite(w http.ResponseWriter, r *http.Request, bucket string) {
	ro.handleGetBucketRawXML(w, r, bucket, "website configuration",
		ro.storage.GetBucketWebsite,
		"NoSuchWebsiteConfiguration",
		"The specified bucket does not have a website configuration.",
		"",
	)
}

func (ro *Router) handleDeleteBucketWebsite(w http.ResponseWriter, r *http.Request, bucket string) {
	ro.handleDeleteBucketRawXML(
		w,
		r,
		bucket,
		"website configuration",
		ro.storage.DeleteBucketWebsite,
	)
}

// --- Logging (#83) ---

func (ro *Router) handlePutBucketLogging(w http.ResponseWriter, r *http.Request, bucket string) {
	ro.handlePutBucketRawXML(w, r, bucket, "logging configuration", ro.storage.PutBucketLogging)
}

func (ro *Router) handleGetBucketLogging(w http.ResponseWriter, r *http.Request, bucket string) {
	ro.handleGetBucketRawXML(w, r, bucket, "logging configuration",
		ro.storage.GetBucketLogging,
		"",
		"",
		"<BucketLoggingStatus xmlns=\"http://doc.s3.amazonaws.com/2006-03-01\"/>",
	)
}

// --- AccelerateConfiguration (#84) ---

func (ro *Router) handlePutBucketAccelerate(w http.ResponseWriter, r *http.Request, bucket string) {
	ro.handlePutBucketRawXML(
		w,
		r,
		bucket,
		"accelerate configuration",
		ro.storage.PutBucketAccelerate,
	)
}

func (ro *Router) handleGetBucketAccelerate(w http.ResponseWriter, r *http.Request, bucket string) {
	ro.handleGetBucketRawXML(w, r, bucket, "accelerate configuration",
		ro.storage.GetBucketAccelerate,
		"",
		"",
		"<AccelerateConfiguration xmlns=\"http://s3.amazonaws.com/doc/2006-03-01/\"/>",
	)
}

// --- Replication (#91) ---

func (ro *Router) handlePutBucketReplication(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	body, err := io.ReadAll(r.Body)
	if err != nil || !isWellFormedXML(body) {
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}

	var cfg replicationConfig
	if err := xml.Unmarshal(body, &cfg); err != nil {
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	for _, rule := range cfg.Rules {
		if ruleHasTagFilter(rule) &&
			(rule.DeleteMarkerReplication == nil ||
				rule.DeleteMarkerReplication.Status != "Disabled") {
			writeError(w, r, http.StatusBadRequest, "InvalidRequest",
				"DeleteMarkerReplication must be Disabled when using tag filters.")
			return
		}
	}

	if err := ro.storage.PutBucketReplication(bucket, stripXMLDecl(string(body))); err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleGetBucketReplication(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handleGetBucketRawXML(w, r, bucket, "replication configuration",
		ro.storage.GetBucketReplication,
		"ReplicationConfigurationNotFoundError",
		"The replication configuration was not found.",
		"",
	)
}

func (ro *Router) handleDeleteBucketReplication(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handleDeleteBucketRawXML(
		w,
		r,
		bucket,
		"replication configuration",
		ro.storage.DeleteBucketReplication,
	)
}

// --- RequestPayment (#92) ---

const defaultRequestPaymentResponse = `<RequestPaymentConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
	`<Payer>BucketOwner</Payer></RequestPaymentConfiguration>`

func (ro *Router) handlePutBucketRequestPayment(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handlePutBucketRawXML(
		w,
		r,
		bucket,
		"request payment configuration",
		ro.storage.PutBucketRequestPayment,
	)
}

func (ro *Router) handleGetBucketRequestPayment(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handleGetBucketRawXML(w, r, bucket, "request payment configuration",
		ro.storage.GetBucketRequestPayment,
		"",
		"",
		defaultRequestPaymentResponse,
	)
}

// --- ObjectLockConfiguration (#93) ---

func (ro *Router) handlePutObjectLockConfiguration(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "InternalError",
			"Failed to read request body.")
		return
	}
	if !isWellFormedXML(body) {
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	if err := ro.storage.PutBucketObjectLock(bucket, stripXMLDecl(string(body))); err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		case errors.Is(err, ErrInvalidBucketState):
			writeError(
				w,
				r,
				http.StatusBadRequest,
				"InvalidBucketState",
				"Object Lock configuration cannot be enabled on a bucket that does not have versioning enabled.",
			)
		default:
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleGetObjectLockConfiguration(
	w http.ResponseWriter,
	r *http.Request,
	bucket string,
) {
	ro.handleGetBucketRawXML(w, r, bucket, "object lock configuration",
		ro.storage.GetBucketObjectLock,
		"ObjectLockConfigurationNotFoundError",
		"Object Lock configuration does not exist for this bucket.",
		"",
	)
}
