// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

import "github.com/openbkn-ai/bkn-foundry/comm-go/knversion"

// Knowledge network version errors. The BknBackend.KNVersion codes are bkn-backend's own:
// a query that bkn-backend refuses because of the version it names returns that refusal
// unchanged, so the caller sees the same code whichever service answered. They are declared
// and registered in comm-go/knversion, not here.
const (
	BknBackend_KNVersion_NotVersioned          = knversion.NotVersioned
	BknBackend_KNVersion_InvalidVersion        = knversion.InvalidVersion
	BknBackend_KNVersion_VersionNotFound       = knversion.VersionNotFound
	BknBackend_KNVersion_VersionPurged         = knversion.VersionPurged
	BknBackend_KNVersion_EditionRestricted     = knversion.EditionRestricted
	BknBackend_KNVersion_VersioningDisabling   = knversion.VersioningDisabling
	BknBackend_KNVersion_DraftRevisionMismatch = knversion.DraftRevisionMismatch

	// 400
	OntologyQuery_KNVersion_InvalidParameter = "OntologyQuery.KNVersion.InvalidParameter"
	// 409: two reads of one query were served from different versions
	OntologyQuery_KNVersion_Changed = "OntologyQuery.KNVersion.Changed"
)

// knVersionErrCodeList are the codes this service registers itself.
var knVersionErrCodeList = []string{
	OntologyQuery_KNVersion_InvalidParameter,
	OntologyQuery_KNVersion_Changed,
}

// IsPassThroughKNVersionError reports whether a bkn-backend error code is a refusal about the
// requested version, which is returned to the caller as it is.
func IsPassThroughKNVersionError(code string) bool {
	return knversion.IsRefusal(code)
}
