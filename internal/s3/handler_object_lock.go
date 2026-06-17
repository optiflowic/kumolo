package s3

import (
	"encoding/xml"
	"errors"
	"net/http"
)

func (ro *Router) handlePutObjectRetention(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	var req xmlObjectRetention
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	if req.Mode != "GOVERNANCE" && req.Mode != "COMPLIANCE" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"Mode must be GOVERNANCE or COMPLIANCE.")
		return
	}
	if !req.RetainUntilDate.After(ro.now()) {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"The retain until date must be in the future.")
		return
	}
	versionID := r.URL.Query().Get("versionId")
	retention := ObjectRetention{
		Mode:            req.Mode,
		RetainUntilDate: req.RetainUntilDate,
	}
	if err := ro.storage.PutObjectRetention(bucket, key, versionID, retention); err != nil {
		writeObjectLockError(w, r, bucket, key, "put object retention", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleGetObjectRetention(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	versionID := r.URL.Query().Get("versionId")
	retention, err := ro.storage.GetObjectRetention(bucket, key, versionID)
	if err != nil {
		if errors.Is(err, ErrNoObjectRetention) {
			writeError(w, r, http.StatusNotFound, "NoSuchObjectLockConfiguration",
				"The specified object does not have an ObjectLock configuration.")
			return
		}
		writeObjectLockError(w, r, bucket, key, "get object retention", err)
		return
	}
	writeXML(w, http.StatusOK, xmlObjectRetention{
		Mode:            retention.Mode,
		RetainUntilDate: retention.RetainUntilDate,
	})
}

func (ro *Router) handlePutObjectLegalHold(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	var req xmlObjectLegalHold
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "MalformedXML",
			"The XML you provided was not well-formed.")
		return
	}
	if req.Status != "ON" && req.Status != "OFF" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"Status must be ON or OFF.")
		return
	}
	versionID := r.URL.Query().Get("versionId")
	if err := ro.storage.PutObjectLegalHold(bucket, key, versionID, req.Status); err != nil {
		writeObjectLockError(w, r, bucket, key, "put object legal hold", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (ro *Router) handleGetObjectLegalHold(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key string,
) {
	versionID := r.URL.Query().Get("versionId")
	status, err := ro.storage.GetObjectLegalHold(bucket, key, versionID)
	if err != nil {
		if errors.Is(err, ErrNoObjectLegalHold) {
			writeError(w, r, http.StatusNotFound, "NoSuchObjectLockConfiguration",
				"The specified object does not have an ObjectLock configuration.")
			return
		}
		writeObjectLockError(w, r, bucket, key, "get object legal hold", err)
		return
	}
	writeXML(w, http.StatusOK, xmlObjectLegalHold{Status: status})
}

// writeObjectLockError translates storage errors from object lock operations
// into the appropriate HTTP response.
func writeObjectLockError(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key, op string,
	err error,
) {
	var dme *DeleteMarkerError
	switch {
	case errors.Is(err, ErrBucketNotFound):
		writeError(w, r, http.StatusNotFound, "NoSuchBucket",
			"The specified bucket does not exist.")
	case errors.Is(err, ErrObjectNotFound):
		writeError(w, r, http.StatusNotFound, "NoSuchKey",
			"The specified key does not exist.")
	case errors.As(err, &dme):
		writeError(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed",
			"The specified method is not allowed against this resource type.")
	default:
		writeError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
