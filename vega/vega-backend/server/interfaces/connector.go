// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

// ConnectorConfig holds data source connection configuration.
type ConnectorConfig map[string]any

// TableMeta represents table/asset metadata.
type TableMeta struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Database    string                `json:"database"`   // The name of the affiliated database (used for instance-level connections)
	Schema      string                `json:"schema"`     // The name of the schema to which it belongs (used when making instance-level connections)
	TableType   string                `json:"table_type"` // table | view | materialized_view
	Properties  map[string]any        `json:"properties"` // Extended attributes: charset, collation, engine, row_count, etc
	Columns     []TableColumnMeta     `json:"columns"`
	PKs         []string              `json:"primary_keys"`
	Indices     []TableIndexMeta      `json:"indices"`      // Index list
	ForeignKeys []TableForeignKeyMeta `json:"foreign_keys"` // List of foreign keys
}

// TableColumnMeta represents column metadata.
type TableColumnMeta struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	AliasType   string `json:"alias_type,omitempty"`
	Description string `json:"description"`

	Nullable          bool   `json:"nullable"`
	DefaultValue      string `json:"default_value,omitempty"`      // Default value
	CharMaxLen        int    `json:"char_max_len,omitempty"`       // Maximum character length
	NumPrecision      int    `json:"num_precision,omitempty"`      // Numerical accuracy
	NumScale          int    `json:"num_scale,omitempty"`          // Decimal place of numerical value
	DatetimePrecision int    `json:"datetime_precision,omitempty"` // Date and time accuracy
	Charset           string `json:"charset,omitempty"`            // Character set
	Collation         string `json:"collation,omitempty"`          // Sorting rule
	OrdinalPosition   int    `json:"ordinal_position"`             // Column position (starting from 1
	CheckConstraint   string `json:"check_constraint,omitempty"`
	ColumnKey         string `json:"column_key"` // Column key
}

// TableIndexMeta represents index metadata.
type TableIndexMeta struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
}

// TableForeignKeyMeta represents foreign key metadata.
type TableForeignKeyMeta struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns"`
	OnDelete   string   `json:"on_delete,omitempty"`
	OnUpdate   string   `json:"on_update,omitempty"`
}

// QueryResult represents query execution result.
type QueryResult struct {
	Columns     []string         `json:"columns"`
	Entries     []map[string]any `json:"entries"`
	Total       int64            `json:"total"`
	SearchAfter []any            `json:"-"` // OpenSearch cursor continuation state
}

// FileMeta represents file metadata.
type FileMeta struct {
	Path         string `json:"path"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	LastModified int64  `json:"last_modified"`
	ContentType  string `json:"content_type"`
}

// FilesetMeta represents a file or folder object from a fileset-capable source (e.g. AnyShare).
type FilesetMeta struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	DisplayPath    string              `json:"display_path"` // human-readable path hint for UI / source_identifier option
	SourceMetadata map[string]any      `json:"-"`            // flattened into Resource.SourceMetadata on discover
	Columns        []FilesetColumnMeta `json:"columns"`
}

// FilesetColumnMeta represents column metadata.
type FilesetColumnMeta struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// TopicMeta represents message topic metadata.
type TopicMeta struct {
	Name       string `json:"name"`
	Partitions int    `json:"partitions"`
	Replicas   int    `json:"replicas"`
}

// MetricResult represents time-series query result.
type MetricResult struct {
	Metric string            `json:"metric"`
	Values []MetricValue     `json:"values"`
	Labels map[string]string `json:"labels"`
}

// MetricValue represents a single metric data point.
type MetricValue struct {
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

// IndexMeta represents search index metadata.
type IndexMeta struct {
	Name         string                    `json:"name"`
	CreationTime int64                     `json:"creation_time,omitempty"`
	Description  string                    `json:"description"`
	Properties   map[string]any            `json:"properties"`
	Mapping      map[string]IndexFieldMeta `json:"mapping"`
	MappingMeta  map[string]any            `json:"mapping_meta"`
}

// IndexFieldMeta represents index field metadata.
type IndexFieldMeta struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Type        string         `json:"type"`
	Analyzer    string         `json:"analyzer,omitempty"`
	Searchable  bool           `json:"searchable"`
	Attributes  map[string]any `json:"attributes"`
	// SubFields (multi-fields) are arranged in alphabetical order by Name to ensure stable serialization
	SubFields []IndexSubFieldMeta `json:"sub_fields,omitempty"`
}

// IndexSubFieldMeta represents an OpenSearch multi-field sub-field.
type IndexSubFieldMeta struct {
	Name       string         `json:"name"`       // Subfield name (such as "keyword"
	Type       string         `json:"type"`       // Subfield type (such as "keyword"
	Attributes map[string]any `json:"attributes"` // Attributes of subfields other than type
}

// HealthStatus represents connection health status.
type HealthStatus struct {
	Status  string `json:"status"` // green, yellow, red, unknown
	Message string `json:"message"`
}
