// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package locale holds the messages of the version refusal codes in comm-go/knversion,
// which registers them.
package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

// EnUS are the en-US messages.
var EnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknBackend.KNVersion.NotVersioned":          "Versioning is not enabled for this knowledge network.",
		"BknBackend.KNVersion.InvalidVersion":        "Invalid knowledge network version. Use draft, running or a version number; a versioned network accepts only the main or draft branch.",
		"BknBackend.KNVersion.VersionNotFound":       "The knowledge network version does not exist.",
		"BknBackend.KNVersion.VersionPurged":         "The knowledge network version has been purged and its content is no longer available.",
		"BknBackend.KNVersion.EditionRestricted":     "This version operation requires the Professional edition. The Community edition keeps only the running and the previous version.",
		"BknBackend.KNVersion.VersioningDisabling":   "Versioning is being disabled for this knowledge network. Writes, version operations and reads of the draft or older versions are paused.",
		"BknBackend.KNVersion.DraftRevisionMismatch": "The draft has changed. Reload the draft and retry.",
	},
}
