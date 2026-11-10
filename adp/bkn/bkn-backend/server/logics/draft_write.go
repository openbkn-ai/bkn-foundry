// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// DraftWrite carries the draft precondition of one request, and the draft revision its writes
// produced, for the X-Kn-Draft-Revision response header.
type DraftWrite struct {
	// Expected is the expected_draft_revision of the request; nil when it has none. After a
	// write it becomes the new revision, so a request with several transactions checks each
	// against the previous one.
	Expected *int64
	Revision int64
	Written  bool
	// IfMatch is the If-Match of the request, the expected revision of the one definition it
	// changes. It is cleared once checked, so later transactions of the request do not check
	// it again against the revision the request itself produced.
	IfMatch string
	// RefuseDraftChanges makes the first write refuse a draft with unpublished changes, for a
	// whole-package replace without overwrite_changes. It is cleared once checked.
	RefuseDraftChanges bool
}

type draftWriteKey struct{}

func WithDraftWrite(ctx context.Context, expected *int64) (context.Context, *DraftWrite) {
	dw := &DraftWrite{Expected: expected}
	return context.WithValue(ctx, draftWriteKey{}, dw), dw
}

func DraftWriteFrom(ctx context.Context) *DraftWrite {
	dw, _ := ctx.Value(draftWriteKey{}).(*DraftWrite)
	return dw
}

// GuardDefinitionWrite runs first in every transaction that writes definitions of a network.
// It locks the main row of the network, in share mode or, for writes that update the network
// row itself, exclusively, and then reads the version state, so a write and a version
// operation of the same network never interleave:
//
//   - a network that is not versioned is written as before;
//   - a write to the draft of a versioned network adds one to the draft revision, checking the
//     request's expected_draft_revision;
//   - any other branch of a versioned network is refused. Request handlers already route
//     writes to the draft; this catches a write resolved before versioning was enabled.
func GuardDefinitionWrite(ctx context.Context, tx *sql.Tx, knID, branch string, exclusive bool) error {
	if KNVA == nil || tx == nil {
		return nil
	}
	if _, err := KNVA.LockNetwork(ctx, tx, knID, exclusive); err != nil {
		return versionInternalError(ctx, err)
	}
	state, err := KNVA.GetState(ctx, tx, knID, false)
	if err != nil {
		return versionInternalError(ctx, err)
	}
	if state == nil {
		return nil
	}
	if state.Mode == interfaces.KNVersioningDisabling {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_VersioningDisabling)
	}
	if branch != interfaces.DRAFT_BRANCH {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_InvalidVersion).
			WithErrorDetails("the knowledge network is versioned; definitions are written to its draft")
	}
	dw := DraftWriteFrom(ctx)
	var expected *int64
	if dw != nil {
		expected = dw.Expected
		if dw.RefuseDraftChanges && state.DraftHasChanges() {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_DraftHasChanges).
				WithErrorDetails("pass overwrite_changes=true to replace the unpublished changes")
		}
		dw.RefuseDraftChanges = false
	}
	revision, ok, err := KNVA.MarkDraftWritten(ctx, tx, knID, expected)
	if err != nil {
		return versionInternalError(ctx, err)
	}
	if !ok {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_DraftRevisionMismatch).
			WithErrorDetails(map[string]any{"draft_revision": state.DraftRevision})
	}
	if dw != nil {
		dw.Expected, dw.Revision, dw.Written = &revision, revision, true
	}
	return nil
}

// CheckDefinitionRevision checks the request's If-Match against the definition the transaction
// is about to change or delete, locking its row. It does nothing without If-Match, or when the
// definition does not exist, which the caller reports as before. ids are the definitions the
// request changes; If-Match names one definition, so with several it is refused.
func CheckDefinitionRevision(ctx context.Context, tx *sql.Tx, kind, knID, branch string, ids ...string) error {
	dw := DraftWriteFrom(ctx)
	if KNVA == nil || tx == nil || dw == nil || dw.IfMatch == "" {
		return nil
	}
	if len(ids) != 1 {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KNVersion_InvalidParameter).
			WithErrorDetails("If-Match applies to one definition; use expected_draft_revision to change several")
	}
	current, ok, err := KNVA.DefinitionRevision(ctx, tx, kind, knID, branch, ids[0])
	if err != nil {
		return versionInternalError(ctx, err)
	}
	if !ok {
		return nil
	}
	if current != dw.IfMatch {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_DefinitionModified).
			WithErrorDetails(map[string]any{"current_etag": current})
	}
	dw.IfMatch = ""
	return nil
}

// TouchDefinitionRevision gives a definition a new revision for a change stored outside its row.
func TouchDefinitionRevision(ctx context.Context, tx *sql.Tx, kind, knID, branch, id string) error {
	if KNVA == nil || tx == nil {
		return nil
	}
	if err := KNVA.TouchDefinitionRevision(ctx, tx, kind, knID, branch, id); err != nil {
		return versionInternalError(ctx, err)
	}
	return nil
}

// FillRevisions sets the revision of each loaded definition of the branch.
func FillRevisions[T interfaces.Revisioned](ctx context.Context, kind, knID, branch string, items []T) error {
	if KNVA == nil || len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.RevisionID())
	}
	revisions, err := KNVA.DefinitionRevisions(ctx, kind, knID, branch, ids)
	if err != nil {
		return versionInternalError(ctx, err)
	}
	for _, item := range items {
		item.SetRevision(revisions[item.RevisionID()])
	}
	return nil
}

func versionInternalError(ctx context.Context, err error) error {
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_KNVersion_InternalError).
		WithErrorDetails(err.Error())
}
