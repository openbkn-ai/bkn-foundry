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
)

const (
	ResourceStatusActive     string = "active"
	ResourceStatusDisabled   string = "disabled"
	ResourceStatusDeprecated string = "deprecated"
	ResourceStatusStale      string = "stale"
)

const (
	ResourceLocalIndexStatusUnavailable string = "unavailable"
	ResourceLocalIndexStatusAvailable   string = "available"
	ResourceLocalIndexStatusStale       string = "stale"
)

const (
	DiscoverStatusNew       string = "new"
	DiscoverStatusUnchanged string = "unchanged"
	DiscoverStatusUpdated   string = "updated"
	DiscoverStatusRestored  string = "restored"
	DiscoverStatusMissing   string = "missing"
	DiscoverStatusError     string = "error"
)

var (
	RESOURCE_SORT = map[string]string{
		"name":        "f_name",
		"create_time": "f_create_time",
		"update_time": "f_update_time",
	}
)

// Resource represents a Data Resource entity.
type Resource struct {
	ID          string   `json:"id"`
	CatalogID   string   `json:"catalog_id"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`

	Category string `json:"category"` // Resource category: table/file/fileset/...

	Status             string `json:"status"`               // Status: active/stale/disabled
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

	// Scale information: The list interface is obtained from the original JSON lightweight count without deserializing the complete structure. nil indicates that the source does not have this information (omitted during serialization)
	ColumnCount *int   `json:"column_count,omitempty"` // Number of schema_definition fields
	RowCount    *int64 `json:"row_count,omitempty"`    // Source row count (the most recent estimated snapshot from discover, available only for some resource categories)

	// Fields specific to the logical view
	LogicType       string                 `json:"logic_type,omitempty"`       // Logical types: derived(derived), composite(composite
	LogicDefinition []*LogicDefinitionNode `json:"logic_definition,omitempty"` // Logical definition

	Creator    AccountInfo `json:"creator"`
	CreateTime int64       `json:"create_time"`
	Updater    AccountInfo `json:"updater"`
	UpdateTime int64       `json:"update_time"`

	Operations []string `json:"operations"`
}

const (
	// The maximum length of the Property field name, display name, remarks, feature name, and feature remarks
	MaxLength_PropertyName               = 255
	MaxLength_PropertyDisplayName        = 255
	MaxLength_PropertyFeatureName        = 255
	MaxLength_PropertyDescription        = 1000
	MaxLength_PropertyFeatureDescription = 1000
)

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
	BuildKeyFields          []string `json:"build_key_fields,omitempty"`
	DefaultFulltextAnalyzer string   `json:"default_fulltext_analyzer,omitempty"`
	DefaultEmbeddingModel   string   `json:"default_embedding_model,omitempty"`
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

	Status string `json:"status"`

	Schema           string         `json:"schema,omitempty"`            // The schema to which it belongs is written by the discovery process
	SourceIdentifier string         `json:"source_identifier"`           // Source identifier (original table name/path)
	SourceMetadata   map[string]any `json:"source_metadata,omitempty"`   // Source configuration (JSON
	SchemaDefinition []*Property    `json:"schema_definition,omitempty"` // Schema Definition

	IndexConfig *ResourceIndexConfig `json:"index_config,omitempty"` // Local index configuration

	LogicDefinition []*LogicDefinitionNode `json:"logic_definition,omitempty"` // Logical definition

	ExpectedUpdateTime int64 `json:"expected_update_time,omitempty"`
}

// LocalIndexVectorFieldSuffix is build tasks for vectorization fields generated by the physical fields suffix.
//
// These fields only exist in the local index and do not write back to the resource schema: they are part of the index implementation, not
// Columns of the data source. To issue knn, the query side needs to know this name. Therefore, the naming rules are concentrated here, by the build side
// The generation, query-side identification, and BKN are issued along with the object class Schema, and the three references are from the same source.
const LocalIndexVectorFieldSuffix = "_vector"

// LocalIndexVectorFieldName returns the vector field name of a field.
func LocalIndexVectorFieldName(field string) string {
	return field + LocalIndexVectorFieldSuffix
}

// LocalIndexGeneratedFields return resource in the local index generated by build tasks, but not on the resource schema fields.
//
// Return nil when the resource has no local index: Those fields do not exist yet, and accepting filtering conditions for them will only allow
// It is better to reject the query at the condition construction stage than to blow it up further downstream.
func LocalIndexGeneratedFields(res *Resource) map[string]*Property {
	if !HasAvailableLocalIndex(res) {
		return nil
	}

	generated := map[string]*Property{}
	for _, prop := range res.SchemaDefinition {
		if prop == nil {
			continue
		}
		for _, feature := range prop.Features {
			if feature.FeatureType != PropertyFeatureType_Vector {
				continue
			}
			field := prop.Name
			if feature.RefProperty != "" {
				field = feature.RefProperty
			}
			name := LocalIndexVectorFieldName(field)
			generated[name] = &Property{Name: name, Type: DataType_Vector}
		}
	}
	if len(generated) == 0 {
		return nil
	}
	return generated
}

// HasAvailableLocalIndex reports whether a Resource may use its managed local
// index for queries. A name alone is insufficient because stale indexes are
// retained for diagnostics and later cleanup.
func HasAvailableLocalIndex(res *Resource) bool {
	return res != nil &&
		res.LocalIndexStatus == ResourceLocalIndexStatusAvailable &&
		res.LocalIndexName != ""
}
