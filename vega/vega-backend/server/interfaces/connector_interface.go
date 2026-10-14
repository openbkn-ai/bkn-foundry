// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"errors"
)

var ErrRowCountUnavailable = errors.New("row statistics unavailable")

//go:generate mockgen -source ../interfaces/connector_interface.go -destination ../interfaces/mock/mock_connector_interface.go

// The Connector defines the basic connector interface
type Connector interface {
	// GetType returns the connector type identifier, such as mysql or opensearch.
	GetType() string
	// GetName returns the connector name.
	GetName() string
	// GetMode returns the connector execution mode, such as local or remote.
	GetMode() string
	// GetCategory returns the supported resource category, such as table or index.
	GetCategory() string

	// GetEnabled returns whether the connector is enabled.
	GetEnabled() bool
	// SetEnabled sets whether the connector is enabled.
	SetEnabled(bool)

	// GetSensitiveFields returns configuration field names that require protection, such as passwords.
	GetSensitiveFields() []string
	// GetFieldConfig returns configuration field definitions for form generation and validation.
	GetFieldConfig() map[string]ConnectorFieldConfig

	// New creates and initializes a connector from cfg, returning an error for invalid configuration.
	New(cfg ConnectorConfig) (Connector, error)

	// Connect establishes or reuses a source connection, returning an error on failure.
	Connect(ctx context.Context) error
	// Ping checks whether the source connection is available, returning an error if it is not.
	Ping(ctx context.Context) error
	// Close closes the source connection and releases resources held by the connector.
	Close(ctx context.Context) error
	// TestConnection validates the source connection and configured access scope, returning an error on failure.
	TestConnection(ctx context.Context) error

	// GetMetadata retrieves source metadata, such as version and server information.
	GetMetadata(ctx context.Context) (map[string]any, error)
}

// TableConnector defines the interface for relational database connectors.
// Implementations: mysql, postgresql, dameng, oracle, clickhouse, etc.
type TableConnector interface {
	Connector

	// MapType maps a native source type to a VEGA unified type.
	MapType(nativeType string) string

	// BuildSourceIdentifier builds a SQL table reference with dialect-specific quoting from valid raw name components without connecting to the source.
	BuildSourceIdentifier(table *TableMeta) string
	// BuildOriginalName builds an unquoted qualified name from valid raw name components for CDC and similar uses without connecting to the source.
	BuildOriginalName(table *TableMeta) string
	// DecodeTableMeta decodes source metadata, restoring missing legacy fields from original_name, original_description, and connector configuration without connecting to the source.
	DecodeTableMeta(metadata map[string]any) (*TableMeta, error)

	// ListTables lists tables and views within the configured discovery scope, returning basic metadata with raw name components.
	ListTables(ctx context.Context) ([]*TableMeta, error)
	// GetTableMeta retrieves detailed metadata using the raw name components in table and updates table in place.
	GetTableMeta(ctx context.Context, table *TableMeta) error
	// CountRows counts table or view rows using the generated sourceIdentifier directly, without rebuilding or quoting the table name.
	CountRows(ctx context.Context, sourceIdentifier string) (int64, error)

	// BuildPagedSQL wraps validated read-only SQL with source-specific pagination using offset and limit.
	BuildPagedSQL(sql string, offset, limit int) string
	// BuildCountSQL wraps validated read-only SQL in a query that counts its result rows.
	BuildCountSQL(sql string) string
	// ExecuteQuery executes a single-table query using resource and params, taking the table reference directly from resource.SourceIdentifier.
	ExecuteQuery(ctx context.Context, resource *Resource, params *ResourceDataQueryParams) (*QueryResult, error)
	// ExecuteRawSQL executes read-only SQL that has passed unified query validation, returning column information, data, and a count.
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

	// ListFilesets lists files and directories within the discovery scope, returning fileset metadata.
	ListFilesets(ctx context.Context) ([]*FilesetMeta, error)
	// ExecuteQuery queries a fileset using the resource and query parameters and returns the query result.
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

	// MapType maps a native source type to a VEGA unified type.
	MapType(nativeType string) string

	// ListIndexes lists indexes within the discovery scope, returning basic index metadata.
	ListIndexes(ctx context.Context) ([]*IndexMeta, error)
	// GetIndexMeta retrieves detailed metadata using the identifier in index and updates index in place.
	GetIndexMeta(ctx context.Context, index *IndexMeta) error
	// CountRows returns the exact document count for index, or ErrRowCountUnavailable if unsupported.
	CountRows(ctx context.Context, index *IndexMeta) (int64, error)

	// ExecuteQuery queries indexName using the resource field definitions and query parameters.
	ExecuteQuery(ctx context.Context, indexName string, resource *Resource, params *ResourceDataQueryParams) (*QueryResult, error)
	// ExecuteQueryWithDsl executes source-native DSL against the index identified by resourceName and returns the query result.
	ExecuteQueryWithDsl(ctx context.Context, resourceName string, dsl string) (*QueryResult, error)
	// ExecuteRawQuery executes a native query against indexName, returning column information, data, and a count.
	ExecuteRawQuery(ctx context.Context, indexName string, query map[string]any) (*RawQueryResponse, error)

	// CreateIndex creates indexName with field mappings from properties and mapping metadata from mappingMeta.
	CreateIndex(ctx context.Context, indexName string, properties map[string]any, mappingMeta map[string]string) error
	// UpdateIndex updates field mappings for the specified index.
	UpdateIndex(ctx context.Context, indexName string, properties map[string]any) error
	// DeleteIndex deletes the specified index.
	DeleteIndex(ctx context.Context, indexName string) error
	// CheckIndexExist reports whether the specified index exists, returning an error if the check fails.
	CheckIndexExist(ctx context.Context, indexName string) (bool, error)
	// ValidateAnalyzer reports whether the source supports analyzer, returning an error if validation fails.
	ValidateAnalyzer(ctx context.Context, analyzer string) (bool, error)

	// CreateDocuments writes documents in bulk and returns successfully written IDs; each document may specify its target ID using _id.
	CreateDocuments(ctx context.Context, indexName string, documents []map[string]any) ([]string, error)
	// IndexDocuments writes documents in bulk using map keys as document IDs and returns successfully written IDs.
	IndexDocuments(ctx context.Context, indexName string, documents map[string]map[string]any) ([]string, error)
	// GetDocument retrieves the document identified by docID from the specified index.
	GetDocument(ctx context.Context, indexName string, docID string) (map[string]any, error)
	// GetDocuments retrieves documents identified by docIDs from the specified index.
	GetDocuments(ctx context.Context, indexName string, docIDs []string) ([]map[string]any, error)
	// DeleteDocument deletes the document identified by docID from the specified index.
	DeleteDocument(ctx context.Context, indexName string, docID string) error
	// UpsertDocuments updates documents using the id and document in each request, creating missing documents and returning successfully processed IDs.
	UpsertDocuments(ctx context.Context, indexName string, updateRequests []map[string]any) ([]string, error)
	// DeleteDocuments deletes documents identified by docIDs from the specified index.
	DeleteDocuments(ctx context.Context, indexName string, docIDs []string) error
	// DeleteDocumentsByQuery deletes matching documents from the specified index using the query parameters and field definitions.
	DeleteDocumentsByQuery(ctx context.Context, indexName string, params *ResourceDataQueryParams, schemaDefinition []*Property) error
}

// APIConnector defines the interface for REST/GraphQL API connectors.
// Implementations: rest, graphql, etc.
type APIConnector interface {
	Connector
}
