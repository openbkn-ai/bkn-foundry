// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"fmt"

	"github.com/openbkn-ai/bkn-comm-go/audit"

	"bkn-backend/interfaces/data_type"
)

type contextKey string // 自定义专属的key类型

const (
	CONTENT_TYPE_NAME = "Content-Type"
	CONTENT_TYPE_JSON = "application/json"

	HTTP_HEADER_METHOD_OVERRIDE = "x-http-method-override"
	HTTP_HEADER_ACCOUNT_ID      = "x-account-id"
	HTTP_HEADER_ACCOUNT_TYPE    = "x-account-type"
	HTTP_HEADER_BUSINESS_DOMAIN = "x-business-domain"

	ACCOUNT_INFO_KEY contextKey = "x-account-info" // 避免直接使用string

	OBJECT_NAME_MAX_LENGTH = 40
	DEFAULT_NAME_PATTERN   = ""
	DEFAULT_OFFEST         = "0"
	DEFAULT_LIMIT          = "10" // LIMIT=-1, 不分页
	DEFAULT_SORT           = "update_time"
	DEFAULT_DIRECTION      = "desc"
	DESC_DIRECTION         = "desc"
	ASC_DIRECTION          = "asc"
	MIN_OFFSET             = 0
	MIN_LIMIT              = 1
	MAX_LIMIT              = 1000
	NO_LIMIT               = "-1"
	DEFAULT_SIMPLE_INFO    = "false"
	COMMENT_MAX_LENGTH     = 1000
	NAME_INVALID_CHARACTER = "/:?\\\"<>|：？‘’“”！《》,#[]{}%&*$^!=.'"

	TAGS_MAX_NUMBER = 5

	DEFAULT_FORCE    = "false"
	DEFAULT_GROUP_ID = ""

	DEFAULT_INCLUDE_DETAIL = "false"
	DEFAULT_FORCE_DELETE   = "false"

	QueryParam_ImportMode  = "import_mode"
	QueryParam_Mode        = "mode"
	QueryParam_StrictMode  = "strict_mode"
	QueryParam_DetailLevel = "detail_level"

	// detail_level 取值：summary 只返骨架 + 属性名（砍字段映射/查询算子/逻辑属性
	// 数据源与参数/关系映射规则，去重 concept_groups 嵌套）；full（默认）返全量。
	// 完整字段映射按需走 object-types/:ids、relation-types/:ids 端点获取。
	DetailLevel_Summary = "summary"
	DetailLevel_Full    = "full"

	// 对象的导入模式
	ImportMode_Normal    = "normal"
	ImportMode_Ignore    = "ignore"
	ImportMode_Overwrite = "overwrite"

	Mode_Export = "export"

	// 数据来源类型
	DATA_SOURCE_TYPE_DATA_VIEW = "data_view"
	DATA_SOURCE_TYPE_RESOURCE  = "resource"

	// 对象id的校验
	RegexPattern_Builtin_ID    = "^[a-z0-9_][a-z0-9_-]{0,39}$"
	RegexPattern_NonBuiltin_ID = "^[a-z0-9][a-z0-9_-]{0,39}$"

	// 属性名称约束
	RegexPattern_Property_Name = "^[a-zA-Z0-9][a-zA-Z0-9_-]{0,39}$"

	// 未分组中英文
	UNGROUPED_ZH_CN = "未分组"
	UNGROUPED_EN_US = "Ungrouped"

	// 参数来源
	VALUE_FROM_INPUT    = "input"
	VALUE_FROM_PROPERTY = "property"
	VALUE_FROM_CONST    = "const"
	VALUE_FROM_PARAM    = "param" // RiskFunction 参数：值来自 RiskType 参数，value 为 ParamDef.name

	// 属性类型
	PROPERTY_TYPE_METRIC = "metric"

	// 概念检索未指定 limit 时的最大页面大小。
	ConceptQueryLimit = 10000

	// 按_score排序
	OPENSEARCH_SCORE_FIELD = "_score"

	// 对象索引构建时,存储的对象id
	OBJECT_ID = "__id"

	// 是否包含统计信息
	DEFAULT_INCLUDE_STATISTICS = "false"

	// 获取总数时每批对象类id传递的数量(每批处理的ID数量)
	GET_TOTAL_CONCEPTID_BATCH_SIZE = 900

	// 概念检索默认的条数
	DEFAULT_CONCEPT_SEARCH_LIMIT = 10

	// 概念id字段名
	CONCEPT_ID_FIELD = "id"
)

const (
	MAIN_BRANCH = "main"

	//模块类型
	MODULE_TYPE_KN                     = "knowledge_network"
	MODULE_TYPE_OBJECT_TYPE            = "object_type"
	MODULE_TYPE_RELATION_TYPE          = "relation_type"
	MODULE_TYPE_ACTION_TYPE            = "action_type"
	MODULE_TYPE_CONCEPT_GROUP          = "concept_group"
	MODULE_TYPE_CONCEPT_GROUP_RELATION = "concept_group_relation"
	MODULE_TYPE_ACTION_SCHEDULE        = "action_schedule"
	MODULE_TYPE_RISK_TYPE              = "risk_type"
	MODULE_TYPE_METRIC                 = "metric"
)

const (
	// 概念索引名称
	KN_CONCEPT_INDEX_NAME = "adp-kn_concept"

	// moduleType + id + branch
	KN_CONCEPT_DOCID_TEMPLATE = "%s-%s-%s-%s"
)

// 分页查询参数
type PaginationQueryParameters struct {
	Offset    int
	Limit     int
	Sort      string
	Direction string
}

func GenerateKNAuditObject(id string, name string) audit.AuditObject {
	return audit.AuditObject{
		Type: MODULE_TYPE_KN,
		ID:   id,
		Name: name,
	}
}

func GenerateObjectTypeAuditObject(id string, name string) audit.AuditObject {
	return audit.AuditObject{
		Type: MODULE_TYPE_OBJECT_TYPE,
		ID:   id,
		Name: name,
	}
}

func GenerateRelationTypeAuditObject(id string, name string) audit.AuditObject {
	return audit.AuditObject{
		Type: MODULE_TYPE_RELATION_TYPE,
		ID:   id,
		Name: name,
	}
}

func GenerateActionTypeAuditObject(id string, name string) audit.AuditObject {
	return audit.AuditObject{
		Type: MODULE_TYPE_ACTION_TYPE,
		ID:   id,
		Name: name,
	}
}

func GenerateConceptGroupAuditObject(id string, name string) audit.AuditObject {
	return audit.AuditObject{
		Type: MODULE_TYPE_CONCEPT_GROUP,
		ID:   id,
		Name: name,
	}
}

func GenerateConceptGroupRelationAuditObject(id string, name string) audit.AuditObject {
	return audit.AuditObject{
		Type: MODULE_TYPE_CONCEPT_GROUP_RELATION,
		ID:   id,
		Name: name,
	}
}

func GenerateRiskTypeAuditObject(id string, name string) audit.AuditObject {
	return audit.AuditObject{
		Type: MODULE_TYPE_RISK_TYPE,
		ID:   id,
		Name: name,
	}
}

func GenerateMetricAuditObject(id string, name string) audit.AuditObject {
	return audit.AuditObject{
		Type: MODULE_TYPE_METRIC,
		ID:   id,
		Name: name,
	}
}

type ResourceInfo struct {
	Type       string `json:"type" mapstructure:"type"`
	ID         string `json:"id" mapstructure:"id"`
	Name       string `json:"name" mapstructure:"name"`
	BoxID      string `json:"box_id,omitempty" mapstructure:"box_id"`
	ToolID     string `json:"tool_id,omitempty" mapstructure:"tool_id"`
	ResultPath string `json:"result_path,omitempty" mapstructure:"result_path"`
}

// 概念索引的id生成规则， kn_id + module_type + id + branch
func GenerateConceptDocuemtnID(knID string, moduleType string, id string, branch string) string {
	return fmt.Sprintf(KN_CONCEPT_DOCID_TEMPLATE, knID, moduleType, id, branch)
}

type CommonInfo struct {
	Tags    []string `json:"tags" mapstructure:"tags"`
	Comment string   `json:"comment" mapstructure:"comment"`
	Icon    string   `json:"icon" mapstructure:"icon"`
	Color   string   `json:"color" mapstructure:"color"`

	BKNRawContent string `json:"-" mapstructure:"-"`
}

type AccountInfo struct {
	ID   string `json:"id" mapstructure:"id"`
	Type string `json:"type" mapstructure:"type"`
	Name string `json:"name" mapstructure:"name"`
}

type ID struct {
	ID string `json:"id" mapstructure:"id"`
}

const (
	BKN_CATALOG_ID   = "adp_bkn_catalog"
	BKN_CATALOG_NAME = "adp_bkn_catalog"
	BKN_DATASET_ID   = "adp_bkn_concept_dataset"
	BKN_DATASET_NAME = "adp_bkn_concept_dataset"

	//特征的配置项
	FieldFeatureType_Keyword  = "keyword"
	FieldFeatureType_Fulltext = "fulltext"
	FieldFeatureType_Vector   = "vector"

	FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE            = "ignore_above"
	FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE      = 1024
	FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE_8192 = 8192
)

var (
	BKN_CONCEPT_DATASET = &VegaResource{
		ID:          BKN_DATASET_ID,
		CatalogID:   BKN_CATALOG_ID,
		Name:        BKN_DATASET_NAME,
		Category:    "dataset",
		Description: "BKN的概念存储数据集",
		Tags:        []string{"BKN", "概念索引", "concept"},
		// Vega requires this non-null resource-level configuration. Keep an
		// explicit empty object so the internal create request includes it.
		IndexConfig: &VegaResourceIndexConfig{},
		// Status:           "active",
		// SchemaDefinition: GetBKNConceptSchemaDefinition(vectorDim),
	}
)

// GetBKNConceptSchemaDefinition returns the schema definition for BKN concept dataset
// vectorDim: the dimension of vector field, typically from small model embedding dimension
func GetBKNConceptSchemaDefinition(vectorDim int, defaultSmallModelEnabled bool) []*Property {
	if vectorDim <= 0 {
		vectorDim = 768 // default dimension
	}

	datasetProp := []*Property{
		// Common fields
		{
			Name:         "module_type",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "module_type",
			OriginalName: "module_type",
			Description:  "bkn中的概念模块类型：knowledge_network、object_type、relation_type、action_type、concept_group、risk_type",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_module_type",
					DisplayName: "keyword_module_type",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN概念模块类型的关键词特征",
					RefProperty: "module_type",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "id",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "id",
			OriginalName: "id",
			Description:  "BKN中概念的唯一标识符",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_id",
					DisplayName: "keyword_id",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN概念ID的关键词特征",
					RefProperty: "id",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "name",
			Type:         data_type.DATATYPE_TEXT,
			DisplayName:  "name",
			OriginalName: "name",
			Description:  "BKN中概念的名称",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_name",
					DisplayName: "keyword_name",
					FeatureType: "keyword",
					Description: "BKN中概念名称的关键词特征",
					RefProperty: "name",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
				{
					FeatureName: "fulltext_name",
					DisplayName: "fulltext_name",
					FeatureType: FieldFeatureType_Fulltext,
					Description: "BKN中概念名称的全文检索特征",
					RefProperty: "name",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"analyzer": "standard"},
				},
			},
		},
		{
			Name:         "tags",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "tags",
			OriginalName: "tags",
			Description:  "BKN中概念的标签列表",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_tags",
					DisplayName: "keyword_tags",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN概念标签的关键词特征",
					RefProperty: "tags",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "comment",
			Type:         data_type.DATATYPE_TEXT,
			DisplayName:  "comment",
			OriginalName: "comment",
			Description:  "BKN中概念的注释说明",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_comment",
					DisplayName: "keyword_comment",
					FeatureType: "keyword",
					Description: "BKN中概念注释的关键词特征",
					RefProperty: "comment",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
				{
					FeatureName: "fulltext_comment",
					DisplayName: "fulltext_comment",
					FeatureType: FieldFeatureType_Fulltext,
					Description: "BKN概念注释的全文检索特征",
					RefProperty: "comment",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"analyzer": "standard"},
				},
			},
		},
		{
			Name:         "detail",
			Type:         data_type.DATATYPE_TEXT,
			DisplayName:  "detail",
			OriginalName: "detail",
			Description:  "BKN中概念的详细信息描述",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_detail",
					DisplayName: "keyword_detail",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN概念详情的关键词特征",
					RefProperty: "detail",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
				{
					FeatureName: "fulltext_detail",
					DisplayName: "fulltext_detail",
					FeatureType: FieldFeatureType_Fulltext,
					Description: "BKN概念详情的全文检索特征",
					RefProperty: "detail",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"analyzer": "standard"},
				},
			},
		},
		{
			Name:         "kn_id",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "kn_id",
			OriginalName: "kn_id",
			Description:  "BKN中概念所属的知识网络ID",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_kn_id",
					DisplayName: "keyword_kn_id",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN知识网络ID的关键词特征",
					RefProperty: "kn_id",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "branch",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "branch",
			OriginalName: "branch",
			Description:  "BKN中概念所属的分支名称",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_branch",
					DisplayName: "keyword_branch",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN分支名称的关键词特征",
					RefProperty: "branch",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "creator",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "creator",
			OriginalName: "creator",
			Description:  "BKN中概念的创建者信息",
		},
		{
			Name:         "create_time",
			Type:         data_type.DATATYPE_DATETIME,
			DisplayName:  "create_time",
			OriginalName: "create_time",
			Description:  "BKN中概念的创建时间（毫秒时间戳）",
		},
		{
			Name:         "updater",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "updater",
			OriginalName: "updater",
			Description:  "BKN中概念的更新者信息",
		},
		{
			Name:         "update_time",
			Type:         data_type.DATATYPE_DATETIME,
			DisplayName:  "update_time",
			OriginalName: "update_time",
			Description:  "BKN中概念的更新时间（毫秒时间戳）",
		},
		// Object type specific fields
		{
			Name:         "data_source",
			Type:         data_type.DATATYPE_JSON, // 物化到opensearch中是 object 类型
			DisplayName:  "data_source",
			OriginalName: "data_source",
			Description:  "BKN对象类概念的数据源配置信息",
		},
		{
			Name:         "data_properties.name",
			Type:         data_type.DATATYPE_TEXT,
			DisplayName:  "data_properties.name",
			OriginalName: "data_properties.name",
			Description:  "BKN对象类概念的数据属性名称",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_data_properties_name",
					DisplayName: "keyword_data_properties_name",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN对象类概念的数据属性名称的关键词特征",
					RefProperty: "data_properties.name",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
				{
					FeatureName: "fulltext_data_properties_name",
					DisplayName: "fulltext_data_properties_name",
					FeatureType: FieldFeatureType_Fulltext,
					Description: "BKN对象类概念的数据属性名称的全文检索特征",
					RefProperty: "data_properties.name",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"analyzer": "standard"},
				},
			},
		},
		{
			Name:         "data_properties.display_name",
			Type:         data_type.DATATYPE_TEXT,
			DisplayName:  "data_properties.display_name",
			OriginalName: "data_properties.display_name",
			Description:  "BKN对象类概念的数据属性显示名称",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_data_properties_display_name",
					DisplayName: "keyword_data_properties_display_name",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN对象类概念的数据属性显示名称的关键词特征",
					RefProperty: "data_properties.display_name",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
				{
					FeatureName: "fulltext_data_properties_display_name",
					DisplayName: "fulltext_data_properties_display_name",
					FeatureType: FieldFeatureType_Fulltext,
					Description: "BKN对象类概念的数据属性显示名称的全文检索特征",
					RefProperty: "data_properties.display_name",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"analyzer": "standard"},
				},
			},
		},
		{
			Name:         "data_properties.type",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "data_properties.type",
			OriginalName: "data_properties.type",
			Description:  "BKN对象类概念的数据属性类型",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_data_properties_type",
					DisplayName: "keyword_data_properties_type",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN对象类概念的数据属性类型的关键词特征",
					RefProperty: "data_properties.type",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "data_properties.comment",
			Type:         data_type.DATATYPE_TEXT,
			DisplayName:  "data_properties.comment",
			OriginalName: "data_properties.comment",
			Description:  "BKN对象类概念的数据属性注释",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_data_properties_comment",
					DisplayName: "keyword_data_properties_comment",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN对象类概念的数据属性注释的关键词特征",
					RefProperty: "data_properties.name",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
				{
					FeatureName: "fulltext_data_properties_comment",
					DisplayName: "fulltext_data_properties_comment",
					FeatureType: FieldFeatureType_Fulltext,
					Description: "BKN对象类概念的数据属性注释的全文检索特征",
					RefProperty: "data_properties.comment",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"analyzer": "standard"},
				},
			},
		},
		{
			Name:         "data_properties.mapped_field",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "data_properties.mapped_field",
			OriginalName: "data_properties.mapped_field",
			Description:  "BKN对象类概念的数据属性映射字段",
		},
		{
			Name:         "data_properties.index_config",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "data_properties.index_config",
			OriginalName: "data_properties.index_config",
			Description:  "BKN对象类概念的数据属性索引配置",
		},
		{
			Name:         "data_properties.index_config",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "data_properties.index_config",
			OriginalName: "data_properties.index_config",
			Description:  "BKN对象类概念的数据属性索引配置",
		},
		{
			Name:         "logic_properties.name",
			Type:         data_type.DATATYPE_TEXT,
			DisplayName:  "logic_properties.name",
			OriginalName: "logic_properties.name",
			Description:  "BKN对象类概念的逻辑属性名称",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_logic_properties_name",
					DisplayName: "keyword_logic_properties_name",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN对象类概念的逻辑属性名称的关键词特征",
					RefProperty: "logic_properties.name",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
				{
					FeatureName: "fulltext_logic_properties_name",
					DisplayName: "fulltext_logic_properties_name",
					FeatureType: FieldFeatureType_Fulltext,
					Description: "BKN对象类概念的逻辑属性名称的全文检索特征",
					RefProperty: "logic_properties.name",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"analyzer": "standard"},
				},
			},
		},
		{
			Name:         "logic_properties.display_name",
			Type:         data_type.DATATYPE_TEXT,
			DisplayName:  "logic_properties.display_name",
			OriginalName: "logic_properties.display_name",
			Description:  "BKN对象类概念的逻辑属性显示名称",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_logic_properties_display_name",
					DisplayName: "keyword_logic_properties_display_name",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN对象类概念的逻辑属性显示名称的关键词特征",
					RefProperty: "logic_properties.display_name",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
				{
					FeatureName: "fulltext_logic_properties_display_name",
					DisplayName: "fulltext_logic_properties_display_name",
					FeatureType: FieldFeatureType_Fulltext,
					Description: "BKN对象类概念的逻辑属性显示名称的全文检索特征",
					RefProperty: "logic_properties.display_name",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"analyzer": "standard"},
				},
			},
		},
		{
			Name:         "logic_properties.type",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "logic_properties.type",
			OriginalName: "logic_properties.type",
			Description:  "BKN对象类概念的逻辑属性类型",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_logic_properties_type",
					DisplayName: "keyword_logic_properties_type",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN对象类概念的逻辑属性类型的关键词特征",
					RefProperty: "logic_properties.type",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "logic_properties.comment",
			Type:         data_type.DATATYPE_TEXT,
			DisplayName:  "logic_properties.comment",
			OriginalName: "logic_properties.comment",
			Description:  "BKN对象类概念的逻辑属性注释",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_logic_properties_comment",
					DisplayName: "keyword_logic_properties_comment",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN对象类概念的逻辑属性注释的关键词特征",
					RefProperty: "logic_properties.comment",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
				{
					FeatureName: "fulltext_logic_properties_comment",
					DisplayName: "fulltext_logic_properties_comment",
					FeatureType: FieldFeatureType_Fulltext,
					Description: "BKN对象类概念的逻辑属性注释的全文检索特征",
					RefProperty: "logic_properties.comment",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{"analyzer": "standard"},
				},
			},
		},
		{
			Name:         "logic_properties.data_source",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "logic_properties.data_source",
			OriginalName: "logic_properties.data_source",
			Description:  "BKN对象类概念的逻辑属性数据源",
		},
		{
			Name:         "logic_properties.parameters", // 逻辑属性的parameters字段需要把struct序列化成json string后存储，不展开
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "logic_properties.parameters",
			OriginalName: "logic_properties.parameters",
			Description:  "BKN对象类概念的逻辑属性参数",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_logic_properties_parameters",
					DisplayName: "keyword_logic_properties_parameters",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN对象类概念的逻辑属性参数的关键词特征",
					RefProperty: "logic_properties.parameters",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE_8192},
				},
			},
		},
		{
			Name:         "primary_keys",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "primary_keys",
			OriginalName: "primary_keys",
			Description:  "BKN对象类概念的主键字段列表",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_primary_keys",
					DisplayName: "keyword_primary_keys",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN主键字段的关键词特征",
					RefProperty: "primary_keys",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "display_key",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "display_key",
			OriginalName: "display_key",
			Description:  "BKN对象类概念的显示键字段",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_display_key",
					DisplayName: "keyword_display_key",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN显示键字段的关键词特征",
					RefProperty: "display_key",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		// Relation type specific fields
		{
			Name:         "source_object_type_id",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "source_object_type_id",
			OriginalName: "source_object_type_id",
			Description:  "BKN关系类概念的源对象类型ID",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_source_object_type_id",
					DisplayName: "keyword_source_object_type_id",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN源对象类型ID的关键词特征",
					RefProperty: "source_object_type_id",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "target_object_type_id",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "target_object_type_id",
			OriginalName: "target_object_type_id",
			Description:  "BKN关系类概念的目标对象类型ID",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_target_object_type_id",
					DisplayName: "keyword_target_object_type_id",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN目标对象类型ID的关键词特征",
					RefProperty: "target_object_type_id",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "type",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "type",
			OriginalName: "type",
			Description:  "BKN关系类概念的关系类型",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_type",
					DisplayName: "keyword_type",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN关系类型的关键词特征",
					RefProperty: "type",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "mapping_rules",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "mapping_rules",
			OriginalName: "mapping_rules",
			Description:  "BKN关系类概念的映射规则配置",
			Features:     []PropertyFeature{},
		},
		// Action type specific fields
		{
			Name:         "action_type",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "action_type",
			OriginalName: "action_type",
			Description:  "BKN行动类概念的行动类型",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_action_type",
					DisplayName: "keyword_action_type",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN行动类型的关键词特征",
					RefProperty: "action_type",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "action_intent",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "action_intent",
			OriginalName: "action_intent",
			Description:  "BKN行动类概念的行动意图",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_action_intent",
					DisplayName: "keyword_action_intent",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN行动意图的关键词特征",
					RefProperty: "action_intent",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "object_type_id",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "object_type_id",
			OriginalName: "object_type_id",
			Description:  "BKN行动类概念关联的对象类型ID",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_object_type_id",
					DisplayName: "keyword_object_type_id",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN对象类型ID的关键词特征",
					RefProperty: "object_type_id",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "condition",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "condition",
			OriginalName: "condition",
			Description:  "BKN行动类概念的触发条件配置",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_condition",
					DisplayName: "keyword_condition",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN行动类概念触发条件配置的关键词特征",
					RefProperty: "condition",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE_8192},
				},
			},
		},
		{
			Name:         "affect",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "affect",
			OriginalName: "affect",
			Description:  "BKN行动类概念的影响范围配置",
			Features:     []PropertyFeature{},
		},
		{
			Name:         "impact_contracts",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "impact_contracts",
			OriginalName: "impact_contracts",
			Description:  "BKN行动类概念的影响契约配置",
			Features:     []PropertyFeature{},
		},
		{
			Name:         "action_source",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "action_source",
			OriginalName: "action_source",
			Description:  "BKN行动类概念的行动来源配置",
			Features:     []PropertyFeature{},
		},
		{
			Name:         "parameters", // 行动类的parameters字段需要把struct序列化成json string后存储，不展开
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "parameters",
			OriginalName: "parameters",
			Description:  "BKN行动类概念的参数",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_parameters",
					DisplayName: "keyword_parameters",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN行动类概念的参数的关键词特征",
					RefProperty: "parameters",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE_8192},
				},
			},
		},
		{
			Name:         "schedule",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "schedule",
			OriginalName: "schedule",
			Description:  "BKN行动类概念的调度配置",
			Features:     []PropertyFeature{},
		},
		// Metric specific fields
		{
			Name:         "unit_type",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "schedule",
			OriginalName: "unit_type",
			Description:  "BKN指标的单位类型",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_unit_type",
					DisplayName: "keyword_unit_type",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN指标的单位类型的关键词特征",
					RefProperty: "unit_type",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "unit",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "unit",
			OriginalName: "unit",
			Description:  "BKN指标的单位",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_unit",
					DisplayName: "keyword_unit",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN指标的单位的关键词特征",
					RefProperty: "unit",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "metric_type",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "metric_type",
			OriginalName: "metric_type",
			Description:  "BKN指标的类型",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_metric_type",
					DisplayName: "keyword_metric_type",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN指标的类型的关键词特征",
					RefProperty: "metric_type",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "scope_type",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "scope_type",
			OriginalName: "scope_type",
			Description:  "BKN指标的统计主体类型",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_scope_type",
					DisplayName: "keyword_scope_type",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN指标的统计主体类型的关键词特征",
					RefProperty: "scope_type",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "scope_ref",
			Type:         data_type.DATATYPE_STRING,
			DisplayName:  "scope_ref",
			OriginalName: "scope_ref",
			Description:  "BKN指标的统计主体ID",
			Features: []PropertyFeature{
				{
					FeatureName: "keyword_scope_ref",
					DisplayName: "keyword_scope_ref",
					FeatureType: FieldFeatureType_Keyword,
					Description: "BKN指标的统计主体ID的关键词特征",
					RefProperty: "scope_ref",
					IsDefault:   true,
					IsNative:    false,
					Config:      map[string]any{FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE_VALUE},
				},
			},
		},
		{
			Name:         "time_dimension",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "time_dimension",
			OriginalName: "time_dimension",
			Description:  "BKN指标的时间维度",
			Features:     []PropertyFeature{},
		},
		{
			Name:         "calculation_formula",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "calculation_formula",
			OriginalName: "calculation_formula",
			Description:  "BKN指标的计算公式",
			Features:     []PropertyFeature{},
		},
		{
			Name:         "analysis_dimensions",
			Type:         data_type.DATATYPE_JSON,
			DisplayName:  "analysis_dimensions",
			OriginalName: "analysis_dimensions",
			Description:  "BKN指标的分析维度",
			Features:     []PropertyFeature{},
		},
	}

	// If default small model is enabled, add vector field
	if defaultSmallModelEnabled {
		datasetProp = append(datasetProp, &Property{
			Name:         "_vector",
			Type:         data_type.DATATYPE_VECTOR,
			DisplayName:  "_vector",
			OriginalName: "_vector",
			Description:  "基于BKN概念的名称、标签、描述、详情信息生成的向量",
			Features: []PropertyFeature{
				{
					FeatureName: "vector_module_type",
					DisplayName: "vector_module_type",
					FeatureType: FieldFeatureType_Vector,
					Description: "向量特征",
					RefProperty: "_vector",
					IsDefault:   true,
					IsNative:    false,
					Config: map[string]any{
						"dimension": vectorDim,
						"method": map[string]any{
							"name":       "hnsw",
							"space_type": "cosinesimil",
							"engine":     "lucene",
							"parameters": map[string]any{
								"ef_construction": 256,
								"m":               48,
							},
						},
					},
				},
			},
		})
	}

	return datasetProp
}
