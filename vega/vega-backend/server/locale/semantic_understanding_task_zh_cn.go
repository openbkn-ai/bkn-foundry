// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var semanticUnderstandingTaskZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.SemanticUnderstandingTask.NotApplicable":             "仅已完成的 Resource 级 dry-run 理解结果可以应用。",
		"VegaBackend.SemanticUnderstandingTask.SchemaMismatch":            "资源结构与任务快照不一致，请重新创建语义理解任务。",
		"VegaBackend.SemanticUnderstandingTask.ConfidenceBelowThreshold":  "理解结果未达到原任务的置信度阈值。",
		"VegaBackend.SemanticUnderstandingTask.InvalidResult":             "保存的语义理解结果无效，无法应用。",
		"VegaBackend.SemanticUnderstandingTask.InternalError.ApplyFailed": "应用语义理解结果失败，请联系管理员。",

		"VegaBackend.SemanticUnderstandingTask.NotFound":                            "语义理解任务不存在。请检查语义理解任务 ID 是否存在",
		"VegaBackend.SemanticUnderstandingTask.HasRunningExecution":                 "语义理解任务正在等待或执行中，不能删除。请等待任务完成或失败后再删除",
		"VegaBackend.SemanticUnderstandingTask.InternalError.GetFailed":             "获取语义理解任务失败。请联系管理员",
		"VegaBackend.SemanticUnderstandingTask.InternalError.DeleteFailed":          "删除语义理解任务失败。请联系管理员",
		"VegaBackend.SemanticUnderstandingTask.InternalError.GetAccountNamesFailed": "获取语义理解任务创建者名称失败。请联系管理员",
	},
}
