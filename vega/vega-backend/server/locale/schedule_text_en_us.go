// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var scheduleTextEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.Schedule.Result.InvalidCreator":                 "The schedule execution identity is invalid; recreate the schedule.",
		"VegaBackend.Schedule.Result.BuildResourceMissing":           "The resource does not exist or no longer belongs to the scheduled catalog.",
		"VegaBackend.Schedule.Result.BuildTargetDisabled":            "The resource or catalog is disabled.",
		"VegaBackend.Schedule.Result.BuildCreateFailed":              "Could not create a build task; check the resource configuration or contact an administrator.",
		"VegaBackend.Schedule.Result.BuildTaskActive":                "The resource already has a pending, running, or stopping build task.",
		"VegaBackend.Schedule.Result.CatalogDisabled":                "The catalog is disabled.",
		"VegaBackend.Schedule.Result.BuildPrimaryKeyInvalid":         "The resource primary key configuration is invalid; a build task cannot be created.",
		"VegaBackend.Schedule.Result.BuildIncrementalFieldsInvalid":  "The resource incremental field configuration is invalid; a build task cannot be created.",
		"VegaBackend.Schedule.Result.BuildBaselineUnavailable":       "Incremental builds require an available index and a valid baseline.",
		"VegaBackend.Schedule.Result.BuildPermissionDenied":          "The schedule creator lacks permission to build the current resource or catalog.",
		"VegaBackend.Schedule.Result.DiscoverServiceUnavailable":     "The discovery task service is unavailable.",
		"VegaBackend.Schedule.Result.DiscoverCatalogReadFailed":      "Could not read or validate the discovery catalog.",
		"VegaBackend.Schedule.Result.DiscoverCatalogUnavailable":     "The discovery catalog does not exist or is disabled.",
		"VegaBackend.Schedule.Result.DiscoverActiveTasksCheckFailed": "Could not check for running discovery tasks.",
		"VegaBackend.Schedule.Result.DiscoverTaskActive":             "The catalog already has a running scheduled discovery task.",
		"VegaBackend.Schedule.Result.DiscoverCreateFailed":           "Could not create a discovery task; check the catalog configuration and permissions.",
		"VegaBackend.Schedule.Result.DiscoverPermissionDenied":       "The schedule creator lacks catalog task management permission; a discovery task cannot be created.",
		"VegaBackend.Schedule.Result.BuildCallFailed":                "The build schedule call failed; contact an administrator.",
		"VegaBackend.Schedule.Result.BuildCronInvalid":               "Invalid schedule expression; correct it and enable the schedule again.",
		"VegaBackend.Schedule.Result.BuildCronNoFuture":              "The schedule expression has no future trigger time.",
		"VegaBackend.Schedule.Result.BuildCreateUnavailable":         "Could not create a build task; contact an administrator.",
		"VegaBackend.Schedule.Result.CronInvalid":                    "The schedule expression is invalid or runs more than once per hour; correct it before enabling.",
		"VegaBackend.Schedule.Result.CronNoFuture":                   "The schedule expression has no valid future trigger time; correct it before enabling.",
		"VegaBackend.Schedule.Result.HealthCheckFailed":              "Could not complete the connection health check; check the catalog configuration.",
		"VegaBackend.Schedule.Result.HealthCatalogDisabled":          "The catalog is disabled; this health check was skipped.",
		"VegaBackend.Schedule.Result.HealthCatalogMissing":           "The catalog does not exist; this health check was skipped.",
	},
}
