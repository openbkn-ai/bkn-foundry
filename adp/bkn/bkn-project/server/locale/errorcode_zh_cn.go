// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var errorcodeZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknProject.InvalidParameter.Limit":                  "分页数量不合法 请检查参数是否正确。",
		"BknProject.InvalidParameter.Cursor":                 "分页游标不合法 请原样传入上一页返回的 next_cursor。",
		"BknProject.Validation.Detail.AuthenticationFailed":  "认证失败。",
		"BknProject.Validation.Detail.LimitOutOfRange":       "limit 必须是 1 到 {{.max}} 之间的整数。",
		"BknProject.Validation.Detail.DependencyUnavailable": "数据库不可达。",
	},
}
