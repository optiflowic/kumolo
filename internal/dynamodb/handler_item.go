package dynamodb

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

func (ro *Router) handlePutItem(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                 string            `json:"TableName"`
		Item                      map[string]any    `json:"Item"`
		ReturnValues              string            `json:"ReturnValues"`
		ConditionExpression       string            `json:"ConditionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
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
	switch req.ReturnValues {
	case "", "NONE", "ALL_OLD":
	default:
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Value '"+req.ReturnValues+"' at 'returnValues' failed to satisfy constraint: Member must satisfy enum value set: [ALL_OLD, NONE]",
		)
		return
	}
	var cond *ConditionCheck
	if req.ConditionExpression != "" {
		cond = &ConditionCheck{
			Expr:   req.ConditionExpression,
			Names:  req.ExpressionAttributeNames,
			Values: req.ExpressionAttributeValues,
		}
	}
	old, err := ro.storage.PutItem(req.TableName, req.Item, cond)
	if err != nil {
		if errors.Is(err, ErrConditionalCheckFailed) {
			slog.Debug("PutItem: condition check failed", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException",
				"The conditional request failed",
			)
			return
		}
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("PutItem: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("PutItem: validation error", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("PutItem failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("put DynamoDB item", "table", req.TableName)
	resp := map[string]any{}
	if req.ReturnValues == "ALL_OLD" && old != nil {
		resp["Attributes"] = old
	}
	writeJSON(w, http.StatusOK, resp)
}

func (ro *Router) handleGetItem(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                string            `json:"TableName"`
		Key                      map[string]any    `json:"Key"`
		ProjectionExpression     string            `json:"ProjectionExpression"`
		ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames"`
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
	item, err := ro.storage.GetItem(req.TableName, req.Key)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("GetItem: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("GetItem: validation error", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("GetItem failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	if item != nil && req.ProjectionExpression != "" {
		var projErr error
		item, projErr = applyProjection(
			item,
			req.ProjectionExpression,
			req.ExpressionAttributeNames,
		)
		if projErr != nil {
			slog.Debug(
				"GetItem: invalid ProjectionExpression",
				"table",
				req.TableName,
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
	slog.Debug("got DynamoDB item", "table", req.TableName)
	resp := map[string]any{}
	if item != nil {
		resp["Item"] = item
	}
	writeJSON(w, http.StatusOK, resp)
}

func (ro *Router) handleDeleteItem(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                 string            `json:"TableName"`
		Key                       map[string]any    `json:"Key"`
		ReturnValues              string            `json:"ReturnValues"`
		ConditionExpression       string            `json:"ConditionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
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
	switch req.ReturnValues {
	case "", "NONE", "ALL_OLD":
	default:
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Value '"+req.ReturnValues+"' at 'returnValues' failed to satisfy constraint: Member must satisfy enum value set: [ALL_OLD, NONE]",
		)
		return
	}
	var cond *ConditionCheck
	if req.ConditionExpression != "" {
		cond = &ConditionCheck{
			Expr:   req.ConditionExpression,
			Names:  req.ExpressionAttributeNames,
			Values: req.ExpressionAttributeValues,
		}
	}
	old, err := ro.storage.DeleteItem(req.TableName, req.Key, cond)
	if err != nil {
		if errors.Is(err, ErrConditionalCheckFailed) {
			slog.Debug("DeleteItem: condition check failed", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException",
				"The conditional request failed",
			)
			return
		}
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("DeleteItem: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("DeleteItem: validation error", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("DeleteItem failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("deleted DynamoDB item", "table", req.TableName)
	resp := map[string]any{}
	if req.ReturnValues == "ALL_OLD" && old != nil {
		resp["Attributes"] = old
	}
	writeJSON(w, http.StatusOK, resp)
}

func (ro *Router) handleUpdateItem(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName                 string            `json:"TableName"`
		Key                       map[string]any    `json:"Key"`
		UpdateExpression          string            `json:"UpdateExpression"`
		ConditionExpression       string            `json:"ConditionExpression"`
		ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames"`
		ExpressionAttributeValues map[string]any    `json:"ExpressionAttributeValues"`
		ReturnValues              string            `json:"ReturnValues"`
		AttributeUpdates          map[string]struct {
			Action string `json:"Action"`
			Value  any    `json:"Value"`
		} `json:"AttributeUpdates"`
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

	switch req.ReturnValues {
	case "", "NONE", "ALL_OLD", "ALL_NEW", "UPDATED_OLD", "UPDATED_NEW":
	default:
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"Value '"+req.ReturnValues+"' at 'returnValues' failed to satisfy constraint: Member must satisfy enum value set: [ALL_NEW, UPDATED_OLD, ALL_OLD, NONE, UPDATED_NEW]",
		)
		return
	}

	var updates map[string]any
	switch {
	case req.UpdateExpression != "":
		var err error
		updates, err = parseUpdateExpression(
			req.UpdateExpression,
			req.ExpressionAttributeNames,
			req.ExpressionAttributeValues,
		)
		if err != nil {
			slog.Debug("UpdateItem: invalid UpdateExpression", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
	case len(req.AttributeUpdates) > 0:
		updates = make(map[string]any, len(req.AttributeUpdates))
		for name, au := range req.AttributeUpdates {
			switch au.Action {
			case "PUT", "":
				updates[name] = au.Value
			case "DELETE":
				updates[name] = nil
			default:
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					"unsupported AttributeUpdates Action: "+au.Action,
				)
				return
			}
		}
	default:
		updates = map[string]any{}
	}

	var cond *ConditionCheck
	if req.ConditionExpression != "" {
		cond = &ConditionCheck{
			Expr:   req.ConditionExpression,
			Names:  req.ExpressionAttributeNames,
			Values: req.ExpressionAttributeValues,
		}
	}
	before, after, err := ro.storage.UpdateItem(req.TableName, req.Key, updates, cond)
	if err != nil {
		if errors.Is(err, ErrConditionalCheckFailed) {
			slog.Debug("UpdateItem: condition check failed", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException",
				"The conditional request failed",
			)
			return
		}
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("UpdateItem: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("UpdateItem: validation error", "table", req.TableName, "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("UpdateItem failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("updated DynamoDB item", "table", req.TableName)
	resp := map[string]any{}
	switch req.ReturnValues {
	case "ALL_NEW":
		resp["Attributes"] = after
	case "ALL_OLD":
		if before != nil {
			resp["Attributes"] = before
		}
	case "UPDATED_NEW":
		attrs := make(map[string]any, len(updates))
		for k := range updates {
			if v, ok := after[k]; ok {
				attrs[k] = v
			}
		}
		if len(attrs) > 0 {
			resp["Attributes"] = attrs
		}
	case "UPDATED_OLD":
		if before != nil {
			attrs := make(map[string]any, len(updates))
			for k := range updates {
				if v, ok := before[k]; ok {
					attrs[k] = v
				}
			}
			if len(attrs) > 0 {
				resp["Attributes"] = attrs
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
