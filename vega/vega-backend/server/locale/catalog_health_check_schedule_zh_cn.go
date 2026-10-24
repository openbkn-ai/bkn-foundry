// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var catalogHealthCheckScheduleZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.CatalogHealthCheckSchedule.InvalidParameter":           "目录健康检查计划参数无效。请检查计划模式和 Cron 配置，并确认目标目录为物理目录",
		"VegaBackend.CatalogHealthCheckSchedule.NotFound":                   "目录健康检查计划不存在。请检查目录 ID 及其健康检查计划是否存在",
		"VegaBackend.CatalogHealthCheckSchedule.UpdateConflict":             "目录健康检查计划在读取后已被更新。请重新加载目录健康检查计划后再提交修改",
		"VegaBackend.CatalogHealthCheckSchedule.InternalError":              "目录健康检查计划服务内部错误。请联系管理员",
		"VegaBackend.CatalogHealthCheckSchedule.InternalError.GetFailed":    "获取目录健康检查计划失败。请联系管理员",
		"VegaBackend.CatalogHealthCheckSchedule.InternalError.UpdateFailed": "更新目录健康检查计划失败。请联系管理员",
	},
}
