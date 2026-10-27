// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var lineageEnUS = i18n.I18nMessages{
	Locale: i18n.AmericanEnglish,
	Messages: map[string]string{
		"BknProject.Lineage.InvalidRequestBody":      "Invalid Request Body. The body must be a JSON object with the documented fields.",
		"BknProject.Lineage.InvalidKNID":             "Invalid Knowledge Network ID. kn_id is required.",
		"BknProject.Lineage.InvalidBranchName":       "Invalid Branch Name. Use a git branch name such as project/<id>; main, names with spaces and names with \"..\" are not allowed.",
		"BknProject.Lineage.InvalidDefinitions":      "Invalid Definitions. The request body must be a non-empty BKN tar in the format bkn-backend exports.",
		"BknProject.Lineage.NotLiveNetwork":          "Not the lineage's live network. A drift check only runs against the network of the lineage's latest release, or the network it was started from before any release; see error_details.",
		"BknProject.Lineage.InvalidSwitchTarget":     "Invalid switch target. Consumers can only switch to a network a release of the lineage created, or the network the lineage was started from.",
		"BknProject.Lineage.AlreadyInUse":            "Network already in use. The lineage's consumers already use this network.",
		"BknProject.Lineage.MainBranchReadOnly":      "Main Branch Is Read-Only. main changes only by syncing from the live network or by publishing.",
		"BknProject.Lineage.NotFound":                "Lineage Not Found. Create the lineage from its knowledge network first.",
		"BknProject.Lineage.BranchNotFound":          "Branch Not Found. The branch or ref does not exist in this lineage.",
		"BknProject.Lineage.AlreadyExists":           "Lineage Already Exists. This knowledge network already has a lineage.",
		"BknProject.Lineage.BranchAlreadyExists":     "Branch Already Exists. Please choose another branch name.",
		"BknProject.Lineage.MergeConflict":           "Merge Conflict. main and the branch changed the same definitions differently; error_details lists the conflicts.",
		"BknProject.Lineage.BknBackendRequestFailed": "BKN Backend Request Failed. bkn-backend refused the request; error_details holds its reply.",
		"BknProject.Lineage.InternalError":           "Internal Error. Please contact the administrator.",
	},
}
