package s3

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
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
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			opName+" read error",
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	if !isWellFormedXML(body) {
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			opName+" malformed XML",
			"bucket",
			bucket,
		)
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	// Strip any leading XML declaration so that writeRawXML does not emit a
	// double prolog when serving the stored body back to the client.
	if err := put(bucket, stripXMLDecl(string(body))); err != nil {
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
			"failed to put "+opName,
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		opName+" updated",
		"bucket",
		bucket,
	)
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
			"failed to delete "+opName,
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	slog.Info( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		opName+" deleted",
		"bucket",
		bucket,
	)
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
			"failed to get "+opName,
			"bucket",
			bucket,
			"err",
			err,
		)
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if body == "" {
		if notFoundCode != "" {
			slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
				opName+" not configured",
				"bucket",
				bucket,
			)
			writeError(w, r, http.StatusNotFound, notFoundCode, notFoundMsg)
			return
		}
		slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
			"get "+opName+" (default)",
			"bucket",
			bucket,
		)
		writeRawXML(w, http.StatusOK, defaultBody)
		return
	}
	slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"get "+opName,
		"bucket",
		bucket,
	)
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

// --- ACL (#79) ---
// ACL is a stub: PUT accepts and ignores the body; GET returns a default owner ACL.

const defaultACLResponse = `<AccessControlPolicy xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
	`<Owner><ID>owner</ID><DisplayName>owner</DisplayName></Owner>` +
	`<AccessControlList><Grant>` +
	`<Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="CanonicalUser">` +
	`<ID>owner</ID><DisplayName>owner</DisplayName></Grantee>` +
	`<Permission>FULL_CONTROL</Permission>` +
	`</Grant></AccessControlList></AccessControlPolicy>`

func (ro *Router) handleGetBucketACL(w http.ResponseWriter, r *http.Request, bucket string) {
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
	slog.Debug( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"get bucket ACL",
		"bucket",
		bucket,
	)
	writeRawXML(w, http.StatusOK, defaultACLResponse)
}

func (ro *Router) handlePutBucketACL(w http.ResponseWriter, r *http.Request, bucket string) {
	_, _ = io.Copy(io.Discard, r.Body)
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
	slog.Info( // #nosec G706 -- bucket comes from URL path; log injection risk accepted for a local dev emulator
		"bucket ACL updated (stub)",
		"bucket",
		bucket,
	)
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleGetObjectACL(w http.ResponseWriter, r *http.Request, bucket, key string) {
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
	if _, err := ro.storage.HeadObject(bucket, key); err != nil {
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
			"failed to head object for ACL",
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
	slog.Debug( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
		"get object ACL",
		"bucket",
		bucket,
		"key",
		key,
	)
	writeRawXML(w, http.StatusOK, defaultACLResponse)
}

func (ro *Router) handlePutObjectACL(w http.ResponseWriter, r *http.Request, bucket, key string) {
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
	if _, err := ro.storage.HeadObject(bucket, key); err != nil {
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
			"failed to head object for ACL",
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
		"object ACL updated (stub)",
		"bucket",
		bucket,
		"key",
		key,
	)
	w.WriteHeader(http.StatusOK)
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
	ro.handlePutBucketRawXML(w, r, bucket, "lifecycle configuration", ro.storage.PutBucketLifecycle)
}

func (ro *Router) handleGetBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	ro.handleGetBucketRawXML(w, r, bucket, "lifecycle configuration",
		ro.storage.GetBucketLifecycle,
		"NoSuchLifecycleConfiguration",
		"The lifecycle configuration does not exist.",
		"",
	)
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
	ro.handlePutBucketRawXML(
		w,
		r,
		bucket,
		"replication configuration",
		ro.storage.PutBucketReplication,
	)
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
		slog.Error(
			"object lock configuration read error",
			"bucket",
			bucket,
			"err",
			err,
		) // #nosec G706
		writeError(w, r, http.StatusInternalServerError, "InternalError",
			"Failed to read request body.")
		return
	}
	if !isWellFormedXML(body) {
		slog.Debug("object lock configuration malformed XML", "bucket", bucket) // #nosec G706
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	if err := ro.storage.PutBucketObjectLock(bucket, stripXMLDecl(string(body))); err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			slog.Debug("bucket not found", "bucket", bucket) // #nosec G706
			writeError(w, r, http.StatusNotFound, "NoSuchBucket",
				"The specified bucket does not exist.")
		case errors.Is(err, ErrInvalidBucketState):
			slog.Debug("versioning not enabled for object lock", "bucket", bucket) // #nosec G706
			writeError(
				w,
				r,
				http.StatusBadRequest,
				"InvalidBucketState",
				"Object Lock configuration cannot be enabled on a bucket that does not have versioning enabled.",
			)
		default:
			slog.Error(
				"failed to put object lock configuration",
				"bucket",
				bucket,
				"err",
				err,
			) // #nosec G706
			writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	slog.Info("object lock configuration updated", "bucket", bucket) // #nosec G706
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
