// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var releaseEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknProject.Release.UnfinishedAttempt":   "Earlier Publish Unfinished. An earlier attempt to publish this version stopped before it was recorded, and the branch has changed since; publish it with a new version number, see error_details.",
		"BknProject.Rollback.PrecheckRequired":   "Rollback Not Prechecked. Read the rollback plan first and send its signature; a rollback is never made without one.",
		"BknProject.Rollback.PlanOutdated":       "Rollback Plan Outdated. Consumers were switched, or the schedules changed, after you read the plan; read it again before rolling back.",
		"BknProject.Rollback.NoPrevious":         "Nothing to Roll Back To. Consumers use the network the lineage started from; there is no earlier version.",
		"BknProject.Rollback.NotProjectRelease":  "Not This Project's Release. Consumers use a version another project published; only that project can roll it back.",
		"BknProject.Release.InvalidVersion":      "Invalid Version. version must be X.Y.Z with digits only and no leading zeros, at most 32 characters.",
		"BknProject.Release.InvalidProjectID":    "Invalid Project ID. project_id must be at most 40 characters.",
		"BknProject.Release.InvalidNotes":        "Invalid Notes. notes must be at most 65535 bytes.",
		"BknProject.Release.NotFound":            "Release Not Found. No release published this knowledge network.",
		"BknProject.Release.NetworkExists":       "Network Already Exists. A knowledge network with the id this version maps to already exists and was not created by a release of this lineage; it is not overwritten. Please choose another version or remove that network.",
		"BknProject.Release.VersionExists":       "Version Already Exists. This version, or the network id it maps to, is already published, or its tag points at another commit; please choose another version.",
		"BknProject.Release.BranchBehind":        "Branch Behind main. main has changes the branch does not have; sync the branch first.",
		"BknProject.Release.Drift":               "Live Definitions Drifted. The live network differs from main; error_details lists the paths. Commit the drift to main, sync the branch, then publish again.",
		"BknProject.Release.CapabilitiesSkipped": "Capabilities Skipped. The network was imported, but some Skills or functions could not be bound; error_details lists them. The release is not recorded; fix the permissions and publish the same version again.",
		"BknProject.Release.InvalidRequestBody":  "Invalid Request Body. The body must be valid JSON.",
		"BknProject.Release.InvalidAccepts":      "Invalid Accepts. Only similar names, partly unverified checks and indexes not ready can be accepted.",
		"BknProject.Release.NotProjectRelease":   "Not a Release of This Project. Only a version this project published can be switched to.",
		"BknProject.Release.NotOwner":            "Not the Project Owner. Only the project owner can publish, sync or switch consumers.",
		"BknProject.Release.ProjectArchived":     "Project Archived. Unarchive the project before publishing.",
		"BknProject.Release.ChecksFailed":        "Release Checks Not Passed. error_details lists the checks that did not pass; fix them and open the release review again.",
		"BknProject.Release.ReviewOutdated":      "Release Review Outdated. The draft or the live version changed after the release review was opened; open it again.",
		"BknProject.Release.RegressionNotFound":  "Regression Not Found. This release has not been checked against its scenario questions yet.",
		"BknProject.Release.RegressionRunning":   "Regression Running. Wait for the current run to finish before running it again.",
		"BknProject.Release.InternalError":       "Internal Error. Please contact the administrator.",
	},
}
