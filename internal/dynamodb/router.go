package dynamodb

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

type store interface {
	CreateTable(meta TableMetadata) error
	DeleteTable(name string) error
	DescribeTable(name string) (TableMetadata, error)
	ListTables() ([]string, error)
	PutItem(tableName string, item map[string]any) error
	GetItem(tableName string, key map[string]any) (map[string]any, error)
	DeleteItem(tableName string, key map[string]any) error
	Scan(tableName string) ([]map[string]any, error)
}

// tableDescription is the DynamoDB API representation of a table.
type tableDescription struct {
	TableName            string                `json:"TableName"`
	TableStatus          string                `json:"TableStatus"`
	TableArn             string                `json:"TableArn"`
	CreationDateTime     float64               `json:"CreationDateTime"`
	KeySchema            []KeySchemaElement    `json:"KeySchema"`
	AttributeDefinitions []AttributeDefinition `json:"AttributeDefinitions"`
	ItemCount            int64                 `json:"ItemCount"`
	TableSizeBytes       int64                 `json:"TableSizeBytes"`
}

func toTableDescription(m TableMetadata) tableDescription {
	return tableDescription{
		TableName:   m.Name,
		TableStatus: m.Status,
		TableArn: fmt.Sprintf(
			"arn:aws:dynamodb:us-east-1:000000000000:table/%s",
			m.Name,
		),
		CreationDateTime:     float64(m.CreatedAt.Unix()),
		KeySchema:            m.KeySchema,
		AttributeDefinitions: m.AttributeDefinitions,
	}
}

// Router handles DynamoDB API requests dispatched via the X-Amz-Target header.
type Router struct {
	storage store
}

func NewRouter(storage *Storage) *Router {
	return &Router{storage: storage}
}

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	op := strings.TrimPrefix(target, "DynamoDB_20120810.")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "failed to read request body")
		return
	}

	switch op {
	case "CreateTable":
		ro.handleCreateTable(w, body)
	case "DeleteTable":
		ro.handleDeleteTable(w, body)
	case "DescribeTable":
		ro.handleDescribeTable(w, body)
	case "ListTables":
		ro.handleListTables(w, body)
	case "PutItem":
		ro.handlePutItem(w, body)
	case "GetItem":
		ro.handleGetItem(w, body)
	case "DeleteItem":
		ro.handleDeleteItem(w, body)
	case "Scan":
		ro.handleScan(w, body)
	default:
		slog.Debug( // #nosec G706 -- target comes from the X-Amz-Target header; log injection risk accepted for a local dev emulator
			"DynamoDB operation not implemented",
			"target",
			target,
		)
		writeError(w, http.StatusNotImplemented, "NotImplemented", "Operation not implemented: "+op)
	}
}

func (ro *Router) handleCreateTable(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName            string                `json:"TableName"`
		KeySchema            []KeySchemaElement    `json:"KeySchema"`
		AttributeDefinitions []AttributeDefinition `json:"AttributeDefinitions"`
		BillingMode          string                `json:"BillingMode"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.TableName == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "TableName is required")
		return
	}
	meta := TableMetadata{
		Name:                 req.TableName,
		KeySchema:            req.KeySchema,
		AttributeDefinitions: req.AttributeDefinitions,
		BillingMode:          req.BillingMode,
	}
	if err := ro.storage.CreateTable(meta); err != nil {
		if errors.Is(err, ErrTableAlreadyExists) {
			slog.Debug("CreateTable: table already exists", "table", req.TableName)
			writeError(w, http.StatusBadRequest, "ResourceInUseException",
				"Table already exists: "+req.TableName)
			return
		}
		slog.Error("CreateTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"InternalServerError",
			"internal server error",
		)
		return
	}
	desc, err := ro.storage.DescribeTable(req.TableName)
	if err != nil {
		slog.Error("DescribeTable after CreateTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("created DynamoDB table", "table", req.TableName)
	writeJSON(w, http.StatusOK, map[string]any{"TableDescription": toTableDescription(desc)})
}

func (ro *Router) handleDeleteTable(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName string `json:"TableName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.TableName == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "TableName is required")
		return
	}
	desc, err := ro.storage.DescribeTable(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("DeleteTable: table not found", "table", req.TableName)
			writeError(w, http.StatusBadRequest, "ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found")
			return
		}
		slog.Error("DescribeTable before DeleteTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"InternalServerError",
			"internal server error",
		)
		return
	}
	if err := ro.storage.DeleteTable(req.TableName); err != nil {
		slog.Error("DeleteTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("deleted DynamoDB table", "table", req.TableName)
	d := toTableDescription(desc)
	d.TableStatus = "DELETING"
	writeJSON(w, http.StatusOK, map[string]any{"TableDescription": d})
}

func (ro *Router) handleDescribeTable(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName string `json:"TableName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.TableName == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "TableName is required")
		return
	}
	meta, err := ro.storage.DescribeTable(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("DescribeTable: table not found", "table", req.TableName)
			writeError(w, http.StatusBadRequest, "ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found")
			return
		}
		slog.Error("DescribeTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Debug("described DynamoDB table", "table", req.TableName)
	writeJSON(w, http.StatusOK, map[string]any{"Table": toTableDescription(meta)})
}

func (ro *Router) handleListTables(w http.ResponseWriter, body []byte) {
	var req struct {
		Limit                   int    `json:"Limit"`
		ExclusiveStartTableName string `json:"ExclusiveStartTableName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	names, err := ro.storage.ListTables()
	if err != nil {
		slog.Error("ListTables failed", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"InternalServerError",
			"internal server error",
		)
		return
	}
	if names == nil {
		names = []string{}
	}
	slog.Debug("listed DynamoDB tables", "count", len(names))
	writeJSON(w, http.StatusOK, map[string]any{"TableNames": names})
}

func (ro *Router) handlePutItem(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName string         `json:"TableName"`
		Item      map[string]any `json:"Item"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.TableName == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "TableName is required")
		return
	}
	if err := ro.storage.PutItem(req.TableName, req.Item); err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("PutItem: table not found", "table", req.TableName)
			writeError(w, http.StatusBadRequest, "ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found")
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("PutItem: validation error", "table", req.TableName, "err", err)
			writeError(w, http.StatusBadRequest, "ValidationException", err.Error())
			return
		}
		slog.Error("PutItem failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("put DynamoDB item", "table", req.TableName)
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (ro *Router) handleGetItem(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName string         `json:"TableName"`
		Key       map[string]any `json:"Key"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.TableName == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "TableName is required")
		return
	}
	item, err := ro.storage.GetItem(req.TableName, req.Key)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("GetItem: table not found", "table", req.TableName)
			writeError(w, http.StatusBadRequest, "ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found")
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("GetItem: validation error", "table", req.TableName, "err", err)
			writeError(w, http.StatusBadRequest, "ValidationException", err.Error())
			return
		}
		slog.Error("GetItem failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"InternalServerError",
			"internal server error",
		)
		return
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
		TableName string         `json:"TableName"`
		Key       map[string]any `json:"Key"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.TableName == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "TableName is required")
		return
	}
	if err := ro.storage.DeleteItem(req.TableName, req.Key); err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("DeleteItem: table not found", "table", req.TableName)
			writeError(w, http.StatusBadRequest, "ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found")
			return
		}
		if errors.Is(err, ErrValidationException) {
			slog.Debug("DeleteItem: validation error", "table", req.TableName, "err", err)
			writeError(w, http.StatusBadRequest, "ValidationException", err.Error())
			return
		}
		slog.Error("DeleteItem failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"InternalServerError",
			"internal server error",
		)
		return
	}
	slog.Info("deleted DynamoDB item", "table", req.TableName)
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (ro *Router) handleScan(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName string `json:"TableName"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid request body")
		return
	}
	if req.TableName == "" {
		writeError(w, http.StatusBadRequest, "ValidationException", "TableName is required")
		return
	}
	items, err := ro.storage.Scan(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("Scan: table not found", "table", req.TableName)
			writeError(w, http.StatusBadRequest, "ResourceNotFoundException",
				"Requested resource not found: Table: "+req.TableName+" not found")
			return
		}
		slog.Error("Scan failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			"InternalServerError",
			"internal server error",
		)
		return
	}
	if items == nil {
		items = []map[string]any{}
	}
	slog.Debug("scanned DynamoDB table", "table", req.TableName, "count", len(items))
	writeJSON(w, http.StatusOK, map[string]any{
		"Items":        items,
		"Count":        len(items),
		"ScannedCount": len(items),
	})
}
