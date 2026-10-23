// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var discoverScheduleZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.DiscoverSchedule.NotFound":                            "采集调度不存在。请检查调度 ID 是否存在",
		"VegaBackend.DiscoverSchedule.InvalidCronExpr":                     "cron 表达式非法。请提供合法的 5 字段 cron 表达式，且相邻两次触发至少间隔 1 小时",
		"VegaBackend.DiscoverSchedule.InvalidStrategies":                   "采集策略非法。strategy 必须是 [full_sync, create_only, cleanup_only] 之一；空值表示 full_sync",
		"VegaBackend.DiscoverSchedule.InvalidTimeRange":                    "采集调度时间范围非法。start_time 和 end_time 必须大于等于 0；end_time 为 0 表示无结束时间，否则 start_time 必须小于等于 end_time",
		"VegaBackend.DiscoverSchedule.IdMismatch":                          "请求体中的 id 与 path 中的 id 不一致。请保持两者一致或省略 body.id",
		"VegaBackend.DiscoverSchedule.CatalogMismatch":                     "不能修改已有调度的 catalog_id。catalog_id 为只读字段；如需在其它 catalog 下调度，请新建一条",
		"VegaBackend.DiscoverSchedule.EnabledFieldNotAllowed":              "PUT 请求不允许修改 enabled 字段。请使用 POST /discover-schedules/{id}/enable 或 /disable 切换状态",
		"VegaBackend.DiscoverSchedule.InternalError.GetFailed":             "获取采集调度失败。请联系管理员",
		"VegaBackend.DiscoverSchedule.InternalError.CreateFailed":          "创建采集调度失败。请联系管理员",
		"VegaBackend.DiscoverSchedule.InternalError.UpdateFailed":          "更新采集调度失败。请联系管理员",
		"VegaBackend.DiscoverSchedule.InternalError.DeleteFailed":          "删除采集调度失败。请联系管理员",
		"VegaBackend.DiscoverSchedule.InternalError.GetAccountNamesFailed": "获取账户名称失败。请联系管理员",
		"VegaBackend.DiscoverSchedule.UpdateConflict":                      "发现调度在读取后已被更新。请重新加载发现调度后再提交修改",
	},
}
