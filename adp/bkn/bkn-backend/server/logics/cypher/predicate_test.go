// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"strings"
	"testing"
)

func compileWhere(t *testing.T, where string) string {
	t.Helper()
	sql := mustCompile(t, "MATCH (o:Order) WHERE "+where+" RETURN o.id AS id")
	const prefix = "SELECT t0.`f_id` AS `id` FROM {{.res_order}} t0 WHERE "
	if !strings.HasPrefix(sql, prefix) {
		t.Fatalf("unexpected statement shape: %s", sql)
	}
	return strings.TrimPrefix(sql, prefix)
}

// The generated condition should read like the one that was written: operators
// in the same order, and parentheses only where nesting requires them.
func TestCompilePredicates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		where string
		want  string
	}{
		{
			name:  "or",
			where: "o.region = 'eu' OR o.region = 'us'",
			want:  "t0.`f_region` = 'eu' OR t0.`f_region` = 'us'",
		},
		{
			name:  "and binds tighter than or",
			where: "o.region = 'eu' AND o.amount > 10 OR o.region = 'us'",
			want:  "(t0.`f_region` = 'eu' AND t0.`f_total` > 10) OR t0.`f_region` = 'us'",
		},
		{
			name:  "not",
			where: "NOT o.region = 'eu'",
			want:  "NOT t0.`f_region` = 'eu'",
		},
		{
			name:  "not over a group",
			where: "NOT (o.region = 'eu' OR o.amount > 10)",
			want:  "NOT (t0.`f_region` = 'eu' OR t0.`f_total` > 10)",
		},
		{
			// Two negations cancel: the statement should read like the
			// condition, not like its history.
			name:  "double negation",
			where: "NOT NOT o.region = 'eu'",
			want:  "t0.`f_region` = 'eu'",
		},
		{
			name:  "is null",
			where: "o.region IS NULL",
			want:  "t0.`f_region` IS NULL",
		},
		{
			name:  "is not null",
			where: "o.region IS NOT NULL",
			want:  "t0.`f_region` IS NOT NULL",
		},
		{
			name:  "in",
			where: "o.region IN ['eu', 'us']",
			want:  "t0.`f_region` IN ('eu', 'us')",
		},
		{
			name:  "in over numbers",
			where: "o.amount IN [1, 2.5, -3]",
			want:  "t0.`f_total` IN (1, 2.5, -3)",
		},
		{
			// Cypher says an empty list matches nothing. SQL has no empty IN,
			// so it is written out rather than handed to the database.
			name:  "in over an empty list",
			where: "o.region IN []",
			want:  "1 = 0",
		},
		{
			name:  "in negated by not",
			where: "NOT o.region IN ['eu']",
			want:  "NOT t0.`f_region` IN ('eu')",
		},
		{
			name:  "mixed tree",
			where: "o.region IS NOT NULL AND (o.region IN ['eu'] OR NOT o.amount > 10)",
			want:  "t0.`f_region` IS NOT NULL AND (t0.`f_region` IN ('eu') OR NOT t0.`f_total` > 10)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := compileWhere(t, tc.where); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestCompilePredicateRejections(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "xor has no portable spelling",
			query: "MATCH (o:Order) WHERE o.region = 'eu' XOR o.amount > 1 RETURN o.id",
			want:  "XOR",
		},
		{
			name:  "in over a computed list",
			query: "MATCH (o:Order) WHERE o.region IN o.id RETURN o.id",
			want:  "IN over something other than a list",
		},
		{
			name:  "in over a list holding a property",
			query: "MATCH (o:Order) WHERE o.region IN [o.id] RETURN o.id",
			want:  "a list holding something other than values",
		},
		{
			name:  "null inside an in list",
			query: "MATCH (o:Order) WHERE o.region IN ['eu', null] RETURN o.id",
			want:  "null in an IN list",
		},
		{
			name:  "is null on something that is not a property",
			query: "MATCH (o:Order) WHERE 1 IS NULL RETURN o.id",
			want:  "IN and IS NULL apply to variable.property",
		},
		{
			name:  "comparing two conditions",
			query: "MATCH (o:Order) WHERE (o.id IS NULL) = (o.region IS NULL) RETURN o.id",
			want:  "comparing a condition",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compile(t, tc.query, GenerateOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compile(%q) = %v, want a rejection mentioning %q", tc.query, err, tc.want)
			}
		})
	}
}
