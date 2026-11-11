// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

// ZhCN are the zh-CN messages.
var ZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknBackend.KNVersion.NotVersioned":          "该知识网络未开启版本管理。",
		"BknBackend.KNVersion.InvalidVersion":        "版本取值不合法。请使用 draft、running 或版本号；版本化网络只接受 main 或 draft 分支。",
		"BknBackend.KNVersion.VersionNotFound":       "知识网络版本不存在。",
		"BknBackend.KNVersion.VersionPurged":         "该版本已清理，内容不再可用。",
		"BknBackend.KNVersion.EditionRestricted":     "该版本操作需要专业版。社区版只保留运行版本与上一版。",
		"BknBackend.KNVersion.VersioningDisabling":   "该知识网络正在关闭版本管理，写入、版本操作以及读取草稿与历史版本暂停。",
		"BknBackend.KNVersion.DraftRevisionMismatch": "草稿已变化，请重新读取草稿后重试。",
	},
}
