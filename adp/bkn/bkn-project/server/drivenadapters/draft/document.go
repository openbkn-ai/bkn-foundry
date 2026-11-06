// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// CommitDraftGuarded writes the draft only if the project, the draft and what lock checks are as
// the caller read them, all in one transaction.
func (da *draftAccess) CommitDraftGuarded(ctx context.Context, draft *interfaces.Draft, guard interfaces.DraftGuard, lock interfaces.DraftLock) (bool, error) {
	if draft == nil || draft.ProjectID == "" || draft.Status != interfaces.DRAFT_STATUS_READY || guard.Owner.ID == "" || guard.Owner.Type == "" || lock == nil || draft.UpdateTime <= guard.DraftUpdateTime {
		return false, nil
	}
	items, err := json.Marshal(draft.Items)
	if err != nil {
		return false, err
	}
	tx, err := da.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Source mutations and archival use the project row first. No network or AI
	// call occurs while locks are held. Ordinary draft edits serialize below.
	var archived bool
	var owner interfaces.AccountInfo
	err = tx.QueryRowContext(ctx, "SELECT f_archived,f_owner,f_owner_type FROM t_bkn_project_project WHERE f_id=? FOR UPDATE", draft.ProjectID).Scan(&archived, &owner.ID, &owner.Type)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if archived || owner != guard.Owner {
		return false, nil
	}
	if locked, err := lock(ctx, tx); err != nil || !locked {
		return false, err
	}
	var status string
	var at int64
	err = tx.QueryRowContext(ctx, "SELECT f_status,f_update_time FROM t_bkn_project_draft WHERE f_project_id=? FOR UPDATE", draft.ProjectID).Scan(&status, &at)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if exists != guard.DraftExists || (exists && (status != interfaces.DRAFT_STATUS_READY || at != guard.DraftUpdateTime)) {
		return false, nil
	}
	if exists {
		_, err = tx.ExecContext(ctx, "UPDATE t_bkn_project_draft SET f_items=?,f_update_time=? WHERE f_project_id=?", string(items), draft.UpdateTime, draft.ProjectID)
	} else {
		_, err = tx.ExecContext(ctx, "INSERT INTO t_bkn_project_draft (f_project_id,f_status,f_items,f_error,f_drafter,f_drafter_type,f_create_time,f_update_time) VALUES (?,?,?,?,?,?,?,?)", draft.ProjectID, draft.Status, string(items), "", draft.Drafter.ID, draft.Drafter.Type, draft.CreateTime, draft.UpdateTime)
	}
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

var _ interfaces.GuardedDraftAccess = (*draftAccess)(nil)
