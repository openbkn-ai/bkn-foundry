// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var riskTypeZhCN = i18n.I18nMessages{
	Locale: "zh-CN",
	Messages: map[string]string{
		"OntologyQuery.RiskType.RiskLevelExceedsMaxAcceptable":                 "评估得到的风险等级超过该风险类允许的最大等级。请调整入参或风险配置，或改用评估风险更低的操作。",
		"OntologyQuery.RiskType.RiskTypeNotFound":                              "引用的风险类不存在。请确认操作类型配置中的 risk_type_id 与已存在的风险类一致。",
		"OntologyQuery.RiskType.InternalError.GetRiskTypesByIDsFailed":         "加载风险类失败。请稍后重试；若持续失败请联系技术支持。",
		"OntologyQuery.RiskType.InternalError.ExecuteRiskAssessmentToolFailed": "风险评估工具执行失败。请检查风险函数关联的工具配置后重试。",
	},
}
