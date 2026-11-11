// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knVersionZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknBackend.KNVersion.AlreadyVersioned":       "该知识网络已开启版本管理。",
		"BknBackend.KNVersion.ReservedBranchInUse":    "该知识网络在保留分支（draft 或 v<N>）上已有数据，请先清除后再开启版本管理。",
		"BknBackend.KNVersion.RunningVersionMismatch": "运行版本已变化，请刷新版本状态后重试。",
		"BknBackend.KNVersion.DraftHasChanges":        "草稿有未发布的修改，确认覆盖后才能继续。",
		"BknBackend.KNVersion.DraftBaseOutdated":      "草稿确定上游之后运行版本被切换过。请查看差异，确认后再发布。",
		"BknBackend.KNVersion.DefinitionModified":     "该定义已被他人修改，请重新读取后重试。",
		"BknBackend.KNVersion.PreconditionRequired":   "写入版本化网络需要前置条件：修改单个定义带 If-Match，其余带 expected_draft_revision。",
		"BknBackend.KNVersion.ConceptIndexFailed":     "概念索引构建失败，请重试。",
		"BknBackend.KNVersion.NothingToPublish":       "草稿没有未发布的修改。",
		"BknBackend.KNVersion.ValidationFailed":       "草稿未通过发布校验，请修正所列问题后重试。",
		"BknBackend.KNVersion.BrokenReferences":       "目标版本引用的资源已不存在，请修正所列问题后重试。",
		"BknBackend.KNVersion.VersionNotActivatable":  "只有历史版本可以切换为运行版本。",
		"BknBackend.KNVersion.VersionNotRestorable":   "只有已废止的版本可以恢复。",
		"BknBackend.KNVersion.VersionNotDeprecatable": "只有历史版本可以废止。",
		"BknBackend.KNVersion.VersionProtected":       "运行版本与草稿的来源版本不能清理。",
		"BknBackend.KNVersion.IdempotencyKeyReused":   "该 Idempotency-Key 已用于其他操作或其他版本。",
		"BknBackend.KNVersion.InvalidParameter":       "版本请求参数不合法，请查看错误详情。",
		"BknBackend.KNVersion.InternalError":          "版本管理内部错误。请重试；若问题持续，请联系管理员。",
	},
}
