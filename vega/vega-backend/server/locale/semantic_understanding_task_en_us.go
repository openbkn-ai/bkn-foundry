// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var semanticUnderstandingTaskEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.SemanticUnderstandingTask.NotFound":                            "Semantic understanding task not found. Please check if the semantic understanding task ID exists",
		"VegaBackend.SemanticUnderstandingTask.HasRunningExecution":                 "Semantic understanding task is pending or running and cannot be deleted. Wait for the task to complete or fail before deleting",
		"VegaBackend.SemanticUnderstandingTask.InternalError.GetFailed":             "Failed to get semantic understanding tasks. Please contact the administrator",
		"VegaBackend.SemanticUnderstandingTask.InternalError.DeleteFailed":          "Failed to delete semantic understanding task. Please contact the administrator",
		"VegaBackend.SemanticUnderstandingTask.InternalError.GetAccountNamesFailed": "Failed to get semantic understanding task creator names. Please contact the administrator",
	},
}
