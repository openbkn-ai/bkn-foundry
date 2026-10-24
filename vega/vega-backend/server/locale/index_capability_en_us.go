// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var indexCapabilityEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.IndexCapability.InternalError.Unavailable": "Full-text analyzer capability is unavailable. Restore OpenSearch connectivity and restart Vega",
	},
}
