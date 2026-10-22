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

func compileComparison(t *testing.T, query string, parameters map[string]any) (string, error) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		return "", err
	}
	plan, err := Compile(analyzed, bindJoinSchema(t), CompileOptions{Parameters: parameters})
	if err != nil {
		return "", err
	}
	return Generate(plan, GenerateOptions{})
}

func TestComparisonsGenerate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
		want       string
	}{
		{
			name:  "one property against another",
			query: "MATCH (o:Order) WHERE o.previous_id < o.id RETURN count(*) AS n",
			want:  "WHERE t0.`f_prev` < t0.`f_id`",
		},
		{
			name:  "properties of two nodes",
			query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WHERE o.region <> c.region RETURN o.id",
			want:  "t0.`f_region` <> t1.`f_region`",
		},
		{
			name:  "a value on the left is turned around",
			query: "MATCH (o:Order) WHERE 50 <= o.amount RETURN o.id",
			want:  "WHERE t0.`f_amount` >= 50",
		},
		{
			name:  "a date constant",
			query: "MATCH (o:Order) WHERE o.placed_at <= date('2024-01-31') RETURN o.id",
			want:  "WHERE t0.`f_placed` <= '2024-01-31'",
		},
		{
			name:       "a date parameter",
			query:      "MATCH (o:Order) WHERE o.placed_at >= datetime($since) RETURN o.id",
			parameters: map[string]any{"since": "2024-01-01T00:00:00"},
			want:       "WHERE t0.`f_placed` >= '2024-01-01T00:00:00'",
		},
		{
			name:  "count of a node counts its key",
			query: "MATCH (o:Order) RETURN count(o) AS n",
			want:  "SELECT COUNT(t0.`f_id`) AS `n`",
		},
		{
			name:  "count of distinct nodes",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) RETURN count(DISTINCT o) AS orders",
			want:  "COUNT(DISTINCT t1.`f_id`) AS `orders`",
		},
		{
			name:  "count of a node with a composite key that must match",
			query: "MATCH (c:Customer) RETURN count(c) AS n",
			want:  "SELECT COUNT(t0.`f_code`) AS `n`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, err := compileComparison(t, tc.query, tc.parameters)
			if err != nil {
				t.Fatalf("%s: %v", tc.query, err)
			}
			if !strings.Contains(sql, tc.want) {
				t.Fatalf("SQL lacks %q:\n%s", tc.want, sql)
			}
		})
	}
}

func TestComparisonsRefuse(t *testing.T) {
	for _, tc := range []struct {
		query      string
		parameters map[string]any
		want       string
	}{
		{query: "MATCH (o:Order) WHERE o.region = o.amount RETURN o.id", want: "never equal or ordered"},
		{query: "MATCH (o:Order) WHERE o.region < date('2024-01-01') RETURN o.id", want: "rather than dates"},
		{query: "MATCH (o:Order) WHERE o.placed_at < date('soon') RETURN o.id", want: "is not a date"},
		{query: "MATCH (o:Order) WHERE o.placed_at < date() RETURN o.id", want: "without one string"},
		{query: "MATCH (o:Order) WHERE o.placed_at < date(1) RETURN o.id", want: "something other than a string"},
		{query: "MATCH (o:Order) WHERE o.placed_at < date($d) RETURN o.id", parameters: map[string]any{"d": 5}, want: "takes a date string"},
		{query: "MATCH (o:Order) WHERE toLower(o.region) = 'east' RETURN o.id", want: "function calls"},
		{query: "MATCH (o:Order) RETURN sum(o) AS n", want: "sum over a whole node"},
		{query: "MATCH (o:Order) RETURN count(x) AS n", want: `variable "x" is not defined`},
	} {
		_, err := compileComparison(t, tc.query, tc.parameters)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.query, err, tc.want)
		}
	}
}

// The executor answers the same comparisons the SQL path does, a date by its
// wall-clock reading, and counts a node the way count(key) does.
func TestComparisonsEvaluate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  [][]any
	}{
		{
			name:  "one property against another, null undecided",
			query: "MATCH (o:Order) WHERE o.previous_id < o.id RETURN o.id AS id ORDER BY id",
			want:  [][]any{{2}, {3}},
		},
		{
			name:  "properties of the two ends of a relationship",
			query: "MATCH (a:Order)-[:FOLLOWS]->(b:Order) WHERE a.amount < b.amount RETURN a.id AS a, b.id AS b",
			want:  [][]any{{1, 2}},
		},
		{
			name:  "a date against a date constant",
			query: "MATCH (o:Order) WHERE o.placed_at >= date('2024-01-31') RETURN o.id AS id ORDER BY id",
			want:  [][]any{{2}, {3}},
		},
		{
			name: "count of an optional node skips the rows it is missing from",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) " +
				"RETURN o.id AS ord, count(i) AS items ORDER BY ord",
			want: [][]any{{1, 2}, {2, 1}, {3, 0}, {4, 0}},
		},
		{
			name:  "count of distinct nodes",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) RETURN count(i) AS items, count(DISTINCT o) AS orders",
			want:  [][]any{{3, 2}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, _ := mustRunFake(t, tc.query, nil)
			if !reflect.DeepEqual(rows, tc.want) {
				t.Fatalf("rows = %v, want %v", rows, tc.want)
			}
		})
	}
}

func TestPropertyComparisonDescriptor(t *testing.T) {
	query := "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WHERE o.region <> c.region RETURN o.id"
	plan, _ := compileBindJoin(t, query, nil)
	descriptor, err := BuildSemanticQueryDescriptor(plan, query)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, predicate := range descriptor.Predicates {
		if predicate.Expression == semanticComparison && predicate.Operator == "<>" && len(predicate.PropertyRefs) == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("predicates = %+v, want the property comparison", descriptor.Predicates)
	}
}

// An OPTIONAL MATCH condition may compare properties, and a later required
// use of the optional node through a property comparison is still refused.
func TestPropertyComparisonsAndOptionalMatch(t *testing.T) {
	rows, _ := mustRunFake(t, "MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) WHERE i.id > o.previous_id "+
		"RETURN o.id AS ord, count(i) AS items ORDER BY ord", nil)
	if want := [][]any{{1, 0}, {2, 1}, {3, 0}, {4, 0}}; !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	for _, query := range []string{
		"MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) MATCH (o)-[:PLACED_BY]->(c:Customer) " +
			"WHERE i.id > o.id RETURN o.id",
		"MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) WHERE o.id > o.previous_id RETURN o.id",
	} {
		if _, err := compileComparison(t, query, nil); err == nil {
			t.Errorf("%s: compiled, want it refused", query)
		}
	}
}

// date() and its kin read a wall-clock date; a time zone offset would be read
// differently by the data source and the executor, so it is refused.
func TestTemporalRefusesOffsets(t *testing.T) {
	for _, query := range []string{
		"MATCH (o:Order) WHERE o.placed_at >= datetime('2024-01-01T00:00:00+08:00') RETURN o.id",
		"MATCH (o:Order) WHERE o.placed_at >= datetime('2024-01-01T00:00:00Z') RETURN o.id",
	} {
		if _, err := compileComparison(t, query, nil); err == nil || !strings.Contains(err.Error(), "is not a date") {
			t.Errorf("%s: err = %v, want the offset refused", query, err)
		}
	}
	if _, err := compileComparison(t, "MATCH (o:Order) WHERE o.placed_at >= datetime($at) RETURN o.id",
		map[string]any{"at": "2024-01-01T00:00:00Z"}); err == nil || !strings.Contains(err.Error(), "takes a date string") {
		t.Errorf("parameter with an offset: err = %v, want it refused", err)
	}
}
