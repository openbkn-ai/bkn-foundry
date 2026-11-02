// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbbuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const referenceTable = "t_bkn_project_kb_reference"

type references struct{ db *sql.DB }

func NewReferences(db *sql.DB) interfaces.KBReferenceAccess { return &references{db: db} }
func readReference(row scanner) (*interfaces.KBReference, error) {
	var id, project, data string
	var revision int64
	if err := row.Scan(&id, &project, &revision, &data); err != nil {
		return nil, err
	}
	var record interfaces.KBReference
	if json.Unmarshal([]byte(data), &record) != nil || record.ID != id || record.ProjectID != project || record.Revision != revision {
		return nil, errCorrupt
	}
	return &record, nil
}
func (a *references) Get(ctx context.Context, project, id string) (*interfaces.KBReference, error) {
	result, err := readReference(a.db.QueryRowContext(ctx, "SELECT f_id,f_project_id,f_revision,f_data FROM "+referenceTable+" WHERE f_project_id=? AND f_id=?", project, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return result, err
}
func (a *references) Put(ctx context.Context, next *interfaces.KBReference, expected int64) (bool, error) {
	if next == nil || !safeID.MatchString(next.ID) || len(next.ID) > 40 || !safeID.MatchString(next.ProjectID) || len(next.ProjectID) > 40 || next.Revision != expected+1 || expected < 0 || next.UpdateTime < 1 || next.Owner.ID == "" || next.Owner.Type != "user" || !hash.MatchString(next.ModelFingerprint) || next.SelectionID == "" || next.SelectionRevision < 1 || next.DraftUpdateTime < 1 || next.GenerationID == "" || len(next.Files) == 0 || len(next.Files) > 1000 {
		return false, nil
	}
	seen := map[string]bool{}
	for _, file := range next.Files {
		if file.File.SampleRole != "acceptance" || file.File.KnowledgeBaseID == "" || file.File.FileID == "" || file.File.ContentVersionID == "" || seen[file.File.Key()] {
			return false, nil
		}
		seen[file.File.Key()] = true
		if file.RowID != "" && (!hash.MatchString(file.RowID) || !hash.MatchString(file.ReviewHash) || !hash.MatchString(file.InputDigest) || file.Reviewer != next.Owner || file.ReviewedAt < 1) {
			return false, nil
		}
	}
	payload, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	owner, archived, err := project(ctx, tx, next.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if archived || owner != next.Owner {
		return false, nil
	}
	active, err := current(ctx, tx, &interfaces.KBBuild{ProjectID: next.ProjectID, SelectionID: next.SelectionID, SelectionRevision: next.SelectionRevision, DraftUpdateTime: next.DraftUpdateTime})
	if err != nil || !active {
		return false, err
	}
	previous, err := readReference(tx.QueryRowContext(ctx, "SELECT f_id,f_project_id,f_revision,f_data FROM "+referenceTable+" WHERE f_project_id=? AND f_id=? FOR UPDATE", next.ProjectID, next.ID))
	if errors.Is(err, sql.ErrNoRows) {
		if expected != 0 {
			return false, nil
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO "+referenceTable+" (f_id,f_project_id,f_revision,f_data,f_update_time) VALUES (?,?,?,?,?)", next.ID, next.ProjectID, next.Revision, string(payload), next.UpdateTime)
	} else {
		if err != nil {
			return false, err
		}
		if previous.Revision != expected || next.UpdateTime <= previous.UpdateTime || previous.Owner != next.Owner || previous.ModelFingerprint != next.ModelFingerprint || previous.SelectionID != next.SelectionID || previous.SelectionRevision != next.SelectionRevision || previous.GenerationID != next.GenerationID || !reflect.DeepEqual(previous.Dataset, next.Dataset) || len(previous.Files) != len(next.Files) {
			return false, nil
		}
		for i := range previous.Files {
			if previous.Files[i].File != next.Files[i].File {
				return false, nil
			}
		}
		var result sql.Result
		result, err = tx.ExecContext(ctx, "UPDATE "+referenceTable+" SET f_revision=?,f_data=?,f_update_time=? WHERE f_project_id=? AND f_id=? AND f_revision=?", next.Revision, string(payload), next.UpdateTime, next.ProjectID, next.ID, expected)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count != 1 {
				return false, errCorrupt
			}
		}
	}
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
