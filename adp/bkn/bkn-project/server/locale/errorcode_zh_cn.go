// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var errorcodeZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknProject.KnowledgeBase.SourceUnavailable":         "知识库暂时不可用，请稍后重试。",
		"BknProject.KnowledgeBase.InvalidSelection":          "请检查选择的来源范围和筛选条件。",
		"BknProject.KnowledgeBase.AuthenticationExpired":     "登录已失效，请重新登录。",
		"BknProject.KnowledgeBase.PermissionDenied":          "你没有读取这个知识库的权限。",
		"BknProject.KnowledgeBase.SourceMissing":             "选择的来源已不可用。",
		"BknProject.KnowledgeBase.SelectionConflict":         "来源选择已变化，请刷新后重试。",
		"BknProject.KnowledgeBase.VersionGone":               "这版原文已不再保留，请选择仍保留的版本后继续。",
		"BknProject.InvalidParameter.Limit":                  "分页数量不合法 请检查参数是否正确。",
		"BknProject.InvalidParameter.Cursor":                 "分页游标不合法 请原样传入上一页返回的 next_cursor。",
		"BknProject.Validation.Detail.AuthenticationFailed":  "认证失败。",
		"BknProject.Validation.Detail.LimitOutOfRange":       "limit 必须是 1 到 {{.max}} 之间的整数。",
		"BknProject.Validation.Detail.DependencyUnavailable": "数据库不可达。",
	},
}
