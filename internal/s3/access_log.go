package s3

import (
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// bucketLoggingStatus is the parsed form of the BucketLoggingStatus XML stored
// by PutBucketLogging. It identifies the target bucket and key prefix for log
// delivery.
type bucketLoggingStatus struct {
	XMLName        xml.Name        `xml:"BucketLoggingStatus"`
	LoggingEnabled *loggingEnabled `xml:"LoggingEnabled"`
}

type loggingEnabled struct {
	TargetBucket string `xml:"TargetBucket"`
	TargetPrefix string `xml:"TargetPrefix"`
}

// responseRecorder wraps http.ResponseWriter to capture the status code and
// the number of bytes written to the response body.
type responseRecorder struct {
	http.ResponseWriter
	status       int
	bytesWritten int64
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (rr *responseRecorder) WriteHeader(status int) {
	rr.status = status
	rr.ResponseWriter.WriteHeader(status)
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	n, err := rr.ResponseWriter.Write(b)
	rr.bytesWritten += int64(n)
	return n, err
}

func (rr *responseRecorder) Flush() {
	if f, ok := rr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// appendAccessLog reads the logging configuration for bucket. If logging is
// enabled it formats an S3 server access log record and writes it to the
// configured target bucket under the configured prefix.
// Errors are logged as warnings and never propagate to the caller.
func (ro *Router) appendAccessLog(r *http.Request, rec *responseRecorder, start time.Time) {
	bucket, key := parsePath(r.URL.Path)
	if bucket == "" {
		return
	}

	logXML, err := ro.storage.GetBucketLogging(bucket)
	if err != nil || logXML == "" {
		return
	}

	var ls bucketLoggingStatus
	if err := xml.Unmarshal([]byte(logXML), &ls); err != nil ||
		ls.LoggingEnabled == nil { //nolint:gosec // G709: logXML is config stored from a prior authenticated request, not direct user input
		return
	}
	le := ls.LoggingEnabled
	if le.TargetBucket == "" {
		return
	}

	entry := formatLogEntry(bucket, key, r, rec, start)
	var nonce [4]byte
	_, _ = rand.Read(nonce[:])
	objKey := logObjectKey(le.TargetPrefix, start, nonce)
	if err := ro.storage.WriteAccessLog(le.TargetBucket, objKey, entry); err != nil {
		slog.Warn( // #nosec G706 -- target_bucket is from stored config, not direct user input
			"access log write failed",
			"target_bucket",
			le.TargetBucket,
			"err",
			err,
		)
	}
}

// formatLogEntry returns a single record in the S3 server access log format.
// Fields that kumolo does not track are represented as "-".
// Ref: https://docs.aws.amazon.com/AmazonS3/latest/userguide/LogFormat.html
func formatLogEntry(
	bucket, key string,
	r *http.Request,
	rec *responseRecorder,
	t time.Time,
) string {
	remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteIP = r.RemoteAddr
	}

	keyField := "-"
	if key != "" {
		keyField = key
	}

	requestURI := r.RequestURI
	if requestURI == "" {
		requestURI = r.URL.RequestURI()
	}

	referer := r.Referer()
	if referer == "" {
		referer = "-"
	}
	userAgent := r.UserAgent()
	if userAgent == "" {
		userAgent = "-"
	}

	bytesStr := "-"
	if rec.bytesWritten > 0 {
		bytesStr = fmt.Sprintf("%d", rec.bytesWritten)
	}

	// Fields in AWS S3 server access log order:
	// bucket-owner bucket time remote-ip requester request-id operation key
	// request-uri status error bytes-sent object-size total-time turnaround-time
	// referrer user-agent version-id host-id sig-version cipher auth-type
	// host-name tls-version access-point acl-required
	return fmt.Sprintf(
		`- %s [%s] %s - - %s %s "%s %s %s" %d - %s - - - "%s" "%s" - - - - - - - - -`,
		bucket,
		t.UTC().Format("02/Jan/2006:15:04:05 +0000"),
		remoteIP,
		logOperationName(r.Method, key != ""),
		keyField,
		r.Method,
		requestURI,
		r.Proto,
		rec.status,
		bytesStr,
		referer,
		userAgent,
	)
}

// logOperationName returns the S3 server access log operation field, e.g.
// REST.GET.OBJECT or REST.PUT.BUCKET.
func logOperationName(method string, isObject bool) string {
	target := "BUCKET"
	if isObject {
		target = "OBJECT"
	}
	return "REST." + method + "." + target
}

// logObjectKey generates an S3 object key for a log record under prefix.
// Keys sort chronologically and include a random nonce to avoid collisions
// when two requests start at the same nanosecond.
// Format: {prefix}YYYY-MM-DD-HH-MM-SS-{nanoseconds:09d}-{nonce:hex}
func logObjectKey(prefix string, t time.Time, nonce [4]byte) string {
	return fmt.Sprintf(
		"%s%s-%09d-%x",
		prefix,
		t.UTC().Format("2006-01-02-15-04-05"),
		t.Nanosecond(),
		nonce,
	)
}
