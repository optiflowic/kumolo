package s3

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// bodyFailWriter accepts the first Write (XML declaration) but fails on subsequent writes.
type bodyFailWriter struct {
	header    http.Header
	callCount int
}

func newBodyFailWriter() *bodyFailWriter      { return &bodyFailWriter{header: make(http.Header)} }
func (b *bodyFailWriter) Header() http.Header { return b.header }
func (b *bodyFailWriter) WriteHeader(_ int)   {}
func (b *bodyFailWriter) Write(p []byte) (int, error) {
	b.callCount++
	if b.callCount == 1 {
		return len(p), nil
	}
	return 0, http.ErrHandlerTimeout
}

func TestWriteError(t *testing.T) {
	t.Run("sets Content-Type and status code", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
		w := httptest.NewRecorder()
		writeError(w, req, http.StatusNotFound, "NoSuchBucket", "The bucket does not exist.")
		assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("response body starts with XML declaration", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
		w := httptest.NewRecorder()
		writeError(w, req, http.StatusNotFound, "NoSuchBucket", "The bucket does not exist.")
		assert.True(t, strings.HasPrefix(w.Body.String(), "<?xml version="))
	})

	t.Run("logs warning when XML header write fails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
		writeError(newFailWriter(), req, http.StatusInternalServerError, "InternalError", "oops")
	})

	t.Run("logs warning when XML body encode fails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
		writeError(
			newBodyFailWriter(),
			req,
			http.StatusInternalServerError,
			"InternalError",
			"oops",
		)
	})
}

func TestWriteXML(t *testing.T) {
	t.Run("sets Content-Type and status code", func(t *testing.T) {
		w := httptest.NewRecorder()
		writeXML(w, http.StatusOK, struct {
			XMLName struct{} `xml:"Result"`
		}{})
		assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("logs warning when XML header write fails", func(t *testing.T) {
		writeXML(newFailWriter(), http.StatusOK, struct{}{})
	})

	t.Run("logs warning when XML body encode fails", func(t *testing.T) {
		writeXML(newBodyFailWriter(), http.StatusOK, struct{}{})
	})
}

func TestWriteNotImplemented(t *testing.T) {
	t.Run("returns 501 Not Implemented", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		writeNotImplemented(w, req)
		assert.Equal(t, http.StatusNotImplemented, w.Code)
	})
}
