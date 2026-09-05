// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
)

// Property represents a field definition in vega-backend schema
type Property struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	DisplayName  string            `json:"display_name"`
	OriginalName string            `json:"original_name"`
	Description  string            `json:"description"`
	Features     []PropertyFeature `json:"features,omitempty"`
}

// PropertyFeature represents a field feature (keyword, fulltext, vector)
type PropertyFeature struct {
	FeatureName string         `json:"name"`
	DisplayName string         `json:"display_name"`
	FeatureType string         `json:"feature_type"` // keyword, fulltext, vector
	Description string         `json:"description"`
	RefProperty string         `json:"ref_property"`
	IsDefault   bool           `json:"is_default"`
	IsNative    bool           `json:"is_native"`
	Config      map[string]any `json:"config"`
}

// CatalogRequest represents create catalog request
type CatalogRequest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
	Enabled     bool     `json:"enabled"`
	// Internal system catalog: registered as internal_catalog in the permission service and visible only to super administrators.
	Internal bool `json:"internal"`
	// ConnectorType string         `json:"connector_type"`
	// ConnectorCfg  map[string]any `json:"connector_config"`
}

// Catalog represents a Catalog entity
type Catalog struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Tags          []string `json:"tags"`
	Description   string   `json:"description"`
	Type          string   `json:"type"`
	Enabled       bool     `json:"enabled"`
	ConnectorType string   `json:"connector_type"`
}

// VegaResource represents a Resource entity in vega-backend
type VegaResource struct {
	ID          string   `json:"id"`
	CatalogID   string   `json:"catalog_id"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	// Status           string      `json:"status"`
	SchemaDefinition []*Property              `json:"schema_definition,omitempty"`
	IndexConfig      *VegaResourceIndexConfig `json:"index_config,omitempty"`
	// LocalIndexName is populated by a build task. A non-empty value indicates that the resource has a local index;
	// field-level features are persisted to OpenSearch only in that case.
	LocalIndexName string `json:"index_name,omitempty"`
}

// VegaResourceIndexConfig mirrors vega-backend's resource-level index configuration.
type VegaResourceIndexConfig struct {
	BuildKeyFields          []string `json:"build_key_fields,omitempty"`
	DefaultFulltextAnalyzer string   `json:"default_fulltext_analyzer,omitempty"`
	DefaultEmbeddingModel   string   `json:"default_embedding_model,omitempty"`
}

// CatalogsListResponse represents catalogs list response
type CatalogsListResponse struct {
	Data   []*Catalog `json:"data"`
	Total  int        `json:"total"`
	Offset int        `json:"offset"`
	Limit  int        `json:"limit"`
}

// ResourcesListResponse represents resources list response
type ResourcesListResponse struct {
	Data   []*VegaResource `json:"data"`
	Total  int             `json:"total"`
	Offset int             `json:"offset"`
	Limit  int             `json:"limit"`
}

// DatasetQueryResponse represents dataset query response
type DatasetQueryResponse struct {
	Entries    []map[string]any          `json:"entries"`
	TotalCount int64                     `json:"total_count"`
	Paging     *ResourceDataPagingResult `json:"paging,omitempty"`
}

type ResourceDataPagingRequest struct {
	Mode         string `json:"mode,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	KeepAliveSec int    `json:"keep_alive_sec,omitempty"`
	Cursor       string `json:"cursor,omitempty"`
}

type ResourceDataPagingResult struct {
	NextCursor   *string `json:"next_cursor"`
	ExpiresAtSec *int64  `json:"expires_at_sec"`
}

// ResourceDataQueryParams represents query parameters for dataset data retrieval
type ResourceDataQueryParams struct {
	FilterCondition map[string]any            `json:"filter_condition,omitempty"`
	Paging          ResourceDataPagingRequest `json:"paging,omitempty"`
	NeedTotal       bool                      `json:"need_total,omitempty"`
	Sort            []*SortParams             `json:"sort,omitempty"`
	OutputFields    []string                  `json:"output_fields,omitempty"`
}

// Vega raw query accepts these SQL dialects as input. Generating the catalog's
// own dialect keeps vega from transpiling, which halves its python subprocess
// count -- see the design note in logics/cypher.
const (
	VEGA_QUERY_FORMAT_SQL = "sql"

	VEGA_DIALECT_MYSQL    = "mysql"
	VEGA_DIALECT_POSTGRES = "postgres"
	VEGA_DIALECT_TSQL     = "tsql"

	// VEGA_PAGING_MODE_SINGLE asks vega-backend for one page rather than a
	// cursor session, which is what a compiled statement with its own LIMIT
	// needs.
	VEGA_PAGING_MODE_SINGLE = "single"
)

// RawQueryColumn describes one projected column of a raw query result.
type RawQueryColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// RawQueryRequest is a read-only SQL query submitted to vega-backend. Table
// references are placeholders of the form {{.<resource_id>}} that vega expands
// into physical names after checking the caller may read each one.
//
// InputDialect must be the catalog's own dialect. Vega only transpiles when it
// differs from the target, and every transpile costs a python subprocess pair,
// so matching them keeps the statement on the cheap path -- and makes this
// service, not sqlglot, responsible for literal escaping.
type RawQueryRequest struct {
	Query           string                    `json:"query"`
	QueryFormat     string                    `json:"query_format"`
	InputDialect    string                    `json:"input_dialect"`
	Paging          ResourceDataPagingRequest `json:"paging,omitempty"`
	QueryTimeoutSec int                       `json:"query_timeout_sec,omitempty"`
	NeedTotal       bool                      `json:"need_total,omitempty"`
}

// RawQueryResponse carries the projected columns alongside the rows so a
// caller can map physical column names back to what the query asked for.
type RawQueryResponse struct {
	Columns    []RawQueryColumn          `json:"columns"`
	Entries    []map[string]any          `json:"entries"`
	TotalCount int64                     `json:"total_count"`
	Paging     *ResourceDataPagingResult `json:"paging,omitempty"`
}

// VegaBackendAccess defines the interface for accessing vega-backend service
//
//go:generate mockgen -source ../interfaces/vega_backend_access.go -destination ../interfaces/mock/mock_vega_backend_access.go
type VegaBackendAccess interface {
	// GetCatalogByID gets catalog by ID
	GetCatalogByID(ctx context.Context, id string) (*Catalog, error)

	// CreateCatalog creates a new catalog
	CreateCatalog(ctx context.Context, req *CatalogRequest) (*Catalog, error)

	// GetResourceByID gets resource by ID
	GetResourceByID(ctx context.Context, id string) (*VegaResource, error)

	// CreateResource creates a new resource
	CreateResource(ctx context.Context, req *VegaResource) error

	// DeleteResource deletes a resource by ID
	DeleteResource(ctx context.Context, id string) error

	// QueryResourceData queries data from a vega Resource (same HTTP contract as dataset resource data API).
	QueryResourceData(ctx context.Context, resourceID string, params *ResourceDataQueryParams) (*DatasetQueryResponse, error)

	// RawQuery runs a read-only SQL statement through vega-backend.
	//
	// Unlike the other methods here it never falls back to the admin account:
	// vega authorizes each referenced resource against the caller, so losing
	// the end user's identity would silently turn per-resource authorization
	// off. Without an identity in the context this fails instead.
	RawQuery(ctx context.Context, req *RawQueryRequest) (*RawQueryResponse, error)

	// WriteDatasetDocuments writes documents to dataset
	WriteDatasetDocuments(ctx context.Context, datasetID string, documents []map[string]any) error

	// DeleteDatasetDocumentByID deletes a document by ID from dataset
	DeleteDatasetDocumentByID(ctx context.Context, datasetID string, docID string) error

	// DeleteDatasetDocumentsByQuery deletes documents by query condition from dataset
	DeleteDatasetDocumentsByQuery(ctx context.Context, datasetID string, filterCondition map[string]any) error
}
