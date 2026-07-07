package cognito

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

// poolIDFromARN extracts the user pool ID from an ARN of the form
// arn:...:userpool/{poolID}. Returns ("", false) for malformed input.
func poolIDFromARN(arn string) (string, bool) {
	if len(arn) < 20 || len(arn) > 2048 {
		return "", false
	}
	const prefix = "userpool/"
	idx := strings.LastIndex(arn, prefix)
	if idx == -1 {
		return "", false
	}
	id := arn[idx+len(prefix):]
	if id == "" {
		return "", false
	}
	return id, true
}

// ──── TagResource ─────────────────────────────────────────────────────────────

type tagResourceRequest struct {
	ResourceArn string            `json:"ResourceArn"`
	Tags        map[string]string `json:"Tags"`
}

func (ro *Router) handleTagResource(w http.ResponseWriter, body []byte) {
	var req tagResourceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.ResourceArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"ResourceArn is required",
		)
		return
	}
	poolID, ok := poolIDFromARN(req.ResourceArn)
	if !ok {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"Invalid ResourceArn",
		)
		return
	}
	if len(req.Tags) == 0 {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"Tags must not be empty",
		)
		return
	}
	for k, v := range req.Tags {
		if kLen := utf8.RuneCountInString(k); kLen < 1 || kLen > 128 {
			writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
				"Tag key must be between 1 and 128 characters")
			return
		}
		if utf8.RuneCountInString(v) > 256 {
			writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
				"Tag value must be 256 characters or fewer")
			return
		}
	}
	err := ro.storage.UpdateUserPool(poolID, func(meta *UserPoolMetadata) error {
		if meta.UserPoolTags == nil {
			meta.UserPoolTags = make(map[string]string)
		}
		for k, v := range req.Tags {
			meta.UserPoolTags[k] = v
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"User pool not found.",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to update user pool tags",
		)
		return
	}
	writeEmpty(w)
}

// ──── UntagResource ───────────────────────────────────────────────────────────

type untagResourceRequest struct {
	ResourceArn string   `json:"ResourceArn"`
	TagKeys     []string `json:"TagKeys"`
}

func (ro *Router) handleUntagResource(w http.ResponseWriter, body []byte) {
	var req untagResourceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.ResourceArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"ResourceArn is required",
		)
		return
	}
	poolID, ok := poolIDFromARN(req.ResourceArn)
	if !ok {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"Invalid ResourceArn",
		)
		return
	}
	if len(req.TagKeys) == 0 {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"TagKeys must not be empty",
		)
		return
	}
	err := ro.storage.UpdateUserPool(poolID, func(meta *UserPoolMetadata) error {
		for _, k := range req.TagKeys {
			delete(meta.UserPoolTags, k)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"User pool not found.",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to update user pool tags",
		)
		return
	}
	writeEmpty(w)
}

// ──── ListTagsForResource ─────────────────────────────────────────────────────

type listTagsForResourceRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

func (ro *Router) handleListTagsForResource(w http.ResponseWriter, body []byte) {
	var req listTagsForResourceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.ResourceArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"ResourceArn is required",
		)
		return
	}
	poolID, ok := poolIDFromARN(req.ResourceArn)
	if !ok {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"Invalid ResourceArn",
		)
		return
	}
	meta, err := ro.storage.GetUserPool(poolID)
	if err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"User pool not found.",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to get user pool",
		)
		return
	}
	tags := meta.UserPoolTags
	if tags == nil {
		tags = map[string]string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"Tags": tags})
}
