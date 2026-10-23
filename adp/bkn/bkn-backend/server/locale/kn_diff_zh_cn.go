// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knDiffZhCN = i18n.I18nMessages{
	Locale: "zh-CN",
	Messages: map[string]string{
		"BknBackend.KNDiff.InvalidParameter":                                 "知识网络对比请求参数无效。请检查请求体。",
		"BknBackend.KNDiff.ObjectTypeNotFound":                               "该知识网络分支下不存在此对象类。请检查 kn_id、branch 与 ot_id。",
		"BknBackend.KNDiff.DataStatsUnavailable":                             "该对象类没有可统计的数据资源。请先为对象类绑定数据资源，再对比其数据。",
		"BknBackend.KNDiff.DataStatsQueryFailed":                             "统计对象类背后的数据失败。请稍后重试或联系管理员。",
		"BknBackend.KNDiff.InvalidParameter.Detail.BaseKNIDRequired":         "base.kn_id 不能为空。",
		"BknBackend.KNDiff.InvalidParameter.Detail.TargetKNIDRequired":       "target.kn_id 不能为空。",
		"BknBackend.KNDiff.InvalidParameter.Detail.BaseObjectTypeRequired":   "base.ot_id 不能为空。",
		"BknBackend.KNDiff.InvalidParameter.Detail.TargetObjectTypeRequired": "target.ot_id 不能为空。",
	},
}
