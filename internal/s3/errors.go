package s3

import (
	"encoding/xml"
	"errors"
	"net/http"
)

var (
	ErrBucketNotFound = errors.New("bucket not found")
	ErrBucketNotEmpty = errors.New("bucket not empty")
	ErrObjectNotFound = errors.New("object not found")
)

type errorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource"`
	RequestID string   `xml:"RequestId"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)

	resp := errorResponse{
		Code:      code,
		Message:   message,
		Resource:  r.URL.Path,
		RequestID: "kumolo-local",
	}

	_ = xml.NewEncoder(w).Encode(resp)
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
