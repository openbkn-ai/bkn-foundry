//go:build integration

// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbbuild

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestPublicationSealAndWriterClaimSerializeInMariaDB(t *testing.T) {
	raw := os.Getenv("KB_PUBLICATION_TEST_DSN")
	if raw == "" {
		t.Skip("KB_PUBLICATION_TEST_DSN is required")
	}
	config, err := mysql.ParseDSN(raw)
	if err != nil || !strings.HasPrefix(config.DBName, "kb_test_") {
		t.Fatal("a dedicated kb_test_ schema is required")
	}
	db, err := sql.Open("mysql", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(5)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	projectID := "k6_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	defer func() {
		for _, table := range []string{publicationTable, "t_bkn_project_kb_build", "t_bkn_project_kb_selection", "t_bkn_project_draft"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE f_project_id=?", projectID)
		}
		_, _ = db.Exec("DELETE FROM t_bkn_project_project WHERE f_id=?", projectID)
	}()
	r := processRecord()
	r.Build.ProjectID = projectID
	r.Build.Mode = "full"
	r.Build.State = "succeeded"
	r.Build.Files[0].State = "succeeded"
	r.Identity.Cleaned = true
	if _, err := db.ExecContext(ctx, "INSERT INTO t_bkn_project_project (f_id,f_catalog_ids,f_kn_id,f_branch,f_owner,f_owner_type) VALUES (?,'[]','lineage','project/test','owner','user')", projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO t_bkn_project_kb_selection (f_id,f_project_id,f_state,f_current,f_revision,f_input_hash,f_data,f_holds,f_create_time,f_update_time) VALUES ('selection',?,'active',1,7,?,'{}','[]',1,1)", projectID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO t_bkn_project_draft (f_project_id,f_status,f_items,f_error,f_update_time) VALUES (?,'ready','{}','',10)", projectID); err != nil {
		t.Fatal(err)
	}
	payload, identity, err := encode(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO "+table+" ("+columns+") VALUES (?,?,?,?,?,?,?,?,?,?,?)", r.Build.ID, projectID, r.Build.State, r.Build.Revision, r.InputHash, payload, identity, "", 0, r.Build.CreateTime, r.Build.UpdateTime); err != nil {
		t.Fatal(err)
	}
	p := publicationFixture()
	p.ProjectID = projectID
	p.Snapshot.ProjectID = projectID
	store := NewPublications(db)
	if ok, err := store.Prepare(ctx, p); err != nil || !ok {
		t.Fatal("publication intent not prepared", err)
	}
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	if _, _, err := project(ctx, blocker, projectID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		seal, ok bool
		err      error
	}
	results := make(chan result, 2)
	started := make(chan struct{}, 2)
	go func() {
		started <- struct{}{}
		ok, err := store.Seal(ctx, p.NetworkID, p.RuntimeDigest, 30)
		results <- result{seal: true, ok: ok, err: err}
	}()
	go func() {
		started <- struct{}{}
		record, err := New(db).(interfaces.KBLinkingAccess).ClaimLinking(ctx, projectID, r.Build.ID, r.Build.Owner, "operation", strings.Repeat("e", 64), 30, 90)
		results <- result{ok: record != nil, err: err}
	}()
	<-started
	<-started
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	wins := 0
	for i := 0; i < 2; i++ {
		select {
		case item := <-results:
			if item.err != nil {
				t.Fatal("concurrent scope guard failed", item.err)
			}
			if item.ok {
				wins++
			}
		case <-ctx.Done():
			t.Fatal("scope guard deadlocked")
		}
	}
	if wins != 1 {
		t.Fatal("a generation was sealed while its writer could also run", wins)
	}
}
