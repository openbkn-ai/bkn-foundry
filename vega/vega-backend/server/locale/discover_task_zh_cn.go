// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var discoverTaskZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.DiscoverTask.NotFound":                            "采集任务不存在。请检查采集任务 ID 是否存在",
		"VegaBackend.DiscoverTask.InvalidStatus":                       "采集任务状态无效。status 必须为 pending / running / completed / failed / cancelled 之一",
		"VegaBackend.DiscoverTask.HasRunningExecution":                 "采集任务处于 pending 或 running 状态，无法删除。请等待任务完成或失败后再删除",
		"VegaBackend.DiscoverTask.ResourceRefreshInProgress":           "该资源的元数据刷新任务正在等待或运行。请等待当前刷新任务结束后重试",
		"VegaBackend.DiscoverTask.InternalError.GetFailed":             "获取采集任务失败。请联系管理员",
		"VegaBackend.DiscoverTask.InternalError.DeleteFailed":          "删除采集任务失败。请联系管理员",
		"VegaBackend.DiscoverTask.InternalError.GetAccountNamesFailed": "获取账户名称失败。请联系管理员",
	},
}
