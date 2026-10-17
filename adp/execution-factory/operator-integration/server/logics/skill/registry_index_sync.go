// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package skill

import (
	"context"
	"database/sql"
	"time"
)

const skillIndexOperationTimeout = 30 * time.Second

// finishIndexedSkillWrite keeps semantic indexing out of the write transaction.
// Only committed source changes may wake the existing capability reconciler.
func (r *skillRegistry) finishIndexedSkillWrite(ctx context.Context, tx *sql.Tx, writeErr *error, skillID string, removeIndex bool) {
	if *writeErr != nil {
		if err := tx.Rollback(); err != nil {
			r.Logger.WithContext(ctx).Errorf("rollback skill write failed, skill_id=%s, err=%v", skillID, err)
		}
		return
	}
	if err := tx.Commit(); err != nil {
		*writeErr = err
		r.Logger.WithContext(ctx).Errorf("commit skill write failed, skill_id=%s, err=%v", skillID, err)
		return
	}
	// Purges need no embedding model and must observe the committed deletion or
	// offline state, so a concurrent reconciler can detect a stale upsert.
	if removeIndex && r.indexSync != nil {
		syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), skillIndexOperationTimeout)
		defer cancel()
		if err := r.indexSync.DeleteSkill(syncCtx, skillID); err != nil {
			r.Logger.WithContext(ctx).Errorf("delete committed skill index failed, skill_id=%s, err=%v", skillID, err)
		}
	}
	if r.notifyIndexSync != nil {
		r.notifyIndexSync()
	}
}
