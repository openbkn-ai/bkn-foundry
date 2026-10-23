// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var indexCapabilityZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.IndexCapability.InternalError.Unavailable": "全文分词器能力不可用。请恢复 OpenSearch 连接后重启 Vega",
	},
}
