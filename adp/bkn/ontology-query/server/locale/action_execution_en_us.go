// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var actionExecutionEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"OntologyQuery.ActionExecution.InvalidParameter":      "Invalid request parameter. Please check if the parameters are correct.",
		"OntologyQuery.ActionExecution.ActionTypeNotFound":    "Action type not found. Please check if the action type ID is correct.",
		"OntologyQuery.ActionExecution.ExecutionNotFound":     "Execution record not found. Please check if the execution ID is correct.",
		"OntologyQuery.ActionExecution.DuplicateExecution":    "Duplicate execution detected. An in-flight execution already exists for the same action type, instance set, and dynamic_params within the duplicate window. Please wait for it to complete or query the existing execution.",
		"OntologyQuery.ActionExecution.GetActionTypeFailed":   "Failed to get action type. Please try again. If the error occurs again, please submit a ticket or contact technical support.",
		"OntologyQuery.ActionExecution.CreateExecutionFailed": "Failed to create execution record. Please try again. If the error occurs again, please submit a ticket or contact technical support.",
		"OntologyQuery.ActionExecution.ExecuteToolFailed":     "Tool execution failed. Please check the tool configuration and try again.",
		"OntologyQuery.ActionExecution.ExecuteMCPFailed":      "MCP execution failed. Please check the MCP configuration and try again.",
		"OntologyQuery.ActionExecution.QueryExecutionsFailed": "Failed to query execution logs. Please try again. If the error occurs again, please submit a ticket or contact technical support.",
		"OntologyQuery.ActionExecution.CancelExecutionFailed": "Failed to cancel execution. Please try again. If the error occurs again, please submit a ticket or contact technical support.",
	},
}
