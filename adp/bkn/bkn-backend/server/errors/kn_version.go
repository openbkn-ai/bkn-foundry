// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

// Knowledge network versioning error codes.
const (
	BknBackend_KNVersion_NotVersioned           = "BknBackend.KNVersion.NotVersioned"
	BknBackend_KNVersion_AlreadyVersioned       = "BknBackend.KNVersion.AlreadyVersioned"
	BknBackend_KNVersion_ReservedBranchInUse    = "BknBackend.KNVersion.ReservedBranchInUse"
	BknBackend_KNVersion_InvalidVersion         = "BknBackend.KNVersion.InvalidVersion"
	BknBackend_KNVersion_VersionNotFound        = "BknBackend.KNVersion.VersionNotFound"
	BknBackend_KNVersion_VersionPurged          = "BknBackend.KNVersion.VersionPurged"
	BknBackend_KNVersion_EditionRestricted      = "BknBackend.KNVersion.EditionRestricted"
	BknBackend_KNVersion_VersioningDisabling    = "BknBackend.KNVersion.VersioningDisabling"
	BknBackend_KNVersion_RunningVersionMismatch = "BknBackend.KNVersion.RunningVersionMismatch"
	BknBackend_KNVersion_DraftRevisionMismatch  = "BknBackend.KNVersion.DraftRevisionMismatch"
	BknBackend_KNVersion_DraftHasChanges        = "BknBackend.KNVersion.DraftHasChanges"
	BknBackend_KNVersion_DraftBaseOutdated      = "BknBackend.KNVersion.DraftBaseOutdated"
	BknBackend_KNVersion_DefinitionModified     = "BknBackend.KNVersion.DefinitionModified"
	BknBackend_KNVersion_PreconditionRequired   = "BknBackend.KNVersion.PreconditionRequired"
	BknBackend_KNVersion_ConceptIndexFailed     = "BknBackend.KNVersion.ConceptIndexFailed"
	BknBackend_KNVersion_NothingToPublish       = "BknBackend.KNVersion.NothingToPublish"
	BknBackend_KNVersion_ValidationFailed       = "BknBackend.KNVersion.ValidationFailed"
	BknBackend_KNVersion_BrokenReferences       = "BknBackend.KNVersion.BrokenReferences"
	BknBackend_KNVersion_VersionNotActivatable  = "BknBackend.KNVersion.VersionNotActivatable"
	BknBackend_KNVersion_VersionNotRestorable   = "BknBackend.KNVersion.VersionNotRestorable"
	BknBackend_KNVersion_VersionNotDeprecatable = "BknBackend.KNVersion.VersionNotDeprecatable"
	BknBackend_KNVersion_VersionProtected       = "BknBackend.KNVersion.VersionProtected"
	BknBackend_KNVersion_IdempotencyKeyReused   = "BknBackend.KNVersion.IdempotencyKeyReused"
	BknBackend_KNVersion_InvalidParameter       = "BknBackend.KNVersion.InvalidParameter"
	BknBackend_KNVersion_InternalError          = "BknBackend.KNVersion.InternalError"
)

var KNVersionErrCodeList = []string{
	BknBackend_KNVersion_NotVersioned,
	BknBackend_KNVersion_AlreadyVersioned,
	BknBackend_KNVersion_ReservedBranchInUse,
	BknBackend_KNVersion_InvalidVersion,
	BknBackend_KNVersion_VersionNotFound,
	BknBackend_KNVersion_VersionPurged,
	BknBackend_KNVersion_EditionRestricted,
	BknBackend_KNVersion_VersioningDisabling,
	BknBackend_KNVersion_RunningVersionMismatch,
	BknBackend_KNVersion_DraftRevisionMismatch,
	BknBackend_KNVersion_DraftHasChanges,
	BknBackend_KNVersion_DraftBaseOutdated,
	BknBackend_KNVersion_DefinitionModified,
	BknBackend_KNVersion_PreconditionRequired,
	BknBackend_KNVersion_ConceptIndexFailed,
	BknBackend_KNVersion_NothingToPublish,
	BknBackend_KNVersion_ValidationFailed,
	BknBackend_KNVersion_BrokenReferences,
	BknBackend_KNVersion_VersionNotActivatable,
	BknBackend_KNVersion_VersionNotRestorable,
	BknBackend_KNVersion_VersionNotDeprecatable,
	BknBackend_KNVersion_VersionProtected,
	BknBackend_KNVersion_IdempotencyKeyReused,
	BknBackend_KNVersion_InvalidParameter,
	BknBackend_KNVersion_InternalError,
}
