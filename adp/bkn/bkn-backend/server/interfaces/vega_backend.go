// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

const (
	ResourceLocalIndexStatusAvailable = "available"

	FieldFeatureType_Keyword  = "keyword"
	FieldFeatureType_Fulltext = "fulltext"
	FieldFeatureType_Vector   = "vector"
)

// Property represents a field definition in vega-backend schema.
type Property struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	DisplayName  string            `json:"display_name"`
	OriginalName string            `json:"original_name"`
	Description  string            `json:"description"`
	Features     []PropertyFeature `json:"features,omitempty"`
}

// PropertyFeature represents a field feature (keyword, fulltext, vector).
type PropertyFeature struct {
	FeatureName string         `json:"name"`
	DisplayName string         `json:"display_name"`
	FeatureType string         `json:"feature_type"`
	Description string         `json:"description"`
	RefProperty string         `json:"ref_property"`
	IsDefault   bool           `json:"is_default"`
	IsNative    bool           `json:"is_native"`
	Config      map[string]any `json:"config"`
}

// CatalogRequest represents create catalog request.
type CatalogRequest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
	Enabled     bool     `json:"enabled"`
	// Internal system catalog: registered as internal_catalog in the permission service and visible only to super administrators.
	Internal bool `json:"internal"`
}

// Catalog represents a Catalog entity.
type Catalog struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Tags          []string `json:"tags"`
	Description   string   `json:"description"`
	Type          string   `json:"type"`
	Enabled       bool     `json:"enabled"`
	ConnectorType string   `json:"connector_type"`
}

// VegaResource represents a Resource entity in vega-backend.
type VegaResource struct {
	ID               string                   `json:"id"`
	CatalogID        string                   `json:"catalog_id"`
	Name             string                   `json:"name"`
	Tags             []string                 `json:"tags"`
	Description      string                   `json:"description"`
	Category         string                   `json:"category"`
	SchemaDefinition []*Property              `json:"schema_definition,omitempty"`
	IndexConfig      *VegaResourceIndexConfig `json:"index_config,omitempty"`
	// LocalIndexStatus is the source of truth for whether the managed local index can serve index capabilities.
	LocalIndexStatus string `json:"local_status"`
}

// VegaResourceIndexConfig mirrors vega-backend's resource-level index configuration.
type VegaResourceIndexConfig struct {
	BuildKeyFields          []string `json:"build_key_fields,omitempty"`
	DefaultFulltextAnalyzer string   `json:"default_fulltext_analyzer,omitempty"`
	DefaultEmbeddingModel   string   `json:"default_embedding_model,omitempty"`
}

// CatalogsListResponse represents catalogs list response.
type CatalogsListResponse struct {
	Data   []*Catalog `json:"data"`
	Total  int        `json:"total"`
	Offset int        `json:"offset"`
	Limit  int        `json:"limit"`
}

// ResourcesListResponse represents resources list response.
type ResourcesListResponse struct {
	Data   []*VegaResource `json:"data"`
	Total  int             `json:"total"`
	Offset int             `json:"offset"`
	Limit  int             `json:"limit"`
}

// DatasetQueryResponse represents dataset query response.
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

// ResourceDataQueryParams represents resource data query parameters.
type ResourceDataQueryParams struct {
	FilterCondition map[string]any            `json:"filter_condition,omitempty"`
	Paging          ResourceDataPagingRequest `json:"paging,omitempty"`
	NeedTotal       bool                      `json:"need_total,omitempty"`
	Sort            []*SortParams             `json:"sort,omitempty"`
	OutputFields    []string                  `json:"output_fields,omitempty"`
}
