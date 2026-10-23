// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var auditTextZhCN = i18n.I18nMessages{Locale: i18n.SimplifiedChinese, Messages: map[string]string{
	"VegaBackend.Audit.Action.create":                      "新建",
	"VegaBackend.Audit.Action.update":                      "修改",
	"VegaBackend.Audit.Action.delete":                      "删除",
	"VegaBackend.Audit.Action.import":                      "导入",
	"VegaBackend.Audit.Action.add_members":                 "添加成员",
	"VegaBackend.Audit.Action.remove_members":              "移除成员",
	"VegaBackend.Audit.Action.enable":                      "启用",
	"VegaBackend.Audit.Action.disable":                     "停用",
	"VegaBackend.Audit.Action.start":                       "启动",
	"VegaBackend.Audit.Action.stop":                        "停止",
	"VegaBackend.Audit.Action.attach":                      "绑定",
	"VegaBackend.Audit.Action.detach":                      "解绑",
	"VegaBackend.Audit.Target.knowledge_network":           "领域知识网络",
	"VegaBackend.Audit.Target.object_type":                 "对象类",
	"VegaBackend.Audit.Target.relation_type":               "关系类",
	"VegaBackend.Audit.Target.action_type":                 "行动类",
	"VegaBackend.Audit.Target.risk_type":                   "风险类",
	"VegaBackend.Audit.Target.metric":                      "指标",
	"VegaBackend.Audit.Target.concept_group":               "概念分组",
	"VegaBackend.Audit.Target.action_schedule":             "行动计划",
	"VegaBackend.Audit.Target.kn_capability_binding":       "知识网络能力绑定",
	"VegaBackend.Audit.Target.catalog":                     "数据目录",
	"VegaBackend.Audit.Target.resource":                    "数据资源",
	"VegaBackend.Audit.Target.discovery_schedule":          "发现计划",
	"VegaBackend.Audit.Target.build_task":                  "构建任务",
	"VegaBackend.Audit.Target.connector_type":              "连接器",
	"VegaBackend.Audit.Target.discovery_task":              "发现任务",
	"VegaBackend.Audit.Target.semantic_understanding_task": "语义理解任务",
	"VegaBackend.Audit.Summary":                            "{{.Action}}{{.Target}}“{{.Name}}”",
	"VegaBackend.Audit.NameSeparator":                      "、",
	"VegaBackend.Audit.Names":                              "{{.Names}}，共{{.Count}}个",
	"VegaBackend.Audit.NamesTruncated":                     "{{.Names}}等，共{{.Count}}个（名称摘要已截断）",
}}
