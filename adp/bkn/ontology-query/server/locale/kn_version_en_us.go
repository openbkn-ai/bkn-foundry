// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knVersionEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"OntologyQuery.KNVersion.InvalidParameter": "Invalid kn_version or draft_revision. Use kn_version=draft, running or a version number; draft_revision goes only with kn_version=draft.",
		"OntologyQuery.KNVersion.Changed":          "The knowledge network version changed while the query was running. Retry the query.",
	},
}
