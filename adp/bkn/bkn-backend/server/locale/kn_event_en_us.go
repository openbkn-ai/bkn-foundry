// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knEventEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknBackend.KnEvent.InvalidParameter":  "The event request is invalid. Correct it using the error details and retry.",
		"BknBackend.KnEvent.EventTypeNotFound": "Event type not found. Verify the knowledge network and the event type code.",
		"BknBackend.KnEvent.EventNotFound":     "Event not found. Verify the knowledge network and the event ID.",
		"BknBackend.KnEvent.EventTypeExisted":  "The event type code already exists. Use a code that is unique within the knowledge network.",
		"BknBackend.KnEvent.EventTypeInUse":    "The event type has events and cannot be deleted.",
		"BknBackend.KnEvent.EventClassInUse":   "The event type's event class is used by other definitions and cannot be deleted. Remove the uses listed in the error details first.",
		"BknBackend.KnEvent.SourceMismatch":    "The event type's source does not support this operation: reports are for reported types and rule checks are for rule types.",
		"BknBackend.KnEvent.ActionNotAllowed":  "The event's current status does not allow this action. Refresh and retry with an available action.",
		"BknBackend.KnEvent.TooLarge":          "The request is too large. Reduce the rule, the detail or the number of hits and retry.",
		"BknBackend.KnEvent.InternalError":     "Event service error. Retry later or contact an administrator if the problem persists.",
		"BknBackend.KnEvent.RuleEventTitle":    "{{.type}}: {{.object}}",

		"BknBackend.KnEvent.RuleToolUnavailable":    "The rule tool is unavailable: it does not exist, is disabled, or its tool box is not published.",
		"BknBackend.KnEvent.RuleToolNotCypherTable": "The rule tool must be a Cypher function of this knowledge network that returns a table.",
		"BknBackend.KnEvent.RuleChecking":           "The event type is being checked, or its rule changed during the check. Retry later.",
		"BknBackend.KnEvent.CheckedByPlatform":      "The event type has a rule tool and is checked by the platform on its schedule. It does not accept submitted checks.",

		// What a platform check records: in the network's one business language.
		"BknBackend.KnEvent.Check.NoPermission":    "The check subject has no permission. Ask someone with permission to save the rule again.",
		"BknBackend.KnEvent.Check.ToolUnavailable": "The rule tool is unavailable.",
		"BknBackend.KnEvent.Check.Failed":          "The rule tool failed: {{.error}}",
		"BknBackend.KnEvent.Check.NoKeyColumn":     "The result has no key column.",
		"BknBackend.KnEvent.Check.BadKey":          "The key of row {{.row}} is not a string or a number.",
		"BknBackend.KnEvent.Check.BadResult":       "The result cannot be used: {{.detail}}",
		"BknBackend.KnEvent.Check.Limited":         "The result was truncated: only the objects returned were updated, and the other objects' events do not recover. Narrow the statement's conditions or raise its LIMIT.",

		// What the event classes show: written into the network's definitions in one business language.
		"BknBackend.KnEvent.Class.Comment":                 "Event class: each object is one \"{{.name}}\" event, written by the platform's event module.",
		"BknBackend.KnEvent.Class.Relation":                "About",
		"BknBackend.KnEvent.Class.RelationComment":         "The object a \"{{.name}}\" event is about.",
		"BknBackend.KnEvent.Class.Field.event_id":          "Event ID",
		"BknBackend.KnEvent.Class.Field.event_type":        "Event type",
		"BknBackend.KnEvent.Class.Field.title":             "Title",
		"BknBackend.KnEvent.Class.Field.status":            "Status",
		"BknBackend.KnEvent.Class.Field.severity":          "Severity",
		"BknBackend.KnEvent.Class.Field.source":            "Source",
		"BknBackend.KnEvent.Class.Field.object_key":        "Object key",
		"BknBackend.KnEvent.Class.Field.object_name":       "Object name",
		"BknBackend.KnEvent.Class.Field.occurred_at":       "Occurred at",
		"BknBackend.KnEvent.Class.Field.ended_at":          "Ended at",
		"BknBackend.KnEvent.Class.Field.recorded_at":       "Recorded at",
		"BknBackend.KnEvent.Class.Field.first_detected_at": "First detected at",
		"BknBackend.KnEvent.Class.Field.backlog":           "Backlog",
		"BknBackend.KnEvent.Class.Field.reported_by":       "Reporter ID",
		"BknBackend.KnEvent.Class.Field.reported_by_name":  "Reporter",
		"BknBackend.KnEvent.Class.Field.verified_by":       "Verifier ID",
		"BknBackend.KnEvent.Class.Field.verified_by_name":  "Verifier",
		"BknBackend.KnEvent.Class.Field.verified_at":       "Verified at",
		"BknBackend.KnEvent.Class.Field.merged_into":       "Merged into",
		"BknBackend.KnEvent.Class.Field.detail":            "Detail",
	},
}
