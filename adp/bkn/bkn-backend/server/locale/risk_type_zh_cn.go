// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var riskTypeZhCN = i18n.I18nMessages{
	Locale: "zh-CN",
	Messages: map[string]string{
		"BknBackend.RiskType.Duplicated.IDInFile":                               "存在重复的风险类ID。请检查参数是否正确。",
		"BknBackend.RiskType.Duplicated.Name":                                   "存在重复的风险类名称。请检查参数是否正确。",
		"BknBackend.RiskType.InvalidParameter":                                  "请求参数无效。请检查参数是否正确。",
		"BknBackend.RiskType.RiskTypeIDExisted":                                 "风险类ID已存在。请检查参数是否正确。",
		"BknBackend.RiskType.RiskTypeNameExisted":                               "风险类名称已存在。请检查参数是否正确。",
		"BknBackend.RiskType.RiskFunctionToolNotFound":                          "风险评估工具不存在或不可达。请确认 box_id 与 tool_id 已在 agent-operator-integration 中注册并可访问。",
		"BknBackend.RiskType.InvalidMaxAcceptableLevel":                         "最高可接受等级无效。请使用以下之一：safe, low, medium, high, critical。",
		"BknBackend.RiskType.InternalError":                                     "内部错误。请重试。若问题持续，请提交工单或联系技术支持。",
		"BknBackend.RiskType.InternalError.CheckRiskTypeIfExistFailed":          "检查风险类存在性失败。请重试。若问题持续，请提交工单或联系技术支持。",
		"BknBackend.RiskType.InternalError.GetRiskTypesByIDsFailed":             "按ID获取风险类失败。请重试。若问题持续，请提交工单或联系技术支持。",
		"BknBackend.RiskType.RiskTypeNotFound":                                  "风险类不存在。请重试。若问题持续，请提交工单或联系技术支持。",
		"BknBackend.RiskType.NullParameter.Name":                                "风险类名称为空。请检查参数是否正确。",
		"BknBackend.RiskType.LengthExceeded.Name":                               "风险类名称长度超出限制。请检查参数是否正确。",
		"BknBackend.RiskType.InvalidParameter.Detail.ModuleType":                "风险类模块类型必须为 'risk_type'。",
		"BknBackend.RiskType.InvalidParameter.Detail.DuplicatedIDInFile":        "请求体中已存在风险类 ID '{{.riskTypeID}}'。",
		"BknBackend.RiskType.InvalidParameter.Detail.DuplicatedNameInFile":      "请求体中已存在风险类名称 '{{.riskTypeName}}'。",
		"BknBackend.RiskType.InvalidParameter.Detail.RiskTypeIDAlreadyExists":   "风险类 ID {{.riskTypeID}} 已存在。",
		"BknBackend.RiskType.InvalidParameter.Detail.RiskTypeNameAlreadyExists": "风险类名称 {{.riskTypeName}} 已存在。",
	},
}
