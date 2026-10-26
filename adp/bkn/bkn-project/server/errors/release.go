// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

const (
	// 404
	BknProject_Release_NotFound = "BknProject.Release.NotFound"

	// 500
	BknProject_Release_InternalError = "BknProject.Release.InternalError"
)

var (
	ReleaseErrCodeList = []string{
		BknProject_Release_NotFound,
		BknProject_Release_InternalError,
	}
)
