package sts

import (
	"encoding/xml"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBuffer captures log output in tests.
type testBuffer struct{ buf []byte }

func (b *testBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *testBuffer) String() string { return string(b.buf) }

func setLogger(t *testing.T, buf *testBuffer) {
	t.Helper()
	orig := slog.Default()
	slog.SetDefault(
		slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	)
	t.Cleanup(func() { slog.SetDefault(orig) })
}

// failWriter is an http.ResponseWriter whose Write fails after failAfter successful calls.
type failWriter struct {
	header     http.Header
	failAfter  int
	writeCount int
}

func newFailWriter(failAfter int) *failWriter {
	return &failWriter{header: http.Header{}, failAfter: failAfter}
}

func (w *failWriter) Header() http.Header { return w.header }
func (w *failWriter) WriteHeader(int)     {}
func (w *failWriter) Write(b []byte) (int, error) {
	w.writeCount++
	if w.writeCount > w.failAfter {
		return 0, errors.New("write error")
	}
	return len(b), nil
}

func stsRequest(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, req)
	return w
}

func stsAction(t *testing.T, action string) *httptest.ResponseRecorder {
	t.Helper()
	return stsRequest(t, "Action="+action+"&Version=2011-06-15")
}

func TestHandleGetCallerIdentity(t *testing.T) {
	w := stsAction(t, "GetCallerIdentity")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/xml")

	var resp getCallerIdentityResponse
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, fixedAccount, resp.GetCallerIdentityResult.Account)
	assert.Equal(t, fixedARN, resp.GetCallerIdentityResult.Arn)
	assert.Equal(t, fixedUserID, resp.GetCallerIdentityResult.UserID)
	assert.Equal(t, requestID, resp.ResponseMetadata.RequestID)
}

func TestHandleAssumeRole(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantARN     string
		wantRoleID  string
		wantErrCode string
	}{
		{
			name:       "valid request",
			body:       "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role&RoleSessionName=my-session",
			wantStatus: http.StatusOK,
			wantARN:    "arn:aws:sts::000000000000:assumed-role/my-role/my-session",
			wantRoleID: fixedRoleIDPrefix + ":my-session",
		},
		{
			name:        "missing RoleArn",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleSessionName=my-session",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "missing RoleSessionName",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "RoleArn with no slash",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role-no-slash&RoleSessionName=my-session",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "RoleArn ending with slash",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/&RoleSessionName=my-session",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "RoleArn too short (< 20 chars)",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam:role/x&RoleSessionName=my-session",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name: "RoleArn too long (> 2048 chars)",
			body: "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/" + strings.Repeat(
				"a",
				2020,
			) + "&RoleSessionName=my-session",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "RoleSessionName too short (1 char)",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role&RoleSessionName=x",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name: "RoleSessionName too long (65 chars)",
			body: "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role&RoleSessionName=" + strings.Repeat(
				"a",
				65,
			),
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "RoleSessionName invalid characters",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role&RoleSessionName=bad!name",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:       "DurationSeconds at minimum (900)",
			body:       "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role&RoleSessionName=my-session&DurationSeconds=900",
			wantStatus: http.StatusOK,
			wantARN:    "arn:aws:sts::000000000000:assumed-role/my-role/my-session",
			wantRoleID: fixedRoleIDPrefix + ":my-session",
		},
		{
			name:       "DurationSeconds at maximum (43200)",
			body:       "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role&RoleSessionName=my-session&DurationSeconds=43200",
			wantStatus: http.StatusOK,
			wantARN:    "arn:aws:sts::000000000000:assumed-role/my-role/my-session",
			wantRoleID: fixedRoleIDPrefix + ":my-session",
		},
		{
			name:        "DurationSeconds below minimum (0)",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role&RoleSessionName=my-session&DurationSeconds=0",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "DurationSeconds below minimum (899)",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role&RoleSessionName=my-session&DurationSeconds=899",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "DurationSeconds above maximum (43201)",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role&RoleSessionName=my-session&DurationSeconds=43201",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "DurationSeconds non-numeric",
			body:        "Action=AssumeRole&Version=2011-06-15&RoleArn=arn:aws:iam::123456789012:role/my-role&RoleSessionName=my-session&DurationSeconds=abc",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := stsRequest(t, tt.body)
			assert.Equal(t, tt.wantStatus, w.Code)
			if tt.wantErrCode != "" {
				var errResp errorResponse
				require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &errResp))
				assert.Equal(t, tt.wantErrCode, errResp.Error.Code)
				return
			}
			var resp assumeRoleResponse
			require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &resp))
			creds := resp.AssumeRoleResult.Credentials
			assert.Equal(t, fixedAccessKeyID, creds.AccessKeyID)
			assert.Equal(t, fixedSecretKey, creds.SecretAccessKey)
			assert.Equal(t, fixedSessionToken, creds.SessionToken)
			assert.Equal(t, fixedExpiration, creds.Expiration)
			assert.Equal(t, tt.wantARN, resp.AssumeRoleResult.AssumedRoleUser.Arn)
			assert.Equal(t, tt.wantRoleID, resp.AssumeRoleResult.AssumedRoleUser.AssumedRoleID)
		})
	}
}

func TestHandleGetSessionToken(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantErrCode string
	}{
		{
			name:       "no DurationSeconds",
			body:       "Action=GetSessionToken&Version=2011-06-15",
			wantStatus: http.StatusOK,
		},
		{
			name:       "DurationSeconds at minimum (900)",
			body:       "Action=GetSessionToken&Version=2011-06-15&DurationSeconds=900",
			wantStatus: http.StatusOK,
		},
		{
			name:       "DurationSeconds at maximum (129600)",
			body:       "Action=GetSessionToken&Version=2011-06-15&DurationSeconds=129600",
			wantStatus: http.StatusOK,
		},
		{
			name:        "DurationSeconds below minimum (0)",
			body:        "Action=GetSessionToken&Version=2011-06-15&DurationSeconds=0",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "DurationSeconds below minimum (899)",
			body:        "Action=GetSessionToken&Version=2011-06-15&DurationSeconds=899",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "DurationSeconds above maximum (129601)",
			body:        "Action=GetSessionToken&Version=2011-06-15&DurationSeconds=129601",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
		{
			name:        "DurationSeconds non-numeric",
			body:        "Action=GetSessionToken&Version=2011-06-15&DurationSeconds=abc",
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "ValidationError",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := stsRequest(t, tt.body)
			assert.Equal(t, tt.wantStatus, w.Code)
			if tt.wantErrCode != "" {
				var errResp errorResponse
				require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &errResp))
				assert.Equal(t, tt.wantErrCode, errResp.Error.Code)
				return
			}
			var resp getSessionTokenResponse
			require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &resp))
			creds := resp.GetSessionTokenResult.Credentials
			assert.Equal(t, fixedAccessKeyID, creds.AccessKeyID)
			assert.Equal(t, fixedSecretKey, creds.SecretAccessKey)
			assert.Equal(t, fixedSessionToken, creds.SessionToken)
			assert.Equal(t, fixedExpiration, creds.Expiration)
		})
	}
}

func TestParseFormError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("%invalid"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWriteXMLErrors(t *testing.T) {
	t.Run("write header fails", func(t *testing.T) {
		writeXML(newFailWriter(0), http.StatusOK, getCallerIdentityResponse{})
	})
	t.Run("encode fails", func(t *testing.T) {
		writeXML(newFailWriter(1), http.StatusOK, getCallerIdentityResponse{})
	})
}

func TestUnknownAction(t *testing.T) {
	w := stsAction(t, "UnknownAction")
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp errorResponse
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Sender", resp.Error.Type)
	assert.Equal(t, "InvalidAction", resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "UnknownAction")
}

func TestResponseRecorderFlush(t *testing.T) {
	t.Run("flushes when underlying writer implements http.Flusher", func(t *testing.T) {
		rr := newResponseRecorder(httptest.NewRecorder())
		rr.Flush() // httptest.ResponseRecorder implements http.Flusher
	})

	t.Run("no-op when underlying writer does not implement http.Flusher", func(t *testing.T) {
		rr := newResponseRecorder(newFailWriter(0))
		rr.Flush() // failWriter does not implement http.Flusher
	})
}

func TestEmitRequestLog(t *testing.T) {
	makeRec := func(status int, errCode, errMsg string) *responseRecorder {
		rr := newResponseRecorder(httptest.NewRecorder())
		rr.status = status
		rr.errCode = errCode
		rr.errMsg = errMsg
		return rr
	}

	t.Run("2xx logs at INFO", func(t *testing.T) {
		var buf testBuffer
		setLogger(t, &buf)
		emitRequestLog("GetCallerIdentity", makeRec(http.StatusOK, "", ""), time.Millisecond)
		assert.Contains(t, buf.String(), "INFO")
		assert.Contains(t, buf.String(), "op=GetCallerIdentity")
	})

	t.Run("4xx logs at DEBUG with code", func(t *testing.T) {
		var buf testBuffer
		setLogger(t, &buf)
		emitRequestLog(
			"AssumeRole",
			makeRec(http.StatusBadRequest, "ValidationError", "bad input"),
			time.Millisecond,
		)
		assert.Contains(t, buf.String(), "DEBUG")
		assert.Contains(t, buf.String(), "code=ValidationError")
	})

	t.Run("5xx logs at ERROR with err", func(t *testing.T) {
		var buf testBuffer
		setLogger(t, &buf)
		emitRequestLog(
			"",
			makeRec(http.StatusInternalServerError, "InternalFailure", "disk full"),
			time.Millisecond,
		)
		assert.Contains(t, buf.String(), "ERROR")
		assert.Contains(t, buf.String(), "err=")
		assert.Contains(t, buf.String(), "disk full")
	})
}
