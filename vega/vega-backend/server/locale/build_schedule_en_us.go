// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var buildScheduleEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.BuildSchedule.InvalidCronExpr":            "Invalid schedule cron expression; executions must be at least one hour apart.",
		"VegaBackend.BuildSchedule.InvalidExecuteType":         "Execute type must be full or incremental.",
		"VegaBackend.BuildSchedule.InvalidTimeRange":           "Invalid schedule validity window.",
		"VegaBackend.BuildSchedule.InvalidParameter":           "Invalid build schedule configuration.",
		"VegaBackend.BuildSchedule.NotFound":                   "Build schedule not found.",
		"VegaBackend.BuildSchedule.ResourceMismatch":           "The bound resource cannot be changed.",
		"VegaBackend.BuildSchedule.EnabledFieldNotAllowed":     "Use enable or disable to change schedule state.",
		"VegaBackend.BuildSchedule.UpdateConflict":             "The schedule has changed; reload and retry.",
		"VegaBackend.BuildSchedule.InternalError.GetFailed":    "Failed to retrieve build schedules.",
		"VegaBackend.BuildSchedule.InternalError.CreateFailed": "Failed to create build schedule.",
		"VegaBackend.BuildSchedule.InternalError.UpdateFailed": "Failed to update build schedule.",
		"VegaBackend.BuildSchedule.InternalError.DeleteFailed": "Failed to delete build schedule.",
	},
}
