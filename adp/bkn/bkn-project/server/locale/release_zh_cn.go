// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var releaseZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknProject.Release.NotFound":      "发布记录不存在 该知识网络不是由发布产生的。",
		"BknProject.Release.InternalError": "内部错误 请联系管理员。",
	},
}
