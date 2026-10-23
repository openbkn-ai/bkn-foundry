// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var objectTypeEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"OntologyQuery.ObjectType.InvalidParameter":                           "Invalid Request Paramter. Please check whether the parameter is correct.",
		"OntologyQuery.ObjectType.InvalidParameter.DynamicParams":             "Invalid DynamicParams. Please check whether the parameter is correct.",
		"OntologyQuery.ObjectType.InvalidParameter.IgnoreLocalIndex":          "Invalid ignore_local_index. Please check whether the parameter is correct.",
		"OntologyQuery.ObjectType.InvalidParameter.IncludeTypeInfo":           "Invalid IncludeTypeInfo. Please check whether the parameter is correct.",
		"OntologyQuery.ObjectType.InvalidParameter.SmallModel":                "Invalid Small Model Parameter. Please check whether the parameter is correct.",
		"OntologyQuery.ObjectType.UnsupportLogicPropertyParameterSource":      "Unsupported Logic Property Parameter Source. Please check whether the parameter is correct.",
		"OntologyQuery.ObjectType.TooManyRequests":                            "Too Many Requests. A downstream service is at capacity. Retry later without changing the query.",
		"OntologyQuery.ObjectType.ObjectTypeNotFound":                         "The Object Type Does Not Exist. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"OntologyQuery.ObjectType.SmallModelNotFound":                         "The Small Model Does Not Exist. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"OntologyQuery.ObjectType.InternalError":                              "Internal Error. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"OntologyQuery.ObjectType.InternalError.ExecuteToolFailed":            "Execute Tool Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"OntologyQuery.ObjectType.InternalError.GetMetricDataByIDFailed":      "Get Metric Data By ID Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"OntologyQuery.ObjectType.InternalError.GetViewDataByIDFailed":        "Get View Data By ID Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"OntologyQuery.ObjectType.InternalError.GetObjectTypesByIDFailed":     "Get Object Types By ID Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"OntologyQuery.ObjectType.InternalError.GetSmallModelByIDFailed":      "Get Small Model By ID Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"OntologyQuery.ObjectType.InternalError.ProcessLogicPropertiesFailed": "Process Logic Properties Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
	},
}
