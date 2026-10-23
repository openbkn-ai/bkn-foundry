// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var actionScheduleZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknBackend.ActionSchedule.InvalidParameter":                  "参数无效。请检查参数是否正确。",
		"BknBackend.ActionSchedule.InvalidCronExpression":             "Cron表达式无效。请检查Cron表达式格式是否正确。",
		"BknBackend.ActionSchedule.InvalidStatus":                     "状态值无效。请检查状态值是否正确。",
		"BknBackend.ActionSchedule.ActionTypeNotFound":                "行动类不存在。请检查行动类ID是否正确。",
		"BknBackend.ActionSchedule.NotFound":                          "行动计划不存在。请检查行动计划ID是否正确。",
		"BknBackend.ActionSchedule.InternalError":                     "内部错误。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ActionSchedule.CreateFailed":                      "创建行动计划失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ActionSchedule.UpdateFailed":                      "更新行动计划失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ActionSchedule.DeleteFailed":                      "删除行动计划失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ActionSchedule.GetFailed":                         "获取行动计划失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ActionSchedule.GetActionTypeFailed":               "获取行动类失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ActionSchedule.Detail.NameRequired":               "必须提供 name。",
		"BknBackend.ActionSchedule.Detail.NameLength":                 "name 不能超过 100 个字符。",
		"BknBackend.ActionSchedule.Detail.ActionTypeIDRequired":       "必须提供 action_type_id。",
		"BknBackend.ActionSchedule.Detail.CronExpressionRequired":     "必须提供 cron_expression。",
		"BknBackend.ActionSchedule.Detail.InstanceIdentitiesRequired": "必须提供非空的 _instance_identities。",
		"BknBackend.ActionSchedule.Detail.StatusInvalid":              "status 必须为 active 或 inactive。",
		"BknBackend.ActionSchedule.Detail.UpdateFieldRequired":        "更新时至少提供一个字段。",
		"BknBackend.ActionSchedule.Detail.CronExpressionInvalid":      "Cron 表达式无效。",
		"BknBackend.ActionSchedule.Detail.ActionTypeNotFound":         "行动类 {{.actionTypeID}} 不存在。",
		"BknBackend.ActionSchedule.Detail.StatusValueInvalid":         "status 必须为 active 或 inactive，当前为 {{.status}}。",
		"BknBackend.ActionSchedule.Detail.ScheduleNotFound":           "行动计划 {{.scheduleID}} 不存在。",
		"BknBackend.ActionSchedule.Detail.ScheduleScopeMismatch":      "行动计划 {{.scheduleID}} 不属于知识网络 {{.knID}} 的分支 {{.branch}}。",
	},
}
