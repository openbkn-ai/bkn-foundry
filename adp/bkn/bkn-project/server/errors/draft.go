// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

const (
	// 400
	BknProject_Draft_NoCatalogs         = "BknProject.Draft.NoCatalogs"
	BknProject_Draft_InvalidRequestBody = "BknProject.Draft.InvalidRequestBody"
	BknProject_Draft_EmptyEdit          = "BknProject.Draft.EmptyEdit"
	BknProject_Draft_InvalidName        = "BknProject.Draft.InvalidName"
	BknProject_Draft_InvalidDescription = "BknProject.Draft.InvalidDescription"
	BknProject_Draft_EndpointNotAdopted = "BknProject.Draft.EndpointNotAdopted"
	BknProject_Draft_InvalidIdentity    = "BknProject.Draft.InvalidIdentity"
	BknProject_Draft_NotRestorable      = "BknProject.Draft.NotRestorable"
	BknProject_Draft_MissingUpdateTime  = "BknProject.Draft.MissingUpdateTime"
	BknProject_Draft_NeedsIdentity      = "BknProject.Draft.NeedsIdentity"
	BknProject_Draft_NothingAdopted     = "BknProject.Draft.NothingAdopted"
	BknProject_Draft_InvalidItem        = "BknProject.Draft.InvalidItem"

	// 403
	BknProject_Draft_NotOwner = "BknProject.Draft.NotOwner"

	// 404
	BknProject_Draft_NotFound     = "BknProject.Draft.NotFound"
	BknProject_Draft_ItemNotFound = "BknProject.Draft.ItemNotFound"

	// 409
	BknProject_Draft_Drafting        = "BknProject.Draft.Drafting"
	BknProject_Draft_ProjectArchived = "BknProject.Draft.ProjectArchived"
	BknProject_Draft_NotReady        = "BknProject.Draft.NotReady"
	BknProject_Draft_Stale           = "BknProject.Draft.Stale"
	BknProject_Draft_HasDecisions    = "BknProject.Draft.HasDecisions"
	BknProject_Draft_FieldNotFound   = "BknProject.Draft.FieldNotFound"
	BknProject_Draft_CatalogMissing  = "BknProject.Draft.CatalogMissing"
	BknProject_Draft_HasPending      = "BknProject.Draft.HasPending"

	// 500
	BknProject_Draft_InternalError = "BknProject.Draft.InternalError"
)

// The check of a confirmed draft.
const (
	// 403
	BknProject_Check_NotOwner = "BknProject.Check.NotOwner"

	// 404
	BknProject_Check_NotFound = "BknProject.Check.NotFound"

	// 409
	BknProject_Check_NothingConfirmed = "BknProject.Check.NothingConfirmed"
	BknProject_Check_ConfirmAgain     = "BknProject.Check.ConfirmAgain"
	BknProject_Check_Running          = "BknProject.Check.Running"
)

var (
	DraftErrCodeList = []string{
		BknProject_Draft_NoCatalogs,
		BknProject_Draft_NotFound,
		BknProject_Draft_Drafting,
		BknProject_Draft_InternalError,
		BknProject_Draft_MissingUpdateTime,
		BknProject_Draft_NeedsIdentity,
		BknProject_Draft_InvalidIdentity,
		BknProject_Draft_NotRestorable,
		BknProject_Draft_FieldNotFound,
		BknProject_Draft_InvalidRequestBody,
		BknProject_Draft_EmptyEdit,
		BknProject_Draft_InvalidName,
		BknProject_Draft_InvalidDescription,
		BknProject_Draft_EndpointNotAdopted,
		BknProject_Draft_ItemNotFound,
		BknProject_Draft_ProjectArchived,
		BknProject_Draft_NotReady,
		BknProject_Draft_Stale,
		BknProject_Draft_HasDecisions,
		BknProject_Draft_CatalogMissing,
		BknProject_Draft_NothingAdopted,
		BknProject_Draft_InvalidItem,
		BknProject_Draft_NotOwner,
		BknProject_Draft_HasPending,
		BknProject_Check_NotOwner,
		BknProject_Check_NotFound,
		BknProject_Check_NothingConfirmed,
		BknProject_Check_ConfirmAgain,
		BknProject_Check_Running,
	}
)
