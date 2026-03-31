package s3

import (
	"encoding/xml"
	"errors"
	"fmt"
	"log"
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

	if _, err := fmt.Fprint(w, xml.Header); err != nil {
		log.Printf("warn: failed to write XML header: %v", err)
		return
	}
	if err := xml.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("warn: failed to encode error response: %v", err)
	}
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
