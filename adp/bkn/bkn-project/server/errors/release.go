// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

const (
	// 400
	BknProject_Release_InvalidVersion   = "BknProject.Release.InvalidVersion"
	BknProject_Release_InvalidProjectID = "BknProject.Release.InvalidProjectID"
	BknProject_Release_InvalidNotes     = "BknProject.Release.InvalidNotes"
	// A project's release.
	BknProject_Release_InvalidRequestBody = "BknProject.Release.InvalidRequestBody"
	BknProject_Release_InvalidAccepts     = "BknProject.Release.InvalidAccepts"
	BknProject_Release_NotProjectRelease  = "BknProject.Release.NotProjectRelease"

	// 403
	BknProject_Release_NotOwner = "BknProject.Release.NotOwner"

	// 404
	BknProject_Release_NotFound = "BknProject.Release.NotFound"

	// 409
	BknProject_Release_VersionExists       = "BknProject.Release.VersionExists"
	BknProject_Release_BranchBehind        = "BknProject.Release.BranchBehind"
	BknProject_Release_Drift               = "BknProject.Release.Drift"
	BknProject_Release_CapabilitiesSkipped = "BknProject.Release.CapabilitiesSkipped"
	BknProject_Release_NetworkExists       = "BknProject.Release.NetworkExists"
	// A project's release.
	BknProject_Release_ProjectArchived = "BknProject.Release.ProjectArchived"
	BknProject_Release_ChecksFailed    = "BknProject.Release.ChecksFailed"
	BknProject_Release_ReviewOutdated  = "BknProject.Release.ReviewOutdated"

	// 500
	BknProject_Release_InternalError = "BknProject.Release.InternalError"
)

var (
	ReleaseErrCodeList = []string{
		BknProject_Release_InvalidVersion,
		BknProject_Release_InvalidProjectID,
		BknProject_Release_InvalidNotes,
		BknProject_Release_InvalidRequestBody,
		BknProject_Release_InvalidAccepts,
		BknProject_Release_NotProjectRelease,
		BknProject_Release_NotOwner,
		BknProject_Release_NotFound,
		BknProject_Release_VersionExists,
		BknProject_Release_BranchBehind,
		BknProject_Release_Drift,
		BknProject_Release_CapabilitiesSkipped,
		BknProject_Release_NetworkExists,
		BknProject_Release_ProjectArchived,
		BknProject_Release_ChecksFailed,
		BknProject_Release_ReviewOutdated,
		BknProject_Release_InternalError,
	}
)
