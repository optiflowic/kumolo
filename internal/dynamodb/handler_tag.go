package dynamodb

import (
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"
)

func (ro *Router) handleTagResource(w http.ResponseWriter, body []byte) {
	var req struct {
		ResourceArn string `json:"ResourceArn"`
		Tags        []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"invalid request body",
		)
		return
	}
	if req.ResourceArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"ResourceArn is required",
		)
		return
	}
	tags := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		if n := utf8.RuneCountInString(t.Key); n < tagKeyMinLength || n > tagKeyMaxLength {
			writeError(w, http.StatusBadRequest, ErrTypeValidationException, errMsgTagKeyLength)
			return
		}
		if utf8.RuneCountInString(t.Value) > tagValueMaxLength {
			writeError(w, http.StatusBadRequest, ErrTypeValidationException, errMsgTagValueLength)
			return
		}
		tags[t.Key] = t.Value
	}
	if err := ro.storage.TagResource(req.ResourceArn, tags); err != nil {
		if errors.Is(err, ErrTableNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"Requested resource not found: "+req.ResourceArn,
			)
			return
		}
		if errors.Is(err, ErrTagLimitExceeded) {
			writeError(w, http.StatusBadRequest, ErrTypeLimitExceededException, errMsgTagLimit)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (ro *Router) handleUntagResource(w http.ResponseWriter, body []byte) {
	var req struct {
		ResourceArn string   `json:"ResourceArn"`
		TagKeys     []string `json:"TagKeys"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"invalid request body",
		)
		return
	}
	if req.ResourceArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"ResourceArn is required",
		)
		return
	}
	for _, k := range req.TagKeys {
		if k == "" {
			writeError(w, http.StatusBadRequest, ErrTypeValidationException, errMsgTagKeyEmpty)
			return
		}
	}
	if err := ro.storage.UntagResource(req.ResourceArn, req.TagKeys); err != nil {
		if errors.Is(err, ErrTableNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"Requested resource not found: "+req.ResourceArn,
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (ro *Router) handleListTagsOfResource(w http.ResponseWriter, body []byte) {
	var req struct {
		ResourceArn string `json:"ResourceArn"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"invalid request body",
		)
		return
	}
	if req.ResourceArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"ResourceArn is required",
		)
		return
	}
	tags, err := ro.storage.ListTagsOfResource(req.ResourceArn)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"Requested resource not found: "+req.ResourceArn,
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	tagList := make([]map[string]string, 0, len(tags))
	for k, v := range tags {
		tagList = append(tagList, map[string]string{"Key": k, "Value": v})
	}
	writeJSON(w, http.StatusOK, map[string]any{"Tags": tagList})
}

func (ro *Router) handleUpdateTimeToLive(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName               string `json:"TableName"`
		TimeToLiveSpecification struct {
			AttributeName string `json:"AttributeName"`
			Enabled       bool   `json:"Enabled"`
		} `json:"TimeToLiveSpecification"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"TableName is required",
		)
		return
	}
	spec, err := ro.storage.UpdateTimeToLive(req.TableName, TTLSpec{
		AttributeName: req.TimeToLiveSpecification.AttributeName,
		Enabled:       req.TimeToLiveSpecification.Enabled,
	})
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"TimeToLiveSpecification": map[string]any{
			"AttributeName": spec.AttributeName,
			"Enabled":       spec.Enabled,
		},
	})
}

func (ro *Router) handleDescribeTimeToLive(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName string `json:"TableName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"TableName is required",
		)
		return
	}
	status, spec, err := ro.storage.DescribeTimeToLive(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	ttlDesc := map[string]any{"TimeToLiveStatus": status}
	if spec != nil {
		ttlDesc["AttributeName"] = spec.AttributeName
	}
	writeJSON(w, http.StatusOK, map[string]any{"TimeToLiveDescription": ttlDesc})
}

func (ro *Router) handleDescribeLimits(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"AccountMaxReadCapacityUnits":  80000,
		"AccountMaxWriteCapacityUnits": 80000,
		"TableMaxReadCapacityUnits":    40000,
		"TableMaxWriteCapacityUnits":   40000,
	})
}

func (ro *Router) handleDescribeEndpoints(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Endpoints": []map[string]any{
			{
				"Address":              "localhost:5566",
				"CachePeriodInMinutes": 1440,
			},
		},
	})
}
