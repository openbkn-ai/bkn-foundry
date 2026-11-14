// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var buildScheduleZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.BuildSchedule.InvalidCronExpr":            "调度表达式无效，执行间隔不得短于一小时。",
		"VegaBackend.BuildSchedule.InvalidExecuteType":         "构建类型必须为 full 或 incremental。",
		"VegaBackend.BuildSchedule.InvalidTimeRange":           "调度有效时间范围无效。",
		"VegaBackend.BuildSchedule.InvalidParameter":           "构建调度配置无效。",
		"VegaBackend.BuildSchedule.NotFound":                   "构建调度不存在。",
		"VegaBackend.BuildSchedule.ResourceMismatch":           "不能修改调度绑定的资源。",
		"VegaBackend.BuildSchedule.EnabledFieldNotAllowed":     "请通过启用或停用接口修改调度状态。",
		"VegaBackend.BuildSchedule.UpdateConflict":             "调度已发生变化，请刷新后重试。",
		"VegaBackend.BuildSchedule.InternalError.GetFailed":    "获取构建调度失败。",
		"VegaBackend.BuildSchedule.InternalError.CreateFailed": "创建构建调度失败。",
		"VegaBackend.BuildSchedule.InternalError.UpdateFailed": "更新构建调度失败。",
		"VegaBackend.BuildSchedule.InternalError.DeleteFailed": "删除构建调度失败。",
	},
}
