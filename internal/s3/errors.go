package s3

import (
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

var (
	ErrBucketNotFound      = errors.New("bucket not found")
	ErrBucketNotEmpty      = errors.New("bucket not empty")
	ErrObjectNotFound      = errors.New("object not found")
	ErrUploadNotFound      = errors.New("multipart upload not found")
	ErrInvalidPart         = errors.New("invalid part")
	ErrInvalidPartOrder    = errors.New("parts not in ascending order")
	ErrNoCORSConfiguration = errors.New("no CORS configuration")
	ErrNoBucketPolicy      = errors.New("no bucket policy")
	ErrNoObjectRetention   = errors.New("no object retention")
	ErrNoObjectLegalHold   = errors.New("no object legal hold")
	ErrObjectLocked        = errors.New("object is protected by Object Lock")
	ErrObjectAlreadyExists = errors.New("object already exists")
	ErrInvalidBucketState  = errors.New("invalid bucket state")
)

// DeleteMarkerError is returned when a get/head operation resolves to a delete marker.
// It carries the marker's version ID so the HTTP handler can set x-amz-version-id.
type DeleteMarkerError struct {
	VersionID string
}

func (e *DeleteMarkerError) Error() string { return "object is a delete marker" }

type errorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource"`
	RequestID string   `xml:"RequestId"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeXML(w, status, errorResponse{
		Code:      code,
		Message:   message,
		Resource:  r.URL.Path,
		RequestID: "kumolo-local",
	})
}

func writeNotImplemented(w http.ResponseWriter, r *http.Request) {
	writeError(
		w,
		r,
		http.StatusNotImplemented,
		"NotImplemented",
		"This operation is not implemented.",
	)
}

func writeXML(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	if _, err := fmt.Fprint(w, xml.Header); err != nil {
		slog.Warn("failed to write XML header", "err", err)
		return
	}
	if err := xml.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("failed to encode XML response", "err", err)
	}
}
