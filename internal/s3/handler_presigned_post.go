package s3

import (
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// postResponse is the XML body returned for success_action_status 200 or 201.
type postResponse struct {
	XMLName  xml.Name `xml:"PostResponse"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

// handlePresignedPost handles browser-based HTML form uploads (POST policy / CreatePresignedPost).
// Signature and policy conditions are not verified — consistent with presigned PUT behaviour.
func (ro *Router) handlePresignedPost(w http.ResponseWriter, r *http.Request, bucket string) {
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"Content-Type must be multipart/form-data.")
		return
	}

	var (
		key                   string
		contentType           string
		successActionStatus   string
		successActionRedirect string
		userMeta              = map[string]string{}
		cannedACL             string
	)

	for {
		part, nextErr := mr.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			writeError(w, r, http.StatusBadRequest, "InvalidArgument",
				"Malformed multipart form data.")
			return
		}

		fieldName := strings.ToLower(part.FormName())
		filename := part.FileName()

		// File part: has a filename, or its field name is not a recognised metadata field.
		if filename != "" || !isPresignedTextField(fieldName) {
			if key == "" {
				_ = part.Close()
				writeError(w, r, http.StatusBadRequest, "InvalidArgument",
					"Form field 'key' must appear before the file.")
				return
			}
			if cannedACL != "" {
				if _, err := buildCannedACL(cannedACL); err != nil {
					_ = part.Close()
					writeError(w, r, http.StatusBadRequest, "InvalidArgument",
						"The canned ACL you provided is not valid.")
					return
				}
			}
			key = strings.ReplaceAll(key, "${filename}", filename)
			ct := contentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			meta, putErr := ro.storage.PutObject(
				bucket, key, part, ct, userMeta,
				"", "", false, "", nil, nil, "",
			)
			_ = part.Close()
			if putErr != nil {
				if errors.Is(putErr, ErrBucketNotFound) {
					writeError(w, r, http.StatusNotFound, "NoSuchBucket",
						"The specified bucket does not exist.")
					return
				}
				slog.Error( // #nosec G706 -- bucket/key come from form fields; log injection risk accepted for a local dev emulator
					"presigned post: failed to store object",
					"bucket",
					bucket,
					"key",
					key,
					"err",
					putErr,
				)
				writeError(w, r, http.StatusInternalServerError, "InternalError", putErr.Error())
				return
			}
			if cannedACL != "" {
				if aclXML, aclErr := buildCannedACL(cannedACL); aclErr == nil {
					_ = ro.storage.PutObjectACL(bucket, key, aclXML)
				}
			}
			slog.Info( // #nosec G706 -- bucket/key come from form fields; log injection risk accepted for a local dev emulator
				"object created via presigned POST",
				"bucket",
				bucket,
				"key",
				key,
			)
			ro.replicateObject(bucket, key, meta)
			writePresignedPostResponse(
				w,
				r,
				bucket,
				key,
				meta.ETag,
				successActionStatus,
				successActionRedirect,
			)
			return
		}

		// Text metadata field — read up to 64 KiB (sufficient for any policy or credential field).
		data, readErr := io.ReadAll(io.LimitReader(part, 64*1024))
		_ = part.Close()
		if readErr != nil {
			// untestable: the multipart.Part reader never errors in test environments;
			// triggering a mid-read I/O failure requires byte-level body manipulation.
			writeError(w, r, http.StatusBadRequest, "InvalidArgument",
				"Failed to read form field.")
			return
		}
		value := string(data)

		switch fieldName {
		case "key":
			key = value
		case "content-type":
			contentType = value
		case "success_action_status":
			successActionStatus = value
		case "success_action_redirect":
			successActionRedirect = value
		case "acl":
			cannedACL = value
		default:
			if strings.HasPrefix(fieldName, "x-amz-meta-") {
				userMeta[fieldName[11:]] = value
			}
			// policy, x-amz-algorithm, x-amz-credential, x-amz-date,
			// x-amz-signature, x-amz-security-token, etc. are accepted and ignored.
		}
	}

	writeError(w, r, http.StatusBadRequest, "InvalidArgument",
		"No file content found in the multipart upload.")
}

// isPresignedTextField returns true for known non-file multipart field names in a presigned POST.
func isPresignedTextField(name string) bool {
	switch name {
	case "key", "policy", "acl",
		"content-type", "content-disposition", "content-encoding",
		"cache-control", "expires",
		"success_action_status", "success_action_redirect",
		"x-amz-algorithm", "x-amz-credential", "x-amz-date",
		"x-amz-signature", "x-amz-security-token", "x-amz-storage-class",
		"awsaccesskeyid", "signature": // SigV2 legacy field names
		return true
	}
	return strings.HasPrefix(name, "x-amz-meta-") || strings.HasPrefix(name, "x-amz-")
}

// writePresignedPostResponse sends the appropriate success response for a presigned POST upload.
func writePresignedPostResponse(
	w http.ResponseWriter,
	r *http.Request,
	bucket, key, etag, status, redirect string,
) {
	if redirect != "" {
		u, parseErr := url.Parse(redirect)
		if parseErr == nil {
			q := u.Query()
			q.Set("bucket", bucket)
			q.Set("key", key)
			q.Set("etag", strings.Trim(etag, `"`))
			u.RawQuery = q.Encode()
			w.Header().Set("Location", u.String())
		} else {
			w.Header().Set("Location", redirect)
		}
		w.WriteHeader(http.StatusSeeOther)
		return
	}

	switch status {
	case "201", "200":
		code := http.StatusCreated
		if status == "200" {
			code = http.StatusOK
		}
		loc := "http://" + r.Host + "/" + bucket + "/" + key
		writeXML(w, code, postResponse{
			Location: loc,
			Bucket:   bucket,
			Key:      key,
			ETag:     etag,
		})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
