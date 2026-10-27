// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var draftEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknProject.Draft.NoCatalogs":    "No Data Catalogs. Select the data catalogs that hold the project's structured data before drafting the ontology.",
		"BknProject.Draft.NotFound":      "Draft Not Found. The project has no ontology draft yet.",
		"BknProject.Draft.Drafting":      "Drafting. The project's ontology is being drafted; wait for it to finish before drafting again.",
		"BknProject.Draft.InternalError": "Internal Error. Please contact the administrator.",
	},
}
