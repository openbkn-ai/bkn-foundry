// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var discoverTaskEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"VegaBackend.DiscoverTask.NotFound":                            "Discover task not found. Please check if the discover task ID exists",
		"VegaBackend.DiscoverTask.InvalidStatus":                       "Invalid discover task status. Status must be one of: pending, running, completed, failed, cancelled",
		"VegaBackend.DiscoverTask.HasRunningExecution":                 "Discover task is pending or running and cannot be deleted. Wait for the task to complete or fail before deleting",
		"VegaBackend.DiscoverTask.ResourceRefreshInProgress":           "A metadata refresh for this resource is already pending or running. Wait for the existing refresh to finish before retrying",
		"VegaBackend.DiscoverTask.InternalError.GetFailed":             "Failed to get discover task. Please contact the administrator",
		"VegaBackend.DiscoverTask.InternalError.DeleteFailed":          "Failed to delete discover task. Please contact the administrator",
		"VegaBackend.DiscoverTask.InternalError.GetAccountNamesFailed": "Failed to get account names. Please contact the administrator",
	},
}
