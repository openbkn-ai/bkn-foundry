// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var buildTaskEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.BuildTask.NotFound":                                 "Build task not found. Please check if the build task ID exists",
		"VegaBackend.BuildTask.InvalidParameter.ResourceID":              "Invalid resource ID. Please check if the resource ID is provided and exists",
		"VegaBackend.BuildTask.InvalidParameter.Mode":                    "Invalid build task mode. Mode must be one of: streaming, batch",
		"VegaBackend.BuildTask.InvalidParameter.PrimaryKeyFields":        "Invalid primary key fields. Configure at least one unique primary_key_fields field that exists in the resource schema and has a supported type",
		"VegaBackend.BuildTask.InvalidParameter.IncrementalFields":       "Invalid incremental fields. Configure at least one unique incremental_fields field that exists in the resource schema and has a supported type",
		"VegaBackend.BuildTask.StreamingUnsupported":                     "Streaming build tasks are temporarily unsupported. Create a batch build task instead",
		"VegaBackend.BuildTask.InvalidParameter.UnsupportedSchemaFields": "Resource contains field types unsupported for index builds. Resolve fields with type other before creating an index build task",
		"VegaBackend.BuildTask.InvalidParameter.Analyzer":                "Unavailable full-text analyzer. Choose an analyzer installed in OpenSearch, or update the affected field to use the resource default",
		"VegaBackend.BuildTask.InvalidParameter.EmbeddingModel":          "Missing embedding model for vector field. Set config.embedding_model on the vector feature or index_config.default_embedding_model on the resource",
		"VegaBackend.BuildTask.InvalidStateTransition":                   "Invalid build task state transition. Check current task status; start requires status in {stopped, failed}; stop requires status in {pending, running}",
		"VegaBackend.BuildTask.HasRunningExecution":                      "Active build task cannot be deleted. Stop pending or running tasks and wait for stopping tasks to stop before deleting them",
		"VegaBackend.BuildTask.IncrementalBaselineUnavailable":           "Resource has no usable incremental synchronization baseline. Complete a full batch build first to establish an available index and synchronization checkpoint",
		"VegaBackend.BuildTask.IncrementalResetUnsupported":              "Incremental build tasks do not support reset. Use reset=false to continue from the Resource checkpoint, or create a full batch task to rebuild from the beginning",
		"VegaBackend.BuildTask.IndexConfigChanged":                       "Resource index build configuration has changed. Create a new build task from the Resource's current configuration",
		"VegaBackend.BuildTask.InternalError.CreateFailed":               "Failed to create build task. Please contact the administrator",
		"VegaBackend.BuildTask.InternalError.ValidateAnalyzerFailed":     "Failed to validate full-text analyzer. Check the OpenSearch connection and try again",
		"VegaBackend.BuildTask.InternalError.GetFailed":                  "Failed to get build task. Please contact the administrator",
		"VegaBackend.BuildTask.Exist":                                    "Build task already exists. Please check if the resource already has a build task",
		"VegaBackend.BuildTask.Running":                                  "Build task is running. Please wait for the task to complete or stop before operating",
		"VegaBackend.BuildTask.InvalidStatus":                            "Invalid build task status. Please check the status value",
		"VegaBackend.BuildTask.InvalidExecuteType":                       "Invalid build task execute type. Please check the execute type value",
		"VegaBackend.BuildTask.InternalError.UpdateFailed":               "Failed to update build task. Please contact the administrator",
		"VegaBackend.BuildTask.InternalError.DeleteFailed":               "Failed to delete build task. Please contact the administrator",
		"VegaBackend.BuildTask.InternalError.GetAccountNamesFailed":      "Failed to get account names. Please contact the administrator",
	},
}
