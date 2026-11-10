// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

// Knowledge network version errors. The BknBackend.KNVersion codes are bkn-backend's own:
// a query that bkn-backend refuses because of the version it names returns that refusal
// unchanged, so the caller sees the same code whichever service answered.
const (
	BknBackend_KNVersion_NotVersioned          = "BknBackend.KNVersion.NotVersioned"
	BknBackend_KNVersion_InvalidVersion        = "BknBackend.KNVersion.InvalidVersion"
	BknBackend_KNVersion_VersionNotFound       = "BknBackend.KNVersion.VersionNotFound"
	BknBackend_KNVersion_VersionPurged         = "BknBackend.KNVersion.VersionPurged"
	BknBackend_KNVersion_EditionRestricted     = "BknBackend.KNVersion.EditionRestricted"
	BknBackend_KNVersion_VersioningDisabling   = "BknBackend.KNVersion.VersioningDisabling"
	BknBackend_KNVersion_DraftRevisionMismatch = "BknBackend.KNVersion.DraftRevisionMismatch"

	// 400
	OntologyQuery_KNVersion_InvalidParameter = "OntologyQuery.KNVersion.InvalidParameter"
	// 409: two reads of one query were served from different versions
	OntologyQuery_KNVersion_Changed = "OntologyQuery.KNVersion.Changed"
)

// passThroughKNVersionCodes are the bkn-backend refusals about the requested version.
var passThroughKNVersionCodes = []string{
	BknBackend_KNVersion_NotVersioned,
	BknBackend_KNVersion_InvalidVersion,
	BknBackend_KNVersion_VersionNotFound,
	BknBackend_KNVersion_VersionPurged,
	BknBackend_KNVersion_EditionRestricted,
	BknBackend_KNVersion_VersioningDisabling,
	BknBackend_KNVersion_DraftRevisionMismatch,
}

var knVersionErrCodeList = append([]string{
	OntologyQuery_KNVersion_InvalidParameter,
	OntologyQuery_KNVersion_Changed,
}, passThroughKNVersionCodes...)

// IsPassThroughKNVersionError reports whether a bkn-backend error code is a refusal about the
// requested version, which is returned to the caller as it is.
func IsPassThroughKNVersionError(code string) bool {
	for _, known := range passThroughKNVersionCodes {
		if known == code {
			return true
		}
	}
	return false
}
