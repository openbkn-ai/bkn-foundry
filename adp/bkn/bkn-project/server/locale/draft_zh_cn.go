// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var draftZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknProject.Draft.NoCatalogs":         "项目没有数据目录 请先为项目选择结构化数据所在的数据目录，再起草本体。",
		"BknProject.Draft.NotFound":           "草稿不存在 该项目还没有起草本体。",
		"BknProject.Draft.Drafting":           "正在起草 该项目的本体正在起草，请等待完成后再重新起草。",
		"BknProject.Draft.InternalError":      "内部错误 请联系管理员。",
		"BknProject.Draft.InvalidRequestBody": "请求体不合法 请求体必须是合法的 JSON。",
		"BknProject.Draft.EmptyEdit":          "没有要修改的内容 请至少提供 adopted、name 或 description 中的一项。",
		"BknProject.Draft.InvalidName":        "名称不合法 名称不能为空，最长 128 个字符。",
		"BknProject.Draft.InvalidDescription": "描述不合法 描述最长 1000 个字符。",
		"BknProject.Draft.EndpointNotAdopted": "对象类未采用 请先采用关系类两端的对象类，再采用该关系类。",
		"BknProject.Draft.ItemNotFound":       "草稿项不存在 草稿中没有该项。",
		"BknProject.Draft.ProjectArchived":    "项目已归档 请先取消归档，再修改草稿。",
		"BknProject.Draft.NotReady":           "草稿未就绪 只有起草完成的草稿才能修改。",
		"BknProject.Draft.Stale":              "草稿已被修改 加载之后草稿已被他人修改，请重新加载后再试。",
		"BknProject.Draft.HasDecisions":       "草稿已有决定 重新起草会丢弃草稿上已做的决定，确认丢弃后才能重新起草。",
	},
}
