// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var conceptGroupZhCN = i18n.I18nMessages{
	Locale: "zh-CN",
	Messages: map[string]string{
		"BknBackend.ConceptGroup.Duplicated.Name":                                    "存在重复的概念分组名称。请检查参数是否正确。",
		"BknBackend.ConceptGroup.InvalidParameter":                                   "请求参数不合法。请检查参数是否正确。",
		"BknBackend.ConceptGroup.InvalidParameter.ConceptCondition":                  "概念过滤条件不合法。请检查参数是否正确。",
		"BknBackend.ConceptGroup.InvalidParameter.Direction":                         "路径方向不合法。请检查参数是否正确。",
		"BknBackend.ConceptGroup.InvalidParameter.IncludeStatistics":                 "是否包含统计信息不合法。请检查参数是否正确。",
		"BknBackend.ConceptGroup.InvalidParameter.IncludeTypeInfo":                   "是否包含概念信息不合法。请检查参数是否正确。",
		"BknBackend.ConceptGroup.InvalidParameter.PathLength":                        "路径长度不合法。请检查参数是否正确。",
		"BknBackend.ConceptGroup.ConceptGroupIDExisted":                              "概念分组ID已经存在。请检查参数是否正确。",
		"BknBackend.ConceptGroup.ConceptGroupNameExisted":                            "概念分组名称已经存在。请检查参数是否正确。",
		"BknBackend.ConceptGroup.ConceptGroupRelationExisted":                        "概念与分组的关系已经存在。请检查参数是否正确。",
		"BknBackend.ConceptGroup.ConceptGroupRelationNotExisted":                     "存在概念与分组的关系不存在。请检查参数是否正确。",
		"BknBackend.ConceptGroup.LengthExceeded.Name":                                "概念分组名称长度超出限制。请检查参数是否正确。",
		"BknBackend.ConceptGroup.NullParameter.Direction":                            "方向为空。请检查参数是否正确。",
		"BknBackend.ConceptGroup.NullParameter.Name":                                 "概念分组名称为空。请检查参数是否正确。",
		"BknBackend.ConceptGroup.NullParameter.SourceObjectTypeId":                   "起点对象类为空。请检查参数是否正确。",
		"BknBackend.ConceptGroup.ConceptGroupNotFound":                               "概念分组不存在。请检查参数是否正确。",
		"BknBackend.ConceptGroup.ObjectTypeNotFound":                                 "对象类不存在。请检查参数是否正确。",
		"BknBackend.ConceptGroup.InternalError":                                      "内部错误。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.AddObjectTypesToConceptGroupFailed":   "添加对象类到分组失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.MissingTransaction":                   "缺少事务。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.BeginTransactionFailed":               "开启事务异常。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.CheckConceptGroupIfExistFailed":       "检查概念分组名称是否存在失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.GetConceptGroupByIDFailed":            "按ID获取概念分组失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.UpdateConceptGroupFailed":             "更新概念分组失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.CreateConceptGroupFailed":             "创建概念分组失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.CreateConceptGroupRelationFailed":     "创建概念与分组关系失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.CreateResourcesFailed":                "创建概念分组资源失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.GetActionTypesTotalFailed":            "获取概念分组下的行动类数量失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.GetConceptIDsByConceptGroupIDsFailed": "获取概念分组下的概念ID失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.GetRelationTypesTotalFailed":          "获取概念分组下的关系类数量失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.GetVectorFailed":                      "获取概念分组向量失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.InsertOpenSearchDataFailed":           "概念分组概念索引插入失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.CreateObjectTypesFailed":              "创建对象类失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.CreateRelationTypesFailed":            "创建关系类失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
		"BknBackend.ConceptGroup.InternalError.CreateActionTypesFailed":              "创建行动类失败。请重试该操作，若再次出现该错误请提交工单或联系技术支持工程师。",
	},
}
