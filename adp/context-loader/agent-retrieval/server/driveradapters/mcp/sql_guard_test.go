// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

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
		// 占位符内的词不应触发关键字判定
		`SELECT * FROM {{.delete_logs}}`,
		`SELECT * FROM {{.update_history}}`,
		// 字符串字面量中的关键字不应触发判定
		`SELECT * FROM {{.res1}} WHERE note = 'please delete later'`,
	}
	for _, sql := range cases {
		if err := ensureReadOnlySQL(sql); err != nil {
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
		// 多语句（注入式）
		`SELECT * FROM {{.res1}}; DROP TABLE {{.res1}}`,
		`SELECT 1; SELECT 2`,
		// 用注释藏第二语句仍应被识别为多语句或非 SELECT 起手
		`SELECT * FROM {{.res1}} -- harmless
		 ; DELETE FROM {{.res1}}`,
		// SELECT ... INTO OUTFILE 写文件
		`SELECT * INTO OUTFILE '/tmp/x' FROM {{.res1}}`,
		// 不以 SELECT/WITH 起手
		`SHOW TABLES`,
		`CALL some_proc()`,
	}
	for _, sql := range cases {
		if err := ensureReadOnlySQL(sql); err == nil {
			t.Errorf("expected rejected, got nil error for %q", sql)
		}
	}
}

func TestExtractResourceIDs(t *testing.T) {
	got := extractResourceIDs(`SELECT * FROM {{.res_a}} JOIN {{res_b}} ON 1=1 WHERE x IN (SELECT y FROM {{.res_a}})`)
	want := []string{"res_a", "res_b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractResourceIDs = %v, want %v", got, want)
	}
	if ids := extractResourceIDs(`SELECT 1`); len(ids) != 0 {
		t.Errorf("expected no ids, got %v", ids)
	}
}
