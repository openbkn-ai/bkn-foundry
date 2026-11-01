// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestDocumentCommitGuardsEverySnapshotInOneTransaction(t *testing.T) {
	for _, scenario := range []string{"new", "existing", "archived", "owner_changed", "selection_changed", "selection_released", "draft_changed", "draft_started", "new_draft_race", "write_failed", "commit_failed"} {
		t.Run(scenario, func(t *testing.T) {
			db, m, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			m.ExpectBegin()
			owner := "u"
			archived := false
			if scenario == "owner_changed" {
				owner = "other"
			}
			if scenario == "archived" {
				archived = true
			}
			m.ExpectQuery("SELECT f_archived,f_owner,f_owner_type FROM t_bkn_project_project").WithArgs("p").WillReturnRows(sqlmock.NewRows([]string{"archived", "owner", "type"}).AddRow(archived, owner, "user"))
			success := scenario == "new" || scenario == "existing"
			guard := interfaces.DocumentDraftGuard{Owner: interfaces.AccountInfo{ID: "u", Type: "user"}, SelectionID: "locked", SelectionRevision: 3}
			d := &interfaces.Draft{ProjectID: "p", Status: "ready", Drafter: guard.Owner, CreateTime: 20, UpdateTime: 20}
			if scenario != "archived" && scenario != "owner_changed" {
				revision := int64(3)
				state := "active"
				if scenario == "selection_changed" {
					revision = 4
				}
				if scenario == "selection_released" {
					state = "released"
				}
				m.ExpectQuery("SELECT f_state,f_revision FROM t_bkn_project_kb_selection").WithArgs("p", "locked").WillReturnRows(sqlmock.NewRows([]string{"state", "revision"}).AddRow(state, revision))
				if scenario != "selection_changed" && scenario != "selection_released" {
					q := m.ExpectQuery("SELECT f_status,f_update_time FROM t_bkn_project_draft").WithArgs("p")
					status := "ready"
					at := int64(10)
					if scenario == "new" {
						q.WillReturnError(sql.ErrNoRows)
					} else {
						if scenario == "draft_changed" {
							at = 11
						}
						if scenario == "draft_started" {
							status = "drafting"
						}
						q.WillReturnRows(sqlmock.NewRows([]string{"status", "updated"}).AddRow(status, at))
						if scenario != "new_draft_race" {
							guard.DraftExists = true
							guard.DraftUpdateTime = 10
						}
					}
					if scenario == "new" || scenario == "existing" || scenario == "write_failed" || scenario == "commit_failed" {
						pattern := "UPDATE t_bkn_project_draft"
						if scenario == "new" {
							pattern = "INSERT INTO t_bkn_project_draft"
						}
						exec := m.ExpectExec(pattern)
						if scenario == "write_failed" {
							exec.WillReturnError(errors.New("write unavailable"))
						} else {
							exec.WillReturnResult(sqlmock.NewResult(1, 1))
						}
						if scenario == "commit_failed" {
							m.ExpectCommit().WillReturnError(errors.New("commit unavailable"))
						} else if scenario != "write_failed" {
							m.ExpectCommit()
						}
					}
				}
			}
			if !success && scenario != "commit_failed" {
				m.ExpectRollback()
			}
			written, err := (&draftAccess{db: db}).CommitDocumentDraft(context.Background(), d, guard)
			if written != success || (success && err != nil) {
				t.Fatalf("written=%v error=%v", written, err)
			}
			if (scenario == "write_failed" || scenario == "commit_failed") && err == nil {
				t.Fatal("database error hidden")
			}
			if err := m.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDocumentCandidatesCannotBeSilentlyDiscardedByTableRedrafting(t *testing.T) {
	for _, kind := range []string{"object_types", "relation_types"} {
		if !hasDecisions(`{"` + kind + `":[{"id":"model","document":{"role":"entity"}}],"decisions":[]}`) {
			t.Fatal("document candidates were not protected by explicit discard confirmation")
		}
	}
	if hasDecisions(`{"object_types":[{"id":"table","document":null}],"decisions":[]}`) {
		t.Fatal("table-only reset behavior changed")
	}
}
