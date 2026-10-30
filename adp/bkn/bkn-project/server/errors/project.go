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

// The requirement specification of a project and its documents.
const (
	// 400
	BknProject_Requirement_InvalidRequestBody  = "BknProject.Requirement.InvalidRequestBody"
	BknProject_Requirement_MissingUpdateTime   = "BknProject.Requirement.MissingUpdateTime"
	BknProject_Requirement_UnsupportedFormat   = "BknProject.Requirement.UnsupportedFormat"
	BknProject_Requirement_NotText             = "BknProject.Requirement.NotText"
	BknProject_Requirement_DocumentTooLarge    = "BknProject.Requirement.DocumentTooLarge"
	BknProject_Requirement_TooManyDocuments    = "BknProject.Requirement.TooManyDocuments"
	BknProject_Requirement_InvalidDocumentType = "BknProject.Requirement.InvalidDocumentType"
	BknProject_Requirement_InvalidItem         = "BknProject.Requirement.InvalidItem"
	BknProject_Requirement_InvalidResolution   = "BknProject.Requirement.InvalidResolution"
	BknProject_Requirement_InvalidOption       = "BknProject.Requirement.InvalidOption"
	BknProject_Requirement_NoDocuments         = "BknProject.Requirement.NoDocuments"
	BknProject_Requirement_NoCatalogs          = "BknProject.Requirement.NoCatalogs"
	BknProject_Requirement_InputsTooLarge      = "BknProject.Requirement.InputsTooLarge"
	BknProject_Requirement_InvalidTest         = "BknProject.Requirement.InvalidTest"

	// 403
	BknProject_Requirement_NotOwner = "BknProject.Requirement.NotOwner"

	// 404
	BknProject_Requirement_NotFound         = "BknProject.Requirement.NotFound"
	BknProject_Requirement_DocumentNotFound = "BknProject.Requirement.DocumentNotFound"
	BknProject_Requirement_ItemNotFound     = "BknProject.Requirement.ItemNotFound"
	BknProject_Requirement_TestNotFound     = "BknProject.Requirement.TestNotFound"

	// 409
	BknProject_Requirement_Drafting        = "BknProject.Requirement.Drafting"
	BknProject_Requirement_Generating      = "BknProject.Requirement.Generating"
	BknProject_Requirement_Stale           = "BknProject.Requirement.Stale"
	BknProject_Requirement_HasDecisions    = "BknProject.Requirement.HasDecisions"
	BknProject_Requirement_ProjectArchived = "BknProject.Requirement.ProjectArchived"

	// 500
	BknProject_Requirement_InternalError = "BknProject.Requirement.InternalError"
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
		BknProject_Requirement_InvalidRequestBody,
		BknProject_Requirement_MissingUpdateTime,
		BknProject_Requirement_UnsupportedFormat,
		BknProject_Requirement_NotText,
		BknProject_Requirement_DocumentTooLarge,
		BknProject_Requirement_TooManyDocuments,
		BknProject_Requirement_InvalidDocumentType,
		BknProject_Requirement_InvalidItem,
		BknProject_Requirement_InvalidResolution,
		BknProject_Requirement_InvalidOption,
		BknProject_Requirement_NoDocuments,
		BknProject_Requirement_NoCatalogs,
		BknProject_Requirement_InputsTooLarge,
		BknProject_Requirement_NotOwner,
		BknProject_Requirement_NotFound,
		BknProject_Requirement_DocumentNotFound,
		BknProject_Requirement_ItemNotFound,
		BknProject_Requirement_Drafting,
		BknProject_Requirement_Stale,
		BknProject_Requirement_HasDecisions,
		BknProject_Requirement_ProjectArchived,
		BknProject_Requirement_InternalError,
		BknProject_Requirement_InvalidTest,
		BknProject_Requirement_TestNotFound,
		BknProject_Requirement_Generating,
	}
)
