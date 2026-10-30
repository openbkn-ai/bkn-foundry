// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

const (
	// 400
	BknProject_Lineage_InvalidRequestBody  = "BknProject.Lineage.InvalidRequestBody"
	BknProject_Lineage_InvalidKNID         = "BknProject.Lineage.InvalidKNID"
	BknProject_Lineage_InvalidBranchName   = "BknProject.Lineage.InvalidBranchName"
	BknProject_Lineage_InvalidDefinitions  = "BknProject.Lineage.InvalidDefinitions"
	BknProject_Lineage_NotLiveNetwork      = "BknProject.Lineage.NotLiveNetwork"
	BknProject_Lineage_InvalidSwitchTarget = "BknProject.Lineage.InvalidSwitchTarget"
	// The network cannot be kept as a BKN 3.0 package until the action parameters bkn-backend
	// names are fixed.
	BknProject_Lineage_NetworkNeedsCheck = "BknProject.Lineage.NetworkNeedsCheck"
	// A definition of the network does not read back under the BKN 3.0 rules.
	BknProject_Lineage_NetworkNotReadableAsV3 = "BknProject.Lineage.NetworkNotReadableAsV3"

	// 403
	BknProject_Lineage_MainBranchReadOnly = "BknProject.Lineage.MainBranchReadOnly"

	// 404
	BknProject_Lineage_NotFound       = "BknProject.Lineage.NotFound"
	BknProject_Lineage_BranchNotFound = "BknProject.Lineage.BranchNotFound"

	// 409
	BknProject_Lineage_AlreadyExists       = "BknProject.Lineage.AlreadyExists"
	BknProject_Lineage_BranchAlreadyExists = "BknProject.Lineage.BranchAlreadyExists"
	BknProject_Lineage_MergeConflict       = "BknProject.Lineage.MergeConflict"
	// The branch is still kept in BKN 2.x; syncing it with main moves it to 3.0.
	BknProject_Lineage_BranchNotV3 = "BknProject.Lineage.BranchNotV3"
	// The branch and main are still kept in BKN 2.x; committing main's drift moves main to 3.0,
	// then syncing the branch moves it too.
	BknProject_Lineage_LineageNotV3 = "BknProject.Lineage.LineageNotV3"
	// Definitions to add are already on the branch.
	BknProject_Lineage_DefinitionsExist = "BknProject.Lineage.DefinitionsExist"
	BknProject_Lineage_AlreadyInUse     = "BknProject.Lineage.AlreadyInUse"
	BknProject_Lineage_InUseChanged     = "BknProject.Lineage.InUseChanged"

	// The status bkn-backend replied with.
	BknProject_Lineage_BknBackendRequestFailed = "BknProject.Lineage.BknBackendRequestFailed"
	// The status execution-factory replied with, or 502.
	BknProject_Lineage_CapabilityRequestFailed = "BknProject.Lineage.CapabilityRequestFailed"

	// 500
	BknProject_Lineage_InternalError = "BknProject.Lineage.InternalError"
)

var (
	LineageErrCodeList = []string{
		BknProject_Lineage_InvalidRequestBody,
		BknProject_Lineage_InvalidKNID,
		BknProject_Lineage_InvalidBranchName,
		BknProject_Lineage_InvalidDefinitions,
		BknProject_Lineage_NotLiveNetwork,
		BknProject_Lineage_InvalidSwitchTarget,
		BknProject_Lineage_NetworkNeedsCheck,
		BknProject_Lineage_NetworkNotReadableAsV3,
		BknProject_Lineage_MainBranchReadOnly,
		BknProject_Lineage_NotFound,
		BknProject_Lineage_BranchNotFound,
		BknProject_Lineage_AlreadyExists,
		BknProject_Lineage_BranchAlreadyExists,
		BknProject_Lineage_MergeConflict,
		BknProject_Lineage_BranchNotV3,
		BknProject_Lineage_LineageNotV3,
		BknProject_Lineage_DefinitionsExist,
		BknProject_Lineage_AlreadyInUse,
		BknProject_Lineage_InUseChanged,
		BknProject_Lineage_BknBackendRequestFailed,
		BknProject_Lineage_CapabilityRequestFailed,
		BknProject_Lineage_InternalError,
	}
)
