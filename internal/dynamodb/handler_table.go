package dynamodb

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
)

const maxTableUserTags = 50

func (ro *Router) handleCreateTable(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName             string                 `json:"TableName"`
		KeySchema             []KeySchemaElement     `json:"KeySchema"`
		AttributeDefinitions  []AttributeDefinition  `json:"AttributeDefinitions"`
		BillingMode           string                 `json:"BillingMode"`
		ProvisionedThroughput *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
		StreamSpecification   *struct {
			StreamEnabled  *bool  `json:"StreamEnabled"`
			StreamViewType string `json:"StreamViewType"`
		} `json:"StreamSpecification"`
		GlobalSecondaryIndexes []struct {
			IndexName             string                 `json:"IndexName"`
			KeySchema             []KeySchemaElement     `json:"KeySchema"`
			Projection            map[string]any         `json:"Projection,omitempty"`
			ProvisionedThroughput *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
		} `json:"GlobalSecondaryIndexes"`
		LocalSecondaryIndexes []struct {
			IndexName  string             `json:"IndexName"`
			KeySchema  []KeySchemaElement `json:"KeySchema"`
			Projection map[string]any     `json:"Projection,omitempty"`
		} `json:"LocalSecondaryIndexes"`
		Tags []struct {
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
	if req.TableName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			"TableName is required",
		)
		return
	}
	meta := TableMetadata{
		Name:                  req.TableName,
		KeySchema:             req.KeySchema,
		AttributeDefinitions:  req.AttributeDefinitions,
		BillingMode:           req.BillingMode,
		ProvisionedThroughput: req.ProvisionedThroughput,
	}
	if req.StreamSpecification != nil {
		if req.StreamSpecification.StreamEnabled == nil {
			writeError(w, http.StatusBadRequest,
				ErrTypeValidationException,
				"StreamEnabled is required in StreamSpecification")
			return
		}
		if *req.StreamSpecification.StreamEnabled {
			if err := validateStreamViewType(req.StreamSpecification.StreamViewType); err != nil {
				writeError(w, http.StatusBadRequest,
					ErrTypeValidationException, err.Error())
				return
			}
		}
		meta.StreamSpec = &StreamSpecification{
			StreamEnabled:  *req.StreamSpecification.StreamEnabled,
			StreamViewType: req.StreamSpecification.StreamViewType,
		}
	}
	for _, g := range req.GlobalSecondaryIndexes {
		meta.GlobalSecondaryIndexes = append(meta.GlobalSecondaryIndexes, GlobalSecondaryIndex{
			IndexName:             g.IndexName,
			KeySchema:             g.KeySchema,
			Projection:            g.Projection,
			ProvisionedThroughput: g.ProvisionedThroughput,
		})
	}
	for _, l := range req.LocalSecondaryIndexes {
		meta.LocalSecondaryIndexes = append(meta.LocalSecondaryIndexes, LocalSecondaryIndex{
			IndexName:  l.IndexName,
			KeySchema:  l.KeySchema,
			Projection: l.Projection,
		})
	}
	if err := validateTableIndexes(meta.KeySchema, meta.AttributeDefinitions, meta.GlobalSecondaryIndexes, meta.LocalSecondaryIndexes); err != nil {
		slog.Debug("CreateTable: validation failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeValidationException,
			err.Error(),
		)
		return
	}
	if len(req.Tags) > maxTableUserTags {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeLimitExceededException,
			fmt.Sprintf(
				"Too many tags. Table may not have more than %d user-created tags.",
				maxTableUserTags,
			),
		)
		return
	}
	if err := ro.storage.CreateTable(meta); err != nil {
		if errors.Is(err, ErrTableAlreadyExists) {
			slog.Debug("CreateTable: table already exists", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceInUseException,
				"Table already exists: "+req.TableName,
			)
			return
		}
		slog.Error("CreateTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	if len(req.Tags) > 0 {
		tags := make(map[string]string, len(req.Tags))
		for _, t := range req.Tags {
			tags[t.Key] = t.Value
		}
		arn := fmt.Sprintf("arn:aws:dynamodb:us-east-1:000000000000:table/%s", req.TableName)
		if err := ro.storage.TagResource(arn, tags); err != nil {
			slog.Error("CreateTable: failed to store tags", "table", req.TableName, "err", err)
			if delErr := ro.storage.DeleteTable(req.TableName); delErr != nil {
				slog.Error("CreateTable: rollback failed", "table", req.TableName, "err", delErr)
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
	desc, err := ro.storage.DescribeTable(req.TableName)
	if err != nil {
		slog.Error("DescribeTable after CreateTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
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
	desc, err := ro.storage.DescribeTable(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("DeleteTable: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		slog.Error("DescribeTable before DeleteTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	if err := ro.storage.DeleteTable(req.TableName); err != nil {
		slog.Error("DeleteTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
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
	meta, err := ro.storage.DescribeTable(req.TableName)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("DescribeTable: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		slog.Error("DescribeTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	slog.Debug("described DynamoDB table", "table", req.TableName)
	writeJSON(w, http.StatusOK, map[string]any{"Table": toTableDescription(meta)})
}

func (ro *Router) handleListTables(w http.ResponseWriter, body []byte) {
	var req struct {
		Limit                   *int   `json:"Limit"`
		ExclusiveStartTableName string `json:"ExclusiveStartTableName"`
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
	limit := 100
	if req.Limit != nil {
		if *req.Limit < 1 || *req.Limit > 100 {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeValidationException,
				fmt.Sprintf(
					"Value %d at 'limit' failed to satisfy constraint: Member must have value between 1 and 100, inclusive",
					*req.Limit,
				),
			)
			return
		}
		limit = *req.Limit
	}
	names, err := ro.storage.ListTables()
	if err != nil {
		slog.Error("ListTables failed", "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	if names == nil {
		names = []string{}
	}
	sort.Strings(names)
	// Apply ExclusiveStartTableName pagination cursor.
	// If the cursor table was deleted between calls, resume from the next alphabetically following name.
	if req.ExclusiveStartTableName != "" {
		start := len(names) // default: empty result if cursor is past all names
		for i, name := range names {
			if name == req.ExclusiveStartTableName {
				start = i + 1
				break
			}
			if name > req.ExclusiveStartTableName {
				start = i
				break
			}
		}
		names = names[start:]
	}
	resp := map[string]any{}
	if len(names) > limit {
		resp["LastEvaluatedTableName"] = names[limit-1]
		names = names[:limit]
	}
	resp["TableNames"] = names
	slog.Debug("listed DynamoDB tables", "count", len(names))
	writeJSON(w, http.StatusOK, resp)
}

func (ro *Router) handleUpdateTable(w http.ResponseWriter, body []byte) {
	var req struct {
		TableName             string                `json:"TableName"`
		BillingMode           string                `json:"BillingMode"`
		AttributeDefinitions  []AttributeDefinition `json:"AttributeDefinitions"`
		ProvisionedThroughput *struct {
			ReadCapacityUnits  int64 `json:"ReadCapacityUnits"`
			WriteCapacityUnits int64 `json:"WriteCapacityUnits"`
		} `json:"ProvisionedThroughput"`
		StreamSpecification *struct {
			StreamEnabled  *bool  `json:"StreamEnabled"`
			StreamViewType string `json:"StreamViewType"`
		} `json:"StreamSpecification"`
		GlobalSecondaryIndexUpdates []struct {
			Create *struct {
				IndexName             string             `json:"IndexName"`
				KeySchema             []KeySchemaElement `json:"KeySchema"`
				Projection            map[string]any     `json:"Projection"`
				ProvisionedThroughput *struct {
					ReadCapacityUnits  int64 `json:"ReadCapacityUnits"`
					WriteCapacityUnits int64 `json:"WriteCapacityUnits"`
				} `json:"ProvisionedThroughput"`
			} `json:"Create"`
			Update *struct {
				IndexName             string `json:"IndexName"`
				ProvisionedThroughput struct {
					ReadCapacityUnits  int64 `json:"ReadCapacityUnits"`
					WriteCapacityUnits int64 `json:"WriteCapacityUnits"`
				} `json:"ProvisionedThroughput"`
			} `json:"Update"`
			Delete *struct {
				IndexName string `json:"IndexName"`
			} `json:"Delete"`
		} `json:"GlobalSecondaryIndexUpdates"`
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

	in := UpdateTableInput{
		BillingMode:          req.BillingMode,
		AttributeDefinitions: req.AttributeDefinitions,
	}
	if req.StreamSpecification != nil {
		if req.StreamSpecification.StreamEnabled == nil {
			writeError(w, http.StatusBadRequest,
				ErrTypeValidationException,
				"StreamEnabled is required in StreamSpecification")
			return
		}
		if *req.StreamSpecification.StreamEnabled {
			if err := validateStreamViewType(req.StreamSpecification.StreamViewType); err != nil {
				writeError(w, http.StatusBadRequest,
					ErrTypeValidationException, err.Error())
				return
			}
		}
		in.StreamSpec = &StreamSpecification{
			StreamEnabled:  *req.StreamSpecification.StreamEnabled,
			StreamViewType: req.StreamSpecification.StreamViewType,
		}
	}
	if req.ProvisionedThroughput != nil {
		in.ProvisionedThroughput = &ProvisionedThroughput{
			ReadCapacityUnits:  req.ProvisionedThroughput.ReadCapacityUnits,
			WriteCapacityUnits: req.ProvisionedThroughput.WriteCapacityUnits,
		}
	}
	for _, update := range req.GlobalSecondaryIndexUpdates {
		switch {
		case update.Create != nil:
			gsi := GlobalSecondaryIndex{
				IndexName:  update.Create.IndexName,
				KeySchema:  update.Create.KeySchema,
				Projection: update.Create.Projection,
			}
			if update.Create.ProvisionedThroughput != nil {
				gsi.ProvisionedThroughput = &ProvisionedThroughput{
					ReadCapacityUnits:  update.Create.ProvisionedThroughput.ReadCapacityUnits,
					WriteCapacityUnits: update.Create.ProvisionedThroughput.WriteCapacityUnits,
				}
			}
			in.GSICreates = append(in.GSICreates, gsi)
		case update.Update != nil:
			if in.GSIUpdates == nil {
				in.GSIUpdates = make(map[string]*ProvisionedThroughput)
			}
			in.GSIUpdates[update.Update.IndexName] = &ProvisionedThroughput{
				ReadCapacityUnits:  update.Update.ProvisionedThroughput.ReadCapacityUnits,
				WriteCapacityUnits: update.Update.ProvisionedThroughput.WriteCapacityUnits,
			}
		case update.Delete != nil:
			in.GSIDeletes = append(in.GSIDeletes, update.Delete.IndexName)
		}
	}

	meta, err := ro.storage.UpdateTable(req.TableName, in)
	if err != nil {
		if errors.Is(err, ErrTableNotFound) {
			slog.Debug("UpdateTable: table not found", "table", req.TableName)
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"Requested resource not found: Table: "+req.TableName+" not found",
			)
			return
		}
		slog.Error("UpdateTable failed", "table", req.TableName, "err", err)
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalServerError,
			"internal server error",
		)
		return
	}
	slog.Info("updated DynamoDB table", "table", req.TableName)
	writeJSON(w, http.StatusOK, map[string]any{"TableDescription": toTableDescription(meta)})
}
