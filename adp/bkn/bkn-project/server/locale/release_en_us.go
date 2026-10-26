// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var releaseEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknProject.Release.InvalidVersion":      "Invalid Version. version must be X.Y.Z with digits only and no leading zeros, at most 32 characters.",
		"BknProject.Release.NotFound":            "Release Not Found. No release published this knowledge network.",
		"BknProject.Release.VersionExists":       "Version Already Exists. This version, or the network id it maps to, is already published, or its tag points at another commit; please choose another version.",
		"BknProject.Release.BranchBehind":        "Branch Behind main. main has changes the branch does not have; sync the branch first.",
		"BknProject.Release.Drift":               "Live Definitions Drifted. The live network differs from main; error_details lists the paths. Commit the drift to main, sync the branch, then publish again.",
		"BknProject.Release.CapabilitiesSkipped": "Capabilities Skipped. The network was imported, but some Skills or functions could not be bound; error_details lists them. The release is not recorded; fix the permissions and publish the same version again.",
		"BknProject.Release.InternalError":       "Internal Error. Please contact the administrator.",
	},
}
