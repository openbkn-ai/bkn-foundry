// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var actionTypeEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"OntologyQuery.ActionType.InvalidParameter":                       "Invalid Request Paramter. Please check whether the parameter is correct.",
		"OntologyQuery.ActionType.InvalidParameter.DynamicParams":         "Invalid DynamicParams. Please check whether the parameter is correct.",
		"OntologyQuery.ActionType.InvalidParameter.IgnoreLocalIndex":      "Invalid ignore_local_index. Please check whether the parameter is correct.",
		"OntologyQuery.ActionType.InvalidParameter.IncludeTypeInfo":       "Invalid IncludeTypeInfo. Please check whether the parameter is correct.",
		"OntologyQuery.ActionType.ActionTypeNotFound":                     "The Object Type Does Not Exist. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"OntologyQuery.ActionType.InternalError":                          "Internal Error. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"OntologyQuery.ActionType.InternalError.GetActionTypesByIDFailed": "Get Object Types By ID Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
	},
}
