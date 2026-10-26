// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var errorcodeEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknProject.InvalidParameter.Limit":                  "Invalid Pagination Limit. Please check whether the parameter is correct.",
		"BknProject.InvalidParameter.Cursor":                 "Invalid Pagination Cursor. Please pass the next_cursor of the previous page unchanged.",
		"BknProject.Validation.Detail.AuthenticationFailed":  "Authentication failed.",
		"BknProject.Validation.Detail.LimitOutOfRange":       "limit must be an integer from 1 to {{.max}}.",
		"BknProject.Validation.Detail.DependencyUnavailable": "The database is not reachable.",
	},
}
