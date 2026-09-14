// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

const (
	ResourceCategoryTable     string = "table"
	ResourceCategoryFile      string = "file"
	ResourceCategoryFileset   string = "fileset"
	ResourceCategoryAPI       string = "api"
	ResourceCategoryMetric    string = "metric"
	ResourceCategoryTopic     string = "topic"
	ResourceCategoryIndex     string = "index"
	ResourceCategoryLogicView string = "logicview"
	ResourceCategoryDataset   string = "dataset"

	ResourceSortName       string = "name"
	ResourceSortCreateTime string = "create_time"
	ResourceSortUpdateTime string = "update_time"

	ResourceStatusActive     string = "active"
	ResourceStatusDeprecated string = "deprecated"
	ResourceStatusStale      string = "stale"

	ResourceLocalIndexStatusUnavailable string = "unavailable"
	ResourceLocalIndexStatusAvailable   string = "available"
	ResourceLocalIndexStatusStale       string = "stale"

	DiscoverStatusNew       string = "new"
	DiscoverStatusUnchanged string = "unchanged"
	DiscoverStatusUpdated   string = "updated"
	DiscoverStatusRestored  string = "restored"
	DiscoverStatusMissing   string = "missing"
	DiscoverStatusError     string = "error"

	// The maximum length of the Property field name, display name, remarks, feature name, and feature remarks
	MaxLength_PropertyName               = 255
	MaxLength_PropertyDisplayName        = 255
	MaxLength_PropertyFeatureName        = 255
	MaxLength_PropertyDescription        = 1000
	MaxLength_PropertyFeatureDescription = 1000

	// LocalIndexKeywordSubfieldName is the default keyword multi-field name in managed indexes.
	LocalIndexKeywordSubfieldName = "keyword"
	// LocalIndexFulltextSubfieldName is the default full-text multi-field name in managed indexes.
	LocalIndexFulltextSubfieldName = "fulltext"
	// LocalIndexVectorFieldSuffix is the fixed suffix for generated vector fields in managed indexes.
	LocalIndexVectorFieldSuffix = "_vector"
	// DefaultTextKeywordIgnoreAbove 是 string/text 字段关键字精确匹配的默认最大长度。
	DefaultTextKeywordIgnoreAbove = 256
	// MaxKeywordIgnoreAbove 是本地索引接受的关键字精确匹配最大长度。
	MaxKeywordIgnoreAbove = 8191
)

// RESOURCE_SORT is a whitelist of supported API sort fields. The data access
// layer maps these fields to database columns.
var RESOURCE_SORT = map[string]string{
	ResourceSortName:       "",
	ResourceSortCreateTime: "",
	ResourceSortUpdateTime: "",
}

// Resource represents a Data Resource entity.
type Resource struct {
	ID          string   `json:"id"`
	CatalogID   string   `json:"catalog_id"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`

	Category string `json:"category"` // Resource category: table/file/fileset/...

	Enabled            bool   `json:"enabled"`
	Status             string `json:"status"`               // Status: active/stale/deprecated
	StatusMessage      string `json:"status_message"`       // Status message
	LastDiscoverStatus string `json:"last_discover_status"` // The latest scan observation status

	// New field: Supports automatic discovery
	Schema           string         `json:"schema,omitempty"`            // The schema to which it belongs is written by the discovery process
	SourceIdentifier string         `json:"source_identifier"`           // Source identifier (original table name/path)
	SourceMetadata   map[string]any `json:"source_metadata,omitempty"`   // Source configuration (JSON
	SchemaDefinition []*Property    `json:"schema_definition,omitempty"` // Schema Definition

	// Index related
	IndexConfig      *ResourceIndexConfig `json:"index_config,omitempty"` // Local index configuration
	LocalIndexStatus string               `json:"local_status"`           // Availability of the managed local index
	LocalIndexName   string               `json:"index_name,omitempty"`   // Index name, filled by the build task
	SyncMark         string               `json:"-"`                      // Internal committed batch checkpoint

	// 规模信息：nil 表示当前资源无法提供该统计值，序列化时省略。
	ColumnCount *int   `json:"column_count,omitempty"` // Number of schema_definition fields
	RowCount    *int64 `json:"row_count,omitempty"`    // dataset 为当前本地索引文档数，其他类型来自源端元数据

	// Fields specific to the logical view
	LogicType       string                 `json:"logic_type,omitempty"`       // Logical types: derived(derived), composite(composite
	LogicDefinition []*LogicDefinitionNode `json:"logic_definition,omitempty"` // Logical definition

	Creator    AccountInfo `json:"creator"`
	CreateTime int64       `json:"create_time"`
	Updater    AccountInfo `json:"updater"`
	UpdateTime int64       `json:"update_time"`

	Operations []string `json:"operations"`
}

// ResourceSummary contains the fields returned by resource list queries.
// Extended JSON fields are intentionally excluded.
type ResourceSummary struct {
	ID          string   `json:"id"`
	CatalogID   string   `json:"catalog_id"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`

	Category string `json:"category"`

	Enabled            bool   `json:"enabled"`
	Status             string `json:"status"`
	StatusMessage      string `json:"status_message"`
	LastDiscoverStatus string `json:"last_discover_status"`

	Schema           string `json:"schema,omitempty"`
	SourceIdentifier string `json:"source_identifier"`

	LocalIndexStatus string `json:"local_status"`
	LocalIndexName   string `json:"index_name,omitempty"`
	SyncMark         string `json:"-"`

	LogicType string `json:"logic_type,omitempty"`

	Creator    AccountInfo `json:"creator"`
	CreateTime int64       `json:"create_time"`
	Updater    AccountInfo `json:"updater"`
	UpdateTime int64       `json:"update_time"`

	Operations []string `json:"operations"`
}

type Property struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	Description string `json:"description"`

	OriginalName        string `json:"original_name"`
	OriginalType        string `json:"original_type"`
	OriginalDescription string `json:"original_description"`

	Features   []PropertyFeature `json:"features"`
	Attributes map[string]any    `json:"attributes"`
}

type PropertyFeature struct {
	FeatureName string         `json:"name"`
	DisplayName string         `json:"display_name"`
	FeatureType string         `json:"feature_type"` // Feature types: keyword, fulltext, vector
	Description string         `json:"description"`
	RefProperty string         `json:"ref_property"`
	IsDefault   bool           `json:"is_default"`
	IsNative    bool           `json:"is_native"`
	Config      map[string]any `json:"config"`
}

// ResourceIndexConfig carries resource-level defaults and cross-field build policy.
type ResourceIndexConfig struct {
	PrimaryKeyFields  []string `json:"primary_key_fields,omitempty"`
	IncrementalFields []string `json:"incremental_fields,omitempty"`

	DefaultKeywordIgnoreAbove *int   `json:"default_keyword_ignore_above,omitempty"`
	DefaultFulltextAnalyzer   string `json:"default_fulltext_analyzer,omitempty"`
	DefaultEmbeddingModel     string `json:"default_embedding_model,omitempty"`
}

// ResourcesQueryParams holds resource list query parameters.
type ResourcesQueryParams struct {
	PaginationQueryParams
	Name      string
	CatalogID string
	Category  string
	Status    string
	Schema    string
}

// ResourceCreateRequest represents create resource request.
type ResourceRequest struct {
	ID          string   `json:"id,omitempty"`
	CatalogID   string   `json:"catalog_id"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`

	Category string `json:"category"`

	Enabled bool   `json:"enabled"`
	Status  string `json:"status"`

	Schema           string         `json:"schema,omitempty"`            // The schema to which it belongs is written by the discovery process
	SourceIdentifier string         `json:"source_identifier"`           // Source identifier (original table name/path)
	SourceMetadata   map[string]any `json:"source_metadata,omitempty"`   // Source configuration (JSON
	SchemaDefinition []*Property    `json:"schema_definition,omitempty"` // Schema Definition

	IndexConfig *ResourceIndexConfig `json:"index_config,omitempty"` // Local index configuration

	LogicDefinition []*LogicDefinitionNode `json:"logic_definition,omitempty"` // Logical definition

	ExpectedUpdateTime int64 `json:"expected_update_time,omitempty"`
}
