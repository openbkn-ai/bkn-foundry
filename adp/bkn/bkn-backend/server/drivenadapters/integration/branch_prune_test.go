// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

//go:build integration

package integration

import (
	"context"
	"database/sql"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/kn_version"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestBranchCopierPrunesLeftOutDefinitions(t *testing.T) {
	setting, db := openDB(t)
	knID := newKN(t, db)
	t.Cleanup(func() {
		for _, table := range []string{"t_concept_group_relation", "t_value_type", "t_risk_type"} {
			if _, err := db.Exec("DELETE FROM "+table+" WHERE f_kn_id = ?", knID); err != nil {
				t.Errorf("clean %s: %v", table, err)
			}
		}
	})
	for _, branch := range []string{"draft", "main"} {
		for _, statement := range []string{
			"INSERT INTO t_object_type (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content, f_data_source) VALUES (?, 'ot1', 'ot1', ?, '', '', '')",
			"INSERT INTO t_object_type (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content, f_data_source) VALUES (?, 'ot2', 'ot2', ?, '', '', '')",
			"INSERT INTO t_object_type_status (f_kn_id, f_id, f_branch) VALUES (?, 'ot2', ?)",
			"INSERT INTO t_concept_group (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content) VALUES (?, 'cg1', 'cg1', ?, '', '')",
			"INSERT INTO t_concept_group (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content) VALUES (?, 'cg2', 'cg2', ?, '', '')",
			"INSERT INTO t_concept_group_relation (f_id, f_kn_id, f_branch, f_group_id, f_concept_type, f_concept_id) VALUES (UUID(), ?, ?, 'cg1', 'object_type', 'ot1')",
			"INSERT INTO t_concept_group_relation (f_id, f_kn_id, f_branch, f_group_id, f_concept_type, f_concept_id) VALUES (UUID(), ?, ?, 'cg1', 'object_type', 'ot2')",
			"INSERT INTO t_concept_group_relation (f_id, f_kn_id, f_branch, f_group_id, f_concept_type, f_concept_id) VALUES (UUID(), ?, ?, 'cg2', 'object_type', 'ot1')",
			"INSERT INTO t_risk_type (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content) VALUES (?, 'rk1', 'rk1', ?, '', '')",
		} {
			if _, err := db.Exec(statement, knID, branch); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
	}
	copier := kn_version.NewBranchCopier(setting)
	// Keep ot1 and cg1, drop every risk type, leave value types alone.
	err := inTx(t, db, func(tx *sql.Tx) error {
		return copier.PruneBranch(context.Background(), tx, knID, "draft", map[string][]string{
			interfaces.MODULE_TYPE_OBJECT_TYPE:   {"ot1"},
			interfaces.MODULE_TYPE_CONCEPT_GROUP: {"cg1"},
			interfaces.MODULE_TYPE_RISK_TYPE:     {},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := func(query, branch string) []string {
		rows, err := db.Query(query, knID, branch)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			out = append(out, id)
		}
		return out
	}
	join := func(v []string) string {
		s := ""
		for _, x := range v {
			s += x + ","
		}
		return s
	}
	for _, check := range []struct{ query, branch, want string }{
		{"SELECT f_id FROM t_object_type WHERE f_kn_id = ? AND f_branch = ? ORDER BY f_id", "draft", "ot1,"},
		{"SELECT f_id FROM t_object_type_status WHERE f_kn_id = ? AND f_branch = ? ORDER BY f_id", "draft", ""},
		{"SELECT f_id FROM t_concept_group WHERE f_kn_id = ? AND f_branch = ? ORDER BY f_id", "draft", "cg1,"},
		{"SELECT CONCAT(f_group_id, '/', f_concept_id) FROM t_concept_group_relation WHERE f_kn_id = ? AND f_branch = ? ORDER BY 1", "draft", "cg1/ot1,"},
		{"SELECT f_id FROM t_risk_type WHERE f_kn_id = ? AND f_branch = ?", "draft", ""},
		// Other branches are untouched.
		{"SELECT f_id FROM t_object_type WHERE f_kn_id = ? AND f_branch = ? ORDER BY f_id", "main", "ot1,ot2,"},
		{"SELECT CONCAT(f_group_id, '/', f_concept_id) FROM t_concept_group_relation WHERE f_kn_id = ? AND f_branch = ? ORDER BY 1", "main", "cg1/ot1,cg1/ot2,cg2/ot1,"},
	} {
		if got := join(ids(check.query, check.branch)); got != check.want {
			t.Errorf("%s on %s: %q, want %q", check.query, check.branch, got, check.want)
		}
	}
}
