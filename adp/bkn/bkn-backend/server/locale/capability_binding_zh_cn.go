// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var capabilityBindingZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknBackend.CapabilityBinding.InvalidParameter":                     "能力绑定参数不合法。请检查请求体中的能力类型与标识。",
		"BknBackend.CapabilityBinding.NullParameter.CapabilityID":           "缺少能力标识。skill 需要提供 skill_id，function 需要提供 tool_id。",
		"BknBackend.CapabilityBinding.NullParameter.OwnerID":                "缺少所属工具箱标识。function 类型必须提供 owner_id（工具箱 id），工具标识在执行工厂按箱限定作用域。",
		"BknBackend.CapabilityBinding.InvalidCapabilityType":                "不支持的能力类型。能力类型仅支持 skill 与 function。",
		"BknBackend.CapabilityBinding.InternalError":                        "能力绑定处理失败。请重试或联系支持。",
		"BknBackend.CapabilityBinding.InternalError.BeginTransactionFailed": "开启事务失败。请重试或联系支持。",
		"BknBackend.CapabilityBinding.InternalError.CreateBindingsFailed":   "写入能力绑定失败。请重试或联系支持。",
		"BknBackend.CapabilityBinding.InternalError.DeleteBindingsFailed":   "解除能力绑定失败。请重试或联系支持。",
		"BknBackend.CapabilityBinding.InternalError.ListBindingsFailed":     "查询能力绑定列表失败。请重试或联系支持。",
		"BknBackend.CapabilityBinding.InternalError.GetBindingsTotalFailed": "统计能力绑定数量失败。请重试或联系支持。",
		"BknBackend.CapabilityBinding.TargetNotFound":                       "能力不存在。请确认 skill_id，或 function 的 box_id 与 tool_id 是否正确。",
		"BknBackend.CapabilityBinding.TargetNotAvailable":                   "能力存在但当前不可用。请先在执行工厂发布该工具箱 / 技能，或启用该工具，再挂载。",
		"BknBackend.CapabilityBinding.EmptyToolBox":                         "工具箱内没有可挂载的工具。请先在执行工厂为该工具箱添加并启用工具。",
		"BknBackend.CapabilityBinding.ExecutionFactoryUnavailable":          "执行工厂不可达，未写入任何绑定。请稍后重试；本次请求没有产生部分写入。",
		"BknBackend.CapabilityBinding.ExportMetadataUnavailable":            "能力名称不可用，本次导出已被拒绝。请在执行工厂恢复后重试；本次未导出任何内容，也未改动数据。",
	},
}
