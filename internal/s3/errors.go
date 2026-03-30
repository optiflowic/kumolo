package s3

import (
	"encoding/xml"
	"net/http"
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

func writeNotFound(w http.ResponseWriter, r *http.Request, code, message string) {
	writeError(w, r, http.StatusNotFound, code, message)
}

func writeNotImplemented(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotImplemented, "NotImplemented", "This operation is not implemented.")
}
