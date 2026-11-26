// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knEventZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknBackend.KnEvent.InvalidParameter":    "事件请求参数不合法。请根据错误详情修正后重试。",
		"BknBackend.KnEvent.EventTypeNotFound":   "未找到事件类。请确认知识网络正在运行的版本中有该事件类。",
		"BknBackend.KnEvent.EventNotFound":       "未找到事件。请确认知识网络与事件 ID。",
		"BknBackend.KnEvent.EventTypeInUse":      "该事件类已有事件，不能删除。",
		"BknBackend.KnEvent.SourceMismatch":      "事件类的来源不支持此操作：登记只用于来源为登记的事件类，规则检查只用于来源为规则的事件类。",
		"BknBackend.KnEvent.ActionNotAllowed":    "事件当前状态不允许此操作。请刷新后按可用操作重试。",
		"BknBackend.KnEvent.RecognizedByAction":  "该事件类由记录事件的行动类记录，不能删除，也不能修改其关于的对象类。请先删除或修改该行动类。",
		"BknBackend.KnEvent.RecognitionMismatch": "该事件类的规则检查只能由它在运行版本中记录事件的行动类提交。请确认行动类与事件类的对应关系。",
		"BknBackend.KnEvent.TooLarge":            "请求内容过大。请缩小详情或命中对象的数量后重试。",
		"BknBackend.KnEvent.InternalError":       "事件服务内部错误。请稍后重试；若问题持续，请联系管理员。",
		"BknBackend.KnEvent.RuleEventTitle":      "{{.type}}：{{.object}}",

		// What the event classes show: written into the network's definitions in one business language.
		"BknBackend.KnEvent.Class.Relation":                "关于",
		"BknBackend.KnEvent.Class.RelationComment":         "“{{.name}}”事件所关于的对象。",
		"BknBackend.KnEvent.Class.Field.event_id":          "事件 ID",
		"BknBackend.KnEvent.Class.Field.event_type":        "事件类",
		"BknBackend.KnEvent.Class.Field.title":             "标题",
		"BknBackend.KnEvent.Class.Field.status":            "状态",
		"BknBackend.KnEvent.Class.Field.severity":          "严重程度",
		"BknBackend.KnEvent.Class.Field.source":            "来源",
		"BknBackend.KnEvent.Class.Field.object_key":        "对象标识",
		"BknBackend.KnEvent.Class.Field.object_name":       "对象名称",
		"BknBackend.KnEvent.Class.Field.occurred_at":       "发生时间",
		"BknBackend.KnEvent.Class.Field.ended_at":          "结束时间",
		"BknBackend.KnEvent.Class.Field.recorded_at":       "记录时间",
		"BknBackend.KnEvent.Class.Field.first_detected_at": "首次发现",
		"BknBackend.KnEvent.Class.Field.backlog":           "存量",
		"BknBackend.KnEvent.Class.Field.reported_by":       "报告人 ID",
		"BknBackend.KnEvent.Class.Field.reported_by_name":  "报告人",
		"BknBackend.KnEvent.Class.Field.verified_by":       "核实人 ID",
		"BknBackend.KnEvent.Class.Field.verified_by_name":  "核实人",
		"BknBackend.KnEvent.Class.Field.verified_at":       "核实时间",
		"BknBackend.KnEvent.Class.Field.merged_into":       "合并到",
		"BknBackend.KnEvent.Class.Field.detail":            "详情",
	},
}
