package dynamodb

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

func (ro *Router) handleScan(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                 string            `json:"TableName"`
		Select                    string            `json:"Select"`
		FilterExpression          string            `json:"FilterExpression"`
		ProjectionExpression      string            `json:"ProjectionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		Limit                     *int              `json:"Limit"`
		ExclusiveStartKey         map[string]any    `json:"ExclusiveStartKey"`
		Segment                   *int              `json:"Segment"`
		TotalSegments             *int              `json:"TotalSegments"`
		ReturnConsumedCapacity    string            `json:"ReturnConsumedCapacity"`
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
	if req.Limit != nil && *req.Limit < 1 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			fmt.Sprintf(
				"Value %d at 'limit' failed to satisfy constraint: Member must have value greater than or equal to 1",
				*req.Limit,
			),
		)
		return
	}
	if !validateSelectCommon(w, req.Select, req.ProjectionExpression) {
		return
	}
	if req.Select == "ALL_PROJECTED_ATTRIBUTES" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Select ALL_PROJECTED_ATTRIBUTES is not allowed when scanning a table without an index",
		)
		return
	}
	if (req.Segment == nil) != (req.TotalSegments == nil) {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Segment and TotalSegments must both be specified for parallel scan",
		)
		return
	}
	if req.Segment != nil {
		seg := *req.Segment
		total := *req.TotalSegments
		switch {
		case seg < 0 || seg > 999999:
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				fmt.Sprintf(
					"Value %d at 'segment' failed to satisfy constraint: Member must have value between 0 and 999999, inclusive",
					seg,
				),
			)
			return
		case total < 1 || total > 1000000:
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				fmt.Sprintf(
					"Value %d at 'totalSegments' failed to satisfy constraint: Member must have value between 1 and 1000000, inclusive",
					total,
				),
			)
			return
		case seg >= total:
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				fmt.Sprintf(
					"Value %d at 'segment' failed to satisfy constraint: Member must have value less than TotalSegments (%d)",
					seg,
					total,
				),
			)
			return
		}
	}
	if !validateReturnConsumedCapacity(w, req.ReturnConsumedCapacity) {
		return
	}
	if err := validateUnusedExprRefs(
		req.ExpressionAttributeNames, req.ExpressionAttributeValues,
		req.FilterExpression, req.ProjectionExpression,
	); err != nil {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException", err.Error())
		return
	}
	opts := ScanOptions{
		Limit:             req.Limit,
		ExclusiveStartKey: req.ExclusiveStartKey,
		Segment:           req.Segment,
		TotalSegments:     req.TotalSegments,
	}
	items, lastEvaluatedKey, err := ro.storage.Scan(req.TableName, opts)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("Scan: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("Scan: validation error", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("Scan failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	if items == nil {
		items = []map[string]any{}
	}
	scannedCount := len(items)
	if req.FilterExpression != "" {
		items, err = applyFilterExpression(
			items,
			req.FilterExpression,
			req.ExpressionAttributeNames,
			req.ExpressionAttributeValues,
		)
		if err != nil {
			slog.Debug("Scan: invalid FilterExpression", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
	}
	if req.ProjectionExpression != "" {
		items, err = applyProjectionToItems(
			items,
			req.ProjectionExpression,
			req.ExpressionAttributeNames,
		)
		if err != nil {
			slog.Debug("Scan: invalid ProjectionExpression", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
	}
	slog.Debug("scanned DynamoDB table", "table", req.TableName, "count", len(items))
	if req.Select == "COUNT" {
		resp := map[string]any{
			"Count":        len(items),
			"ScannedCount": scannedCount,
		}
		if lastEvaluatedKey != nil {
			resp["LastEvaluatedKey"] = lastEvaluatedKey
		}
		if cc := buildConsumedCapacity(req.TableName, req.ReturnConsumedCapacity); cc != nil {
			resp["ConsumedCapacity"] = cc
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp := map[string]any{
		"Items":        items,
		"Count":        len(items),
		"ScannedCount": scannedCount,
	}
	if lastEvaluatedKey != nil {
		resp["LastEvaluatedKey"] = lastEvaluatedKey
	}
	if cc := buildConsumedCapacity(req.TableName, req.ReturnConsumedCapacity); cc != nil {
		resp["ConsumedCapacity"] = cc
	}
	writeJSON(w, http.StatusOK, resp)
}

func (ro *Router) handleQuery(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                 string            `json:"TableName"`
		IndexName                 string            `json:"IndexName"`
		Select                    string            `json:"Select"`
		KeyConditionExpression    string            `json:"KeyConditionExpression"`
		FilterExpression          string            `json:"FilterExpression"`
		ProjectionExpression      string            `json:"ProjectionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ScanIndexForward          *bool             `json:"ScanIndexForward"`
		Limit                     *int              `json:"Limit"`
		ExclusiveStartKey         map[string]any    `json:"ExclusiveStartKey"`
		ReturnConsumedCapacity    string            `json:"ReturnConsumedCapacity"`
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
	if req.KeyConditionExpression == "" {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"KeyConditionExpression is required",
		)
		return
	}

	if !validateReturnConsumedCapacity(w, req.ReturnConsumedCapacity) {
		return
	}
	if err := validateUnusedExprRefs(
		req.ExpressionAttributeNames, req.ExpressionAttributeValues,
		req.KeyConditionExpression, req.FilterExpression, req.ProjectionExpression,
	); err != nil {
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException", err.Error())
		return
	}
	hashKeyName, hashKeyValue, skCond, err := parseKeyConditionExpression(
		req.KeyConditionExpression,
		req.ExpressionAttributeNames,
		req.ExpressionAttributeValues,
	)
	if err != nil {
		slog.Debug("Query: invalid KeyConditionExpression", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			err.Error(),
		)
		return
	}

	if !validateSelectCommon(w, req.Select, req.ProjectionExpression) {
		return
	}
	if req.Select == "ALL_PROJECTED_ATTRIBUTES" {
		if req.IndexName == "" {
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				"Select ALL_PROJECTED_ATTRIBUTES is not allowed when querying a table without an index",
			)
			return
		}
		if req.ProjectionExpression != "" {
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				"Select type ALL_PROJECTED_ATTRIBUTES is not allowed with a ProjectionExpression",
			)
			return
		}
	}
	if req.Limit != nil && *req.Limit < 1 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			fmt.Sprintf(
				"Value %d at 'limit' failed to satisfy constraint: Member must have value greater than or equal to 1",
				*req.Limit,
			),
		)
		return
	}
	scanIndexForward := true
	if req.ScanIndexForward != nil {
		scanIndexForward = *req.ScanIndexForward
	}
	opts := QueryOptions{
		ScanIndexForward:  scanIndexForward,
		Limit:             req.Limit,
		ExclusiveStartKey: req.ExclusiveStartKey,
		IndexName:         req.IndexName,
	}

	items, lastEvaluatedKey, err := ro.storage.Query(
		req.TableName,
		hashKeyName,
		hashKeyValue,
		skCond,
		opts,
	)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("Query: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("Query: validation error", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("Query failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	if items == nil {
		items = []map[string]any{}
	}
	scannedCount := len(items)
	if req.FilterExpression != "" {
		items, err = applyFilterExpression(
			items,
			req.FilterExpression,
			req.ExpressionAttributeNames,
			req.ExpressionAttributeValues,
		)
		if err != nil {
			slog.Debug("Query: invalid FilterExpression", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		if items == nil {
			items = []map[string]any{}
		}
	}
	if req.ProjectionExpression != "" {
		items, err = applyProjectionToItems(
			items,
			req.ProjectionExpression,
			req.ExpressionAttributeNames,
		)
		if err != nil {
			slog.Debug("Query: invalid ProjectionExpression", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
	}
	slog.Debug("queried DynamoDB table", "table", req.TableName, "count", len(items))
	if req.Select == "COUNT" {
		resp := map[string]any{
			"Count":        len(items),
			"ScannedCount": scannedCount,
		}
		if lastEvaluatedKey != nil {
			resp["LastEvaluatedKey"] = lastEvaluatedKey
		}
		if cc := buildConsumedCapacity(req.TableName, req.ReturnConsumedCapacity); cc != nil {
			resp["ConsumedCapacity"] = cc
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp := map[string]any{
		"Items":        items,
		"Count":        len(items),
		"ScannedCount": scannedCount,
	}
	if lastEvaluatedKey != nil {
		resp["LastEvaluatedKey"] = lastEvaluatedKey
	}
	if cc := buildConsumedCapacity(req.TableName, req.ReturnConsumedCapacity); cc != nil {
		resp["ConsumedCapacity"] = cc
	}
	writeJSON(w, http.StatusOK, resp)
}
