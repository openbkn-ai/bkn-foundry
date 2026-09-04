// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"


// Vega raw query accepts these SQL dialects as input. Generating the catalog's
// own dialect keeps vega from transpiling, which halves its python subprocess
// count -- see the design note in logics/cypher.
const (
	VEGA_QUERY_FORMAT_SQL = "sql"

	VEGA_DIALECT_MYSQL    = "mysql"
	VEGA_DIALECT_POSTGRES = "postgres"
	VEGA_DIALECT_TSQL     = "tsql"
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

	// WriteDatasetDocument writes one document to a dataset.
	WriteDatasetDocument(ctx context.Context, datasetID, docID string, document map[string]any) error

	// DeleteDatasetDocumentByID deletes a document by ID from dataset
	DeleteDatasetDocumentByID(ctx context.Context, datasetID string, docID string) error

	// DeleteDatasetDocumentsByQuery deletes documents by query condition from dataset
	DeleteDatasetDocumentsByQuery(ctx context.Context, datasetID string, filterCondition map[string]any) error
}
