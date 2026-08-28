// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/connector_interface.go -destination ../interfaces/mock/mock_connector_interface.go

// The Connector defines the basic connector interface
type Connector interface {
	GetType() string
	GetName() string
	GetMode() string
	GetCategory() string

	GetEnabled() bool
	SetEnabled(bool)

	// GetSensitiveFields returns a list of sensitive fields for this connector (such as password)
	GetSensitiveFields() []string
	// GetFieldConfig returns the field configuration definition of this connector (compatible with JSON Schema properties)
	GetFieldConfig() map[string]ConnectorFieldConfig

	New(cfg ConnectorConfig) (Connector, error)

	Connect(ctx context.Context) error
	Ping(ctx context.Context) error
	Close(ctx context.Context) error
	TestConnection(ctx context.Context) error

	GetMetadata(ctx context.Context) (map[string]any, error)
}

// LocalConnectorBuilder is a local connector builder function
type LocalConnectorBuilder func(cfg *ConnectorConfig) (Connector, error)

// TableConnector defines the interface for relational database connectors.
// Implementations: mysql, postgresql, dameng, oracle, clickhouse, etc.
type TableConnector interface {
	Connector

	// MapType maps the native types at the source end to VEGA unified types. If not recognized, always return "Other"
	MapType(nativeType string) string

	ListTables(ctx context.Context) ([]*TableMeta, error)
	GetTableMeta(ctx context.Context, table *TableMeta) error
	// GetTableMetaByIdentifier retrieves complete metadata for one known table.
	GetTableMetaByIdentifier(ctx context.Context, sourceIdentifier string) (*TableMeta, error)

	// ExecuteQuery executes a single-table query statement
	ExecuteQuery(ctx context.Context, resource *Resource, params *ResourceDataQueryParams) (*QueryResult, error)
	// BuildPagedSQL wraps a validated read-only SQL statement with the
	// connector-specific offset/limit syntax.
	BuildPagedSQL(sql string, offset, limit int) string
	// BuildCountSQL wraps a validated read-only SQL statement with the
	// connector-specific total-count syntax.
	BuildCountSQL(sql string) string
	// ExecuteRawSQL executes read-only SQL that has already passed unified query validation.
	ExecuteRawSQL(ctx context.Context, sql string) (*RawQueryResponse, error)
}

// FileConnector defines the interface for file/document storage connectors.
// Implementations: s3, hdfs, minio, feishu, notion, etc.
type FileConnector interface {
	Connector
}

// FilesetConnector defines the interface for file/document storage connectors.
// Implementations: anyshare, s3, hdfs, minio, feishu, notion, etc.
type FilesetConnector interface {
	Connector

	// ListFilesets lists file and folder objects for discovery (typically one level per parent).
	ListFilesets(ctx context.Context) ([]*FilesetMeta, error)
	// ExecuteQuery executes a query on the fileset
	ExecuteQuery(ctx context.Context, resource *Resource, params *ResourceDataQueryParams) (*QueryResult, error)
}

// TopicConnector defines the interface for message queue connectors.
// Implementations: kafka, pulsar, etc.
type TopicConnector interface {
	Connector
}

// MetricConnector defines the interface for time-series database connectors.
// Implementations: prometheus, influxdb, etc.
type MetricConnector interface {
	Connector
}

// IndexConnector defines the interface for search engine connectors.
// Implementations: opensearch, elasticsearch, etc.
type IndexConnector interface {
	Connector

	// MapType maps the native types at the source end to VEGA unified types. If not recognized, always return "Other"
	MapType(nativeType string) string

	ListIndexes(ctx context.Context) ([]*IndexMeta, error)
	GetIndexMeta(ctx context.Context, index *IndexMeta) error
	GetIndexMetaByIdentifier(ctx context.Context, sourceIdentifier string) (*IndexMeta, error)

	// ExecuteQuery executes a query on the index
	ExecuteQuery(ctx context.Context, indexName string, resource *Resource, params *ResourceDataQueryParams) (*QueryResult, error)
	ExecuteQueryWithDsl(ctx context.Context, resourceName string, dsl string) (*QueryResult, error)
	ExecuteRawQuery(ctx context.Context, indexName string, query map[string]any) (*RawQueryResponse, error)

	// for index
	CreateIndex(ctx context.Context, indexName string, schemaDefinition []*Property) error
	UpdateIndex(ctx context.Context, indexName string, schemaDefinition []*Property) error
	DeleteIndex(ctx context.Context, indexName string) error
	CheckIndexExist(ctx context.Context, indexName string) (bool, error)
	ValidateAnalyzer(ctx context.Context, analyzer string) (bool, error)

	// for document
	CreateDocuments(ctx context.Context, indexName string, documents []map[string]any) ([]string, error)
	IndexDocuments(ctx context.Context, indexName string, documents map[string]map[string]any) ([]string, error)
	GetDocument(ctx context.Context, indexName string, docID string) (map[string]any, error)
	DeleteDocument(ctx context.Context, indexName string, docID string) error
	UpsertDocuments(ctx context.Context, indexName string, updateRequests []map[string]any) ([]string, error)
	DeleteDocuments(ctx context.Context, indexName string, docIDs string) error
	DeleteDocumentsByQuery(ctx context.Context, indexName string, params *ResourceDataQueryParams, schemaDefinition []*Property) error
}

// APIConnector defines the interface for REST/GraphQL API connectors.
// Implementations: rest, graphql, etc.
type APIConnector interface {
	Connector
}
