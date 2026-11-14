// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var scheduleTextZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.Schedule.Result.InvalidCreator":                 "调度执行身份无效，请重新创建调度",
		"VegaBackend.Schedule.Result.BuildResourceMissing":           "资源不存在或已不属于调度目录",
		"VegaBackend.Schedule.Result.BuildTargetDisabled":            "资源或目录已停用",
		"VegaBackend.Schedule.Result.BuildCreateFailed":              "无法创建构建任务，请检查资源配置或联系管理员",
		"VegaBackend.Schedule.Result.BuildTaskActive":                "资源已有待执行、运行中或停止中的构建任务",
		"VegaBackend.Schedule.Result.CatalogDisabled":                "目录已停用",
		"VegaBackend.Schedule.Result.BuildPrimaryKeyInvalid":         "资源主键配置无效，无法创建构建任务",
		"VegaBackend.Schedule.Result.BuildIncrementalFieldsInvalid":  "资源增量字段配置无效，无法创建构建任务",
		"VegaBackend.Schedule.Result.BuildBaselineUnavailable":       "增量构建缺少可用索引或有效基线",
		"VegaBackend.Schedule.Result.BuildPermissionDenied":          "调度执行者没有当前目录或资源的构建权限",
		"VegaBackend.Schedule.Result.DiscoverServiceUnavailable":     "探查任务服务不可用",
		"VegaBackend.Schedule.Result.DiscoverCatalogReadFailed":      "无法读取或校验探查目录",
		"VegaBackend.Schedule.Result.DiscoverCatalogUnavailable":     "探查目录不存在或已停用",
		"VegaBackend.Schedule.Result.DiscoverActiveTasksCheckFailed": "无法检查正在执行的探查任务",
		"VegaBackend.Schedule.Result.DiscoverTaskActive":             "目录已有正在执行的定时探查任务",
		"VegaBackend.Schedule.Result.DiscoverCreateFailed":           "无法创建探查任务，请检查目录配置与权限",
		"VegaBackend.Schedule.Result.DiscoverPermissionDenied":       "计划创建者无目录任务管理权限，无法创建探查任务",
		"VegaBackend.Schedule.Result.BuildCallFailed":                "构建调度调用异常，请联系管理员",
		"VegaBackend.Schedule.Result.BuildCronInvalid":               "调度表达式无效，请修正后重新启用",
		"VegaBackend.Schedule.Result.BuildCronNoFuture":              "调度表达式没有可用的未来触发时间",
		"VegaBackend.Schedule.Result.BuildCreateUnavailable":         "无法创建构建任务，请联系管理员",
		"VegaBackend.Schedule.Result.CronInvalid":                    "调度表达式无效或执行间隔不足一小时，请修正后启用",
		"VegaBackend.Schedule.Result.CronNoFuture":                   "调度表达式没有有效的未来触发时间，请修正后启用",
		"VegaBackend.Schedule.Result.HealthCheckFailed":              "无法完成连接健康检查，请检查目录配置",
		"VegaBackend.Schedule.Result.HealthCatalogDisabled":          "目录已停用，跳过本次健康检查",
		"VegaBackend.Schedule.Result.HealthCatalogMissing":           "目录不存在，跳过本次健康检查",
	},
}
