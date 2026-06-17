package dynamodb

import (
	"encoding/json"
	"errors"
	"net/http"
)

func (ro *Router) handleBatchGetItem(w http.ResponseWriter, body []byte) {
	var req struct {
		RequestItems map[string]struct {
			Keys                     []map[string]any  `json:"Keys"`
			ProjectionExpression     string            `json:"ProjectionExpression"`
			ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames"`
		} `json:"RequestItems"`
		ReturnConsumedCapacity string `json:"ReturnConsumedCapacity"`
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
	if len(req.RequestItems) == 0 {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
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
			ErrTypeValidationException,
			"Too many items requested for the BatchGetItem call",
		)
		return
	}
	if !validateReturnConsumedCapacity(w, req.ReturnConsumedCapacity) {
		return
	}
	responses := make(map[string][]map[string]any, len(req.RequestItems))
	for tableName, tableReq := range req.RequestItems {
		if err := validateUnusedExprRefs(
			tableReq.ExpressionAttributeNames, nil,
			tableReq.ProjectionExpression,
		); err != nil {
			writeError(w, http.StatusBadRequest,
				ErrTypeValidationException, err.Error())
			return
		}
		items, err := ro.storage.BatchGetItems(tableName, tableReq.Keys)
		if err != nil {
			if errors.Is(err, ErrTableNotFound) {
				writeError(
					w,
					http.StatusBadRequest,
					ErrTypeResourceNotFoundException,
					"Requested resource not found: Table: "+tableName+" not found",
				)
				return
			}
			if errors.Is(err, ErrValidationException) {
				writeError(
					w,
					http.StatusBadRequest,
					ErrTypeValidationException,
					err.Error(),
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
				writeError(
					w,
					http.StatusBadRequest,
					ErrTypeValidationException,
					projErr.Error(),
				)
				return
			}
		}
		responses[tableName] = items
	}
	resp := map[string]any{
		"Responses":       responses,
		"UnprocessedKeys": map[string]any{},
	}
	if req.ReturnConsumedCapacity != "" && req.ReturnConsumedCapacity != "NONE" {
		ccs := make([]map[string]any, 0, len(req.RequestItems))
		for tableName := range req.RequestItems {
			ccs = append(ccs, buildConsumedCapacity(tableName, req.ReturnConsumedCapacity))
		}
		resp["ConsumedCapacity"] = ccs
	}
	writeJSON(w, http.StatusOK, resp)
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
		ReturnConsumedCapacity string `json:"ReturnConsumedCapacity"`
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
	if len(req.RequestItems) == 0 {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
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
			ErrTypeValidationException,
			"Too many items requested for the BatchWriteItem call",
		)
		return
	}
	if !validateReturnConsumedCapacity(w, req.ReturnConsumedCapacity) {
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
				writeError(
					w,
					http.StatusBadRequest,
					ErrTypeResourceNotFoundException,
					"Requested resource not found: Table: "+tableName+" not found",
				)
				return
			}
			if errors.Is(err, ErrValidationException) {
				writeError(
					w,
					http.StatusBadRequest,
					ErrTypeValidationException,
					err.Error(),
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
	}
	resp := map[string]any{
		"UnprocessedItems": map[string]any{},
	}
	if req.ReturnConsumedCapacity != "" && req.ReturnConsumedCapacity != "NONE" {
		ccs := make([]map[string]any, 0, len(req.RequestItems))
		for tableName := range req.RequestItems {
			ccs = append(ccs, buildConsumedCapacity(tableName, req.ReturnConsumedCapacity))
		}
		resp["ConsumedCapacity"] = ccs
	}
	writeJSON(w, http.StatusOK, resp)
}
