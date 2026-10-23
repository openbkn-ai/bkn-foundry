// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var auditTextEnUS = i18n.I18nMessages{Locale: i18n.AmericanEnglish, Messages: map[string]string{
	"VegaBackend.Audit.Action.create":                      "create",
	"VegaBackend.Audit.Action.update":                      "update",
	"VegaBackend.Audit.Action.delete":                      "delete",
	"VegaBackend.Audit.Action.import":                      "import",
	"VegaBackend.Audit.Action.add_members":                 "add members",
	"VegaBackend.Audit.Action.remove_members":              "remove members",
	"VegaBackend.Audit.Action.enable":                      "enable",
	"VegaBackend.Audit.Action.disable":                     "disable",
	"VegaBackend.Audit.Action.start":                       "start",
	"VegaBackend.Audit.Action.stop":                        "stop",
	"VegaBackend.Audit.Action.attach":                      "attach",
	"VegaBackend.Audit.Action.detach":                      "detach",
	"VegaBackend.Audit.Target.knowledge_network":           "knowledge network",
	"VegaBackend.Audit.Target.object_type":                 "object type",
	"VegaBackend.Audit.Target.relation_type":               "relation type",
	"VegaBackend.Audit.Target.action_type":                 "action type",
	"VegaBackend.Audit.Target.risk_type":                   "risk type",
	"VegaBackend.Audit.Target.metric":                      "metric",
	"VegaBackend.Audit.Target.concept_group":               "concept group",
	"VegaBackend.Audit.Target.action_schedule":             "action schedule",
	"VegaBackend.Audit.Target.kn_capability_binding":       "kn capability binding",
	"VegaBackend.Audit.Target.catalog":                     "catalog",
	"VegaBackend.Audit.Target.resource":                    "resource",
	"VegaBackend.Audit.Target.discovery_schedule":          "discovery schedule",
	"VegaBackend.Audit.Target.build_task":                  "build task",
	"VegaBackend.Audit.Target.connector_type":              "connector type",
	"VegaBackend.Audit.Target.discovery_task":              "discovery task",
	"VegaBackend.Audit.Target.semantic_understanding_task": "semantic understanding task",
	"VegaBackend.Audit.Summary":                            "{{.Action}} {{.Target}} \"{{.Name}}\"",
	"VegaBackend.Audit.NameSeparator":                      `{{", "}}`,
	"VegaBackend.Audit.Names":                              "{{.Names}}, {{.Count}} total",
	"VegaBackend.Audit.NamesTruncated":                     "{{.Names}} and more, {{.Count}} total (names truncated)",
}}
