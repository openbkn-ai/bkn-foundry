// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var riskTypeEnUS = i18n.I18nMessages{
	Locale: "en-US",
	Messages: map[string]string{
		"OntologyQuery.RiskType.RiskLevelExceedsMaxAcceptable":                 "Risk level exceeds the maximum acceptable level for this risk type. Adjust inputs or risk configuration, or use an action with a lower assessed risk.",
		"OntologyQuery.RiskType.RiskTypeNotFound":                              "Referenced risk type does not exist. Check that risk_type_id in the action type configuration matches an existing risk type.",
		"OntologyQuery.RiskType.InternalError.GetRiskTypesByIDsFailed":         "Failed to load risk types. Please try again. If the error persists, contact technical support.",
		"OntologyQuery.RiskType.InternalError.ExecuteRiskAssessmentToolFailed": "Risk assessment tool execution failed. Please check the risk function tool configuration and try again.",
	},
}
