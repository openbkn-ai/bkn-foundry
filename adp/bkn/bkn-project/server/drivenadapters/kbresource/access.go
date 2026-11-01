// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbresource

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const Table = "t_bkn_project_kb_selection"
const columns = "f_id,f_project_id,f_state,f_current,f_retained,f_revision,f_expected_id,f_input_hash,f_data,f_holds,f_create_time,f_update_time"

type access struct{ db *sql.DB }

func New(db *sql.DB) interfaces.KBResourceAccess { return &access{db: db} }

type scanner interface{ Scan(...any) error }

func scan(row scanner) (*interfaces.KBSelection, bool, error) {
	var id, project, state, expected, hash, data, holds string
	var current, retained bool
	var revision, created, updated int64
	if err := row.Scan(&id, &project, &state, &current, &retained, &revision, &expected, &hash, &data, &holds, &created, &updated); err != nil {
		return nil, false, err
	}
	selection := &interfaces.KBSelection{}
	if err := json.Unmarshal([]byte(data), selection); err != nil {
		return nil, false, err
	}
	if err := json.Unmarshal([]byte(holds), &selection.Holds); err != nil {
		return nil, false, err
	}
	selection.ID = id
	selection.ProjectID = project
	selection.State = state
	selection.Retained = retained
	selection.Revision = revision
	selection.ExpectedID = expected
	selection.InputHash = hash
	selection.CreateTime = created
	selection.UpdateTime = updated
	return selection, current, nil
}
func (a *access) Read(ctx context.Context, project string) (*interfaces.KBResourceState, error) {
	rows, err := a.db.QueryContext(ctx, "SELECT "+columns+" FROM "+Table+" WHERE f_project_id=? AND (f_current=1 OR f_state='pending')", project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	state := &interfaces.KBResourceState{}
	for rows.Next() {
		selection, current, err := scan(rows)
		if err != nil {
			return nil, err
		}
		if current {
			state.Current = selection
		}
		if selection.State == "pending" {
			state.Pending = selection
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	err = a.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+Table+" WHERE f_project_id=? AND f_state='release_pending' AND f_retained=0", project).Scan(&state.CleanupPending)
	return state, err
}
func (a *access) Get(ctx context.Context, project, id string) (*interfaces.KBSelection, error) {
	selection, _, err := scan(a.db.QueryRowContext(ctx, "SELECT "+columns+" FROM "+Table+" WHERE f_project_id=? AND f_id=?", project, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return selection, err
}
func (a *access) mutate(ctx context.Context, project string, requireOpen bool, fn func(*sql.Tx) error) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var archived bool
	if err = tx.QueryRowContext(ctx, "SELECT f_archived FROM t_bkn_project_project WHERE f_id=? FOR UPDATE", project).Scan(&archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return interfaces.ErrKBSelectionConflict
		}
		return err
	}
	if requireOpen && archived {
		return interfaces.ErrKBSelectionConflict
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func currentID(ctx context.Context, tx *sql.Tx, project string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, "SELECT f_id FROM "+Table+" WHERE f_project_id=? AND f_current=1", project).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
func (a *access) Prepare(ctx context.Context, selection *interfaces.KBSelection) error {
	data, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	holds, err := json.Marshal(selection.Holds)
	if err != nil {
		return err
	}
	return a.mutate(ctx, selection.ProjectID, true, func(tx *sql.Tx) error {
		current, err := currentID(ctx, tx, selection.ProjectID)
		if err != nil {
			return err
		}
		if current != selection.ExpectedID {
			return interfaces.ErrKBSelectionConflict
		}
		var pending string
		err = tx.QueryRowContext(ctx, "SELECT f_id FROM "+Table+" WHERE f_project_id=? AND f_state='pending'", selection.ProjectID).Scan(&pending)
		if err == nil {
			return interfaces.ErrKBSelectionConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO "+Table+" ("+columns+") VALUES (?,?,'pending',0,0,1,?,?,?,?,?,?)", selection.ID, selection.ProjectID, selection.ExpectedID, selection.InputHash, string(data), string(holds), selection.CreateTime, selection.UpdateTime)
		return err
	})
}
func (a *access) Activate(ctx context.Context, project, id string) error {
	return a.mutate(ctx, project, true, func(tx *sql.Tx) error {
		selection, current, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM "+Table+" WHERE f_project_id=? AND f_id=? FOR UPDATE", project, id))
		if errors.Is(err, sql.ErrNoRows) {
			return interfaces.ErrKBSelectionConflict
		}
		if err != nil {
			return err
		}
		if selection.State == "active" && current {
			return nil
		}
		if selection.State != "pending" {
			return interfaces.ErrKBSelectionConflict
		}
		previous, err := currentID(ctx, tx, project)
		if err != nil {
			return err
		}
		if previous != selection.ExpectedID {
			return interfaces.ErrKBSelectionConflict
		}
		now := time.Now().UnixMilli()
		_, err = tx.ExecContext(ctx, "UPDATE "+Table+" SET f_current=0,f_state=CASE WHEN f_retained=1 THEN 'retained' WHEN f_state='released' THEN 'released' ELSE 'release_pending' END,f_revision=f_revision+1,f_update_time=? WHERE f_project_id=? AND f_current=1", now, project)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE "+Table+" SET f_current=1,f_state='active',f_revision=f_revision+1,f_update_time=? WHERE f_project_id=? AND f_id=?", now, project, id)
		return err
	})
}
func (a *access) SetSamples(ctx context.Context, project, id string, revision int64, files []interfaces.KBResourceFile, plan interfaces.KBSamplePlan) error {
	return a.mutate(ctx, project, true, func(tx *sql.Tx) error {
		selection, current, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM "+Table+" WHERE f_project_id=? AND f_id=? FOR UPDATE", project, id))
		if errors.Is(err, sql.ErrNoRows) {
			return interfaces.ErrKBSelectionConflict
		}
		if err != nil {
			return err
		}
		if !current || selection.State != "active" || selection.Revision != revision || len(files) != len(selection.Files) {
			return interfaces.ErrKBSelectionConflict
		}
		for i, file := range files {
			original := &selection.Files[i]
			if original.Key() != file.Key() || original.ContentVersionID != file.ContentVersionID {
				return interfaces.ErrKBSelectionConflict
			}
			if file.SampleRole != "" && file.SampleRole != "debug" && file.SampleRole != "acceptance" {
				return interfaces.ErrKBSamplePlan
			}
			original.SampleRole = file.SampleRole
		}
		selection.Samples = plan
		selection.Revision++
		selection.UpdateTime = time.Now().UnixMilli()
		selection.Decisions = append(selection.Decisions, interfaces.KBResourceDecision{Kind: "samples", By: selection.Owner, Time: selection.UpdateTime, Samples: &plan})
		data, err := json.Marshal(selection)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE "+Table+" SET f_data=?,f_revision=?,f_update_time=? WHERE f_project_id=? AND f_id=?", string(data), selection.Revision, selection.UpdateTime, project, id)
		return err
	})
}
func (a *access) Retire(ctx context.Context, project, id string) error {
	return a.mutate(ctx, project, false, func(tx *sql.Tx) error {
		var state string
		if err := tx.QueryRowContext(ctx, "SELECT f_state FROM "+Table+" WHERE f_project_id=? AND f_id=? FOR UPDATE", project, id).Scan(&state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return interfaces.ErrKBSelectionConflict
			}
			return err
		}
		if state == "active" {
			return interfaces.ErrKBSelectionConflict
		}
		if state != "pending" {
			return nil
		}
		_, err := tx.ExecContext(ctx, "UPDATE "+Table+" SET f_state='release_pending',f_revision=f_revision+1,f_update_time=? WHERE f_project_id=? AND f_id=?", time.Now().UnixMilli(), project, id)
		return err
	})
}
func (a *access) Clear(ctx context.Context, project string) error {
	return a.mutate(ctx, project, false, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE "+Table+" SET f_current=0,f_state=CASE WHEN f_retained=1 THEN 'retained' WHEN f_state='released' THEN 'released' ELSE 'release_pending' END,f_revision=f_revision+1,f_update_time=? WHERE f_project_id=? AND (f_current=1 OR f_state='pending')", time.Now().UnixMilli(), project)
		return err
	})
}
func (a *access) Cleanup(ctx context.Context, project string) ([]*interfaces.KBSelection, error) {
	rows, err := a.db.QueryContext(ctx, "SELECT "+columns+" FROM "+Table+" WHERE f_project_id=? AND f_state='release_pending' AND f_retained=0", project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	selections := []*interfaces.KBSelection{}
	for rows.Next() {
		selection, _, err := scan(rows)
		if err != nil {
			return nil, err
		}
		selections = append(selections, selection)
	}
	return selections, rows.Err()
}
func (a *access) MarkReleased(ctx context.Context, project, id, holdID string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state, raw string
	var retained bool
	if err = tx.QueryRowContext(ctx, "SELECT f_state,f_retained,f_holds FROM "+Table+" WHERE f_project_id=? AND f_id=? FOR UPDATE", project, id).Scan(&state, &retained, &raw); err != nil {
		return err
	}
	if state == "released" {
		return tx.Commit()
	}
	if state != "release_pending" || retained {
		return interfaces.ErrKBSelectionConflict
	}
	var holds []interfaces.KBHoldGroup
	if err = json.Unmarshal([]byte(raw), &holds); err != nil {
		return err
	}
	found := holdID == ""
	allReleased := true
	for i := range holds {
		if holds[i].ID == holdID {
			holds[i].Released = true
			found = true
		}
		allReleased = allReleased && holds[i].Released
	}
	if !found {
		return interfaces.ErrKBSelectionConflict
	}
	if allReleased {
		state = "released"
	}
	data, err := json.Marshal(holds)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE "+Table+" SET f_state=?,f_holds=?,f_update_time=? WHERE f_project_id=? AND f_id=?", state, string(data), time.Now().UnixMilli(), project, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
