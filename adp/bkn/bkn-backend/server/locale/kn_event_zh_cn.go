// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knEventZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknBackend.KnEvent.InvalidParameter":  "事件请求参数不合法。请根据错误详情修正后重试。",
		"BknBackend.KnEvent.EventTypeNotFound": "未找到事件类型。请确认知识网络与事件类型编码。",
		"BknBackend.KnEvent.EventNotFound":     "未找到事件。请确认知识网络与事件 ID。",
		"BknBackend.KnEvent.EventTypeExisted":  "事件类型编码已存在。请在同一知识网络内使用唯一的编码。",
		"BknBackend.KnEvent.EventTypeInUse":    "该事件类型已有事件，不能删除。",
		"BknBackend.KnEvent.SourceMismatch":    "事件类型的来源不支持此操作：登记只用于用户登记的类型，规则检查只用于规则类型。",
		"BknBackend.KnEvent.ActionNotAllowed":  "事件当前状态不允许此操作。请刷新后按可用操作重试。",
		"BknBackend.KnEvent.TooLarge":          "请求内容过大。请缩小规则、详情或命中对象的数量后重试。",
		"BknBackend.KnEvent.InternalError":     "事件服务内部错误。请稍后重试；若问题持续，请联系管理员。",
		"BknBackend.KnEvent.RuleEventTitle":    "{{.type}}：{{.object}}",
	},
}
