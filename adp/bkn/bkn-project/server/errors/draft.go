// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

const (
	// 400
	BknProject_Draft_NoCatalogs            = "BknProject.Draft.NoCatalogs"
	BknProject_Draft_InvalidRequestBody    = "BknProject.Draft.InvalidRequestBody"
	BknProject_Draft_EmptyEdit             = "BknProject.Draft.EmptyEdit"
	BknProject_Draft_InvalidName           = "BknProject.Draft.InvalidName"
	BknProject_Draft_InvalidDescription    = "BknProject.Draft.InvalidDescription"
	BknProject_Draft_EndpointNotAdopted    = "BknProject.Draft.EndpointNotAdopted"
	BknProject_Draft_InvalidIdentity       = "BknProject.Draft.InvalidIdentity"
	BknProject_Draft_InvalidIncrementalKey = "BknProject.Draft.InvalidIncrementalKey"
	BknProject_Draft_NotRestorable         = "BknProject.Draft.NotRestorable"
	BknProject_Draft_MissingUpdateTime     = "BknProject.Draft.MissingUpdateTime"
	BknProject_Draft_NeedsIdentity         = "BknProject.Draft.NeedsIdentity"
	BknProject_Draft_NothingAdopted        = "BknProject.Draft.NothingAdopted"
	BknProject_Draft_InvalidItem           = "BknProject.Draft.InvalidItem"
	BknProject_Draft_InvalidQuestion       = "BknProject.Draft.InvalidQuestion"

	// 403
	BknProject_Draft_NotOwner = "BknProject.Draft.NotOwner"

	// 404
	BknProject_Draft_NotFound     = "BknProject.Draft.NotFound"
	BknProject_Draft_ItemNotFound = "BknProject.Draft.ItemNotFound"

	// 409
	BknProject_Draft_Drafting              = "BknProject.Draft.Drafting"
	BknProject_Draft_ProjectArchived       = "BknProject.Draft.ProjectArchived"
	BknProject_Draft_NotReady              = "BknProject.Draft.NotReady"
	BknProject_Draft_Stale                 = "BknProject.Draft.Stale"
	BknProject_Draft_HasDecisions          = "BknProject.Draft.HasDecisions"
	BknProject_Draft_FieldNotFound         = "BknProject.Draft.FieldNotFound"
	BknProject_Draft_CatalogMissing        = "BknProject.Draft.CatalogMissing"
	BknProject_Draft_HasPending            = "BknProject.Draft.HasPending"
	BknProject_Draft_PartialNotAccepted    = "BknProject.Draft.PartialNotAccepted"
	BknProject_Draft_UndecidedNotAccepted  = "BknProject.Draft.UndecidedNotAccepted"
	BknProject_Draft_ConflictsNotAccepted  = "BknProject.Draft.ConflictsNotAccepted"
	BknProject_Draft_SourceChangesOutdated = "BknProject.Draft.SourceChangesOutdated"
	BknProject_Draft_NoSourceChanges       = "BknProject.Draft.NoSourceChanges"

	// 503
	BknProject_Draft_SourceUnreachable = "BknProject.Draft.SourceUnreachable"

	// 500
	BknProject_Draft_InternalError = "BknProject.Draft.InternalError"

	// 502, 504: Ask AI could not answer.
	BknProject_Draft_AskUnavailable = "BknProject.Draft.AskUnavailable"
	BknProject_Draft_AskTimeout     = "BknProject.Draft.AskTimeout"
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

// The local index builds of a confirmed draft.
const (
	// 403
	BknProject_Index_NotOwner = "BknProject.Index.NotOwner"

	// 409
	BknProject_Index_NothingConfirmed = "BknProject.Index.NothingConfirmed"
	BknProject_Index_ConfirmAgain     = "BknProject.Index.ConfirmAgain"
)

// The semantic understanding of a project's catalogs.
const (
	// 400
	BknProject_Understand_InvalidApplyMode = "BknProject.Understand.InvalidApplyMode"

	// 403
	BknProject_Understand_NotOwner = "BknProject.Understand.NotOwner"

	// 409
	BknProject_Understand_NoCatalogs = "BknProject.Understand.NoCatalogs"
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
		BknProject_Draft_InvalidIncrementalKey,
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
		BknProject_Draft_InvalidQuestion,
		BknProject_Draft_AskUnavailable,
		BknProject_Draft_AskTimeout,
		BknProject_Draft_NotOwner,
		BknProject_Draft_HasPending,
		BknProject_Draft_PartialNotAccepted,
		BknProject_Draft_UndecidedNotAccepted,
		BknProject_Draft_ConflictsNotAccepted,
		BknProject_Draft_SourceChangesOutdated,
		BknProject_Draft_NoSourceChanges,
		BknProject_Draft_SourceUnreachable,
		BknProject_Check_NotOwner,
		BknProject_Check_NotFound,
		BknProject_Check_NothingConfirmed,
		BknProject_Check_ConfirmAgain,
		BknProject_Check_Running,
		BknProject_Index_NotOwner,
		BknProject_Index_NothingConfirmed,
		BknProject_Index_ConfirmAgain,
		BknProject_Understand_InvalidApplyMode,
		BknProject_Understand_NotOwner,
		BknProject_Understand_NoCatalogs,
	}
)
