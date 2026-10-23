// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var knDiffEnUS = i18n.I18nMessages{
	Locale: "en-US",
	Messages: map[string]string{
		"BknBackend.KNDiff.InvalidParameter":                                 "Invalid knowledge network comparison request. Please check the request body.",
		"BknBackend.KNDiff.ObjectTypeNotFound":                               "The object type does not exist in that knowledge network branch. Please check kn_id, branch and ot_id.",
		"BknBackend.KNDiff.DataStatsUnavailable":                             "The object type has no data resource to count. Please bind the object type to a data resource before comparing its data.",
		"BknBackend.KNDiff.DataStatsQueryFailed":                             "Failed to count the data behind the object type. Please retry later or contact the administrator.",
		"BknBackend.KNDiff.InvalidParameter.Detail.BaseKNIDRequired":         "base.kn_id is required.",
		"BknBackend.KNDiff.InvalidParameter.Detail.TargetKNIDRequired":       "target.kn_id is required.",
		"BknBackend.KNDiff.InvalidParameter.Detail.BaseObjectTypeRequired":   "base.ot_id is required.",
		"BknBackend.KNDiff.InvalidParameter.Detail.TargetObjectTypeRequired": "target.ot_id is required.",
	},
}
