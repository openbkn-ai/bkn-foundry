// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var auditTextZhCN = i18n.I18nMessages{Locale: i18n.SimplifiedChinese, Messages: map[string]string{
	"BknBackend.Audit.Action.create":                      "新建",
	"BknBackend.Audit.Action.update":                      "修改",
	"BknBackend.Audit.Action.delete":                      "删除",
	"BknBackend.Audit.Action.import":                      "导入",
	"BknBackend.Audit.Action.add_members":                 "添加成员",
	"BknBackend.Audit.Action.remove_members":              "移除成员",
	"BknBackend.Audit.Action.enable":                      "启用",
	"BknBackend.Audit.Action.disable":                     "停用",
	"BknBackend.Audit.Action.start":                       "启动",
	"BknBackend.Audit.Action.stop":                        "停止",
	"BknBackend.Audit.Action.attach":                      "绑定",
	"BknBackend.Audit.Action.detach":                      "解绑",
	"BknBackend.Audit.Target.knowledge_network":           "领域知识网络",
	"BknBackend.Audit.Target.object_type":                 "对象类",
	"BknBackend.Audit.Target.relation_type":               "关系类",
	"BknBackend.Audit.Target.action_type":                 "行动类",
	"BknBackend.Audit.Target.risk_type":                   "风险类",
	"BknBackend.Audit.Target.value_type":                  "值类型",
	"BknBackend.Audit.Target.metric":                      "指标",
	"BknBackend.Audit.Target.concept_group":               "概念分组",
	"BknBackend.Audit.Target.action_schedule":             "行动计划",
	"BknBackend.Audit.Target.kn_capability_binding":       "知识网络能力绑定",
	"BknBackend.Audit.Target.catalog":                     "数据目录",
	"BknBackend.Audit.Target.resource":                    "数据资源",
	"BknBackend.Audit.Target.discovery_schedule":          "发现计划",
	"BknBackend.Audit.Target.build_task":                  "构建任务",
	"BknBackend.Audit.Target.connector_type":              "连接器",
	"BknBackend.Audit.Target.discovery_task":              "发现任务",
	"BknBackend.Audit.Target.semantic_understanding_task": "语义理解任务",
	"BknBackend.Audit.Summary":                            "{{.Action}}{{.Target}}“{{.Name}}”",
	"BknBackend.Audit.NameSeparator":                      "、",
	"BknBackend.Audit.Names":                              "{{.Names}}，共{{.Count}}个",
	"BknBackend.Audit.NamesTruncated":                     "{{.Names}}等，共{{.Count}}个（名称摘要已截断）",
}}
