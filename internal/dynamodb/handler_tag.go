package dynamodb

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
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
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.ResourceArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"ResourceArn is required",
		)
		return
	}
	tags := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		tags[t.Key] = t.Value
	}
	if err := ro.storage.TagResource(req.ResourceArn, tags); err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("TagResource: resource not found", "arn", req.ResourceArn)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: "+req.ResourceArn,
			)
			return
		}
		slog.Error("TagResource failed", "arn", req.ResourceArn, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("tagged DynamoDB resource", "arn", req.ResourceArn, "count", len(tags))
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
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.ResourceArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"ResourceArn is required",
		)
		return
	}
	if err := ro.storage.UntagResource(req.ResourceArn, req.TagKeys); err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("UntagResource: resource not found", "arn", req.ResourceArn)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: "+req.ResourceArn,
			)
			return
		}
		slog.Error("UntagResource failed", "arn", req.ResourceArn, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("untagged DynamoDB resource", "arn", req.ResourceArn)
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
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.ResourceArn == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"ResourceArn is required",
		)
		return
	}
	tags, err := ro.storage.ListTagsOfResource(req.ResourceArn)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("ListTagsOfResource: resource not found", "arn", req.ResourceArn)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: "+req.ResourceArn,
			)
			return
		}
		slog.Error("ListTagsOfResource failed", "arn", req.ResourceArn, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	tagList := make([]map[string]string, 0, len(tags))
	for k, v := range tags {
		tagList = append(tagList, map[string]string{"Key": k, "Value": v})
	}
	slog.Debug("listed DynamoDB resource tags", "arn", req.ResourceArn, "count", len(tagList))
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
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
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
			slog.Debug("UpdateTimeToLive: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		slog.Error("UpdateTimeToLive failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("updated TTL", "table", req.TableName, "enabled", spec.Enabled)
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
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"invalid request body",
		)
		return
	}
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TableName is required",
		)
		return
	}
	status, spec, err := ro.storage.DescribeTimeToLive(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("DescribeTimeToLive: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		slog.Error("DescribeTimeToLive failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	ttlDesc := map[string]any{"TimeToLiveStatus": status}
	if spec != nil {
		ttlDesc["AttributeName"] = spec.AttributeName
	}
	slog.Debug("described TTL", "table", req.TableName, "status", status)
	writeJSON(w, http.StatusOK, map[string]any{"TimeToLiveDescription": ttlDesc})
}

func (ro *Router) handleDescribeLimits(w http.ResponseWriter) {
	slog.Debug("DescribeLimits")
	writeJSON(w, http.StatusOK, map[string]any{
		"AccountMaxReadCapacityUnits":  80000,
		"AccountMaxWriteCapacityUnits": 80000,
		"TableMaxReadCapacityUnits":    40000,
		"TableMaxWriteCapacityUnits":   40000,
	})
}

func (ro *Router) handleDescribeEndpoints(w http.ResponseWriter) {
	slog.Debug("DescribeEndpoints")
	writeJSON(w, http.StatusOK, map[string]any{
		"Endpoints": []map[string]any{
			{
				"Address":              "localhost:5566",
				"CachePeriodInMinutes": 1440,
			},
		},
	})
}
