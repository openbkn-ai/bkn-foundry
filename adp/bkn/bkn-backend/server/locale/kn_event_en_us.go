// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knEventEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknBackend.KnEvent.InvalidParameter":    "The event request is invalid. Correct it using the error details and retry.",
		"BknBackend.KnEvent.EventTypeNotFound":   "Event class not found. Verify that the version the knowledge network runs has the event class.",
		"BknBackend.KnEvent.EventNotFound":       "Event not found. Verify the knowledge network and the event ID.",
		"BknBackend.KnEvent.EventTypeInUse":      "The event class has events and cannot be deleted.",
		"BknBackend.KnEvent.RecognizedByAction":  "A record-event action type records this event class: the class cannot be deleted, nor its subject changed. Delete or change the action type first.",
		"BknBackend.KnEvent.RecognitionMismatch": "Rule checks of this event class can only come from its record-event action type in the running version. Check which action type records the class.",
		"BknBackend.KnEvent.SourceMismatch":      "The event class's source does not support this operation: reports are for classes whose source is report, and rule checks for classes whose source is rule.",
		"BknBackend.KnEvent.ActionNotAllowed":    "The event's current status does not allow this action. Refresh and retry with an available action.",
		"BknBackend.KnEvent.TooLarge":            "The request is too large. Reduce the detail or the number of hits and retry.",
		"BknBackend.KnEvent.InternalError":       "Event service error. Retry later or contact an administrator if the problem persists.",
		"BknBackend.KnEvent.RuleEventTitle":      "{{.type}}: {{.object}}",

		// What the event classes show: written into the network's definitions in one business language.
		"BknBackend.KnEvent.Class.Relation":                "About",
		"BknBackend.KnEvent.Class.RelationComment":         "The object a \"{{.name}}\" event is about.",
		"BknBackend.KnEvent.Class.Field.event_id":          "Event ID",
		"BknBackend.KnEvent.Class.Field.event_type":        "Event class",
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
