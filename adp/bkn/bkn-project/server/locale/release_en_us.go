// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var releaseEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknProject.Release.NotFound":      "Release Not Found. No release published this knowledge network.",
		"BknProject.Release.InternalError": "Internal Error. Please contact the administrator.",
	},
}
