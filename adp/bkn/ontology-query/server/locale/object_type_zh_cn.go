// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var objectTypeZhCN = i18n.I18nMessages{
	Locale: "zh-CN",
	Messages: map[string]string{
		"OntologyQuery.ObjectType.InvalidParameter":                           "请求参数不合法。请检查参数是否正确。",
		"OntologyQuery.ObjectType.InvalidParameter.IncludeTypeInfo":           "是否包含类信息参数无效。请检查参数是否正确。",
		"OntologyQuery.ObjectType.InvalidParameter.IgnoreLocalIndex":          "ignore_local_index 参数无效。请检查参数是否正确。",
		"OntologyQuery.ObjectType.InvalidParameter.SmallModel":                "小模型参数不合法。请检查参数是否正确。",
		"OntologyQuery.ObjectType.InvalidParameter.DynamicParams":             "动态参数无效。请检查参数是否正确。",
		"OntologyQuery.ObjectType.UnsupportLogicPropertyParameterSource":      "不支持的逻辑属性的参数来源。请检查参数是否正确。",
		"OntologyQuery.ObjectType.TooManyRequests":                            "请求过多。下游服务容量已满，请稍后原样重试，无需修改查询。",
		"OntologyQuery.ObjectType.ObjectTypeNotFound":                         "对象类不存在。请检查参数是否正确。",
		"OntologyQuery.ObjectType.SmallModelNotFound":                         "小模型不存在。请检查参数是否正确。",
		"OntologyQuery.ObjectType.InternalError":                              "内部错误。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"OntologyQuery.ObjectType.InternalError.ExecuteToolFailed":            "工具执行失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"OntologyQuery.ObjectType.InternalError.GetMetricDataByIDFailed":      "按id获取指标数据失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"OntologyQuery.ObjectType.InternalError.GetViewDataByIDFailed":        "按id获取视图数据失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"OntologyQuery.ObjectType.InternalError.GetObjectTypesByIDFailed":     "按id获取对象类信息失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"OntologyQuery.ObjectType.InternalError.GetSmallModelByIDFailed":      "按ID获取小模型失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"OntologyQuery.ObjectType.InternalError.ProcessLogicPropertiesFailed": "处理逻辑属性失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
	},
}
