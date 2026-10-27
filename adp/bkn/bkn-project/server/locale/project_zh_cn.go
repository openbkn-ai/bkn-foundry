// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var projectZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknProject.Project.InvalidRequestBody":      "请求体不合法 请求体必须是合法的 JSON。",
		"BknProject.Project.InvalidName":             "项目名称不合法 名称不能为空，长度不超过 128 个字符。",
		"BknProject.Project.InvalidNetwork":          "目标知识网络不合法 kn_id（选择已有网络）和 new_kn（新建网络，需填写名称）必须且只能给出一个。",
		"BknProject.Project.InvalidCatalogIDs":       "数据目录不合法 catalog_ids 最多 100 个，每个不能为空，长度不超过 40 个字符。",
		"BknProject.Project.InvalidArchived":         "归档筛选不合法 archived 只能是 false、true 或 all。",
		"BknProject.Project.NotOwner":                "不是项目负责人 只有项目负责人可以归档或取消归档。",
		"BknProject.Project.NotFound":                "项目不存在 项目不存在，或你无权查看它的知识网络。",
		"BknProject.Project.BknBackendRequestFailed": "bkn-backend 请求失败 bkn-backend 拒绝了请求，返回内容见 error_details。",
		"BknProject.Project.InternalError":           "内部错误 请联系管理员。",
	},
}
