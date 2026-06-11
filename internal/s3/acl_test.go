package s3

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCannedACL(t *testing.T) {
	tests := []struct {
		canned      string
		wantErr     bool
		wantAllRead bool // AllUsers has READ
		wantAllWrt  bool // AllUsers has WRITE
		wantAuthRd  bool // AuthenticatedUsers has READ
	}{
		{"private", false, false, false, false},
		{"public-read", false, true, false, false},
		{"public-read-write", false, true, true, false},
		{"authenticated-read", false, false, false, true},
		{"bucket-owner-read", false, false, false, false},
		{"bucket-owner-full-control", false, false, false, false},
		{"log-delivery-write", false, false, false, false},
		{"invalid-acl", true, false, false, false},
		{"", true, false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.canned, func(t *testing.T) {
			xml, err := buildCannedACL(tt.canned)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, xml, "owner")
			assert.Equal(t, tt.wantAllRead, aclAllowsAnonymous(xml, aclPermRead), "AllUsers READ")
			assert.Equal(t, tt.wantAllWrt, aclAllowsAnonymous(xml, aclPermWrite), "AllUsers WRITE")
		})
	}
}

func TestDefaultACLXML(t *testing.T) {
	xml := defaultACLXML()
	assert.Contains(t, xml, "FULL_CONTROL")
	assert.Contains(t, xml, aclOwnerID)
	// defaultACLXML is a private ACL (owner only): no AllUsers grants.
	assert.False(t, aclAllowsAnonymous(xml, aclPermRead))
	assert.False(t, aclAllowsAnonymous(xml, aclPermWrite))
}

func TestACLAllowsAnonymous(t *testing.T) {
	tests := []struct {
		name       string
		aclXML     string
		permission string
		want       bool
	}{
		{"empty ACL allows READ (no ACL configured)", "", aclPermRead, true},
		{"empty ACL allows WRITE (no ACL configured)", "", aclPermWrite, true},
		{"private denies READ", mustBuildCannedACL(t, "private"), aclPermRead, false},
		{"public-read allows READ", mustBuildCannedACL(t, "public-read"), aclPermRead, true},
		{"public-read denies WRITE", mustBuildCannedACL(t, "public-read"), aclPermWrite, false},
		{
			"public-read-write allows READ",
			mustBuildCannedACL(t, "public-read-write"),
			aclPermRead,
			true,
		},
		{
			"public-read-write allows WRITE",
			mustBuildCannedACL(t, "public-read-write"),
			aclPermWrite,
			true,
		},
		{
			"authenticated-read denies anon READ",
			mustBuildCannedACL(t, "authenticated-read"),
			aclPermRead,
			false,
		},
		{"invalid XML denies", "not xml", aclPermRead, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, aclAllowsAnonymous(tt.aclXML, tt.permission))
		})
	}
}

func mustBuildCannedACL(t *testing.T, canned string) string {
	t.Helper()
	xml, err := buildCannedACL(canned)
	require.NoError(t, err)
	return xml
}

func TestIsAnonymousRequest(t *testing.T) {
	t.Run("no auth header is anonymous", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		assert.True(t, isAnonymousRequest(r))
	})

	t.Run("Authorization header is authenticated", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=...")
		assert.False(t, isAnonymousRequest(r))
	})

	t.Run("presigned URL is authenticated", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/?X-Amz-Signature=abc", nil)
		assert.False(t, isAnonymousRequest(r))
	})
}

func TestACLStorageRoundtrip(t *testing.T) {
	s, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	require.NoError(t, s.CreateBucket("bucket", "us-east-1", false))

	t.Run("bucket ACL default is empty", func(t *testing.T) {
		acl, err := s.GetBucketACL("bucket")
		require.NoError(t, err)
		assert.Empty(t, acl)
	})

	t.Run("PutBucketACL stores and retrieves", func(t *testing.T) {
		xml := mustBuildCannedACL(t, "public-read")
		require.NoError(t, s.PutBucketACL("bucket", xml))

		got, err := s.GetBucketACL("bucket")
		require.NoError(t, err)
		assert.Equal(t, xml, got)
	})

	t.Run("PutBucketACL unknown bucket returns error", func(t *testing.T) {
		err := s.PutBucketACL("no-such-bucket", "xml")
		require.Error(t, err)
	})

	_, err = s.PutObject(
		"bucket",
		"obj",
		strings.NewReader("data"),
		"text/plain",
		nil,
		"",
		"",
		false,
		"",
		nil,
		nil,
		"",
	)
	require.NoError(t, err)

	t.Run("object ACL default is empty", func(t *testing.T) {
		acl, err := s.GetObjectACL("bucket", "obj")
		require.NoError(t, err)
		assert.Empty(t, acl)
	})

	t.Run("PutObjectACL stores and retrieves", func(t *testing.T) {
		xml := mustBuildCannedACL(t, "public-read")
		require.NoError(t, s.PutObjectACL("bucket", "obj", xml))

		got, err := s.GetObjectACL("bucket", "obj")
		require.NoError(t, err)
		assert.Equal(t, xml, got)
	})

	t.Run("PutObjectACL unknown key returns error", func(t *testing.T) {
		err := s.PutObjectACL("bucket", "no-such-key", "xml")
		require.Error(t, err)
	})
}

func TestACLHandlers(t *testing.T) {
	ro := newTestRouter(t)

	createBucket := func(t *testing.T, bucket string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/"+bucket, nil)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}

	putObject := func(t *testing.T, bucket, key, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/"+bucket+"/"+key, strings.NewReader(body))
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}

	t.Run("GetBucketACL returns default private ACL when unset", func(t *testing.T) {
		createBucket(t, "b-get-default")
		req := httptest.NewRequest(http.MethodGet, "/b-get-default?acl", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "AccessControlPolicy")
		assert.Contains(t, body, "FULL_CONTROL")
	})

	t.Run("PutBucketACL with canned ACL header", func(t *testing.T) {
		createBucket(t, "b-put-canned")
		req := httptest.NewRequest(http.MethodPut, "/b-put-canned?acl", nil)
		req.Header.Set(amzACL, "public-read")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		// Verify stored ACL
		req2 := httptest.NewRequest(http.MethodGet, "/b-put-canned?acl", nil)
		w2 := httptest.NewRecorder()
		ro.ServeHTTP(w2, req2)
		assert.Equal(t, http.StatusOK, w2.Code)
		assert.Contains(t, w2.Body.String(), "AllUsers")
	})

	t.Run("PutBucketACL with XML body", func(t *testing.T) {
		createBucket(t, "b-put-xml")
		xml := mustBuildCannedACL(t, "public-read")
		req := httptest.NewRequest(http.MethodPut, "/b-put-xml?acl", strings.NewReader(xml))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("PutBucketACL invalid canned ACL returns 400", func(t *testing.T) {
		createBucket(t, "b-put-bad-acl")
		req := httptest.NewRequest(http.MethodPut, "/b-put-bad-acl?acl", nil)
		req.Header.Set(amzACL, "not-a-valid-acl")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("GetObjectACL returns default private ACL when unset", func(t *testing.T) {
		createBucket(t, "b-obj-acl-default")
		putObject(t, "b-obj-acl-default", "key", "data")

		req := httptest.NewRequest(http.MethodGet, "/b-obj-acl-default/key?acl", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "FULL_CONTROL")
	})

	t.Run("PutObjectACL with canned ACL header", func(t *testing.T) {
		createBucket(t, "b-obj-put-acl")
		putObject(t, "b-obj-put-acl", "key", "data")

		req := httptest.NewRequest(http.MethodPut, "/b-obj-put-acl/key?acl", nil)
		req.Header.Set(amzACL, "public-read")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		// Verify stored ACL
		req2 := httptest.NewRequest(http.MethodGet, "/b-obj-put-acl/key?acl", nil)
		w2 := httptest.NewRecorder()
		ro.ServeHTTP(w2, req2)
		assert.Equal(t, http.StatusOK, w2.Code)
		assert.Contains(t, w2.Body.String(), "AllUsers")
	})

	t.Run("GetBucketACL unknown bucket returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/no-such-bucket?acl", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("GetObjectACL unknown key returns 404", func(t *testing.T) {
		createBucket(t, "b-acl-nokey")
		req := httptest.NewRequest(http.MethodGet, "/b-acl-nokey/no-such-key?acl", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run(
		"PutBucketACL XML prolog body — GET returns exactly one <?xml declaration",
		func(t *testing.T) {
			createBucket(t, "b-prolog-bucket")
			xmlWithProlog := `<?xml version="1.0" encoding="UTF-8"?>` + mustBuildCannedACL(
				t,
				"public-read",
			)
			req := httptest.NewRequest(
				http.MethodPut,
				"/b-prolog-bucket?acl",
				strings.NewReader(xmlWithProlog),
			)
			req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)

			req2 := httptest.NewRequest(http.MethodGet, "/b-prolog-bucket?acl", nil)
			w2 := httptest.NewRecorder()
			ro.ServeHTTP(w2, req2)
			assert.Equal(t, http.StatusOK, w2.Code)
			body := w2.Body.String()
			assert.Equal(t, 1, strings.Count(body, "<?xml"), "expected exactly one XML declaration")
			assert.Contains(t, body, "AllUsers")
		},
	)

	t.Run(
		"PutObjectACL XML prolog body — GET returns exactly one <?xml declaration",
		func(t *testing.T) {
			createBucket(t, "b-prolog-obj")
			putObject(t, "b-prolog-obj", "key", "data")

			xmlWithProlog := `<?xml version="1.0" encoding="UTF-8"?>` + mustBuildCannedACL(
				t,
				"public-read",
			)
			req := httptest.NewRequest(
				http.MethodPut,
				"/b-prolog-obj/key?acl",
				strings.NewReader(xmlWithProlog),
			)
			req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)

			req2 := httptest.NewRequest(http.MethodGet, "/b-prolog-obj/key?acl", nil)
			w2 := httptest.NewRecorder()
			ro.ServeHTTP(w2, req2)
			assert.Equal(t, http.StatusOK, w2.Code)
			body := w2.Body.String()
			assert.Equal(t, 1, strings.Count(body, "<?xml"), "expected exactly one XML declaration")
			assert.Contains(t, body, "AllUsers")
		},
	)
}

func TestACLEnforcement(t *testing.T) {
	ro := newTestRouter(t)

	authedPut := func(t *testing.T, path, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "authed PUT failed: %s", path)
	}

	anonGet := func(t *testing.T, path string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		return w.Code
	}

	anonPut := func(t *testing.T, path string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader("data"))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		return w.Code
	}

	anonDelete := func(t *testing.T, path string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		return w.Code
	}

	t.Run("private bucket: anonymous GetObject denied", func(t *testing.T) {
		authedPut(t, "/priv-bucket", "")
		// Explicitly set bucket ACL to private so anonymous access is denied.
		reqACL := httptest.NewRequest(http.MethodPut, "/priv-bucket?acl", nil)
		reqACL.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		reqACL.Header.Set(amzACL, "private")
		wACL := httptest.NewRecorder()
		ro.ServeHTTP(wACL, reqACL)
		require.Equal(t, http.StatusOK, wACL.Code)

		authedPut(t, "/priv-bucket/obj", "data")
		// Also set object ACL to private.
		reqObjACL := httptest.NewRequest(http.MethodPut, "/priv-bucket/obj?acl", nil)
		reqObjACL.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		reqObjACL.Header.Set(amzACL, "private")
		wObjACL := httptest.NewRecorder()
		ro.ServeHTTP(wObjACL, reqObjACL)
		require.Equal(t, http.StatusOK, wObjACL.Code)

		assert.Equal(t, http.StatusForbidden, anonGet(t, "/priv-bucket/obj"))
	})

	t.Run("public-read object: anonymous GetObject allowed", func(t *testing.T) {
		authedPut(t, "/pub-read-bucket", "")
		req := httptest.NewRequest(
			http.MethodPut,
			"/pub-read-bucket/pub-obj",
			strings.NewReader("data"),
		)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		req.Header.Set(amzACL, "public-read")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)

		assert.Equal(t, http.StatusOK, anonGet(t, "/pub-read-bucket/pub-obj"))
	})

	t.Run("public-read object: anonymous PUT still denied", func(t *testing.T) {
		// bucket has private ACL → anon PUT denied regardless of object ACL
		assert.Equal(t, http.StatusForbidden, anonPut(t, "/priv-bucket/new-obj"))
	})

	t.Run("public-read-write bucket: anonymous PUT allowed", func(t *testing.T) {
		authedPut(t, "/pub-rw-bucket", "")
		// Set bucket ACL to public-read-write via authed request
		reqACL := httptest.NewRequest(http.MethodPut, "/pub-rw-bucket?acl", nil)
		reqACL.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		reqACL.Header.Set(amzACL, "public-read-write")
		wACL := httptest.NewRecorder()
		ro.ServeHTTP(wACL, reqACL)
		require.Equal(t, http.StatusOK, wACL.Code)

		assert.Equal(t, http.StatusOK, anonPut(t, "/pub-rw-bucket/anon-obj"))
	})

	t.Run("public-read-write bucket: anonymous DELETE allowed", func(t *testing.T) {
		authedPut(t, "/pub-rw-bucket/del-target", "data")
		assert.Equal(t, http.StatusNoContent, anonDelete(t, "/pub-rw-bucket/del-target"))
	})

	t.Run("private bucket: anonymous ListObjects denied", func(t *testing.T) {
		// priv-bucket has explicit private ACL set earlier in this test suite
		assert.Equal(t, http.StatusForbidden, anonGet(t, "/priv-bucket"))
	})

	t.Run("public-read bucket: anonymous ListObjects allowed", func(t *testing.T) {
		authedPut(t, "/pub-list-bucket", "")
		reqACL := httptest.NewRequest(http.MethodPut, "/pub-list-bucket?acl", nil)
		reqACL.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		reqACL.Header.Set(amzACL, "public-read")
		wACL := httptest.NewRecorder()
		ro.ServeHTTP(wACL, reqACL)
		require.Equal(t, http.StatusOK, wACL.Code)

		req := httptest.NewRequest(http.MethodGet, "/pub-list-bucket", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("PutObject with x-amz-acl sets object ACL", func(t *testing.T) {
		authedPut(t, "/acl-put-bucket", "")
		req := httptest.NewRequest(
			http.MethodPut,
			"/acl-put-bucket/pub-obj",
			strings.NewReader("data"),
		)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		req.Header.Set(amzACL, "public-read")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)

		// Anonymous can read the object
		assert.Equal(t, http.StatusOK, anonGet(t, "/acl-put-bucket/pub-obj"))
	})

	t.Run("PutObject with invalid x-amz-acl returns 400", func(t *testing.T) {
		authedPut(t, "/acl-invalid-bucket", "")
		req := httptest.NewRequest(
			http.MethodPut,
			"/acl-invalid-bucket/obj",
			strings.NewReader("data"),
		)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		req.Header.Set(amzACL, "bad-acl")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("authenticated request always allowed regardless of ACL", func(t *testing.T) {
		authedPut(t, "/auth-priv-bucket", "")
		authedPut(t, "/auth-priv-bucket/obj", "data")

		req := httptest.NewRequest(http.MethodGet, "/auth-priv-bucket/obj", nil)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestParseACLBody(t *testing.T) {
	t.Run("valid XML is accepted", func(t *testing.T) {
		xml := mustBuildCannedACL(t, "public-read")
		result, err := parseACLBody([]byte(xml))
		require.NoError(t, err)
		assert.Equal(t, xml, result)
	})

	t.Run("invalid XML is rejected", func(t *testing.T) {
		_, err := parseACLBody([]byte("<not valid xml"))
		require.Error(t, err)
	})
}

// anonHeadStatus makes an anonymous HEAD request and returns the status code.
func anonHeadStatus(t *testing.T, ro *Router, path string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodHead, path, nil)
	w := httptest.NewRecorder()
	ro.ServeHTTP(w, req)
	return w.Code
}

func TestACLHeadEnforcement(t *testing.T) {
	ro := newTestRouter(t)

	// Create bucket and object, then explicitly set private ACL on both.
	for _, path := range []string{"/head-priv", "/head-priv/obj"} {
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader("data"))
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}
	for _, path := range []string{"/head-priv?acl", "/head-priv/obj?acl"} {
		req := httptest.NewRequest(http.MethodPut, path, nil)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		req.Header.Set(amzACL, "private")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}

	t.Run("private object: anonymous HEAD denied", func(t *testing.T) {
		assert.Equal(t, http.StatusForbidden, anonHeadStatus(t, ro, "/head-priv/obj"))
	})

	t.Run("public-read object: anonymous HEAD allowed", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodPut,
			"/head-priv/obj?acl",
			strings.NewReader(""),
		)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		req.Header.Set(amzACL, "public-read")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)

		assert.Equal(t, http.StatusOK, anonHeadStatus(t, ro, "/head-priv/obj"))
	})
}

// TestACLHandlerErrorPaths covers 500-level and MalformedXML paths using mock storage.
func TestACLHandlerErrorPaths(t *testing.T) {
	t.Run("GetBucketACL 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getBucketACLErr: errors.New("disk fail"),
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?acl", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "InternalError")
	})

	t.Run("PutBucketACL MalformedXML when no body and no header", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: true})
		req := httptest.NewRequest(http.MethodPut, "/b?acl", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "MalformedXML")
	})

	t.Run("PutBucketACL 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			putBucketACLErr: errors.New("disk fail"),
		})
		req := httptest.NewRequest(http.MethodPut, "/b?acl", nil)
		req.Header.Set(amzACL, "private")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "InternalError")
	})

	t.Run("GetObjectACL 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getObjectACLErr: errors.New("disk fail"),
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b/k?acl", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "InternalError")
	})

	t.Run("PutObjectACL MalformedXML when no body and no header", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: true})
		req := httptest.NewRequest(http.MethodPut, "/b/k?acl", nil)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "MalformedXML")
	})

	t.Run("PutObjectACL 500 on storage error", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			putObjectACLErr: errors.New("disk fail"),
		})
		req := httptest.NewRequest(http.MethodPut, "/b/k?acl", nil)
		req.Header.Set(amzACL, "private")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "InternalError")
	})
}

// TestACLEnforcementNewOps covers ACL enforcement for the operations added in the review pass.
func TestACLEnforcementNewOps(t *testing.T) {
	privateBucketACL := mustBuildCannedACL(t, "private")
	privateObjACL := mustBuildCannedACL(t, "private")

	t.Run("ListObjects: anonymous denied by private ACL", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:       true,
			getBucketACLResult: privateBucketACL,
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b", nil))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("ListObjects: anonymous + GetBucketACL 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getBucketACLErr: errors.New("disk fail"),
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("ListObjectsV2: anonymous denied by private ACL", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:       true,
			getBucketACLResult: privateBucketACL,
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?list-type=2", nil))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("ListObjectsV2: anonymous + GetBucketACL bucket not found → 404", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: false})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?list-type=2", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	t.Run("ListObjectsV2: anonymous + GetBucketACL 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getBucketACLErr: errors.New("disk fail"),
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?list-type=2", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("DeleteObjects: anonymous denied by private ACL", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:       true,
			getBucketACLResult: privateBucketACL,
		})
		body := `<Delete><Object><Key>k</Key></Object></Delete>`
		req := httptest.NewRequest(http.MethodPost, "/b?delete", strings.NewReader(body))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("DeleteObjects: anonymous + GetBucketACL bucket not found → 404", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: false})
		body := `<Delete><Object><Key>k</Key></Object></Delete>`
		req := httptest.NewRequest(http.MethodPost, "/b?delete", strings.NewReader(body))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("DeleteObjects: anonymous + GetBucketACL 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getBucketACLErr: errors.New("disk fail"),
		})
		body := `<Delete><Object><Key>k</Key></Object></Delete>`
		req := httptest.NewRequest(http.MethodPost, "/b?delete", strings.NewReader(body))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("ListObjectVersions: anonymous denied by private ACL", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:       true,
			getBucketACLResult: privateBucketACL,
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?versions", nil))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run(
		"ListObjectVersions: anonymous + GetBucketACL bucket not found → 404",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{bucketExists: false})
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?versions", nil))
			assert.Equal(t, http.StatusNotFound, w.Code)
		},
	)

	t.Run("ListObjectVersions: anonymous + GetBucketACL 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getBucketACLErr: errors.New("disk fail"),
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/b?versions", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("GetObjectAttributes: anonymous denied by private object ACL", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:   true,
			headObjectMeta: ObjectMetadata{ACL: privateObjACL},
		})
		req := httptest.NewRequest(http.MethodGet, "/b/k?attributes", nil)
		req.Header.Set(amzObjectAttributes, "ETag")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("PutObject: anonymous + GetBucketACL 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getBucketACLErr: errors.New("disk fail"),
		})
		req := httptest.NewRequest(http.MethodPut, "/b/k", strings.NewReader("data"))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("DeleteObject: anonymous denied by private ACL", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:       true,
			getBucketACLResult: privateBucketACL,
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b/k", nil))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("DeleteObject: anonymous + GetBucketACL 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getBucketACLErr: errors.New("disk fail"),
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b/k", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("PutObject: PutObjectACL fails — rolls back and returns 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			putObjectMeta:   ObjectMetadata{ETag: `"abc"`},
			putObjectACLErr: errors.New("acl write fail"),
		})
		req := httptest.NewRequest(http.MethodPut, "/b/k", strings.NewReader("data"))
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		req.Header.Set(amzACL, "public-read")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "InternalError")
	})

	t.Run(
		"PutObject: ACL rollback versioned — deleteObjectVersion succeeds → 500",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{
				bucketExists:    true,
				putObjectMeta:   ObjectMetadata{ETag: `"abc"`, VersionID: "v1"},
				putObjectACLErr: errors.New("acl write fail"),
			})
			req := httptest.NewRequest(http.MethodPut, "/b/k", strings.NewReader("data"))
			req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
			req.Header.Set(amzACL, "public-read")
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusInternalServerError, w.Code)
			assert.Contains(t, w.Body.String(), "InternalError")
		},
	)

	t.Run("PutObject: ACL rollback non-versioned delete fails — still 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			putObjectMeta:   ObjectMetadata{ETag: `"abc"`},
			putObjectACLErr: errors.New("acl write fail"),
			deleteObjectErr: errors.New("delete fail"),
		})
		req := httptest.NewRequest(http.MethodPut, "/b/k", strings.NewReader("data"))
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		req.Header.Set(amzACL, "public-read")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "InternalError")
	})

	t.Run("PutObject: ACL rollback versioned delete fails — still 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:           true,
			putObjectMeta:          ObjectMetadata{ETag: `"abc"`, VersionID: "v1"},
			putObjectACLErr:        errors.New("acl write fail"),
			deleteObjectVersionErr: errors.New("delete fail"),
		})
		req := httptest.NewRequest(http.MethodPut, "/b/k", strings.NewReader("data"))
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		req.Header.Set(amzACL, "public-read")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "InternalError")
	})

	// CopyObject ACL enforcement
	t.Run("CopyObject: anonymous source read denied by private object ACL", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:   true,
			headObjectMeta: ObjectMetadata{ACL: privateObjACL},
		})
		req := httptest.NewRequest(http.MethodPut, "/b/dst", nil)
		req.Header.Set(amzCopySource, "/b/src")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run(
		"CopyObject: anonymous destination write denied by private bucket ACL",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{
				bucketExists:       true,
				headObjectMeta:     ObjectMetadata{ACL: ""},
				getBucketACLResult: privateBucketACL,
			})
			req := httptest.NewRequest(http.MethodPut, "/b/dst", nil)
			req.Header.Set(amzCopySource, "/b/src")
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusForbidden, w.Code)
		},
	)

	t.Run(
		"CopyObject: anonymous + destination GetBucketACL bucket not found → 404",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{
				bucketExists:   false,
				headObjectMeta: ObjectMetadata{ACL: ""},
			})
			req := httptest.NewRequest(http.MethodPut, "/b/dst", nil)
			req.Header.Set(amzCopySource, "/b/src")
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		},
	)

	t.Run("CopyObject: anonymous + destination GetBucketACL 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			headObjectMeta:  ObjectMetadata{ACL: ""},
			getBucketACLErr: errors.New("disk fail"),
		})
		req := httptest.NewRequest(http.MethodPut, "/b/dst", nil)
		req.Header.Set(amzCopySource, "/b/src")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	// CreateMultipartUpload ACL enforcement
	t.Run("CreateMultipartUpload: anonymous denied by private bucket ACL", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:       true,
			getBucketACLResult: privateBucketACL,
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/b/k?uploads", nil))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run(
		"CreateMultipartUpload: anonymous + GetBucketACL bucket not found → 404",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{bucketExists: false})
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/b/k?uploads", nil))
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		},
	)

	t.Run("CreateMultipartUpload: anonymous + GetBucketACL 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getBucketACLErr: errors.New("disk fail"),
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/b/k?uploads", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	// UploadPart ACL enforcement
	t.Run("UploadPart: anonymous denied by private bucket ACL", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:       true,
			getBucketACLResult: privateBucketACL,
		})
		req := httptest.NewRequest(
			http.MethodPut,
			"/b/k?partNumber=1&uploadId=uid",
			strings.NewReader("data"),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("UploadPart: anonymous + GetBucketACL bucket not found → 404", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: false})
		req := httptest.NewRequest(
			http.MethodPut,
			"/b/k?partNumber=1&uploadId=uid",
			strings.NewReader("data"),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("UploadPart: anonymous + GetBucketACL 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getBucketACLErr: errors.New("disk fail"),
		})
		req := httptest.NewRequest(
			http.MethodPut,
			"/b/k?partNumber=1&uploadId=uid",
			strings.NewReader("data"),
		)
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	// UploadPartCopy ACL enforcement
	t.Run(
		"UploadPartCopy: anonymous destination write denied by private bucket ACL",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{
				bucketExists:       true,
				getBucketACLResult: privateBucketACL,
			})
			req := httptest.NewRequest(http.MethodPut, "/b/k?partNumber=1&uploadId=uid", nil)
			req.Header.Set(amzCopySource, "/src-b/src-k")
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusForbidden, w.Code)
		},
	)

	t.Run("UploadPartCopy: anonymous source read denied by private object ACL", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:   true,
			headObjectMeta: ObjectMetadata{ACL: privateObjACL},
		})
		req := httptest.NewRequest(http.MethodPut, "/b/k?partNumber=1&uploadId=uid", nil)
		req.Header.Set(amzCopySource, "/src-b/src-k")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run(
		"UploadPartCopy: anonymous + destination GetBucketACL bucket not found → 404",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{bucketExists: false})
			req := httptest.NewRequest(http.MethodPut, "/b/k?partNumber=1&uploadId=uid", nil)
			req.Header.Set(amzCopySource, "/src-b/src-k")
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		},
	)

	t.Run("UploadPartCopy: anonymous + destination GetBucketACL 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getBucketACLErr: errors.New("disk fail"),
		})
		req := httptest.NewRequest(http.MethodPut, "/b/k?partNumber=1&uploadId=uid", nil)
		req.Header.Set(amzCopySource, "/src-b/src-k")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	// CompleteMultipartUpload ACL enforcement
	t.Run("CompleteMultipartUpload: anonymous denied by private bucket ACL", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:       true,
			getBucketACLResult: privateBucketACL,
		})
		body := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"e"</ETag></Part></CompleteMultipartUpload>`
		req := httptest.NewRequest(http.MethodPost, "/b/k?uploadId=uid", strings.NewReader(body))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run(
		"CompleteMultipartUpload: anonymous + GetBucketACL bucket not found → 404",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{bucketExists: false})
			body := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"e"</ETag></Part></CompleteMultipartUpload>`
			req := httptest.NewRequest(
				http.MethodPost,
				"/b/k?uploadId=uid",
				strings.NewReader(body),
			)
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		},
	)

	t.Run("CompleteMultipartUpload: anonymous + GetBucketACL 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getBucketACLErr: errors.New("disk fail"),
		})
		body := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"e"</ETag></Part></CompleteMultipartUpload>`
		req := httptest.NewRequest(http.MethodPost, "/b/k?uploadId=uid", strings.NewReader(body))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	// AbortMultipartUpload ACL enforcement
	t.Run("AbortMultipartUpload: anonymous denied by private bucket ACL", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:       true,
			getBucketACLResult: privateBucketACL,
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b/k?uploadId=uid", nil))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run(
		"AbortMultipartUpload: anonymous + GetBucketACL bucket not found → 404",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{bucketExists: false})
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b/k?uploadId=uid", nil))
			assert.Equal(t, http.StatusNotFound, w.Code)
		},
	)

	t.Run("AbortMultipartUpload: anonymous + GetBucketACL 500", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			getBucketACLErr: errors.New("disk fail"),
		})
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/b/k?uploadId=uid", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// TestACLCoverageGaps covers storage-error paths that are reachable but were previously untested.
func TestACLCoverageGaps(t *testing.T) {
	t.Run("PutObjectACL with invalid XML body returns 400 InvalidArgument", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: true})
		req := httptest.NewRequest(http.MethodPut, "/b/k?acl", strings.NewReader("<not-valid-xml"))
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	// Anonymous PutObject: ACL check passes, then putFn returns ErrBucketNotFound (race-like).
	t.Run(
		"PutObject anonymous: ACL passes then putFn returns ErrBucketNotFound → 404",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{bucketExists: true, putObjectErr: ErrBucketNotFound})
			req := httptest.NewRequest(http.MethodPut, "/b/k", strings.NewReader("data"))
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		},
	)

	// DeleteObject with versionId: DeleteObjectVersion returns ErrBucketNotFound.
	t.Run(
		"DeleteObject versionId: DeleteObjectVersion ErrBucketNotFound → 404",
		func(t *testing.T) {
			ro := newRouterWithMock(&mockStore{
				bucketExists:           true,
				deleteObjectVersionErr: ErrBucketNotFound,
			})
			req := httptest.NewRequest(http.MethodDelete, "/b/k?versionId=abc", nil)
			req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
			w := httptest.NewRecorder()
			ro.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Contains(t, w.Body.String(), "NoSuchBucket")
		},
	)

	// DeleteObject without versionId: DeleteObject returns ErrBucketNotFound.
	t.Run("DeleteObject: DeleteObject ErrBucketNotFound → 404", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{
			bucketExists:    true,
			deleteObjectErr: ErrBucketNotFound,
		})
		req := httptest.NewRequest(http.MethodDelete, "/b/k", nil)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	// ListObjects: bucket vanished between ACL check and ListObjects call.
	t.Run("ListObjects: ListObjects ErrBucketNotFound → 404", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{listObjectsErr: ErrBucketNotFound})
		req := httptest.NewRequest(http.MethodGet, "/b", nil)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	// ListObjectsV2: same race pattern.
	t.Run("ListObjectsV2: ListObjects ErrBucketNotFound → 404", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{listObjectsErr: ErrBucketNotFound})
		req := httptest.NewRequest(http.MethodGet, "/b?list-type=2", nil)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	// DeleteObjects authenticated: bucket does not exist → BucketExists returns false → 404.
	t.Run("DeleteObjects authenticated: BucketExists false → 404", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{bucketExists: false})
		body := `<Delete><Object><Key>k</Key></Object></Delete>`
		req := httptest.NewRequest(http.MethodPost, "/b?delete", strings.NewReader(body))
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})

	// ListObjectVersions: bucket vanished between ACL check and ListObjectVersions call.
	t.Run("ListObjectVersions: ErrBucketNotFound from storage → 404", func(t *testing.T) {
		ro := newRouterWithMock(&mockStore{listObjectVersionsErr: ErrBucketNotFound})
		req := httptest.NewRequest(http.MethodGet, "/b?versions", nil)
		req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test")
		w := httptest.NewRecorder()
		ro.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "NoSuchBucket")
	})
}

// TestACLStorageBucketNotFound covers the bucket-not-found paths in PutObjectACL / GetObjectACL.
func TestACLStorageBucketNotFound(t *testing.T) {
	s, err := NewStorage(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	t.Run("PutObjectACL unknown bucket returns ErrBucketNotFound", func(t *testing.T) {
		err := s.PutObjectACL("no-such-bucket", "key", "xml")
		require.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("GetObjectACL unknown bucket returns ErrBucketNotFound", func(t *testing.T) {
		_, err := s.GetObjectACL("no-such-bucket", "key")
		require.ErrorIs(t, err, ErrBucketNotFound)
	})
}
