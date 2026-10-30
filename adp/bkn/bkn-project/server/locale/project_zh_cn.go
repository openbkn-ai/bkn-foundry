// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var projectZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknProject.Project.InvalidRequestBody":      "请求体不合法 请求体必须是合法的 JSON。",
		"BknProject.Project.InvalidName":             "项目名称不合法 名称不能为空，长度不超过 128 个字符。",
		"BknProject.Project.InvalidNetwork":          "目标知识网络不合法 kn_id（选择已有网络）和 new_kn（新建网络，需填写名称）必须且只能给出一个。",
		"BknProject.Project.InvalidCatalogIDs":       "数据目录不合法 catalog_ids 最多 100 个，每个不能为空，长度不超过 40 个字符。",
		"BknProject.Project.InvalidArchived":         "归档筛选不合法 archived 只能是 false、true 或 all。",
		"BknProject.Project.InvalidNamingLanguage":   "命名语言不合法 naming_language 只能是 zh-CN 或 en-US。",
		"BknProject.Project.NotOwner":                "不是项目负责人 只有项目负责人可以修改项目。",
		"BknProject.Project.NotFound":                "项目不存在 项目不存在，或你无权查看它的知识网络。",
		"BknProject.Project.CatalogMissing":          "数据目录已被删除 所选的数据目录已不存在，请选择其他数据目录。",
		"BknProject.Project.BknBackendRequestFailed": "bkn-backend 请求失败 bkn-backend 拒绝了请求，返回内容见 error_details。",
		"BknProject.Project.InternalError":           "内部错误 请联系管理员。",
		"BknProject.Requirement.InvalidRequestBody":  "请求体不合法 请求体必须是合法的 JSON。",
		"BknProject.Requirement.MissingUpdateTime":   "缺少更新时间 请带上读取需求规格时的 update_time，刷新后再试。",
		"BknProject.Requirement.UnsupportedFormat":   "文件格式不支持 只能上传 Markdown（.md、.markdown）或纯文本（.txt）文件。",
		"BknProject.Requirement.NotText":             "不是文本文件 文件必须是 UTF-8 编码的文本。",
		"BknProject.Requirement.DocumentTooLarge":    "文件过大 文件超过了单个需求资料的大小上限。",
		"BknProject.Requirement.TooManyDocuments":    "需求资料过多 项目的需求资料已达数量上限，请先移除不用的资料。",
		"BknProject.Requirement.InvalidDocumentType": "文档类型不合法 doc_type 只能是 prd、policy、standard 或 other。",
		"BknProject.Requirement.InvalidItem":         "条目不合法 条目缺少必填内容、内容过长，或引用了不存在的条目，见 error_details。",
		"BknProject.Requirement.InvalidResolution":   "裁决不合法 裁决需要选择一方文档或写明结论，并给出理由。",
		"BknProject.Requirement.InvalidOption":       "选项不存在 所选的取舍选项不在这个分叉的选项里。",
		"BknProject.Requirement.NoDocuments":         "没有需求资料 请先上传至少一份需求资料再起草。",
		"BknProject.Requirement.NoCatalogs":          "没有数据目录 请先为项目选择数据目录再起草。",
		"BknProject.Requirement.InputsTooLarge":      "需求资料过长 需求资料的总长度超过一次起草的上限，请精简或移除部分资料后再试。",
		"BknProject.Requirement.NotOwner":            "不是项目负责人 只有项目负责人可以修改需求规格和需求资料。",
		"BknProject.Requirement.NotFound":            "没有需求规格 项目还没有起草需求规格。",
		"BknProject.Requirement.DocumentNotFound":    "需求资料不存在 项目中没有这份需求资料。",
		"BknProject.Requirement.ItemNotFound":        "条目不存在 需求规格中没有这个条目。",
		"BknProject.Requirement.Drafting":            "正在起草 需求规格正在起草，请等它完成后再试。",
		"BknProject.Requirement.Stale":               "需求规格已被修改 加载之后需求规格已被他人修改，请重新加载后再试。",
		"BknProject.Requirement.HasDecisions":        "需求规格已有裁决 重新起草会丢弃已做的冲突裁决和建模取舍，确认丢弃后才能重新起草。",
		"BknProject.Requirement.ProjectArchived":     "项目已归档 归档的项目不能修改需求规格和需求资料。",
		"BknProject.Requirement.InternalError":       "内部错误 需求规格处理失败，请联系管理员。",
		"BknProject.Requirement.InvalidTest":         "测试问法不合法 测试问法不能为空、不能过长，取值所属的概念必须存在，见 error_details。",
		"BknProject.Requirement.TestNotFound":        "测试问法不存在 这个场景问题没有这条测试问法。",
		"BknProject.Requirement.InvalidExportTarget": "导出对象不合法 target 只能是 workshop 或 eval。",
		"BknProject.Requirement.Generating":          "正在生成 这个场景问题的测试问法与开发指导正在生成，请等它完成后再试。",
	},
}
