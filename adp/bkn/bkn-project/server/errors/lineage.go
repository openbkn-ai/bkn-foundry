// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

const (
	// 400
	BknProject_Lineage_InvalidRequestBody = "BknProject.Lineage.InvalidRequestBody"
	BknProject_Lineage_InvalidKNID        = "BknProject.Lineage.InvalidKNID"
	BknProject_Lineage_InvalidBranchName  = "BknProject.Lineage.InvalidBranchName"
	BknProject_Lineage_InvalidDefinitions = "BknProject.Lineage.InvalidDefinitions"
	BknProject_Lineage_NotLiveNetwork     = "BknProject.Lineage.NotLiveNetwork"

	// 403
	BknProject_Lineage_MainBranchReadOnly = "BknProject.Lineage.MainBranchReadOnly"

	// 404
	BknProject_Lineage_NotFound       = "BknProject.Lineage.NotFound"
	BknProject_Lineage_BranchNotFound = "BknProject.Lineage.BranchNotFound"

	// 409
	BknProject_Lineage_AlreadyExists       = "BknProject.Lineage.AlreadyExists"
	BknProject_Lineage_BranchAlreadyExists = "BknProject.Lineage.BranchAlreadyExists"
	BknProject_Lineage_MergeConflict       = "BknProject.Lineage.MergeConflict"

	// The status bkn-backend replied with.
	BknProject_Lineage_BknBackendRequestFailed = "BknProject.Lineage.BknBackendRequestFailed"

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
		BknProject_Lineage_MainBranchReadOnly,
		BknProject_Lineage_NotFound,
		BknProject_Lineage_BranchNotFound,
		BknProject_Lineage_AlreadyExists,
		BknProject_Lineage_BranchAlreadyExists,
		BknProject_Lineage_MergeConflict,
		BknProject_Lineage_BknBackendRequestFailed,
		BknProject_Lineage_InternalError,
	}
)
