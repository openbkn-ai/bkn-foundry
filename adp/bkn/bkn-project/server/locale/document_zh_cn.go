// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var documentZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknProject.Draft.EventParticipantInUse":            "该关系仍是事件参与方，请先修改事件的参与方设置再删除。",
		"BknProject.Draft.DocumentRelation.NewName":         "文档关系",
		"BknProject.Draft.DocumentRelation.ManualReason":    "由建模人配置，构建时按所选字段核对匹配。",
		"BknProject.Draft.DocumentDataNotReady":             "文档对象的数据准备尚未完成，暂时无法确认草稿；已审核的候选会保留。",
		"BknProject.KnowledgeBase.NoDebugSamples":           "请先选定并保存调试样本。",
		"BknProject.KnowledgeBase.NoLocatedSamples":         "所选样本没有可定位的文本，请改选已完成解析的文件。",
		"BknProject.KnowledgeBase.SampleLimit":              "所选样本过大，请减少调试文件数量或选择较小的文件。",
		"BknProject.KnowledgeBase.InvalidProposal":          "这次建议未通过原文或模型规则校验，请调整样本后重试。",
		"BknProject.KnowledgeBase.ModelUnavailable":         "概念抽取暂不可用，请检查内置助手配置后重试。",
		"BknProject.KnowledgeBase.ExtractionTimeout":        "概念抽取超时，请减少样本后重试。",
		"BknProject.Draft.DocumentField._id":                "记录标识",
		"BknProject.Draft.DocumentField.entity_id":          "实体标识",
		"BknProject.Draft.DocumentField.document_id":        "文档",
		"BknProject.Draft.DocumentField.block_id":           "原文块",
		"BknProject.Draft.DocumentField.content_version_id": "内容版本",
		"BknProject.Draft.DocumentField.source_quote":       "原文引文",
		"BknProject.Draft.DocumentField.source_start":       "引文起点",
		"BknProject.Draft.DocumentField.source_end":         "引文终点",
		"BknProject.Draft.DocumentField.extraction_version": "抽取版本",
		"BknProject.Draft.DocumentField.effective_from":     "生效时间",
		"BknProject.Draft.DocumentField.effective_to":       "失效时间",
		"BknProject.Draft.DocumentField.status":             "状态",
		"BknProject.Draft.DocumentField.recorded_at":        "记录时间",
		"BknProject.Draft.DocumentField.knowledge_base_id":  "知识库",
		"BknProject.Draft.DocumentField.file_id":            "文件",
		"BknProject.Draft.DocumentField.name":               "文件名称",
		"BknProject.Draft.DocumentField.image_ref":          "图片引用",
		"BknProject.Draft.Evidence.document":                "来源文档",
		"BknProject.Draft.Evidence.block":                   "原文块",
		"BknProject.Draft.Evidence.image":                   "来源图片",
		"BknProject.Draft.Evidence.contains_blocks":         "包含原文块",
		"BknProject.Draft.Evidence.contains_images":         "包含来源图片",
		"BknProject.Draft.Evidence.reason":                  "记录原文的来源与定位",
		"BknProject.Draft.Evidence.reference_document":      "{{.name}}的来源文档",
		"BknProject.Draft.Evidence.reference_block":         "{{.name}}的原文块",
	},
}
