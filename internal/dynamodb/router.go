package dynamodb

import (
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
	PutItem(tableName string, item map[string]any, cond *ConditionCheck) (map[string]any, error)
	GetItem(tableName string, key map[string]any) (map[string]any, error)
	DeleteItem(tableName string, key map[string]any, cond *ConditionCheck) (map[string]any, error)
	Scan(tableName string, opts ScanOptions) ([]map[string]any, map[string]any, error)
	UpdateItem(
		tableName string,
		key map[string]any,
		updates map[string]any,
		cond *ConditionCheck,
	) (map[string]any, map[string]any, error)
	Query(
		tableName, hashKeyName string,
		hashKeyValue any,
		skCond *SortKeyCondition,
		opts QueryOptions,
	) ([]map[string]any, map[string]any, error)
	BatchGetItems(tableName string, keys []map[string]any) ([]map[string]any, error)
	BatchWriteItems(tableName string, puts []map[string]any, deletes []map[string]any) error
	UpdateTimeToLive(tableName string, spec TTLSpec) (TTLSpec, error)
	DescribeTimeToLive(tableName string) (string, *TTLSpec, error)
	TagResource(resourceARN string, tags map[string]string) error
	UntagResource(resourceARN string, tagKeys []string) error
	ListTagsOfResource(resourceARN string) (map[string]string, error)
	UpdateTable(tableName string, in UpdateTableInput) (TableMetadata, error)
	TransactGetItems(gets []TransactGetInput) ([]map[string]any, error)
	TransactWriteItems(actions []TransactWriteAction) error
	DescribeContinuousBackups(tableName string) (TableMetadata, error)
	UpdateContinuousBackups(tableName string, enabled bool) (TableMetadata, error)
	DescribeKinesisStreamingDestination(tableName string) ([]KinesisDestination, error)
	EnableKinesisStreamingDestination(
		tableName, streamARN, precision string,
	) (KinesisDestination, bool, error)
	DisableKinesisStreamingDestination(tableName, streamARN string) (KinesisDestination, error)
}

// billingModeSummary mirrors the AWS BillingModeSummary shape.
type billingModeSummary struct {
	BillingMode                       string  `json:"BillingMode"`
	LastUpdateToPayPerRequestDateTime float64 `json:"LastUpdateToPayPerRequestDateTime,omitempty"`
}

// warmThroughput mirrors the AWS WarmThroughput shape returned in TableDescription.
// Required by AWS provider v6+ which polls this field after CreateTable.
type warmThroughput struct {
	ReadUnitsPerSecond  int64  `json:"ReadUnitsPerSecond"`
	WriteUnitsPerSecond int64  `json:"WriteUnitsPerSecond"`
	Status              string `json:"Status"`
}

// tableDescription is the DynamoDB API representation of a table.
type tableDescription struct {
	TableName              string                 `json:"TableName"`
	TableStatus            string                 `json:"TableStatus"`
	TableArn               string                 `json:"TableArn"`
	CreationDateTime       float64                `json:"CreationDateTime"`
	KeySchema              []KeySchemaElement     `json:"KeySchema"`
	AttributeDefinitions   []AttributeDefinition  `json:"AttributeDefinitions"`
	BillingModeSummary     *billingModeSummary    `json:"BillingModeSummary,omitempty"`
	ProvisionedThroughput  *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
	GlobalSecondaryIndexes []gsiDescription       `json:"GlobalSecondaryIndexes,omitempty"`
	LocalSecondaryIndexes  []lsiDescription       `json:"LocalSecondaryIndexes,omitempty"`
	WarmThroughput         *warmThroughput        `json:"WarmThroughput,omitempty"`
	ItemCount              int64                  `json:"ItemCount"`
	TableSizeBytes         int64                  `json:"TableSizeBytes"`
}

type gsiDescription struct {
	IndexName             string                 `json:"IndexName"`
	IndexStatus           string                 `json:"IndexStatus"`
	KeySchema             []KeySchemaElement     `json:"KeySchema"`
	Projection            map[string]any         `json:"Projection,omitempty"`
	ProvisionedThroughput *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
	IndexSizeBytes        int64                  `json:"IndexSizeBytes"`
	ItemCount             int64                  `json:"ItemCount"`
}

type lsiDescription struct {
	IndexName             string                 `json:"IndexName"`
	IndexStatus           string                 `json:"IndexStatus"`
	KeySchema             []KeySchemaElement     `json:"KeySchema"`
	Projection            map[string]any         `json:"Projection,omitempty"`
	ProvisionedThroughput *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
	IndexSizeBytes        int64                  `json:"IndexSizeBytes"`
	ItemCount             int64                  `json:"ItemCount"`
}

func toTableDescription(m TableMetadata) tableDescription {
	desc := tableDescription{
		TableName:   m.Name,
		TableStatus: m.Status,
		TableArn: fmt.Sprintf(
			"arn:aws:dynamodb:us-east-1:000000000000:table/%s",
			m.Name,
		),
		CreationDateTime:      float64(m.CreatedAt.Unix()),
		KeySchema:             m.KeySchema,
		AttributeDefinitions:  m.AttributeDefinitions,
		ProvisionedThroughput: m.ProvisionedThroughput,
		WarmThroughput:        &warmThroughput{Status: "ACTIVE"},
	}
	if m.BillingMode != "" {
		bms := &billingModeSummary{BillingMode: m.BillingMode}
		if m.BillingModeUpdatedAt != nil {
			bms.LastUpdateToPayPerRequestDateTime = float64(m.BillingModeUpdatedAt.Unix())
		}
		desc.BillingModeSummary = bms
	}
	for _, gsi := range m.GlobalSecondaryIndexes {
		desc.GlobalSecondaryIndexes = append(desc.GlobalSecondaryIndexes, gsiDescription{
			IndexName:             gsi.IndexName,
			IndexStatus:           "ACTIVE",
			KeySchema:             gsi.KeySchema,
			Projection:            gsi.Projection,
			ProvisionedThroughput: gsi.ProvisionedThroughput,
		})
	}
	for _, lsi := range m.LocalSecondaryIndexes {
		desc.LocalSecondaryIndexes = append(desc.LocalSecondaryIndexes, lsiDescription{
			IndexName:             lsi.IndexName,
			IndexStatus:           "ACTIVE",
			KeySchema:             lsi.KeySchema,
			Projection:            lsi.Projection,
			ProvisionedThroughput: m.ProvisionedThroughput,
		})
	}
	return desc
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
		writeError(
			w,
			http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			"failed to read request body",
		)
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
	case "UpdateItem":
		ro.handleUpdateItem(w, body)
	case "Query":
		ro.handleQuery(w, body)
	case "BatchGetItem":
		ro.handleBatchGetItem(w, body)
	case "BatchWriteItem":
		ro.handleBatchWriteItem(w, body)
	case "UpdateTable":
		ro.handleUpdateTable(w, body)
	case "UpdateTimeToLive":
		ro.handleUpdateTimeToLive(w, body)
	case "DescribeTimeToLive":
		ro.handleDescribeTimeToLive(w, body)
	case "TagResource":
		ro.handleTagResource(w, body)
	case "UntagResource":
		ro.handleUntagResource(w, body)
	case "ListTagsOfResource":
		ro.handleListTagsOfResource(w, body)
	case "TransactGetItems":
		ro.handleTransactGetItems(w, body)
	case "TransactWriteItems":
		ro.handleTransactWriteItems(w, body)
	case "DescribeContinuousBackups":
		ro.handleDescribeContinuousBackups(w, body)
	case "UpdateContinuousBackups":
		ro.handleUpdateContinuousBackups(w, body)
	case "DescribeKinesisStreamingDestination":
		ro.handleDescribeKinesisStreamingDestination(w, body)
	case "EnableKinesisStreamingDestination":
		ro.handleEnableKinesisStreamingDestination(w, body)
	case "DisableKinesisStreamingDestination":
		ro.handleDisableKinesisStreamingDestination(w, body)
	case "DescribeLimits":
		ro.handleDescribeLimits(w)
	case "DescribeEndpoints":
		ro.handleDescribeEndpoints(w)
	default:
		slog.Debug( // #nosec G706 -- target comes from the X-Amz-Target header; log injection risk accepted for a local dev emulator
			"DynamoDB operation not implemented",
			"target",
			target,
		)
		writeError(
			w,
			http.StatusNotImplemented,
			"com.amazonaws.dynamodb.v20120810#NotImplemented",
			"Operation not implemented: "+op,
		)
	}
}

func validateTableIndexes(
	tableKeySchema []KeySchemaElement,
	attrDefs []AttributeDefinition,
	gsis []GlobalSecondaryIndex,
	lsis []LocalSecondaryIndex,
) error {
	defined := make(map[string]bool, len(attrDefs))
	for _, a := range attrDefs {
		defined[a.AttributeName] = true
	}

	tableHashKey := ""
	for _, k := range tableKeySchema {
		if !defined[k.AttributeName] {
			return fmt.Errorf(
				"%w: attribute '%s' is used in table key schema but not defined in AttributeDefinitions",
				ErrValidationException,
				k.AttributeName,
			)
		}
		if k.KeyType == "HASH" {
			tableHashKey = k.AttributeName
		}
	}

	if len(lsis) > 5 {
		return fmt.Errorf(
			"%w: number of local secondary indexes exceeds per-table limit of 5",
			ErrValidationException,
		)
	}

	for _, gsi := range gsis {
		hasHash := false
		for _, k := range gsi.KeySchema {
			if k.KeyType == "HASH" {
				hasHash = true
			}
			if !defined[k.AttributeName] {
				return fmt.Errorf(
					"%w: attribute '%s' is used in index '%s' but not defined in AttributeDefinitions",
					ErrValidationException,
					k.AttributeName,
					gsi.IndexName,
				)
			}
		}
		if !hasHash {
			return fmt.Errorf(
				"%w: GlobalSecondaryIndex '%s' must have a HASH key element",
				ErrValidationException, gsi.IndexName,
			)
		}
	}

	indexNames := make(map[string]bool, len(gsis)+len(lsis))
	for _, gsi := range gsis {
		if indexNames[gsi.IndexName] {
			return fmt.Errorf(
				"%w: duplicate index name '%s'",
				ErrValidationException, gsi.IndexName,
			)
		}
		indexNames[gsi.IndexName] = true
	}

	for _, lsi := range lsis {
		if indexNames[lsi.IndexName] {
			return fmt.Errorf(
				"%w: duplicate index name '%s'",
				ErrValidationException, lsi.IndexName,
			)
		}
		indexNames[lsi.IndexName] = true

		lsiHashKey := ""
		hasRange := false
		for _, k := range lsi.KeySchema {
			switch k.KeyType {
			case "HASH":
				lsiHashKey = k.AttributeName
			case "RANGE":
				hasRange = true
			}
			if !defined[k.AttributeName] {
				return fmt.Errorf(
					"%w: attribute '%s' is used in index '%s' but not defined in AttributeDefinitions",
					ErrValidationException,
					k.AttributeName,
					lsi.IndexName,
				)
			}
		}
		if lsiHashKey != tableHashKey {
			return fmt.Errorf(
				"%w: LocalSecondaryIndex '%s' must have the same HASH key as the table (expected '%s')",
				ErrValidationException,
				lsi.IndexName,
				tableHashKey,
			)
		}
		if !hasRange {
			return fmt.Errorf(
				"%w: LocalSecondaryIndex '%s' must have a RANGE key element",
				ErrValidationException, lsi.IndexName,
			)
		}
	}

	return nil
}
