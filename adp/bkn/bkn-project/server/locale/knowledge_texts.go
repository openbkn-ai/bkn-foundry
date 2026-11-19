// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

// KnowledgeDocumentTexts name the knowledge base's rules and concepts as a document runs cite, by
// the project's content language.
var KnowledgeDocumentTexts = map[string]map[string]string{
	"zh-CN": {
		"name":     "知识库中的规则和概念",
		"rules":    "规则",
		"concepts": "概念",
	},
	"en-US": {
		"name":     "Rules and concepts of the knowledge base",
		"rules":    "Rules",
		"concepts": "Concepts",
	},
}

// InputConceptTexts say why scenario drafting added the concept of an object type: a question's
// input reads a property of it.
var InputConceptTexts = map[string]string{
	"zh-CN": "场景问题的输入取自已采纳的对象类「%s」",
	"en-US": "A scenario question takes its input from the adopted object type %s",
}

// ModelBasisTexts say what a question rests on when the project has no requirement document to
// cite: the adopted concept model it was drafted from.
var ModelBasisTexts = map[string]string{
	"zh-CN": "项目没有需求文档可引用，问题依据已采纳的概念模型起草",
	"en-US": "The project has no requirement document to cite; the question is drafted from the adopted concept model",
}
