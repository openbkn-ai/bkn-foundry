// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"reflect"
	"strings"
	"testing"
)

func compileWithClause(t *testing.T, query string) (string, error) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		return "", err
	}
	plan, err := Compile(analyzed, bindJoinSchema(t), CompileOptions{})
	if err != nil {
		return "", err
	}
	return Generate(plan, GenerateOptions{})
}

func TestWithGenerates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  []string
	}{
		{
			name: "a node groups by its key, and the aggregate is tested per group",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(*) AS n WHERE n > 1 " +
				"RETURN o.region AS region, n ORDER BY n DESC",
			want: []string{"SELECT t1.`f_region` AS `region`, COUNT(*) AS `n`", "GROUP BY t1.`f_id`, t1.`f_region`",
				"HAVING COUNT(*) > 1", "ORDER BY `n` DESC"},
		},
		{
			name:  "a condition on a grouped value filters rows before grouping",
			query: "MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE r = 'east' AND n >= 1 RETURN r, n",
			want:  []string{"WHERE t0.`f_region` = 'east'", "GROUP BY t0.`f_region`", "HAVING COUNT(*) >= 1"},
		},
		{
			name:  "a condition on an aggregate that is not returned",
			query: "MATCH (o:Order) WITH o.region AS r, sum(o.amount) AS total WHERE total > 60 RETURN r",
			want:  []string{"SELECT t0.`f_region` AS `r`", "GROUP BY t0.`f_region`", "HAVING SUM(t0.`f_amount`) > 60"},
		},
		{
			name:  "a WITH that only renames",
			query: "MATCH (o:Order) WITH o.amount AS a WHERE a > 30 RETURN a ORDER BY a",
			want:  []string{"SELECT t0.`f_amount` AS `a`", "WHERE t0.`f_amount` > 30", "ORDER BY `a`"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, err := compileWithClause(t, tc.query)
			if err != nil {
				t.Fatalf("%s: %v", tc.query, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(sql, want) {
					t.Errorf("SQL lacks %q:\n%s", want, sql)
				}
			}
		})
	}
}

// The executor answers a WITH the way the SQL path does: groups by what the
// WITH grouped by, then keeps the groups its condition holds for.
func TestWithEvaluates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  [][]any
	}{
		{
			name: "orders with more than one item",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(*) AS n WHERE n > 1 " +
				"RETURN o.region AS region, n",
			want: [][]any{{"east", 2}},
		},
		{
			name:  "two objects sharing a property stay two groups",
			query: "MATCH (o:Order) WITH o, count(*) AS n RETURN o.region AS r, n ORDER BY r",
			want:  [][]any{{nil, 1}, {"east", 1}, {"east", 1}, {"west", 1}},
		},
		{
			name:  "a condition on a grouped value",
			query: "MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE r = 'east' RETURN r, n",
			want:  [][]any{{"east", 2}},
		},
		{
			name:  "a condition on an aggregate that is not returned",
			query: "MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE n >= 2 RETURN r",
			want:  [][]any{{"east"}},
		},
		{
			name:  "sorting by an aggregate that is not returned",
			query: "MATCH (o:Order) WITH o.region AS r, count(*) AS n RETURN r ORDER BY n DESC, r",
			want:  [][]any{{"east"}, {nil}, {"west"}},
		},
		{
			name:  "sum tested per group",
			query: "MATCH (o:Order) WITH o.region AS r, sum(o.amount) AS total WHERE total > 60 RETURN r, total ORDER BY r",
			want:  [][]any{{"east", 70}, {"west", 70}},
		},
		{
			name:  "one group, condition fails",
			query: "MATCH (o:Order) WITH count(*) AS n WHERE n > 10 RETURN n",
			want:  [][]any{},
		},
		{
			name:  "one group, condition holds",
			query: "MATCH (o:Order) WITH count(*) AS n WHERE n > 1 RETURN n",
			want:  [][]any{{4}},
		},
		{
			name:  "a WITH that only renames, counted",
			query: "MATCH (o:Order) WITH o.amount AS a WHERE a > 30 RETURN count(*) AS n",
			want:  [][]any{{2}},
		},
		{
			name:  "a renamed node",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o AS ord, count(i) AS n RETURN ord.id AS id, n ORDER BY id",
			want:  [][]any{{1, 2}, {2, 1}},
		},
		{
			name: "an optional node counted per group",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) WITH o, count(i) AS items WHERE items = 0 " +
				"RETURN o.id AS id ORDER BY id",
			want: [][]any{{3}, {4}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, _ := mustRunFake(t, tc.query, nil)
			if len(rows) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(rows, tc.want) {
				t.Fatalf("rows = %v, want %v", rows, tc.want)
			}
		})
	}
}

func TestWithRefuses(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"MATCH (o:Order) WITH DISTINCT o.region AS r RETURN r", "WITH DISTINCT"},
		{"MATCH (o:Order) WITH * RETURN o.id", "WITH *"},
		{"MATCH (o:Order) WITH o.region AS r ORDER BY r RETURN r", "ORDER BY, SKIP or LIMIT on WITH"},
		{"MATCH (o:Order) WITH o.region RETURN o.region", "an unnamed value in WITH"},
		{"MATCH (o:Order) WITH o MATCH (o)-[:PLACED_BY]->(c:Customer) RETURN c.name", "MATCH after WITH"},
		{"MATCH (o:Order) WITH o WITH o RETURN o.id", "more than one WITH"},
		{"MATCH (o:Order) WITH o.region AS r, count(*) AS n RETURN sum(n)", "this RETURN after WITH"},
		{"MATCH (o:Order) WITH o.region AS r RETURN o.amount", "not visible after the WITH"},
		{"MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE o.amount > 1 RETURN r", "not visible after the WITH"},
		{"MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE n * 2 > 1 RETURN r", "arithmetic in the WHERE of a WITH"},
		{"MATCH (o:Order) WITH o RETURN o", "returning a whole node"},
		{"MATCH (o:Order) WITH x RETURN x.id", "not a node of the MATCH pattern"},
		{"MATCH (o:Order) WITH o.region AS r WHERE r IN ['east'] RETURN r", "IN, STARTS WITH"},
		{"MATCH (o:Order) WITH o.region AS r, o.amount + 1 AS a, count(*) AS n RETURN r", "grouping by a computed value"},
		{"MATCH (o:Order) WITH o.region AS r, o.region AS r RETURN r", "a name WITH uses twice"},
		{"MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) WITH i.id AS item WHERE item > 0 RETURN item",
			"requiring what an OPTIONAL MATCH introduced"},
	} {
		_, err := compileWithClause(t, tc.query)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.query, err, tc.want)
		}
	}
}

func TestWithDescriptor(t *testing.T) {
	query := "MATCH (o:Order) WITH o.region AS r, count(*) AS n WHERE n > 1 RETURN r, n"
	plan, _ := compileBindJoin(t, query, nil)
	descriptor, err := BuildSemanticQueryDescriptor(plan, query)
	if err != nil {
		t.Fatal(err)
	}
	var having bool
	for _, predicate := range descriptor.Predicates {
		if strings.HasPrefix(predicate.LogicalPath, "having") && predicate.Operator == ">" {
			having = true
		}
	}
	if !having || len(descriptor.Grouping) != 1 {
		t.Fatalf("predicates = %+v, grouping = %v", descriptor.Predicates, descriptor.Grouping)
	}
}

// A condition returned as a value records the properties it reads, a property
// comparison included.
func TestReturnedPropertyComparisonDescriptor(t *testing.T) {
	query := "MATCH (o:Order) RETURN o.previous_id < o.id AS later"
	plan, _ := compileBindJoin(t, query, nil)
	descriptor, err := BuildSemanticQueryDescriptor(plan, query)
	if err != nil {
		t.Fatal(err)
	}
	if refs := descriptor.Projections[0].PropertyRefs; len(refs) != 2 {
		t.Fatalf("property refs = %v, want both properties", refs)
	}
}
