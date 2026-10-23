// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var riskTypeEnUS = i18n.I18nMessages{
	Locale: "en-US",
	Messages: map[string]string{
		"BknBackend.RiskType.Duplicated.IDInFile":                               "Same Risk Type ID Existed. Please check whether the parameter is correct.",
		"BknBackend.RiskType.Duplicated.Name":                                   "Same Risk Type Name Existed. Please check whether the parameter is correct.",
		"BknBackend.RiskType.InvalidParameter":                                  "Invalid Request Parameter. Please check whether the parameter is correct.",
		"BknBackend.RiskType.RiskTypeIDExisted":                                 "Same Risk Type ID Existed. Please check whether the parameter is correct.",
		"BknBackend.RiskType.RiskTypeNameExisted":                               "Same Risk Type Name Existed. Please check whether the parameter is correct.",
		"BknBackend.RiskType.RiskFunctionToolNotFound":                          "Risk assessment tool not found or unreachable. Verify box_id and tool_id are registered with agent-operator-integration and reachable.",
		"BknBackend.RiskType.InvalidMaxAcceptableLevel":                         "Invalid Max Acceptable Level. Please use one of: safe, low, medium, high, critical.",
		"BknBackend.RiskType.InternalError":                                     "Internal Error. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.RiskType.InternalError.CheckRiskTypeIfExistFailed":          "Check Risk Type Exist Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.RiskType.InternalError.GetRiskTypesByIDsFailed":             "Get Risk Type By IDs Failed. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.RiskType.RiskTypeNotFound":                                  "The Risk Type Does Not Exist. Please try again. If the error occurs again, please submit the work order or contact technical support.",
		"BknBackend.RiskType.NullParameter.Name":                                "Risk Type Name Is Empty. Please check whether the parameter is correct.",
		"BknBackend.RiskType.LengthExceeded.Name":                               "The Length of Risk Type Name Out of Limit. Please check whether the parameter is correct.",
		"BknBackend.RiskType.InvalidParameter.Detail.ModuleType":                "Risk type module type must be 'risk_type'.",
		"BknBackend.RiskType.InvalidParameter.Detail.DuplicatedIDInFile":        "Risk type ID '{{.riskTypeID}}' already exists in the request body.",
		"BknBackend.RiskType.InvalidParameter.Detail.DuplicatedNameInFile":      "Risk type name '{{.riskTypeName}}' already exists in the request body.",
		"BknBackend.RiskType.InvalidParameter.Detail.RiskTypeIDAlreadyExists":   "Risk type ID {{.riskTypeID}} already exists.",
		"BknBackend.RiskType.InvalidParameter.Detail.RiskTypeNameAlreadyExists": "Risk type name {{.riskTypeName}} already exists.",
	},
}
