// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var auditTextEnUS = i18n.I18nMessages{Locale: i18n.AmericanEnglish, Messages: map[string]string{
	"BknBackend.Audit.Action.create":                      "create",
	"BknBackend.Audit.Action.update":                      "update",
	"BknBackend.Audit.Action.delete":                      "delete",
	"BknBackend.Audit.Action.import":                      "import",
	"BknBackend.Audit.Action.add_members":                 "add members",
	"BknBackend.Audit.Action.remove_members":              "remove members",
	"BknBackend.Audit.Action.enable":                      "enable",
	"BknBackend.Audit.Action.disable":                     "disable",
	"BknBackend.Audit.Action.start":                       "start",
	"BknBackend.Audit.Action.stop":                        "stop",
	"BknBackend.Audit.Action.attach":                      "attach",
	"BknBackend.Audit.Action.detach":                      "detach",
	"BknBackend.Audit.Target.knowledge_network":           "knowledge network",
	"BknBackend.Audit.Target.object_type":                 "object type",
	"BknBackend.Audit.Target.relation_type":               "relation type",
	"BknBackend.Audit.Target.action_type":                 "action type",
	"BknBackend.Audit.Target.risk_type":                   "risk type",
	"BknBackend.Audit.Target.value_type":                  "value type",
	"BknBackend.Audit.Target.metric":                      "metric",
	"BknBackend.Audit.Target.concept_group":               "concept group",
	"BknBackend.Audit.Target.action_schedule":             "action schedule",
	"BknBackend.Audit.Target.kn_capability_binding":       "kn capability binding",
	"BknBackend.Audit.Target.catalog":                     "catalog",
	"BknBackend.Audit.Target.resource":                    "resource",
	"BknBackend.Audit.Target.discovery_schedule":          "discovery schedule",
	"BknBackend.Audit.Target.build_task":                  "build task",
	"BknBackend.Audit.Target.connector_type":              "connector type",
	"BknBackend.Audit.Target.discovery_task":              "discovery task",
	"BknBackend.Audit.Target.semantic_understanding_task": "semantic understanding task",
	"BknBackend.Audit.Summary":                            "{{.Action}} {{.Target}} \"{{.Name}}\"",
	"BknBackend.Audit.NameSeparator":                      `{{", "}}`,
	"BknBackend.Audit.Names":                              "{{.Names}}, {{.Count}} total",
	"BknBackend.Audit.NamesTruncated":                     "{{.Names}} and more, {{.Count}} total (names truncated)",
}}
