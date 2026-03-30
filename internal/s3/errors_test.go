package s3

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// failWriter is an http.ResponseWriter whose Write always fails.
type failWriter struct {
	header http.Header
}

func newFailWriter() *failWriter          { return &failWriter{header: make(http.Header)} }
func (f *failWriter) Header() http.Header { return f.header }
func (f *failWriter) WriteHeader(_ int)   {}
func (f *failWriter) Write(_ []byte) (int, error) {
	return 0, http.ErrHandlerTimeout
}

func TestWriteErrorXMLEncodeFailure(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	// Should not panic even when the ResponseWriter fails to write.
	writeError(newFailWriter(), req, http.StatusInternalServerError, "InternalError", "oops")
}

func TestWriteErrorSetsContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	w := httptest.NewRecorder()
	writeError(w, req, http.StatusNotFound, "NoSuchBucket", "The bucket does not exist.")
	assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestWriteNotImplemented(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	writeNotImplemented(w, req)
	assert.Equal(t, http.StatusNotImplemented, w.Code)
}
