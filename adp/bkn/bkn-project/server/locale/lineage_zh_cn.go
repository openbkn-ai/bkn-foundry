// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var lineageZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknProject.Lineage.InvalidRequestBody":      "请求体不合法 请求体必须是包含约定字段的 JSON 对象。",
		"BknProject.Lineage.InvalidKNID":             "知识网络 ID 不合法 kn_id 不能为空。",
		"BknProject.Lineage.InvalidBranchName":       "分支名不合法 请使用形如 project/<id> 的 git 分支名，不能是 main，也不能包含空格或 \"..\"。",
		"BknProject.Lineage.InvalidDefinitions":      "定义包不合法 请求体必须是非空的 BKN tar 包，格式与 bkn-backend 导出的一致。",
		"BknProject.Lineage.NotLiveNetwork":          "不是该谱系的线上网络 漂移检查只能针对该谱系最新发布的网络，尚未发布时为创建谱系所用的网络，见 error_details。",
		"BknProject.Lineage.InvalidSwitchTarget":     "切换目标不合法 只能切换到该谱系某次发布建成的网络，或创建谱系所用的网络。",
		"BknProject.Lineage.AlreadyInUse":            "该网络已在使用 谱系的使用方已经指向这个网络，无需切换。",
		"BknProject.Lineage.MainBranchReadOnly":      "main 分支只读 main 只能通过同步线上网络或发布来更新。",
		"BknProject.Lineage.NotFound":                "发布谱系不存在 请先从知识网络创建该谱系。",
		"BknProject.Lineage.BranchNotFound":          "分支不存在 该谱系中没有这个分支或引用。",
		"BknProject.Lineage.AlreadyExists":           "发布谱系已存在 该知识网络已经有发布谱系。",
		"BknProject.Lineage.BranchAlreadyExists":     "分支已存在 请换一个分支名。",
		"BknProject.Lineage.MergeConflict":           "合并冲突 main 与该分支对同一定义做了不同修改，冲突明细见 error_details。",
		"BknProject.Lineage.BknBackendRequestFailed": "bkn-backend 请求失败 bkn-backend 拒绝了请求，返回内容见 error_details。",
		"BknProject.Lineage.CapabilityRequestFailed": "函数或 Skill 请求失败 读取或复制网络使用的函数（工具箱）或 Skill 失败，具体对象与原因见 error_details。Skill 必须已发布才能随网络发布。",
		"BknProject.Lineage.InternalError":           "内部错误 请联系管理员。",
	},
}
