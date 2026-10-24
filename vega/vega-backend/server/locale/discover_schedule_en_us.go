// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var discoverScheduleEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.DiscoverSchedule.NotFound":                            "Discover schedule not found. Please check if the schedule ID exists",
		"VegaBackend.DiscoverSchedule.InvalidCronExpr":                     "Invalid cron expression. Please provide a valid 5-field cron expression with at least 1 hour between adjacent executions",
		"VegaBackend.DiscoverSchedule.InvalidStrategies":                   "Invalid discover strategy. Strategy must be one of [full_sync, create_only, cleanup_only]; empty means full_sync",
		"VegaBackend.DiscoverSchedule.InvalidTimeRange":                    "Invalid discover schedule time range. start_time and end_time must be greater than or equal to 0; end_time may be 0 for no end time, otherwise start_time must be less than or equal to end_time",
		"VegaBackend.DiscoverSchedule.IdMismatch":                          "Body id does not match path id. Either omit body.id or set it equal to the path id",
		"VegaBackend.DiscoverSchedule.CatalogMismatch":                     "Cannot change catalog_id of an existing schedule. catalog_id is read-only; create a new schedule under the target catalog instead",
		"VegaBackend.DiscoverSchedule.EnabledFieldNotAllowed":              "PUT request must not change the 'enabled' field. Use POST /discover-schedules/{id}/enable or /disable to change enabled state",
		"VegaBackend.DiscoverSchedule.InternalError.GetFailed":             "Failed to get discover schedule. Please contact the administrator",
		"VegaBackend.DiscoverSchedule.InternalError.CreateFailed":          "Failed to create discover schedule. Please contact the administrator",
		"VegaBackend.DiscoverSchedule.InternalError.UpdateFailed":          "Failed to update discover schedule. Please contact the administrator",
		"VegaBackend.DiscoverSchedule.InternalError.DeleteFailed":          "Failed to delete discover schedule. Please contact the administrator",
		"VegaBackend.DiscoverSchedule.InternalError.GetAccountNamesFailed": "Failed to get account names. Please contact the administrator",
		"VegaBackend.DiscoverSchedule.UpdateConflict":                      "The discover schedule was updated after it was read. Reload the discover schedule and retry the update",
	},
}
