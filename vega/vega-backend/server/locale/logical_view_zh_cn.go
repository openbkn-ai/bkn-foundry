// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var logicalViewZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.LogicalView.InvalidParameter.JoinType":          "不支持的逻辑视图关联类型。请检查逻辑视图join节点join_type配置",
		"VegaBackend.LogicalView.InvalidParameter.LogicDefinition":   "无效的逻辑视图定义。请检查逻辑视图定义",
		"VegaBackend.LogicalView.InvalidParameter.FieldName":         "无效的字段名称。请检查字段参数",
		"VegaBackend.LogicalView.LengthExceeded.FieldName":           "字段名称长度超限。请缩短名称",
		"VegaBackend.LogicalView.LengthExceeded.FieldDisplayName":    "字段显示名称长度超限。请缩短显示名称",
		"VegaBackend.LogicalView.LengthExceeded.FieldComment":        "字段备注长度超限。请缩短备注内容",
		"VegaBackend.LogicalView.Duplicated.NodeID":                  "逻辑视图逻辑定义里节点ID重复。请使用不同的节点ID",
		"VegaBackend.LogicalView.Duplicated.FieldName":               "逻辑视图字段名称已存在。请使用不同的字段名称",
		"VegaBackend.LogicalView.Duplicated.FieldDisplayName":        "逻辑视图字段显示名称已存在。请使用不同的显示名称",
		"VegaBackend.LogicalView.InvalidParameter.FieldFeatureName":  "无效的字段特征名称。请检查特征名称",
		"VegaBackend.LogicalView.LengthExceeded.FieldFeatureName":    "字段特征名称长度超限。请缩短特征名称",
		"VegaBackend.LogicalView.Duplicated.FieldFeatureName":        "字段特征名称已存在。请使用不同的特征名称",
		"VegaBackend.LogicalView.LengthExceeded.FieldFeatureComment": "字段特征备注长度超限。请缩短特征备注内容",
		"VegaBackend.LogicalView.DefinitionInvalid":                  "衍生逻辑视图定义已失效{{if .Fields}}，涉及源字段：{{.Fields}}{{else}}，请检查固定过滤条件{{end}}。请编辑视图，重新配置输出字段或过滤条件。请根据当前源资源字段修改视图的输出字段或固定过滤条件，然后重新查询",
	},
}
