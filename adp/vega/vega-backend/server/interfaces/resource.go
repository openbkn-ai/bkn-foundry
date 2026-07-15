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

	Category string `json:"category"` // 资源类别：table/file/fileset/...

	Status             string `json:"status"`               // 状态：active/stale/disabled
	StatusMessage      string `json:"status_message"`       // 状态消息
	LastDiscoverStatus string `json:"last_discover_status"` // 最近一次扫描观察状态

	// 新增字段：支持自动发现
	Database         string         `json:"database,omitempty"`          // 所属数据库（实例级 Catalog 时填充）
	SourceIdentifier string         `json:"source_identifier"`           // 源端标识（原始表名/路径）
	SourceMetadata   map[string]any `json:"source_metadata,omitempty"`   // 源端配置（JSON）
	SchemaDefinition []*Property    `json:"schema_definition,omitempty"` // Schema定义

	// 索引相关
	IndexConfig    *ResourceIndexConfig `json:"index_config,omitempty"` // 本地索引配置
	LocalIndexName string               `json:"index_name,omitempty"`   // 索引名称，由构建任务填充

	// 规模信息：列表接口从原始 JSON 轻量计数得到，不反序列化完整结构；nil 表示源端无该信息（序列化时省略）
	ColumnCount *int   `json:"column_count,omitempty"` // schema_definition 字段数
	RowCount    *int64 `json:"row_count,omitempty"`    // 源端行数（最近一次 discover 的估算快照，仅部分资源类别有）

	// Extensions 根级可检索业务 KV（t_entity_extension）；列表默认省略
	Extensions map[string]string `json:"extensions,omitempty"`

	// 逻辑视图特有的字段
	LogicType       string                 `json:"logic_type,omitempty"`       // 逻辑类型: derived(衍生), composite(复合)
	LogicDefinition []*LogicDefinitionNode `json:"logic_definition,omitempty"` // 逻辑定义

	Creator    AccountInfo `json:"creator"`
	CreateTime int64       `json:"create_time"`
	Updater    AccountInfo `json:"updater"`
	UpdateTime int64       `json:"update_time"`

	Operations []string `json:"operations"`
}

const (
	// Property 字段名称、显示名、备注、特征名、特征备注的最大长度
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
	// Extensions 字段级展示用（schema_definition JSON 内），不参与列表筛选
	Extensions map[string]string `json:"extensions,omitempty"`
}

type PropertyFeature struct {
	FeatureName string         `json:"name"`
	DisplayName string         `json:"display_name"`
	FeatureType string         `json:"feature_type"` // 特性类型：keyword, fulltext, vector
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
	Name                 string
	CatalogID            string
	Category             string
	Status               string
	Database             string
	ExtensionKeys        []string
	ExtensionValues      []string
	IncludeExtensions    bool
	IncludeExtensionKeys string
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

	Database         string         `json:"database,omitempty"`          // 所属数据库（实例级 Catalog 时填充）
	SourceIdentifier string         `json:"source_identifier"`           // 源端标识（原始表名/路径）
	SourceMetadata   map[string]any `json:"source_metadata,omitempty"`   // 源端配置（JSON）
	SchemaDefinition []*Property    `json:"schema_definition,omitempty"` // Schema定义

	IndexConfig *ResourceIndexConfig `json:"index_config,omitempty"` // 本地索引配置

	LogicDefinition []*LogicDefinitionNode `json:"logic_definition,omitempty"` // 逻辑定义

	Extensions *map[string]string `json:"extensions,omitempty"`
}
