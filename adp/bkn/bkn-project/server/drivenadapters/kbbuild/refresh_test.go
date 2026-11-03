// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbbuild

import (
	"context"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"strings"
	"testing"
)

func refreshRows(r *interfaces.KBRefreshRecord) *sqlmock.Rows {
	raw, _ := json.Marshal(r)
	return sqlmock.NewRows(strings.Split(refreshColumns, ",")).AddRow(r.LineageID, r.NetworkID, r.State, r.Revision, string(raw), r.LeaseID, r.LeaseUntil, r.NextCheck, r.UpdateTime)
}
func TestRefreshClaimSerializesWorkersAcrossConsumerSwitches(t *testing.T) {
	for _, scenario := range []string{"claim", "other_live_worker", "same_worker", "expired", "not_due", "attention"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			record := &interfaces.KBRefreshRecord{LineageID: "lineage", NetworkID: "old-network", State: "idle", Revision: 1, UpdateTime: 10}
			switch scenario {
			case "other_live_worker":
				record.LeaseID = "old-worker"
				record.LeaseUntil = 99
			case "same_worker":
				record.LeaseID = "worker"
				record.LeaseUntil = 99
			case "expired":
				record.LeaseID = "old-worker"
				record.LeaseUntil = 19
			case "not_due":
				record.NextCheck = 30
			case "attention":
				record.State = "attention"
			}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT .* FROM " + refreshTable + " WHERE f_lineage_id=.+ FOR UPDATE").WithArgs("lineage").WillReturnRows(refreshRows(record))
			valid := scenario == "claim" || scenario == "same_worker" || scenario == "expired"
			if valid {
				mock.ExpectExec("UPDATE "+refreshTable).WithArgs("worker", int64(60), int64(20), "lineage", int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			result, err := NewRefreshes(db).Claim(context.Background(), "lineage", "worker", 20, 60)
			if err != nil || (result != nil) != valid {
				t.Fatalf("unsafe lineage lease %+v %v", result, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRefreshSaveCannotReplaceAnotherLeaseOrSkipRevision(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	record := &interfaces.KBRefreshRecord{LineageID: "lineage", NetworkID: "network", State: "running", Revision: 3, UpdateTime: 20, LeaseID: "worker", LeaseUntil: 60, Pending: &interfaces.KBRefreshBatch{Build: &interfaces.KBBuild{ID: "batch", ProjectID: "project", ModelFingerprint: strings.Repeat("b", 64), GenerationID: "generation", Files: []interfaces.KBFileBuild{}}}}
	mock.ExpectExec("UPDATE "+refreshTable+" SET").WithArgs("network", "running", int64(3), sqlmock.AnyArg(), "worker", int64(60), int64(0), int64(20), "lineage", int64(2), "worker", int64(20)).WillReturnResult(sqlmock.NewResult(0, 0))
	saved, err := NewRefreshes(db).Save(context.Background(), record, 2, "worker", 20, 60)
	if err != nil || saved {
		t.Fatal("lost lease accepted")
	}
	// Invalid revisions are rejected before reaching SQL.
	if saved, err := NewRefreshes(db).Save(context.Background(), record, 3, "worker", 20, 60); err != nil || saved {
		t.Fatal("skipped revision accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
