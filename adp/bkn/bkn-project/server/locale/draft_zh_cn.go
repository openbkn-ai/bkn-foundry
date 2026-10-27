// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var draftZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknProject.Draft.NoCatalogs":    "项目没有数据目录 请先为项目选择结构化数据所在的数据目录，再起草本体。",
		"BknProject.Draft.NotFound":      "草稿不存在 该项目还没有起草本体。",
		"BknProject.Draft.Drafting":      "正在起草 该项目的本体正在起草，请等待完成后再重新起草。",
		"BknProject.Draft.InternalError": "内部错误 请联系管理员。",
	},
}
