//go:build integration

// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbbuild

import (
	"context"
	"database/sql"
	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRefreshJournalResumesOnlyAcknowledgedTasksUnderOneLineageLeaseInMariaDB(t *testing.T) {
	dsn := os.Getenv("KB_PUBLICATION_TEST_DSN")
	if dsn == "" {
		t.Skip("KB_PUBLICATION_TEST_DSN is required")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(config.DBName, "kb_test_") {
		t.Fatal("a dedicated kb_test_ schema is required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(5)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	lineage := "r_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	defer func() { _, _ = db.Exec("DELETE FROM "+refreshTable+" WHERE f_lineage_id=?", lineage) }()
	store := NewRefreshes(db)
	if err := store.Ensure(ctx, lineage, 10); err != nil {
		t.Fatal(err)
	}
	type result struct {
		record *interfaces.KBRefreshRecord
		err    error
	}
	results := make(chan result, 2)
	for _, worker := range []string{"worker-a", "worker-b"} {
		go func(worker string) {
			record, err := store.Claim(ctx, lineage, worker, 20, 60)
			results <- result{record, err}
		}(worker)
	}
	var winner *interfaces.KBRefreshRecord
	wins := 0
	for range 2 {
		outcome := <-results
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if outcome.record != nil {
			winner = outcome.record
			wins++
		}
	}
	if wins != 1 {
		t.Fatal("concurrent workers both acquired the lineage", wins)
	}
	winner.NetworkID = "release-v1"
	winner.State = "running"
	winner.Revision++
	winner.UpdateTime = 21
	winner.Pending = &interfaces.KBRefreshBatch{Build: &interfaces.KBBuild{ID: "batch", ProjectID: "archived-project", GenerationID: "generation", ModelFingerprint: strings.Repeat("b", 64), Files: []interfaces.KBFileBuild{{File: interfaces.KBResourceFile{KnowledgeBaseID: "1", FileID: "2", ContentVersionID: "3"}, State: "waiting", AgentTaskID: "actual-acknowledged-task"}}}, Changed: []int{0}}
	if ok, err := store.Save(ctx, winner, 1, winner.LeaseID, 21, 60); err != nil || !ok {
		t.Fatal("cannot persist actual task receipt", err)
	}
	if err := store.Ensure(ctx, lineage, 30); err != nil {
		t.Fatal(err)
	}
	resumed, err := store.Claim(ctx, lineage, "replacement-worker", 61, 100)
	if err != nil || resumed == nil {
		t.Fatal("expired worker lease cannot be resumed", err)
	}
	if resumed.Pending.Build.Files[0].AgentTaskID != "actual-acknowledged-task" || resumed.Revision != 2 {
		t.Fatal("resume reset actual Agent acknowledgement")
	}
	winner.Revision++
	winner.UpdateTime = 62
	if ok, err := store.Save(ctx, winner, 2, winner.LeaseID, 62, 100); err != nil || ok {
		t.Fatal("obsolete worker overwrote replacement lease", err)
	}
	resumed.Revision++
	resumed.State = "attention"
	resumed.Reason = "submission_unknown"
	resumed.UpdateTime = 63
	if ok, err := store.Save(ctx, resumed, 2, resumed.LeaseID, 63, 100); err != nil || !ok {
		t.Fatal("uncertainty cannot be journaled", err)
	}
	if next, err := store.Claim(ctx, lineage, "third-worker", 101, 150); err != nil || next != nil {
		t.Fatal("uncertain submission was automatically runnable", err)
	}
}
