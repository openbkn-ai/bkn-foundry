// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var objectMetricZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknBackend.ObjectMetric.InvalidDefinition": "对象指标定义不合法。请根据字段级校验结果修正定义后重试。",
		"BknBackend.ObjectMetric.NotFound":          "未找到对象指标。请确认指标 ID、版本和分支。",
		"BknBackend.ObjectMetric.DuplicateCode":     "对象指标编码已存在。请使用同一知识网络内唯一且稳定的指标编码。",
		"BknBackend.ObjectMetric.ImmutableVersion":  "该指标版本不可修改。已发布版本不可变，请创建新草稿版本。",
		"BknBackend.ObjectMetric.DependencyInvalid": "对象指标依赖不合法。请检查依赖版本、对象对齐、时间窗口和循环依赖。",
		"BknBackend.ObjectMetric.DeleteBlocked":     "该对象指标版本仍被引用。请先删除或升级所有指标及逻辑属性引用，再删除该版本。",
		"BknBackend.ObjectMetric.InternalError":     "对象指标服务内部错误。请稍后重试；若问题持续，请联系管理员。",
	},
}
