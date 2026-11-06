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
		"BknBackend.KnEvent.SourceMismatch":    "The event type's source does not support this operation: reports are for reported types and rule checks are for rule types.",
		"BknBackend.KnEvent.ActionNotAllowed":  "The event's current status does not allow this action. Refresh and retry with an available action.",
		"BknBackend.KnEvent.TooLarge":          "The request is too large. Reduce the rule, the detail or the number of hits and retry.",
		"BknBackend.KnEvent.InternalError":     "Event service error. Retry later or contact an administrator if the problem persists.",
		"BknBackend.KnEvent.RuleEventTitle":    "{{.type}}: {{.object}}",
	},
}
