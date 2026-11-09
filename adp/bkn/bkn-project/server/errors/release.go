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
	// A rollback.
	BknProject_Rollback_PrecheckRequired = "BknProject.Rollback.PrecheckRequired"

	// 403
	BknProject_Release_SourcePermission = "BknProject.Release.SourcePermission"
	BknProject_Release_NotOwner         = "BknProject.Release.NotOwner"

	// 404
	BknProject_Release_NotFound           = "BknProject.Release.NotFound"
	BknProject_Release_RegressionNotFound = "BknProject.Release.RegressionNotFound"

	// 409
	BknProject_Release_VersionExists       = "BknProject.Release.VersionExists"
	BknProject_Release_BranchBehind        = "BknProject.Release.BranchBehind"
	BknProject_Release_Drift               = "BknProject.Release.Drift"
	BknProject_Release_CapabilitiesSkipped = "BknProject.Release.CapabilitiesSkipped"
	// Publishing into the versioned network.
	BknProject_Release_NotPrepared      = "BknProject.Release.NotPrepared"
	BknProject_Release_DraftHasChanges  = "BknProject.Release.DraftHasChanges"
	BknProject_Release_DraftChanged     = "BknProject.Release.DraftChanged"
	BknProject_Release_DraftOutdated    = "BknProject.Release.DraftOutdated"
	BknProject_Release_RunningChanged   = "BknProject.Release.RunningChanged"
	BknProject_Release_NothingToPublish = "BknProject.Release.NothingToPublish"
	BknProject_Release_ValidationFailed = "BknProject.Release.ValidationFailed"
	BknProject_Release_NotVersioned     = "BknProject.Release.NotVersioned"
	// A project's release.
	BknProject_Release_ProjectArchived = "BknProject.Release.ProjectArchived"
	BknProject_Release_ChecksFailed    = "BknProject.Release.ChecksFailed"
	BknProject_Release_ReviewOutdated  = "BknProject.Release.ReviewOutdated"
	// A release's regression.
	BknProject_Release_RegressionRunning = "BknProject.Release.RegressionRunning"
	// A rollback.
	BknProject_Rollback_PlanOutdated      = "BknProject.Rollback.PlanOutdated"
	BknProject_Rollback_NoPrevious        = "BknProject.Rollback.NoPrevious"
	BknProject_Rollback_NotProjectRelease = "BknProject.Rollback.NotProjectRelease"
	BknProject_Rollback_BrokenReferences  = "BknProject.Rollback.BrokenReferences"
	BknProject_Rollback_NotActivatable    = "BknProject.Rollback.NotActivatable"
	BknProject_Rollback_EditionRestricted = "BknProject.Rollback.EditionRestricted"
	BknProject_Release_VersionPurged      = "BknProject.Release.VersionPurged"

	// 503
	BknProject_Release_NotReady = "BknProject.Release.NotReady"

	// 500
	BknProject_Release_InternalError = "BknProject.Release.InternalError"
)

var (
	ReleaseErrCodeList = []string{
		BknProject_Release_SourcePermission,
		BknProject_Release_InvalidVersion,
		BknProject_Release_InvalidProjectID,
		BknProject_Release_InvalidNotes,
		BknProject_Release_InvalidRequestBody,
		BknProject_Release_InvalidAccepts,
		BknProject_Release_NotProjectRelease,
		BknProject_Release_NotOwner,
		BknProject_Release_NotFound,
		BknProject_Release_RegressionNotFound,
		BknProject_Release_VersionExists,
		BknProject_Release_BranchBehind,
		BknProject_Release_Drift,
		BknProject_Release_CapabilitiesSkipped,
		BknProject_Release_NotPrepared,
		BknProject_Release_DraftHasChanges,
		BknProject_Release_DraftChanged,
		BknProject_Release_DraftOutdated,
		BknProject_Release_RunningChanged,
		BknProject_Release_NothingToPublish,
		BknProject_Release_ValidationFailed,
		BknProject_Release_NotVersioned,
		BknProject_Rollback_BrokenReferences,
		BknProject_Rollback_NotActivatable,
		BknProject_Rollback_EditionRestricted,
		BknProject_Release_VersionPurged,
		BknProject_Release_NotReady,
		BknProject_Release_ProjectArchived,
		BknProject_Release_ChecksFailed,
		BknProject_Release_ReviewOutdated,
		BknProject_Release_RegressionRunning,
		BknProject_Rollback_PrecheckRequired,
		BknProject_Rollback_PlanOutdated,
		BknProject_Rollback_NoPrevious,
		BknProject_Rollback_NotProjectRelease,
		BknProject_Release_InternalError,
	}
)
