// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var datasetZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.Dataset.InvalidParameter.SchemaDefinition":  "无效的数据集 schema 定义。schema_definition 不能为空，至少包含一个字段",
		"VegaBackend.Dataset.InvalidParameter.FieldName":         "无效的字段名称。请检查字段参数",
		"VegaBackend.Dataset.LengthExceeded.FieldName":           "字段名称长度超限。请缩短名称",
		"VegaBackend.Dataset.LengthExceeded.FieldDisplayName":    "字段显示名称长度超限。请缩短显示名称",
		"VegaBackend.Dataset.LengthExceeded.FieldComment":        "字段备注长度超限。请缩短备注内容",
		"VegaBackend.Dataset.Duplicated.FieldName":               "数据集字段名称已存在。请使用不同的字段名称",
		"VegaBackend.Dataset.Duplicated.FieldDisplayName":        "数据集字段显示名称已存在。请使用不同的显示名称",
		"VegaBackend.Dataset.InvalidParameter.FieldType":         "无效的字段类型。请使用 VEGA 支持的字段类型",
		"VegaBackend.Dataset.InvalidParameter.FieldFeatureName":  "无效的字段特征名称。请检查特征名称",
		"VegaBackend.Dataset.LengthExceeded.FieldFeatureName":    "字段特征名称长度超限。请缩短特征名称",
		"VegaBackend.Dataset.Duplicated.FieldFeatureName":        "字段特征名称已存在。请使用不同的特征名称",
		"VegaBackend.Dataset.LengthExceeded.FieldFeatureComment": "字段特征备注长度超限。请缩短特征备注内容",
		"VegaBackend.Dataset.InvalidParameter.FieldFeatureType":  "无效的字段特征类型。请使用 keyword/fulltext/vector 之一",
		"VegaBackend.Dataset.InvalidParameter.FieldFeatureRef":   "字段特征引用字段无效。请确认 ref_property 非空且存在于字段列表",
		"VegaBackend.Dataset.Unsupported.FieldFeatureRefType":    "字段特征引用字段类型不匹配。请确认引用字段类型与特征类型兼容（如 fulltext 需要 text，keyword 需要 string，vector 需要 vector）",
		"VegaBackend.Dataset.Duplicated.DefaultFeaturePerType":   "同一特征类型只能有一个默认特征。请取消其他默认特征",
	},
}
