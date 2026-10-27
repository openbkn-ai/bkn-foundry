// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

const (
	// 400
	BknProject_Draft_NoCatalogs = "BknProject.Draft.NoCatalogs"

	// 404
	BknProject_Draft_NotFound = "BknProject.Draft.NotFound"

	// 409
	BknProject_Draft_Drafting = "BknProject.Draft.Drafting"

	// 500
	BknProject_Draft_InternalError = "BknProject.Draft.InternalError"
)

var (
	DraftErrCodeList = []string{
		BknProject_Draft_NoCatalogs,
		BknProject_Draft_NotFound,
		BknProject_Draft_Drafting,
		BknProject_Draft_InternalError,
	}
)
