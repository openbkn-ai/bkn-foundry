// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var catalogHealthCheckScheduleEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.CatalogHealthCheckSchedule.InvalidParameter":           "Invalid catalog health check schedule parameters. Check the schedule mode and cron configuration, and ensure the target catalog is physical",
		"VegaBackend.CatalogHealthCheckSchedule.NotFound":                   "Catalog health check schedule not found. Check whether the catalog ID and its health check schedule exist",
		"VegaBackend.CatalogHealthCheckSchedule.UpdateConflict":             "The catalog health check schedule was updated after it was read. Reload the catalog health check schedule and retry the update",
		"VegaBackend.CatalogHealthCheckSchedule.InternalError":              "Catalog health check schedule internal error. Please contact the administrator",
		"VegaBackend.CatalogHealthCheckSchedule.InternalError.GetFailed":    "Failed to get the catalog health check schedule. Please contact the administrator",
		"VegaBackend.CatalogHealthCheckSchedule.InternalError.UpdateFailed": "Failed to update the catalog health check schedule. Please contact the administrator",
	},
}
