package dynamodb

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

func (ro *Router) handleBatchGetItem(w http.ResponseWriter, body []byte) {
	var req struct {
		RequestItems map[string]struct {
			Keys                     []map[string]any  `json:"Keys"`
			ProjectionExpression     string            `json:"ProjectionExpression"`
			ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames"`
		} `json:"RequestItems"`
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
	if len(req.RequestItems) == 0 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"RequestItems is required",
		)
		return
	}
	totalKeys := 0
	for _, tableReq := range req.RequestItems {
		totalKeys += len(tableReq.Keys)
	}
	if totalKeys > 100 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Too many items requested for the BatchGetItem call",
		)
		return
	}
	responses := make(map[string][]map[string]any, len(req.RequestItems))
	for tableName, tableReq := range req.RequestItems {
		if err := validateUnusedExprRefs(
			tableReq.ExpressionAttributeNames, nil,
			tableReq.ProjectionExpression,
		); err != nil {
			writeError(w, http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException", err.Error())
			return
		}
		items, err := ro.storage.BatchGetItems(tableName, tableReq.Keys)
		if err != nil {
			if errors.Is(err, ErrTableNotFound) {
				slog.Debug("BatchGetItem: table not found", "table", tableName)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
					"Requested resource not found: Table: "+tableName+" not found",
				)
				return
			}
			if errors.Is(err, ErrValidationException) {
				slog.Debug("BatchGetItem: validation error", "table", tableName, "err", err)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					err.Error(),
				)
				return
			}
			slog.Error("BatchGetItem failed", "table", tableName, "err", err)
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
		if tableReq.ProjectionExpression != "" {
			var projErr error
			items, projErr = applyProjectionToItems(
				items,
				tableReq.ProjectionExpression,
				tableReq.ExpressionAttributeNames,
			)
			if projErr != nil {
				slog.Debug(
					"BatchGetItem: invalid ProjectionExpression",
					"table",
					tableName,
					"err",
					projErr,
				)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					projErr.Error(),
				)
				return
			}
		}
		responses[tableName] = items
	}
	slog.Debug("batch got DynamoDB items", "tables", len(req.RequestItems))
	writeJSON(w, http.StatusOK, map[string]any{
		"Responses":       responses,
		"UnprocessedKeys": map[string]any{},
	})
}

func (ro *Router) handleBatchWriteItem(w http.ResponseWriter, body []byte) {
	var req struct {
		RequestItems map[string][]struct {
			PutRequest *struct {
				Item map[string]any `json:"Item"`
			} `json:"PutRequest"`
			DeleteRequest *struct {
				Key map[string]any `json:"Key"`
			} `json:"DeleteRequest"`
		} `json:"RequestItems"`
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
	if len(req.RequestItems) == 0 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"RequestItems is required",
		)
		return
	}
	totalOps := 0
	for _, writeReqs := range req.RequestItems {
		totalOps += len(writeReqs)
	}
	if totalOps > 25 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Too many items requested for the BatchWriteItem call",
		)
		return
	}
	for tableName, writeReqs := range req.RequestItems {
		var puts []map[string]any
		var deletes []map[string]any
		for _, wr := range writeReqs {
			if wr.PutRequest != nil {
				puts = append(puts, wr.PutRequest.Item)
			} else if wr.DeleteRequest != nil {
				deletes = append(deletes, wr.DeleteRequest.Key)
			}
		}
		if err := ro.storage.BatchWriteItems(tableName, puts, deletes); err != nil {
			if errors.Is(err, ErrTableNotFound) {
				slog.Debug("BatchWriteItem: table not found", "table", tableName)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
					"Requested resource not found: Table: "+tableName+" not found",
				)
				return
			}
			if errors.Is(err, ErrValidationException) {
				slog.Debug("BatchWriteItem: validation error", "table", tableName, "err", err)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					err.Error(),
				)
				return
			}
			slog.Error("BatchWriteItem failed", "table", tableName, "err", err)
			writeError(
				w,
				http.StatusInternalServerError,
				"com.amazonaws.dynamodb.v20120810#InternalServerError",
				"internal server error",
			)
			return
		}
	}
	slog.Info("batch wrote DynamoDB items", "tables", len(req.RequestItems))
	writeJSON(w, http.StatusOK, map[string]any{
		"UnprocessedItems": map[string]any{},
	})
}
