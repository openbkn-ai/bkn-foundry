// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var documentZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
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
