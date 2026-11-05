// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbbuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func (a *access) Runnable(ctx context.Context, limit int) ([]interfaces.KBBuildKey, error) {
	if limit < 1 || limit > 1000 {
		return nil, errCorrupt
	}
	rows, err := a.db.QueryContext(ctx, "SELECT f_project_id,f_id FROM "+table+" WHERE f_state IN ('preparing','queued','running','attention','finishing','cancel_pending') ORDER BY f_update_time,f_id LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []interfaces.KBBuildKey{}
	for rows.Next() {
		var key interfaces.KBBuildKey
		if err = rows.Scan(&key.ProjectID, &key.ID); err != nil {
			return nil, err
		}
		result = append(result, key)
	}
	return result, rows.Err()
}
func (a *access) List(ctx context.Context, project string, limit int) ([]*interfaces.KBBuild, error) {
	if limit < 1 || limit > 100 {
		return nil, errCorrupt
	}
	rows, err := a.db.QueryContext(ctx, "SELECT "+columns+" FROM "+table+" WHERE f_project_id=? ORDER BY f_create_time DESC,f_id DESC LIMIT ?", project, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []*interfaces.KBBuild{}
	for rows.Next() {
		record, err := read(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, record.Build)
	}
	return result, rows.Err()
}
func (a *access) Cancel(ctx context.Context, projectID, id string, caller interfaces.AccountInfo, now int64) (bool, error) {
	if caller.ID == "" || caller.Type != "user" || now <= 0 {
		return false, nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	owner, _, err := project(ctx, tx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if owner != caller {
		return false, nil
	}
	r, err := read(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM "+table+" WHERE f_project_id=? AND f_id=? FOR UPDATE", projectID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !terminal(r.Build.State) && r.Build.State != "cancel_pending" {
		old := r.Build.Revision
		linking := r.Build.State == "linking" || r.Build.State == "linking_attention"
		r.Build.State = "cancel_pending"
		// Linking reuses acknowledged rows after extraction delegation cleanup.
		// It has no background identity left for the extraction worker to clean.
		if r.Identity.Cleaned {
			r.Build.State = "cancelled"
			if linking {
				// Cancel only the continuation; successful extraction remains reusable.
				r.Build.State = "succeeded"
				if r.Build.Linking != nil {
					r.Build.Linking.State = "cancelled"
				}
			}
		}
		r.Build.Revision++
		r.Build.UpdateTime = max(now, r.Build.UpdateTime+1)
		payload, err := json.Marshal(r.Build)
		if err != nil {
			return false, err
		}
		result, err := tx.ExecContext(ctx, "UPDATE "+table+" SET f_state=?,f_revision=?,f_data=?,f_lease_id='',f_lease_until=0,f_update_time=? WHERE f_project_id=? AND f_id=? AND f_revision=?", r.Build.State, r.Build.Revision, string(payload), r.Build.UpdateTime, projectID, id, old)
		if err != nil {
			return false, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if count != 1 {
			return false, errCorrupt
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// WorkItems reads each record because progress lives in its process data. It
// serves an operator endpoint, is capped, and returns no names or identities.
func (a *access) WorkItems(ctx context.Context) ([]interfaces.KBWorkItem, error) {
	rows, err := a.db.QueryContext(ctx, "SELECT f_state,f_data,f_update_time FROM "+table+" WHERE f_state IN ('preparing','queued','running','attention','finishing','cancel_pending') ORDER BY f_update_time LIMIT 200")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []interfaces.KBWorkItem{}
	for rows.Next() {
		var state, data string
		var updated int64
		if err = rows.Scan(&state, &data, &updated); err != nil {
			return nil, err
		}
		var build struct {
			ProgressTime int64 `json:"progress_time"`
		}
		// A record written before progress was tracked reports its last save.
		if json.Unmarshal([]byte(data), &build) != nil || build.ProgressTime == 0 {
			build.ProgressTime = updated
		}
		result = append(result, interfaces.KBWorkItem{State: state, ProgressTime: build.ProgressTime})
	}
	return result, rows.Err()
}

var _ interfaces.KBBuildQueue = (*access)(nil)
var _ interfaces.KBWorkItems = (*access)(nil)
