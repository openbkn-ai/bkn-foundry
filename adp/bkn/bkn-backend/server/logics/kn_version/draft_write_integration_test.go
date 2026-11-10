// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

//go:build integration

package kn_version

import (
	"context"
	"database/sql"
	"testing"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// inDraftWrite runs fn in a transaction that starts like every definition write: the draft guard
// first, then fn, with the request's preconditions in ctx.
func (f *fixture) inDraftWrite(ctx context.Context, branch string, fn func(context.Context, *sql.Tx) error) error {
	f.t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	if err := logics.GuardDefinitionWrite(ctx, tx, f.knID, branch, false); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (f *fixture) revision(branch string) string {
	f.t.Helper()
	var revision string
	if err := f.db.QueryRow("SELECT f_revision FROM t_object_type WHERE f_kn_id = ? AND f_id = 'ot1' AND f_branch = ?",
		f.knID, branch).Scan(&revision); err != nil {
		f.t.Fatal(err)
	}
	return revision
}

func TestDraftWritePreconditions(t *testing.T) {
	f := setup(t)
	logics.SetKNVersionAccess(f.svc.va, f.svc.bc)
	t.Cleanup(func() { logics.SetKNVersionAccess(nil, nil) })
	if _, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{}); err != nil {
		t.Fatal(err)
	}
	// Copies keep the revision, so an ETag read from the running version matches the draft.
	if f.revision(interfaces.DRAFT_BRANCH) != "r1" || f.revision("v1") != "r1" {
		t.Fatalf("copied revisions: draft %s v1 %s", f.revision(interfaces.DRAFT_BRANCH), f.revision("v1"))
	}
	check := func(ctx context.Context, tx *sql.Tx, ids ...string) error {
		return logics.CheckDefinitionRevision(ctx, tx, interfaces.RevisionKindObjectType, f.knID, interfaces.DRAFT_BRANCH, ids...)
	}
	withIfMatch := func(ifMatch string, expected *int64) (context.Context, *logics.DraftWrite) {
		ctx, dw := logics.WithDraftWrite(f.ctx, expected)
		dw.IfMatch = ifMatch
		return ctx, dw
	}

	// A stale If-Match is refused with the current ETag, and the draft revision is unchanged.
	ctx, _ := withIfMatch("stale", nil)
	err := f.inDraftWrite(ctx, interfaces.DRAFT_BRANCH, func(ctx context.Context, tx *sql.Tx) error { return check(ctx, tx, "ot1") })
	wantCode(t, err, berrors.BknBackend_KNVersion_DefinitionModified)
	if got := f.state().Draft.Revision; got != 0 {
		t.Fatalf("draft revision after a refused write = %d", got)
	}

	// If-Match names one definition.
	ctx, _ = withIfMatch("r1", nil)
	err = f.inDraftWrite(ctx, interfaces.DRAFT_BRANCH, func(ctx context.Context, tx *sql.Tx) error { return check(ctx, tx, "ot1", "ot2") })
	wantCode(t, err, berrors.BknBackend_KNVersion_InvalidParameter)

	// A matching If-Match passes once; a second transaction of the same request is not checked
	// again against the revision the first one produced.
	ctx, dw := withIfMatch("r1", nil)
	err = f.inDraftWrite(ctx, interfaces.DRAFT_BRANCH, func(ctx context.Context, tx *sql.Tx) error {
		if err := check(ctx, tx, "ot1"); err != nil {
			return err
		}
		return logics.TouchDefinitionRevision(ctx, tx, interfaces.RevisionKindObjectType, f.knID, interfaces.DRAFT_BRANCH, "ot1")
	})
	if err != nil || dw.IfMatch != "" || dw.Revision != 1 {
		t.Fatalf("matching If-Match: %v %+v", err, dw)
	}
	touched := f.revision(interfaces.DRAFT_BRANCH)
	if touched == "r1" || f.revision("v1") != "r1" {
		t.Fatalf("touch changed draft %s, v1 %s", touched, f.revision("v1"))
	}
	if err := f.inDraftWrite(ctx, interfaces.DRAFT_BRANCH, func(ctx context.Context, tx *sql.Tx) error { return check(ctx, tx, "ot1") }); err != nil {
		t.Fatalf("second transaction of the request: %v", err)
	}

	// The old ETag no longer matches; a missing definition is left to the caller.
	ctx, _ = withIfMatch("r1", nil)
	err = f.inDraftWrite(ctx, interfaces.DRAFT_BRANCH, func(ctx context.Context, tx *sql.Tx) error { return check(ctx, tx, "ot1") })
	wantCode(t, err, berrors.BknBackend_KNVersion_DefinitionModified)
	ctx, _ = withIfMatch("r1", nil)
	if err := f.inDraftWrite(ctx, interfaces.DRAFT_BRANCH, func(ctx context.Context, tx *sql.Tx) error { return check(ctx, tx, "missing") }); err != nil {
		t.Fatalf("missing definition: %v", err)
	}

	// Reads return the stored revision.
	items := []*interfaces.ObjectType{{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "ot1"}}}
	if err := logics.FillRevisions(f.ctx, interfaces.RevisionKindObjectType, f.knID, interfaces.DRAFT_BRANCH, items); err != nil || items[0].Revision != touched {
		t.Fatalf("fill revisions: %v %q want %q", err, items[0].Revision, touched)
	}

	// Writes outside the draft of a versioned network are refused by the guard.
	err = f.inDraftWrite(f.ctx, interfaces.MAIN_BRANCH, func(context.Context, *sql.Tx) error { return nil })
	wantCode(t, err, berrors.BknBackend_KNVersion_InvalidVersion)
}
