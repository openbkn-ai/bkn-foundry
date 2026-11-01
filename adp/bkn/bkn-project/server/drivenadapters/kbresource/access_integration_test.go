//go:build integration

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbresource

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
	. "github.com/smartystreets/goconvey/convey"
)

func TestSelectionMariaDB(t *testing.T) {
	dsn := os.Getenv("KB_SELECTION_TEST_DSN")
	if dsn == "" {
		t.Skip("KB_SELECTION_TEST_DSN is required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "kb_test_") {
		t.Fatal("a dedicated kb_test_ database is required")
	}
	cfg.MultiStatements = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal("cannot open test database")
	}
	defer db.Close()
	ctx := context.Background()
	// Fresh installations use the full snapshot; upgrades use the additive file.
	for _, file := range []string{"init.sql", "13-kb-selection.sql"} {
		raw, err := os.ReadFile("../../../../../../migrations/bkn-project/mariadb/0.2.1/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, strings.ReplaceAll(string(raw), "USE openbkn;", "")); err != nil {
			t.Fatalf("test schema %s: %v", file, err)
		}
	}
	Convey("Real transactions preserve source and hold lifecycle invariants", t, func() {
		project := "e2e-" + uuid.NewString()
		_, err = db.ExecContext(ctx, "INSERT INTO t_bkn_project_project (f_id,f_name,f_kn_id,f_branch,f_baseline_network_id,f_catalog_ids,f_owner,f_owner_type,f_create_time,f_update_time) VALUES (?,?,?,?,?,'[]','u','user',1,1)", project, "Source test", "e2e-network", "main", "e2e-network")
		So(err, ShouldBeNil)
		defer func() {
			_, _ = db.ExecContext(ctx, "DELETE FROM "+Table+" WHERE f_project_id=?", project)
			_, _ = db.ExecContext(ctx, "DELETE FROM t_bkn_project_project WHERE f_id=?", project)
		}()
		store := New(db)
		makeSelection := func(expected string) *interfaces.KBSelection {
			return &interfaces.KBSelection{ID: uuid.NewString(), ProjectID: project, ExpectedID: expected, InputHash: strings.Repeat("a", 64), State: "pending", Revision: 1, Owner: interfaces.AccountInfo{ID: "u", Type: "user"}, Ranges: []interfaces.KBSourceRange{}, Files: []interfaces.KBResourceFile{{KnowledgeBaseID: "1", FileID: "5", ContentVersionID: "11"}}, Holds: []interfaces.KBHoldGroup{{ID: uuid.NewString(), KnowledgeBaseID: "1", VersionIDs: []string{"11"}}}, CreateTime: 1, UpdateTime: 1}
		}
		first := makeSelection("")
		So(store.Prepare(ctx, first), ShouldBeNil)
		So(store.Activate(ctx, project, first.ID), ShouldBeNil)
		state, err := store.Read(ctx, project)
		So(err, ShouldBeNil)
		So(state.Current.Revision, ShouldEqual, 2)
		Convey("Sample revisions cannot overwrite newer decisions", func() {
			plan := interfaces.KBSamplePlan{Mode: "specified", DebugIDs: []string{"1/5"}}
			files := append([]interfaces.KBResourceFile{}, state.Current.Files...)
			files[0].SampleRole = "debug"
			So(store.SetSamples(ctx, project, first.ID, 2, files, plan), ShouldBeNil)
			So(store.SetSamples(ctx, project, first.ID, 2, files, plan), ShouldEqual, interfaces.ErrKBSelectionConflict)
			got, err := store.Get(ctx, project, first.ID)
			So(err, ShouldBeNil)
			So(got.Files[0].SampleRole, ShouldEqual, "debug")
			So(got.Decisions, ShouldHaveLength, 1)
		})
		Convey("Concurrent replacement intents produce exactly one pending operation", func() {
			left, right := makeSelection(first.ID), makeSelection(first.ID)
			var wg sync.WaitGroup
			outcomes := make(chan error, 2)
			for _, s := range []*interfaces.KBSelection{left, right} {
				wg.Add(1)
				go func() { defer wg.Done(); outcomes <- store.Prepare(ctx, s) }()
			}
			wg.Wait()
			close(outcomes)
			success, conflict := 0, 0
			for err := range outcomes {
				if err == nil {
					success++
				} else if err == interfaces.ErrKBSelectionConflict {
					conflict++
				} else {
					So(err, ShouldBeNil)
				}
			}
			So(success, ShouldEqual, 1)
			So(conflict, ShouldEqual, 1)
		})
		Convey("Retired intents cannot activate and release acknowledgements are required", func() {
			next := makeSelection(first.ID)
			So(store.Prepare(ctx, next), ShouldBeNil)
			So(store.Retire(ctx, project, next.ID), ShouldBeNil)
			So(store.Activate(ctx, project, next.ID), ShouldEqual, interfaces.ErrKBSelectionConflict)
			So(store.MarkReleased(ctx, project, next.ID, ""), ShouldBeNil)
			got, err := store.Get(ctx, project, next.ID)
			So(err, ShouldBeNil)
			So(got.State, ShouldEqual, "release_pending")
			So(store.MarkReleased(ctx, project, next.ID, next.Holds[0].ID), ShouldBeNil)
			got, err = store.Get(ctx, project, next.ID)
			So(err, ShouldBeNil)
			So(got.State, ShouldEqual, "released")
		})
		Convey("Archived projects refuse both new intents and activation", func() {
			next := makeSelection(first.ID)
			So(store.Prepare(ctx, next), ShouldBeNil)
			_, err = db.ExecContext(ctx, "UPDATE t_bkn_project_project SET f_archived=1 WHERE f_id=?", project)
			So(err, ShouldBeNil)
			So(store.Prepare(ctx, makeSelection(first.ID)), ShouldEqual, interfaces.ErrKBSelectionConflict)
			So(store.Activate(ctx, project, next.ID), ShouldEqual, interfaces.ErrKBSelectionConflict)
		})
		Convey("Published references never enter the release queue", func() {
			_, err = db.ExecContext(ctx, "UPDATE "+Table+" SET f_retained=1 WHERE f_id=?", first.ID)
			So(err, ShouldBeNil)
			So(store.Clear(ctx, project), ShouldBeNil)
			pending, err := store.Cleanup(ctx, project)
			So(err, ShouldBeNil)
			So(pending, ShouldBeEmpty)
			So(store.MarkReleased(ctx, project, first.ID, first.Holds[0].ID), ShouldEqual, interfaces.ErrKBSelectionConflict)
		})
	})
}
