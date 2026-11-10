// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knVersionEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknBackend.KNVersion.NotVersioned":          "Versioning is not enabled for this knowledge network.",
		"BknBackend.KNVersion.InvalidVersion":        "Invalid knowledge network version. Use draft, running or a version number.",
		"BknBackend.KNVersion.VersionNotFound":       "The knowledge network version does not exist.",
		"BknBackend.KNVersion.VersionPurged":         "The knowledge network version has been purged and its content is no longer available. Retry the query.",
		"BknBackend.KNVersion.EditionRestricted":     "This version requires the Professional edition. The Community edition keeps only the running and the previous version.",
		"BknBackend.KNVersion.VersioningDisabling":   "Versioning is being disabled for this knowledge network. Reads of the draft or older versions are paused.",
		"BknBackend.KNVersion.DraftRevisionMismatch": "The draft has changed. Reload the draft and retry.",
		"OntologyQuery.KNVersion.InvalidParameter":   "Invalid kn_version or draft_revision. Use kn_version=draft, running or a version number; draft_revision goes only with kn_version=draft.",
		"OntologyQuery.KNVersion.Changed":            "The knowledge network version changed while the query was running. Retry the query.",
	},
}
