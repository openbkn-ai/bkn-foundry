// Copyright openbkn.ai

//go:build integration

package integration

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/action_schedule"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/knowledge_network"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// Deleting a network reads every branch it is on and removes its schedules on all of them.
func TestKNDeleteFindsEveryBranchAndSchedule(t *testing.T) {
	setting, db := openDB(t)
	ctx := context.Background()
	knID := newKN(t, db)
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM t_action_schedule WHERE f_kn_id = ?", knID); err != nil {
			t.Errorf("clean t_action_schedule: %v", err)
		}
	})
	kna := knowledge_network.NewKNAccess(setting)
	asa := action_schedule.NewActionScheduleAccess(setting)
	branches := []string{otherBranch, interfaces.MAIN_BRANCH}
	for _, branch := range branches {
		must(t, inTx(t, db, func(tx *sql.Tx) error {
			return kna.CreateKN(ctx, tx, &interfaces.KN{KNID: knID, KNName: knID, Branch: branch})
		}))
		must(t, asa.CreateSchedule(ctx, nil, &interfaces.ActionSchedule{
			ID: knID + "-" + branch, Name: branch, KNID: knID, Branch: branch,
			ActionTypeID: "at", CronExpression: "* * * * *", Status: interfaces.ScheduleStatusInactive,
		}))
	}

	got, err := kna.ListKNBranches(ctx, knID)
	must(t, err)
	if !reflect.DeepEqual(got, branches) {
		t.Fatalf("branches = %v, want %v", got, branches)
	}

	must(t, inTx(t, db, func(tx *sql.Tx) error {
		if err := asa.DeleteSchedulesByKnID(ctx, tx, knID); err != nil {
			return err
		}
		for _, branch := range branches {
			if _, err := kna.DeleteKN(ctx, tx, knID, branch); err != nil {
				return err
			}
		}
		return nil
	}))

	var schedules int
	must(t, db.QueryRow("SELECT COUNT(*) FROM t_action_schedule WHERE f_kn_id = ?", knID).Scan(&schedules))
	if schedules != 0 {
		t.Fatalf("%d schedules left after deleting the network", schedules)
	}
	if got, err := kna.ListKNBranches(ctx, knID); err != nil || len(got) != 0 {
		t.Fatalf("branches after delete = %v, %v, want none", got, err)
	}
}
