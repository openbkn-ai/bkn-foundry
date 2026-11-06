// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbresource

import (
	"context"
	"database/sql"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Hooks is what the source selection of a project adds to archiving it.
type Hooks struct {
	Projects  interfaces.ProjectService
	Resources interfaces.KBResourceAccess
}

var _ projecthooks.Projects = Hooks{}

// Archiving queues releasing the sources of the project in its archive transaction. The intent
// survives interrupted remote cleanup, without retaining operator credentials.
func (Hooks) Archiving(ctx context.Context, tx *sql.Tx, projectID string, updateTime int64) error {
	_, err := tx.ExecContext(ctx, "UPDATE t_bkn_project_kb_selection SET f_state=CASE WHEN f_retained=1 THEN 'retained' ELSE 'release_pending' END, f_revision=f_revision+1, f_update_time=? WHERE f_project_id=? AND f_state IN ('pending','active')", updateTime, projectID)
	return err
}

// Archived releases the sources queued. A failed call stays retryable and does not turn a committed
// archive into a failed action.
func (h Hooks) Archived(ctx context.Context, token, projectID string, caller interfaces.AccountInfo) {
	if h.Resources == nil {
		return
	}
	if providers, available := kb.Lookup(); available {
		_, _ = NewService(h.Projects, h.Resources, providers.Source).Cleanup(ctx, token, projectID, caller)
	}
}
