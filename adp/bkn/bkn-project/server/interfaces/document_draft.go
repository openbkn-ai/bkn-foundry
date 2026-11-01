// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// DocumentDraftGuard binds an extraction to the source selection and draft the
// operator saw. It is a local compare-and-write guard, never a KB access grant.
type DocumentDraftGuard struct {
	Owner             AccountInfo
	SelectionID       string
	SelectionRevision int64
	DraftExists       bool
	DraftUpdateTime   int64
}

// DocumentDraftAccess adds verified candidates to the existing draft without
// overwriting concurrent edits or results from replaced/released selections.
// All guards are checked in the same transaction as the draft write.
type DocumentDraftAccess interface {
	CommitDocumentDraft(context.Context, *Draft, DocumentDraftGuard) (bool, error)
}
