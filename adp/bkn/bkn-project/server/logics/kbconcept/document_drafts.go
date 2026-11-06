// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"context"
	"database/sql"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// DocumentDrafts writes document items to drafts guarded by the source selection they were
// extracted from. ok is false when drafts cannot be written guarded.
func DocumentDrafts(drafts interfaces.DraftAccess) (interfaces.DocumentDraftAccess, bool) {
	if writer, ok := drafts.(interfaces.DocumentDraftAccess); ok {
		return writer, true
	}
	guarded, ok := drafts.(interfaces.GuardedDraftAccess)
	if !ok {
		return nil, false
	}
	return documentDrafts{guarded}, true
}

type documentDrafts struct{ drafts interfaces.GuardedDraftAccess }

// CommitDocumentDraft writes the draft only while the selection read is still current and active,
// at the revision read.
func (d documentDrafts) CommitDocumentDraft(ctx context.Context, draft *interfaces.Draft, guard interfaces.DocumentDraftGuard) (bool, error) {
	if guard.SelectionID == "" || guard.SelectionRevision < 1 {
		return false, nil
	}
	return d.drafts.CommitDraftGuarded(ctx, draft, interfaces.DraftGuard{Owner: guard.Owner, DraftExists: guard.DraftExists, DraftUpdateTime: guard.DraftUpdateTime}, func(ctx context.Context, tx *sql.Tx) (bool, error) {
		var state string
		var revision int64
		err := tx.QueryRowContext(ctx, "SELECT f_state,f_revision FROM t_bkn_project_kb_selection WHERE f_project_id=? AND f_id=? AND f_current=1 FOR UPDATE", draft.ProjectID, guard.SelectionID).Scan(&state, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return state == "active" && revision == guard.SelectionRevision, nil
	})
}
