// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var actionTypeZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"OntologyQuery.ActionType.InvalidParameter":                       "请求参数不合法。请检查参数是否正确。",
		"OntologyQuery.ActionType.InvalidParameter.IncludeTypeInfo":       "是否包含类信息参数无效。请检查参数是否正确。",
		"OntologyQuery.ActionType.InvalidParameter.IgnoreLocalIndex":      "ignore_local_index 参数无效。请检查参数是否正确。",
		"OntologyQuery.ActionType.InvalidParameter.DynamicParams":         "动态参数无效。请检查参数是否正确。",
		"OntologyQuery.ActionType.ActionTypeNotFound":                     "行动类不存在。请检查参数是否正确。",
		"OntologyQuery.ActionType.InternalError":                          "内部错误。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"OntologyQuery.ActionType.InternalError.GetActionTypesByIDFailed": "按id获取行动类信息失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
	},
}
