// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var errorcodeEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknProject.KnowledgeBase.SourceUnavailable":         "The knowledge base is temporarily unavailable. Please try again.",
		"BknProject.KnowledgeBase.InvalidSelection":          "Check the selected source range and filters.",
		"BknProject.KnowledgeBase.AuthenticationExpired":     "Your sign-in has expired. Please sign in again.",
		"BknProject.KnowledgeBase.PermissionDenied":          "You do not have permission to read this knowledge base.",
		"BknProject.KnowledgeBase.SourceMissing":             "The selected source is no longer available.",
		"BknProject.KnowledgeBase.SelectionConflict":         "The source selection has changed. Refresh before trying again.",
		"BknProject.KnowledgeBase.VersionGone":               "This version of the original is no longer kept. Select a retained version before continuing.",
		"BknProject.InvalidParameter.Limit":                  "Invalid Pagination Limit. Please check whether the parameter is correct.",
		"BknProject.InvalidParameter.Cursor":                 "Invalid Pagination Cursor. Please pass the next_cursor of the previous page unchanged.",
		"BknProject.Validation.Detail.AuthenticationFailed":  "Authentication failed.",
		"BknProject.Validation.Detail.LimitOutOfRange":       "limit must be an integer from 1 to {{.max}}.",
		"BknProject.Validation.Detail.DependencyUnavailable": "The database is not reachable.",
	},
}
