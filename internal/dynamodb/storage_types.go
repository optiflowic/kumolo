package dynamodb

import "time"

// KeySchemaElement is one element of a table's key schema.
type KeySchemaElement struct {
	AttributeName string `json:"AttributeName"`
	KeyType       string `json:"KeyType"`
}

// AttributeDefinition describes a key attribute's type.
type AttributeDefinition struct {
	AttributeName string `json:"AttributeName"`
	AttributeType string `json:"AttributeType"`
}

// TTLSpec holds the TimeToLive configuration for a table.
type TTLSpec struct {
	AttributeName string `json:"attributeName"`
	Enabled       bool   `json:"enabled"`
}

type PITRStatus struct {
	Enabled   bool       `json:"enabled"`
	EnabledAt *time.Time `json:"enabledAt,omitempty"`
}

type KinesisDestination struct {
	StreamARN string `json:"streamArn"`
	Status    string `json:"status"`    // ACTIVE | DISABLED
	Precision string `json:"precision"` // MILLISECOND | MICROSECOND
}

// ProvisionedThroughput holds read/write capacity units.
type ProvisionedThroughput struct {
	ReadCapacityUnits  int64 `json:"ReadCapacityUnits,omitempty"`
	WriteCapacityUnits int64 `json:"WriteCapacityUnits,omitempty"`
}

// GlobalSecondaryIndex holds the definition of a GSI.
type GlobalSecondaryIndex struct {
	IndexName             string                 `json:"indexName"`
	KeySchema             []KeySchemaElement     `json:"keySchema"`
	Projection            map[string]any         `json:"projection,omitempty"`
	ProvisionedThroughput *ProvisionedThroughput `json:"provisionedThroughput,omitempty"`
}

// LocalSecondaryIndex holds the definition of an LSI.
type LocalSecondaryIndex struct {
	IndexName  string             `json:"indexName"`
	KeySchema  []KeySchemaElement `json:"keySchema"`
	Projection map[string]any     `json:"projection,omitempty"`
}

// TableMetadata is stored as <table>.table.json at the storage root.
type TableMetadata struct {
	Name                   string                 `json:"name"`
	KeySchema              []KeySchemaElement     `json:"keySchema"`
	AttributeDefinitions   []AttributeDefinition  `json:"attributeDefinitions"`
	BillingMode            string                 `json:"billingMode,omitempty"`
	BillingModeUpdatedAt   *time.Time             `json:"billingModeUpdatedAt,omitempty"`
	ProvisionedThroughput  *ProvisionedThroughput `json:"provisionedThroughput,omitempty"`
	GlobalSecondaryIndexes []GlobalSecondaryIndex `json:"globalSecondaryIndexes,omitempty"`
	LocalSecondaryIndexes  []LocalSecondaryIndex  `json:"localSecondaryIndexes,omitempty"`
	Status                 string                 `json:"status"`
	CreatedAt              time.Time              `json:"createdAt"`
	TTL                    *TTLSpec               `json:"ttl,omitempty"`
	Tags                   map[string]string      `json:"tags,omitempty"`
	PITR                   *PITRStatus            `json:"pitr,omitempty"`
	KinesisDestinations    []KinesisDestination   `json:"kinesisDestinations,omitempty"`
	StreamSpec             *StreamSpecification   `json:"streamSpec,omitempty"`
	StreamLabel            string                 `json:"streamLabel,omitempty"`
}

// Sort key condition operators used in SortKeyCondition.Operator.
const (
	OpEQ         = "="
	OpLT         = "<"
	OpLTE        = "<="
	OpGT         = ">"
	OpGTE        = ">="
	OpBETWEEN    = "BETWEEN"
	OpBeginsWith = "begins_with"
)

// SortKeyCondition describes an optional sort key filter applied during Query.
type SortKeyCondition struct {
	Name     string
	Operator string // one of the Op* constants
	Value    any    // comparison value (DynamoDB typed)
	Value2   any    // upper bound for BETWEEN
}

// QueryOptions controls pagination and sort order for Query.
type QueryOptions struct {
	ScanIndexForward  bool
	Limit             *int // nil means no limit; must be >= 1 when set
	ExclusiveStartKey map[string]any
	IndexName         string // non-empty to query a GSI or LSI
}

// ScanOptions controls pagination and parallel scan for Scan.
type ScanOptions struct {
	Limit             *int           // nil means no limit; must be >= 1 when set
	ExclusiveStartKey map[string]any // resume from the item after this primary key
	Segment           *int           // parallel scan: 0-based segment index
	TotalSegments     *int           // parallel scan: total number of segments
}

// StreamSpecification holds DynamoDB Streams configuration for a table.
type StreamSpecification struct {
	StreamEnabled  bool   `json:"streamEnabled"`
	StreamViewType string `json:"streamViewType,omitempty"`
}

// ConditionCheck holds a parsed ConditionExpression with its attribute maps.
type ConditionCheck struct {
	Expr   string
	Names  map[string]string
	Values map[string]any
}
