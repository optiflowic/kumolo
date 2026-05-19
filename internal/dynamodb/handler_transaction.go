package dynamodb

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

func (ro *Router) handleTransactGetItems(w http.ResponseWriter, body []byte) {
	var req struct {
		TransactItems []struct {
			Get *struct {
				TableName                string            `json:"TableName"`
				Key                      map[string]any    `json:"Key"`
				ProjectionExpression     string            `json:"ProjectionExpression"`
				ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames"`
			} `json:"Get"`
		} `json:"TransactItems"`
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
	if len(req.TransactItems) == 0 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TransactItems is required",
		)
		return
	}
	if len(req.TransactItems) > 100 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			fmt.Sprintf(
				"Member must have length less than or equal to 100, but received length %d",
				len(req.TransactItems),
			),
		)
		return
	}
	gets := make([]TransactGetInput, len(req.TransactItems))
	projections := make([]struct {
		expr  string
		names map[string]string
	}, len(req.TransactItems))
	for i, ti := range req.TransactItems {
		if ti.Get == nil {
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				"each TransactItems entry must contain a Get",
			)
			return
		}
		if err := validateUnusedExprRefs(
			ti.Get.ExpressionAttributeNames, nil,
			ti.Get.ProjectionExpression,
		); err != nil {
			writeError(w, http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException", err.Error())
			return
		}
		gets[i] = TransactGetInput{
			TableName: ti.Get.TableName,
			Key:       ti.Get.Key,
		}
		projections[i].expr = ti.Get.ProjectionExpression
		projections[i].names = ti.Get.ExpressionAttributeNames
	}
	items, err := ro.storage.TransactGetItems(gets)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("TransactGetItems: table not found")
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("TransactGetItems: validation error", "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("TransactGetItems failed", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	responses := make([]map[string]any, len(items))
	for i, item := range items {
		if item == nil {
			responses[i] = map[string]any{}
			continue
		}
		if projections[i].expr != "" {
			var projErr error
			item, projErr = applyProjection(item, projections[i].expr, projections[i].names)
			if projErr != nil {
				slog.Debug("TransactGetItems: invalid ProjectionExpression", "err", projErr)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					projErr.Error(),
				)
				return
			}
		}
		responses[i] = map[string]any{"Item": item}
	}
	slog.Debug("TransactGetItems", "count", len(items))
	writeJSON(w, http.StatusOK, map[string]any{"Responses": responses})
}

func (ro *Router) handleTransactWriteItems(w http.ResponseWriter, body []byte) {
	var req struct {
		TransactItems []struct {
			Put *struct {
				TableName                           string            `json:"TableName"`
				Item                                map[string]any    `json:"Item"`
				ConditionExpression                 string            `json:"ConditionExpression"`
				ExpressionAttributeNames            map[string]string `json:"ExpressionAttributeNames"`
				ExpressionAttributeValues           map[string]any    `json:"ExpressionAttributeValues"`
				ReturnValuesOnConditionCheckFailure string            `json:"ReturnValuesOnConditionCheckFailure"`
			} `json:"Put"`
			Delete *struct {
				TableName                           string            `json:"TableName"`
				Key                                 map[string]any    `json:"Key"`
				ConditionExpression                 string            `json:"ConditionExpression"`
				ExpressionAttributeNames            map[string]string `json:"ExpressionAttributeNames"`
				ExpressionAttributeValues           map[string]any    `json:"ExpressionAttributeValues"`
				ReturnValuesOnConditionCheckFailure string            `json:"ReturnValuesOnConditionCheckFailure"`
			} `json:"Delete"`
			Update *struct {
				TableName                           string            `json:"TableName"`
				Key                                 map[string]any    `json:"Key"`
				UpdateExpression                    string            `json:"UpdateExpression"`
				ConditionExpression                 string            `json:"ConditionExpression"`
				ExpressionAttributeNames            map[string]string `json:"ExpressionAttributeNames"`
				ExpressionAttributeValues           map[string]any    `json:"ExpressionAttributeValues"`
				ReturnValuesOnConditionCheckFailure string            `json:"ReturnValuesOnConditionCheckFailure"`
			} `json:"Update"`
			ConditionCheck *struct {
				TableName                           string            `json:"TableName"`
				Key                                 map[string]any    `json:"Key"`
				ConditionExpression                 string            `json:"ConditionExpression"`
				ExpressionAttributeNames            map[string]string `json:"ExpressionAttributeNames"`
				ExpressionAttributeValues           map[string]any    `json:"ExpressionAttributeValues"`
				ReturnValuesOnConditionCheckFailure string            `json:"ReturnValuesOnConditionCheckFailure"`
			} `json:"ConditionCheck"`
		} `json:"TransactItems"`
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
	if len(req.TransactItems) == 0 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"TransactItems is required",
		)
		return
	}
	if len(req.TransactItems) > 100 {
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			fmt.Sprintf(
				"Member must have length less than or equal to 100, but received length %d",
				len(req.TransactItems),
			),
		)
		return
	}

	actions := make([]TransactWriteAction, 0, len(req.TransactItems))
	for _, ti := range req.TransactItems {
		switch {
		case ti.Put != nil:
			if err := validateUnusedExprRefs(
				ti.Put.ExpressionAttributeNames, ti.Put.ExpressionAttributeValues,
				ti.Put.ConditionExpression,
			); err != nil {
				writeError(w, http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException", err.Error())
				return
			}
			var cond *ConditionCheck
			if ti.Put.ConditionExpression != "" {
				cond = &ConditionCheck{
					Expr:   ti.Put.ConditionExpression,
					Names:  ti.Put.ExpressionAttributeNames,
					Values: ti.Put.ExpressionAttributeValues,
				}
			}
			actions = append(actions, TransactWriteAction{
				Put: &TransactPut{
					TableName:                      ti.Put.TableName,
					Item:                           ti.Put.Item,
					Cond:                           cond,
					ReturnValuesOnConditionFailure: ti.Put.ReturnValuesOnConditionCheckFailure,
				},
			})

		case ti.Delete != nil:
			if err := validateUnusedExprRefs(
				ti.Delete.ExpressionAttributeNames, ti.Delete.ExpressionAttributeValues,
				ti.Delete.ConditionExpression,
			); err != nil {
				writeError(w, http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException", err.Error())
				return
			}
			var cond *ConditionCheck
			if ti.Delete.ConditionExpression != "" {
				cond = &ConditionCheck{
					Expr:   ti.Delete.ConditionExpression,
					Names:  ti.Delete.ExpressionAttributeNames,
					Values: ti.Delete.ExpressionAttributeValues,
				}
			}
			actions = append(actions, TransactWriteAction{
				Delete: &TransactDelete{
					TableName:                      ti.Delete.TableName,
					Key:                            ti.Delete.Key,
					Cond:                           cond,
					ReturnValuesOnConditionFailure: ti.Delete.ReturnValuesOnConditionCheckFailure,
				},
			})

		case ti.Update != nil:
			if err := validateUnusedExprRefs(
				ti.Update.ExpressionAttributeNames, ti.Update.ExpressionAttributeValues,
				ti.Update.UpdateExpression, ti.Update.ConditionExpression,
			); err != nil {
				writeError(w, http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException", err.Error())
				return
			}
			updates, err := parseUpdateExpression(
				ti.Update.UpdateExpression,
				ti.Update.ExpressionAttributeNames,
				ti.Update.ExpressionAttributeValues,
			)
			if err != nil {
				slog.Debug("TransactWriteItems: invalid UpdateExpression", "err", err)
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					err.Error(),
				)
				return
			}
			var cond *ConditionCheck
			if ti.Update.ConditionExpression != "" {
				cond = &ConditionCheck{
					Expr:   ti.Update.ConditionExpression,
					Names:  ti.Update.ExpressionAttributeNames,
					Values: ti.Update.ExpressionAttributeValues,
				}
			}
			actions = append(actions, TransactWriteAction{
				Update: &TransactUpdate{
					TableName:                      ti.Update.TableName,
					Key:                            ti.Update.Key,
					Updates:                        updates,
					Cond:                           cond,
					ReturnValuesOnConditionFailure: ti.Update.ReturnValuesOnConditionCheckFailure,
				},
			})

		case ti.ConditionCheck != nil:
			if ti.ConditionCheck.ConditionExpression == "" {
				writeError(
					w,
					http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException",
					"ConditionExpression is required for ConditionCheck",
				)
				return
			}
			if err := validateUnusedExprRefs(
				ti.ConditionCheck.ExpressionAttributeNames, ti.ConditionCheck.ExpressionAttributeValues,
				ti.ConditionCheck.ConditionExpression,
			); err != nil {
				writeError(w, http.StatusBadRequest,
					"com.amazonaws.dynamodb.v20120810#ValidationException", err.Error())
				return
			}
			cond := &ConditionCheck{
				Expr:   ti.ConditionCheck.ConditionExpression,
				Names:  ti.ConditionCheck.ExpressionAttributeNames,
				Values: ti.ConditionCheck.ExpressionAttributeValues,
			}
			actions = append(actions, TransactWriteAction{
				ConditionCheck: &TransactConditionCheck{
					TableName:                      ti.ConditionCheck.TableName,
					Key:                            ti.ConditionCheck.Key,
					Cond:                           cond,
					ReturnValuesOnConditionFailure: ti.ConditionCheck.ReturnValuesOnConditionCheckFailure,
				},
			})

		default:
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				"each TransactItems entry must contain Put, Delete, Update, or ConditionCheck",
			)
			return
		}
	}

	err := ro.storage.TransactWriteItems(actions)
	if err != nil {
		var txErr *TransactionCanceledError
		if errors.As(err, &txErr) {
			slog.Debug("TransactWriteItems: transaction canceled", "reasons", len(txErr.Reasons))
			codes := make([]string, len(txErr.Reasons))
			for i, r := range txErr.Reasons {
				codes[i] = r.Code
			}
			type cancelResp struct {
				Type                string               `json:"__type"`
				Message             string               `json:"message"`
				CancellationReasons []CancellationReason `json:"CancellationReasons"`
			}
			w.Header().Set("Content-Type", "application/x-amz-json-1.0")
			w.WriteHeader(http.StatusBadRequest)
			if encErr := json.NewEncoder(w).Encode(cancelResp{
				Type: "com.amazonaws.dynamodb.v20120810#TransactionCanceledException",
				Message: "Transaction cancelled, please refer cancellation reasons for specific reasons [" +
					strings.Join(codes, ", ") + "]",
				CancellationReasons: txErr.Reasons,
			}); encErr != nil {
				slog.Warn(
					"failed to encode TransactionCanceledException",
					"err",
					encErr,
				) // untestable: cancelResp contains no unencodable types
			}
			return
		}
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("TransactWriteItems: table not found")
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ResourceNotFoundException",
				"Requested resource not found",
			)
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("TransactWriteItems: validation error", "err", err)
			writeError(
				w,
				http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				err.Error(),
			)
			return
		}
		slog.Error("TransactWriteItems failed", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"com.amazonaws.dynamodb.v20120810#InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("TransactWriteItems succeeded", "count", len(actions))
	writeJSON(w, http.StatusOK, map[string]any{})
}
