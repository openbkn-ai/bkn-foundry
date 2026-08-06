// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knrunsql

import (
	"reflect"
	"testing"
)

func TestEnsureReadOnlySQL_Allowed(t *testing.T) {
	cases := []string{
		`SELECT * FROM {{.res1}} LIMIT 10`,
		`select id, name from {{.res1}} where age > 18`,
		`WITH t AS (SELECT * FROM {{.res1}}) SELECT * FROM t`,
		`(SELECT 1 FROM {{.res1}}) UNION (SELECT 2 FROM {{.res2}})`,
		`SELECT * FROM {{.res1}};`, // 单个结尾分号允许
		`SELECT * FROM {{.delete_logs}}`,
		`SELECT * FROM {{.update_history}}`,
		`SELECT * FROM {{.res1}} WHERE note = 'please delete later'`,
		`SELECT COUNT(*) FROM {{.res_audit}} WHERE op = "delete" GROUP BY user_id`,
		`SELECT * FROM {{.res_logs}} WHERE note = 'it\'s fine' AND op = 'delete'`,
		"SELECT * FROM {{.res_logs}} WHERE note = 'it''s fine' AND op = 'delete'",
		"SELECT `delete` FROM {{.res_logs}}",
		`SELECT * FROM {{.res-001}}`,
	}
	for _, sql := range cases {
		if err := EnsureReadOnlySQL(sql); err != nil {
			t.Errorf("expected allowed, got error for %q: %v", sql, err)
		}
	}
}

func TestEnsureReadOnlySQL_Rejected(t *testing.T) {
	cases := []string{
		``,
		`   `,
		`DELETE FROM {{.res1}}`,
		`DROP TABLE {{.res1}}`,
		`INSERT INTO {{.res1}} VALUES (1)`,
		`UPDATE {{.res1}} SET a = 1`,
		`TRUNCATE TABLE {{.res1}}`,
		`ALTER TABLE {{.res1}} ADD COLUMN x int`,
		`CREATE TABLE x (a int)`,
		`GRANT ALL ON {{.res1}} TO u`,
		`SELECT * FROM {{.res1}}; DROP TABLE {{.res1}}`,
		`SELECT 1; SELECT 2`,
		`SELECT * FROM {{.res1}} -- harmless
			 ; DELETE FROM {{.res1}}`,
		"SELECT `a\\` , x FROM {{.res1}}; DROP TABLE t",
		`SELECT "a\" , x FROM {{.res1}}; DROP TABLE t`,
		`SELECT * INTO OUTFILE '/tmp/x' FROM {{.res1}}`,
		`SHOW TABLES`,
		`CALL some_proc()`,
	}
	for _, sql := range cases {
		if err := EnsureReadOnlySQL(sql); err == nil {
			t.Errorf("expected rejected, got nil error for %q", sql)
		}
	}
}

func TestExtractResourceIDs(t *testing.T) {
	got := ExtractResourceIDs(`SELECT * FROM {{.res_a}} JOIN {{res_b}} ON 1=1 WHERE x IN (SELECT y FROM {{.res_a}})`)
	want := []string{"res_a", "res_b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractResourceIDs = %v, want %v", got, want)
	}
	if ids := ExtractResourceIDs(`SELECT 1`); len(ids) != 0 {
		t.Errorf("expected no ids, got %v", ids)
	}
	if ids := ExtractResourceIDs(`SELECT * FROM {{.res-001}}`); !reflect.DeepEqual(ids, []string{"res-001"}) {
		t.Errorf("expected hyphenated resource id, got %v", ids)
	}
	if ids := ExtractResourceIDs(`SELECT * FROM {{.RES-001}}`); len(ids) != 0 {
		t.Errorf("expected uppercase resource id to be rejected, got %v", ids)
	}
}
