// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knVersionZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"OntologyQuery.KNVersion.InvalidParameter": "kn_version 或 draft_revision 不合法。kn_version 取 draft、running 或版本号；draft_revision 只能与 kn_version=draft 同时使用。",
		"OntologyQuery.KNVersion.Changed":          "查询期间知识网络版本发生变化，请重试查询。",
	},
}
