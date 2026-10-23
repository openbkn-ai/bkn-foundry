// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var valueTypeZhCN = i18n.I18nMessages{Locale: "zh-CN", Messages: map[string]string{
	"BknBackend.ValueType.InvalidParameter":     "值类型不合法 请对照 BKN 规范第 16 节检查值类型。",
	"BknBackend.ValueType.Duplicated.IDInFile":  "值类型 ID 重复 请检查参数是否正确。",
	"BknBackend.ValueType.Duplicated.Name":      "值类型名称重复 请检查参数是否正确。",
	"BknBackend.ValueType.ValueTypeIDExisted":   "值类型 ID 已存在 请检查参数是否正确。",
	"BknBackend.ValueType.ValueTypeNameExisted": "值类型名称已存在 请检查参数是否正确。",
	"BknBackend.ValueType.NullParameter.Name":   "值类型名称为空 请检查参数是否正确。",
	"BknBackend.ValueType.LengthExceeded.Name":  "值类型名称长度超出限制 请检查参数是否正确。",
	"BknBackend.ValueType.ValueTypeNotFound":    "值类型不存在 请检查值类型 ID 是否正确。",
	"BknBackend.ValueType.Referenced":           "值类型仍被引用 错误详情列出了使用该值类型的数据属性。请先修改这些属性的约束，再删除值类型或修改其 base_type。",
	"BknBackend.ValueType.InternalError":        "内部错误 请联系管理员。",
}}
