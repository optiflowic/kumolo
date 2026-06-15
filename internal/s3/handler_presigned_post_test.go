package s3

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type presignedPostField struct {
	name, value string
}

// buildPresignedPostBody constructs a multipart/form-data body for presigned POST tests.
// fields are written in the given order; the file part is always appended last.
func buildPresignedPostBody(
	t *testing.T,
	fields []presignedPostField,
	fileContent []byte,
	filename string,
) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for _, f := range fields {
		require.NoError(t, mw.WriteField(f.name, f.value))
	}
	h := textproto.MIMEHeader{}
	cd := `form-data; name="file"`
	if filename != "" {
		cd += `; filename="` + filename + `"`
	}
	h.Set("Content-Disposition", cd)
	fw, err := mw.CreatePart(h)
	require.NoError(t, err)
	_, err = fw.Write(fileContent)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	return &b, mw.FormDataContentType()
}

// minPresignedFields returns the minimum SigV4 form fields needed for a valid presigned POST.
func minPresignedFields(key string) []presignedPostField {
	return []presignedPostField{
		{"key", key},
		{"x-amz-algorithm", "AWS4-HMAC-SHA256"},
		{"x-amz-credential", "AKID/20260615/us-east-1/s3/aws4_request"},
		{"x-amz-date", "20260615T000000Z"},
		{"x-amz-signature", "fakesig"},
		{"policy", "base64encodedpolicy"},
	}
}

func TestHandlePresignedPost(t *testing.T) {
	newBucket := func(t *testing.T) *Router {
		t.Helper()
		ro := newTestRouter(t)
		ro.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPut, "/test-bucket", nil),
		)
		return ro
	}

	t.Run("default response is 204 No Content", func(t *testing.T) {
		ro := newBucket(t)
		body, ct := buildPresignedPostBody(t,
			minPresignedFields("uploads/photo.jpg"),
			[]byte("hello world"), "photo.jpg",
		)
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, w.Body.String())

		// Verify object was stored.
		getW := httptest.NewRecorder()
		ro.ServeHTTP(
			getW,
			httptest.NewRequest(http.MethodGet, "/test-bucket/uploads/photo.jpg", nil),
		)
		assert.Equal(t, http.StatusOK, getW.Code)
		assert.Equal(t, "hello world", getW.Body.String())
	})

	t.Run("success_action_status=201 returns 201 with PostResponse XML", func(t *testing.T) {
		ro := newBucket(t)
		fields := append(minPresignedFields("uploads/doc.txt"),
			presignedPostField{"success_action_status", "201"},
		)
		body, ct := buildPresignedPostBody(t, fields, []byte("content"), "doc.txt")
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))
		bs := w.Body.String()
		assert.Contains(t, bs, "<PostResponse>")
		assert.Contains(t, bs, "<Bucket>test-bucket</Bucket>")
		assert.Contains(t, bs, "<Key>uploads/doc.txt</Key>")
		assert.Contains(t, bs, "<ETag>")
	})

	t.Run("success_action_status=200 returns 200 with PostResponse XML", func(t *testing.T) {
		ro := newBucket(t)
		fields := append(minPresignedFields("uploads/doc.txt"),
			presignedPostField{"success_action_status", "200"},
		)
		body, ct := buildPresignedPostBody(t, fields, []byte("content"), "doc.txt")
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "<PostResponse>")
	})

	t.Run(
		"success_action_redirect returns 303 with Location including query params",
		func(t *testing.T) {
			ro := newBucket(t)
			fields := append(minPresignedFields("uploads/photo.jpg"),
				presignedPostField{"success_action_redirect", "https://example.com/done"},
			)
			body, ct := buildPresignedPostBody(t, fields, []byte("data"), "photo.jpg")
			req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
			req.Header.Set("Content-Type", ct)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)

			assert.Equal(t, http.StatusSeeOther, w.Code)
			loc := w.Header().Get("Location")
			u, parseErr := url.Parse(loc)
			require.NoError(t, parseErr)
			q := u.Query()
			assert.Equal(t, "https", u.Scheme)
			assert.Equal(t, "example.com", u.Host)
			assert.Equal(t, "/done", u.Path)
			assert.Equal(t, "test-bucket", q.Get("bucket"))
			assert.Equal(t, "uploads/photo.jpg", q.Get("key"))
			assert.NotEmpty(t, q.Get("etag"))
		},
	)

	t.Run(
		"success_action_redirect takes precedence over success_action_status",
		func(t *testing.T) {
			ro := newBucket(t)
			fields := append(minPresignedFields("uploads/photo.jpg"),
				presignedPostField{"success_action_status", "201"},
				presignedPostField{"success_action_redirect", "https://example.com/done"},
			)
			body, ct := buildPresignedPostBody(t, fields, []byte("data"), "photo.jpg")
			req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
			req.Header.Set("Content-Type", ct)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)

			assert.Equal(t, http.StatusSeeOther, w.Code)
		},
	)

	t.Run("dollar-brace filename substitution in key", func(t *testing.T) {
		ro := newBucket(t)
		body, ct := buildPresignedPostBody(t,
			minPresignedFields("uploads/${filename}"),
			[]byte("data"), "image.png",
		)
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)

		getW := httptest.NewRecorder()
		ro.ServeHTTP(
			getW,
			httptest.NewRequest(http.MethodGet, "/test-bucket/uploads/image.png", nil),
		)
		assert.Equal(t, http.StatusOK, getW.Code)
	})

	t.Run("Content-Type field is stored on the object", func(t *testing.T) {
		ro := newBucket(t)
		fields := append(minPresignedFields("uploads/image.png"),
			presignedPostField{"Content-Type", "image/png"},
		)
		body, ct := buildPresignedPostBody(t, fields, []byte("\x89PNG"), "image.png")
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusNoContent, w.Code)

		getW := httptest.NewRecorder()
		ro.ServeHTTP(
			getW,
			httptest.NewRequest(http.MethodGet, "/test-bucket/uploads/image.png", nil),
		)
		assert.Equal(t, http.StatusOK, getW.Code)
		assert.Equal(t, "image/png", getW.Header().Get("Content-Type"))
	})

	t.Run("x-amz-meta-* fields are stored as user metadata", func(t *testing.T) {
		ro := newBucket(t)
		fields := append(minPresignedFields("uploads/file.txt"),
			presignedPostField{"x-amz-meta-author", "alice"},
		)
		body, ct := buildPresignedPostBody(t, fields, []byte("hello"), "file.txt")
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusNoContent, w.Code)

		getW := httptest.NewRecorder()
		ro.ServeHTTP(
			getW,
			httptest.NewRequest(http.MethodGet, "/test-bucket/uploads/file.txt", nil),
		)
		assert.Equal(t, http.StatusOK, getW.Code)
		assert.Equal(t, "alice", getW.Header().Get("X-Amz-Meta-Author"))
	})

	t.Run("missing key field returns 400 InvalidArgument", func(t *testing.T) {
		ro := newBucket(t)
		body, ct := buildPresignedPostBody(t, []presignedPostField{
			{"policy", "base64policy"},
			{"x-amz-algorithm", "AWS4-HMAC-SHA256"},
			{"x-amz-credential", "AKID/20260615/us-east-1/s3/aws4_request"},
			{"x-amz-date", "20260615T000000Z"},
			{"x-amz-signature", "fakesig"},
		}, []byte("data"), "file.txt")
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("no file part returns 400 InvalidArgument", func(t *testing.T) {
		ro := newBucket(t)
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		require.NoError(t, mw.WriteField("key", "uploads/file.txt"))
		require.NoError(t, mw.WriteField("policy", "base64policy"))
		require.NoError(t, mw.Close())
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", &b)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("non-existent bucket returns 404 NoSuchBucket", func(t *testing.T) {
		ro := newTestRouter(t)
		body, ct := buildPresignedPostBody(t,
			minPresignedFields("uploads/file.txt"),
			[]byte("data"), "file.txt",
		)
		req := httptest.NewRequest(http.MethodPost, "/nonexistent-bucket", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("non-multipart POST to bucket still returns 501", func(t *testing.T) {
		ro := newBucket(t)
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotImplemented, w.Code)
	})

	t.Run("multipart/form-data without boundary returns 400 InvalidArgument", func(t *testing.T) {
		ro := newBucket(t)
		req := httptest.NewRequest(
			http.MethodPost,
			"/test-bucket",
			strings.NewReader("not multipart"),
		)
		req.Header.Set("Content-Type", "multipart/form-data") // no boundary param
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("corrupted multipart body returns 400 InvalidArgument", func(t *testing.T) {
		ro := newBucket(t)
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", errReader{})
		req.Header.Set("Content-Type", "multipart/form-data; boundary=testboundary")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("invalid canned ACL returns 400 InvalidArgument", func(t *testing.T) {
		ro := newBucket(t)
		fields := []presignedPostField{
			{"key", "uploads/file.txt"},
			{"acl", "not-a-valid-acl"},
		}
		body, ct := buildPresignedPostBody(t, fields, []byte("data"), "file.txt")
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("valid canned ACL is applied to uploaded object", func(t *testing.T) {
		ro := newBucket(t)
		fields := append(minPresignedFields("uploads/photo.jpg"),
			presignedPostField{"acl", "public-read"},
		)
		body, ct := buildPresignedPostBody(t, fields, []byte("data"), "photo.jpg")
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusNoContent, w.Code)

		aclW := httptest.NewRecorder()
		ro.ServeHTTP(
			aclW,
			httptest.NewRequest(http.MethodGet, "/test-bucket/uploads/photo.jpg?acl", nil),
		)
		assert.Equal(t, http.StatusOK, aclW.Code)
		assert.Contains(t, aclW.Body.String(), "AllUsers")
		assert.Contains(t, aclW.Body.String(), "READ")
	})

	t.Run("PutObject internal error returns 500 InternalError", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{putObjectErr: errors.New("disk full")})
		body, ct := buildPresignedPostBody(t,
			minPresignedFields("uploads/file.txt"),
			[]byte("data"), "file.txt",
		)
		req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "InternalError")
	})

	t.Run(
		"malformed success_action_redirect falls back to raw URL as Location",
		func(t *testing.T) {
			ro := newBucket(t)
			fields := append(minPresignedFields("uploads/file.txt"),
				presignedPostField{"success_action_redirect", "\x00invalid"},
			)
			body, ct := buildPresignedPostBody(t, fields, []byte("data"), "file.txt")
			req := httptest.NewRequest(http.MethodPost, "/test-bucket", body)
			req.Header.Set("Content-Type", ct)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusSeeOther, w.Code)
			assert.Equal(t, "\x00invalid", w.Header().Get("Location"))
		},
	)
}

func TestIsPresignedTextField(t *testing.T) {
	known := []string{
		"key", "policy", "acl",
		"content-type", "content-disposition", "content-encoding",
		"cache-control", "expires",
		"success_action_status", "success_action_redirect",
		"x-amz-algorithm", "x-amz-credential", "x-amz-date",
		"x-amz-signature", "x-amz-security-token", "x-amz-storage-class",
		"x-amz-meta-author", "x-amz-custom-field",
		"awsaccesskeyid", "signature",
	}
	for _, name := range known {
		assert.True(t, isPresignedTextField(name), "expected %q to be a text field", name)
	}
	notText := []string{"file", "upload", "content", "data", "attachment"}
	for _, name := range notText {
		assert.False(t, isPresignedTextField(name), "expected %q NOT to be a text field", name)
	}
}
