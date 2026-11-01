//go:build integration

// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestDocumentCandidateCommitMariaDB(t *testing.T) {
	dsn := os.Getenv("KB_SELECTION_TEST_DSN")
	if dsn == "" {
		t.Skip("dedicated KB_SELECTION_TEST_DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "kb_test_") {
		t.Fatal("dedicated kb_test_ schema required")
	}
	cfg.MultiStatements = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, name := range []string{"init.sql", "13-kb-selection.sql"} {
		raw, err := os.ReadFile("../../../../../../migrations/bkn-project/mariadb/0.2.1/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, strings.ReplaceAll(string(raw), "USE openbkn;", "")); err != nil {
			t.Fatal("cannot initialize dedicated test tables", err)
		}
	}
	project := "e2e-" + uuid.NewString()
	selection := uuid.NewString()
	_, err = db.ExecContext(ctx, "INSERT INTO t_bkn_project_project (f_id,f_name,f_kn_id,f_branch,f_baseline_network_id,f_catalog_ids,f_owner,f_owner_type,f_create_time,f_update_time) VALUES (?,?,?,?,?,'[]','u','user',1,1)", project, "Candidate test", "e2e-network", "main", "e2e-network")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"t_bkn_project_draft", "t_bkn_project_kb_selection"} {
			_, _ = db.ExecContext(ctx, "DELETE FROM "+table+" WHERE f_project_id=?", project)
		}
		_, _ = db.ExecContext(ctx, "DELETE FROM t_bkn_project_project WHERE f_id=?", project)
	}()
	_, err = db.ExecContext(ctx, "INSERT INTO t_bkn_project_kb_selection (f_id,f_project_id,f_state,f_current,f_retained,f_revision,f_expected_id,f_input_hash,f_data,f_holds,f_create_time,f_update_time) VALUES (?,?,'active',1,0,3,'','','{}','[]',1,1)", selection, project)
	if err != nil {
		t.Fatal(err)
	}
	store := &draftAccess{db: db}
	owner := interfaces.AccountInfo{ID: "u", Type: "user"}
	guard := interfaces.DocumentDraftGuard{Owner: owner, SelectionID: selection, SelectionRevision: 3}
	draft := &interfaces.Draft{ProjectID: project, Status: "ready", Drafter: owner, CreateTime: 5, UpdateTime: 5}
	ok, err := store.CommitDocumentDraft(ctx, draft, guard)
	if err != nil || !ok {
		t.Fatalf("first draft: %v %v", ok, err)
	}
	// Two valid extractions based on one draft snapshot cannot both overwrite it.
	guard.DraftExists = true
	guard.DraftUpdateTime = 5
	results := make(chan bool, 2)
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"Order", "Patent"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			next := *draft
			next.UpdateTime = 6
			next.Items.ObjectTypes = []*interfaces.DraftObjectType{{ID: "business", Name: name}}
			ok, err := store.CommitDocumentDraft(ctx, &next, guard)
			results <- ok
			failures <- err
		}(name)
	}
	wg.Wait()
	close(results)
	close(failures)
	writes := 0
	for ok := range results {
		if ok {
			writes++
		}
	}
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if writes != 1 {
		t.Fatalf("expected one guarded winner, got %d", writes)
	}
	guard.DraftUpdateTime = 6
	draft.UpdateTime = 7
	for _, scenario := range []string{"archived", "owner", "selection", "revision"} {
		switch scenario {
		case "archived":
			_, err = db.ExecContext(ctx, "UPDATE t_bkn_project_project SET f_archived=1 WHERE f_id=?", project)
		case "owner":
			_, err = db.ExecContext(ctx, "UPDATE t_bkn_project_project SET f_archived=0,f_owner='other' WHERE f_id=?", project)
		case "selection":
			_, err = db.ExecContext(ctx, "UPDATE t_bkn_project_project SET f_owner='u' WHERE f_id=?", project)
			if err == nil {
				_, err = db.ExecContext(ctx, "UPDATE t_bkn_project_kb_selection SET f_state='released' WHERE f_id=?", selection)
			}
		case "revision":
			_, err = db.ExecContext(ctx, "UPDATE t_bkn_project_kb_selection SET f_state='active',f_revision=4 WHERE f_id=?", selection)
		}
		if err != nil {
			t.Fatal(err)
		}
		ok, err = store.CommitDocumentDraft(ctx, draft, guard)
		if err != nil || ok {
			t.Fatalf("%s guard failed: %v %v", scenario, ok, err)
		}
	}
	current, exists, err := store.GetDraft(ctx, project)
	if err != nil || !exists || current.UpdateTime != 6 {
		t.Fatal("rejected extraction changed stored draft")
	}
}
