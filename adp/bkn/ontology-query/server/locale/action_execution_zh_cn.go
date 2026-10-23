// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var actionExecutionZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"OntologyQuery.ActionExecution.InvalidParameter":      "请求参数无效。请检查参数是否正确。",
		"OntologyQuery.ActionExecution.ActionTypeNotFound":    "行动类不存在。请检查行动类ID是否正确。",
		"OntologyQuery.ActionExecution.ExecutionNotFound":     "执行记录不存在。请检查执行ID是否正确。",
		"OntologyQuery.ActionExecution.DuplicateExecution":    "检测到重复执行。同一行动类、实例集合与 dynamic_params 在防重复窗口内已有进行中的执行，请等待完成或查询已有 execution。",
		"OntologyQuery.ActionExecution.GetActionTypeFailed":   "获取行动类失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"OntologyQuery.ActionExecution.CreateExecutionFailed": "创建执行记录失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"OntologyQuery.ActionExecution.ExecuteToolFailed":     "工具执行失败。请检查工具配置并重试。",
		"OntologyQuery.ActionExecution.ExecuteMCPFailed":      "MCP执行失败。请检查MCP配置并重试。",
		"OntologyQuery.ActionExecution.QueryExecutionsFailed": "查询执行日志失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"OntologyQuery.ActionExecution.CancelExecutionFailed": "取消执行失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
	},
}
