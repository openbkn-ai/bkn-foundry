// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

const (
	// 400
	BknProject_Project_InvalidRequestBody = "BknProject.Project.InvalidRequestBody"
	BknProject_Project_InvalidName        = "BknProject.Project.InvalidName"
	BknProject_Project_InvalidNetwork     = "BknProject.Project.InvalidNetwork"
	BknProject_Project_InvalidCatalogIDs  = "BknProject.Project.InvalidCatalogIDs"
	BknProject_Project_InvalidArchived    = "BknProject.Project.InvalidArchived"
	// BknProject_Project_InvalidNamingLanguage: a naming language other than zh-CN or en-US.
	BknProject_Project_InvalidNamingLanguage = "BknProject.Project.InvalidNamingLanguage"

	// 403
	BknProject_Project_NotOwner = "BknProject.Project.NotOwner"

	// 404
	BknProject_Project_NotFound = "BknProject.Project.NotFound"

	// 4xx or 502
	BknProject_Project_BknBackendRequestFailed = "BknProject.Project.BknBackendRequestFailed"

	// 500
	BknProject_Project_InternalError = "BknProject.Project.InternalError"
)

var (
	ProjectErrCodeList = []string{
		BknProject_Project_InvalidRequestBody,
		BknProject_Project_InvalidName,
		BknProject_Project_InvalidNetwork,
		BknProject_Project_InvalidCatalogIDs,
		BknProject_Project_InvalidArchived,
		BknProject_Project_InvalidNamingLanguage,
		BknProject_Project_NotOwner,
		BknProject_Project_NotFound,
		BknProject_Project_BknBackendRequestFailed,
		BknProject_Project_InternalError,
	}
)
