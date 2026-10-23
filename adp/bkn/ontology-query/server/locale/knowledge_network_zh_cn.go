// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knowledgeNetworkZhCN = i18n.I18nMessages{
	Locale: "zh-CN",
	Messages: map[string]string{
		"OntologyQuery.KnowledgeNetwork.NullParameter.Direction":                      "方向为空。请检查参数是否正确。",
		"OntologyQuery.KnowledgeNetwork.NullParameter.SourceObjectTypeId":             "起点对象类为空。请检查参数是否正确。",
		"OntologyQuery.KnowledgeNetwork.NullParameter.TypePathObjectTypes":            "关系类路径中的节点对象类为空。请检查参数是否正确。",
		"OntologyQuery.KnowledgeNetwork.NullParameter.TypePathRelationTypes":          "关系类路径中的边（关系类）为空。请检查参数是否正确。",
		"OntologyQuery.KnowledgeNetwork.InvalidParameter":                             "请求参数不合法。请检查参数是否正确。",
		"OntologyQuery.KnowledgeNetwork.InvalidParameter.Direction":                   "路径方向参数无效。请检查参数是否正确。",
		"OntologyQuery.KnowledgeNetwork.InvalidParameter.IncludeTypeInfo":             "是否包含类信息参数无效。请检查参数是否正确。",
		"OntologyQuery.KnowledgeNetwork.InvalidParameter.PathLength":                  "探索路径长度的参数无效。请检查参数是否正确。",
		"OntologyQuery.KnowledgeNetwork.InvalidParameter.TypePath":                    "关系类路径参数无效。请检查参数是否正确。",
		"OntologyQuery.KnowledgeNetwork.KnowledgeNetworkNotFound":                     "业务知识网络不存在。请检查参数是否正确。",
		"OntologyQuery.KnowledgeNetwork.RelationTypeNotFound":                         "关系类不存在。请检查参数是否正确。",
		"OntologyQuery.KnowledgeNetwork.InternalError.GetViewDataByIDFailed":          "获取视图数据失败错误。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"OntologyQuery.KnowledgeNetwork.InternalError.GetKnowledgeNetworksByIDFailed": "获取业务知识网络信息失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"OntologyQuery.KnowledgeNetwork.InternalError.GetRelationTypeFailed":          "获取关系类信息失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
	},
}
