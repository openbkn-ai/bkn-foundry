// Copyright openbkn.ai

//go:build integration

package integration

import (
	"context"
	"database/sql"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/kn_version"
)

// Needs the 0.2.2 schema or later (f_revision, capability binding keyed by branch).
func TestBranchCopierReplacesAndDeletesBranches(t *testing.T) {
	setting, db := openDB(t)
	ctx := context.Background()
	knID := newKN(t, db)
	t.Cleanup(func() {
		for _, table := range []string{"t_object_metric_reference", "t_object_metric_definition",
			"t_kn_capability_binding", "t_risk_type", "t_value_type", "t_concept_group_relation"} {
			if _, err := db.Exec("DELETE FROM "+table+" WHERE f_kn_id = ?", knID); err != nil {
				t.Errorf("clean %s: %v", table, err)
			}
		}
	})
	copier := kn_version.NewBranchCopier(setting)

	seed := []string{
		"INSERT INTO t_knowledge_network (f_id, f_name, f_branch, f_comment, f_bkn_raw_content, f_skill_content) VALUES (?, ?, 'main', 'c', 'raw-main', 'skill-main')",
		"INSERT INTO t_object_type (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content, f_data_source, f_revision) VALUES (?, 'ot1', 'ot1', 'main', '', '', '', 'rev-ot1')",
		"INSERT INTO t_object_type_status (f_kn_id, f_id, f_branch) VALUES (?, 'ot1', 'main')",
		"INSERT INTO t_relation_type (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content) VALUES (?, 'rt1', 'rt1', 'main', '', '')",
		"INSERT INTO t_action_type (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content, f_action_source) VALUES (?, 'at1', 'at1', 'main', '', '', '')",
		"INSERT INTO t_concept_group (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content) VALUES (?, 'cg1', 'cg1', 'main', '', '')",
		"INSERT INTO t_concept_group_relation (f_id, f_kn_id, f_branch, f_group_id, f_concept_type, f_concept_id) VALUES ('cgr-main', ?, 'main', 'cg1', 'object_type', 'ot1')",
		"INSERT INTO t_value_type (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content) VALUES (?, 'vt1', 'vt1', 'main', '', '')",
		"INSERT INTO t_risk_type (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content) VALUES (?, 'rk1', 'rk1', 'main', '', '')",
		"INSERT INTO t_kn_capability_binding (f_id, f_kn_id, f_branch, f_capability_type, f_owner_id, f_capability_id) VALUES ('b1', ?, 'main', 'skill', '', 's1')",
		"INSERT INTO t_object_metric_definition (f_kn_id, f_branch, f_id, f_version, f_code, f_description, f_metric_type, f_definition) VALUES (?, 'main', 'm1', 1, 'm1', '', '', '')",
		"INSERT INTO t_object_metric_reference (f_kn_id, f_branch, f_source_id, f_target_metric_id, f_target_version) VALUES (?, 'main', 'ot1', 'm1', 1)",
	}
	for i, statement := range seed {
		args := []any{knID}
		if i == 0 {
			args = append(args, knID)
		}
		if _, err := db.Exec(statement, args...); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	tables := []string{"t_object_type", "t_object_type_status", "t_relation_type", "t_action_type",
		"t_concept_group", "t_concept_group_relation", "t_value_type", "t_risk_type",
		"t_kn_capability_binding", "t_object_metric_definition", "t_object_metric_reference"}
	count := func(table, branch string) int {
		t.Helper()
		var n int
		must(t, db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE f_kn_id = ? AND f_branch = ?", knID, branch).Scan(&n))
		return n
	}
	content := func(branch string) (raw, skill string) {
		t.Helper()
		must(t, db.QueryRow("SELECT f_bkn_raw_content, f_skill_content FROM t_knowledge_network WHERE f_id = ? AND f_branch = ?",
			knID, branch).Scan(&raw, &skill))
		return raw, skill
	}

	// main -> v1 creates every row and the network row.
	must(t, inTx(t, db, func(tx *sql.Tx) error { return copier.ReplaceBranch(ctx, tx, knID, "main", "v1") }))
	for _, table := range tables {
		if got := count(table, "v1"); got != 1 {
			t.Errorf("%s on v1 = %d rows, want 1", table, got)
		}
	}
	var revision, bindingID, relationID string
	must(t, db.QueryRow("SELECT f_revision FROM t_object_type WHERE f_kn_id = ? AND f_branch = 'v1'", knID).Scan(&revision))
	must(t, db.QueryRow("SELECT f_id FROM t_kn_capability_binding WHERE f_kn_id = ? AND f_branch = 'v1'", knID).Scan(&bindingID))
	must(t, db.QueryRow("SELECT f_id FROM t_concept_group_relation WHERE f_kn_id = ? AND f_branch = 'v1'", knID).Scan(&relationID))
	if revision != "rev-ot1" || bindingID != "b1" || relationID == "cgr-main" {
		t.Fatalf("v1 revision %q binding %q relation %q: want the revision and binding id kept, a new relation id",
			revision, bindingID, relationID)
	}
	if raw, skill := content("v1"); raw != "raw-main" || skill != "skill-main" {
		t.Fatalf("v1 network content = %q, %q", raw, skill)
	}

	// A draft edited away from main, then replaced by main again: no duplicates, content back.
	must(t, inTx(t, db, func(tx *sql.Tx) error { return copier.ReplaceBranch(ctx, tx, knID, "main", "draft") }))
	if _, err := db.Exec("UPDATE t_knowledge_network SET f_name = 'renamed' WHERE f_id = ?", knID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE t_knowledge_network SET f_skill_content = 'skill-draft' WHERE f_id = ? AND f_branch = 'draft'", knID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM t_relation_type WHERE f_kn_id = ? AND f_branch = 'draft'", knID); err != nil {
		t.Fatal(err)
	}
	must(t, inTx(t, db, func(tx *sql.Tx) error { return copier.ReplaceBranch(ctx, tx, knID, "v1", "draft") }))
	for _, table := range tables {
		if got := count(table, "draft"); got != 1 {
			t.Errorf("%s on draft = %d rows after replace, want 1", table, got)
		}
	}
	var name string
	must(t, db.QueryRow("SELECT f_name FROM t_knowledge_network WHERE f_id = ? AND f_branch = 'draft'", knID).Scan(&name))
	if _, skill := content("draft"); skill != "skill-main" || name != "renamed" {
		t.Fatalf("draft skill %q name %q: want the version's skill and the current name kept", skill, name)
	}
	// Replacing with the same content affects no rows and still succeeds.
	must(t, inTx(t, db, func(tx *sql.Tx) error { return copier.ReplaceBranch(ctx, tx, knID, "v1", "draft") }))

	// A branch with no network row cannot be a source.
	if err := inTx(t, db, func(tx *sql.Tx) error { return copier.ReplaceBranch(ctx, tx, knID, "v9", "draft") }); err == nil {
		t.Fatal("replace from a missing branch succeeded")
	}

	// DeleteBranch removes the network row as well.
	must(t, inTx(t, db, func(tx *sql.Tx) error { return copier.DeleteBranch(ctx, tx, knID, "v1") }))
	for _, table := range append(tables, "t_knowledge_network") {
		column := "f_kn_id"
		if table == "t_knowledge_network" {
			column = "f_id"
		}
		var n int
		must(t, db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+column+" = ? AND f_branch = 'v1'", knID).Scan(&n))
		if n != 0 {
			t.Errorf("%s keeps %d rows on v1 after DeleteBranch", table, n)
		}
	}
}
