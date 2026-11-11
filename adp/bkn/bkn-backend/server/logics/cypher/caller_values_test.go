// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// The cases below are about values a caller passes rather than ones written in
// the query. A statement saved as a function gets them from a script or a
// model, so a value that cannot mean what the statement compares it with has
// to be refused, and an ordinary value such as an empty list has to work.

// A string compared with a date property has to be a date. The data source
// would otherwise read one it cannot parse as some fixed instant -- MySQL as
// the earliest -- and return every row, or none, without a word.
func TestDateComparedWithAStringThatIsNotADate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
	}{
		{"a word", "MATCH (o:Order) WHERE o.placed_at >= $since RETURN o.id", map[string]any{"since": "not-a-date"}},
		{"a date in words", "MATCH (o:Order) WHERE o.placed_at >= $since RETURN o.id", map[string]any{"since": "2024年1月1日"}},
		{"empty", "MATCH (o:Order) WHERE o.placed_at < $since RETURN o.id", map[string]any{"since": ""}},
		{"no such day", "MATCH (o:Order) WHERE o.placed_at = $since RETURN o.id", map[string]any{"since": "2024-13-45"}},
		{"a month", "MATCH (o:Order) WHERE o.placed_at >= $since RETURN o.id", map[string]any{"since": "2024-01"}},
		{"slashes", "MATCH (o:Order) WHERE o.placed_at >= $since RETURN o.id", map[string]any{"since": "2024/01/01"}},
		{"written in the query", "MATCH (o:Order) WHERE o.placed_at >= 'soon' RETURN o.id", nil},
		{"value on the left", "MATCH (o:Order) WHERE $since <= o.placed_at RETURN o.id", map[string]any{"since": "soon"}},
		{"in a list", "MATCH (o:Order) WHERE o.placed_at IN ['2024-01-01', 'soon'] RETURN o.id", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileComparison(t, tc.query, tc.parameters)
			if err == nil || !strings.Contains(err.Error(), "is not a date") || !strings.Contains(err.Error(), "placed_at") {
				t.Fatalf("err = %v, want it refused as not a date, naming placed_at", err)
			}
		})
	}
}

// The forms a date already reaches the executor in keep working, and only
// date properties are checked.
func TestDateComparedWithADateString(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
		want       string
	}{
		{"a day", "MATCH (o:Order) WHERE o.placed_at >= $since RETURN o.id",
			map[string]any{"since": "2024-01-31"}, "WHERE t0.`f_placed` >= '2024-01-31'"},
		{"a time", "MATCH (o:Order) WHERE o.placed_at < $before RETURN o.id",
			map[string]any{"before": "2024-01-31T12:00:00"}, "WHERE t0.`f_placed` < '2024-01-31T12:00:00'"},
		{"a time with a space", "MATCH (o:Order) WHERE o.placed_at < $before RETURN o.id",
			map[string]any{"before": "2024-01-31 12:00:00"}, "WHERE t0.`f_placed` < '2024-01-31 12:00:00'"},
		{"with an offset", "MATCH (o:Order) WHERE o.placed_at >= $since RETURN o.id",
			map[string]any{"since": "2024-01-31T00:00:00+08:00"}, "WHERE t0.`f_placed` >= '2024-01-31T00:00:00+08:00'"},
		{"a string property is not a date", "MATCH (o:Order) WHERE o.region = $region RETURN o.id",
			map[string]any{"region": "not-a-date"}, "WHERE t0.`f_region` = 'not-a-date'"},
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

// A time of day is not a date: "09:30:00" is a value a time property holds.
func TestTimePropertyTakesATimeOfDay(t *testing.T) {
	shift := objectType("ot_shift", "Shift", resource("res_shift", "shifts"),
		dataProperty("id", "f_id"), dataProperty("starts", "f_starts"))
	shift.DataProperties[0].Type = "integer"
	shift.DataProperties[1].Type = "time"
	shift.PrimaryKeys = []string{"id"}
	schema := testSchema(t, &fakeSchemaSource{objectTypes: []*interfaces.ObjectType{shift}})
	tree, err := Parse("MATCH (s:Shift) WHERE s.starts >= $at RETURN s.id")
	if err != nil {
		t.Fatal(err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(analyzed, schema, CompileOptions{Parameters: map[string]any{"at": "09:30:00"}}); err != nil {
		t.Fatalf("a time of day against a time property: %v", err)
	}
}

// An empty list is what a caller passes when there is nothing to look for, and
// IN over it matches nothing -- as the same list written in the query does.
func TestEmptyListParameterMatchesNothing(t *testing.T) {
	empty := map[string]any{"ids": []any{}}
	rows, _ := mustRunFake(t, "MATCH (o:Order) WHERE o.id IN $ids RETURN o.id", empty)
	if len(rows) != 0 {
		t.Fatalf("IN an empty list: rows = %v, want none", rows)
	}
	rows, _ = mustRunFake(t, "MATCH (o:Order) WHERE NOT o.id IN $ids RETURN o.id AS id ORDER BY id", empty)
	if want := [][]any{{1}, {2}, {3}, {4}}; !reflect.DeepEqual(rows, want) {
		t.Fatalf("NOT IN an empty list: rows = %v, want %v", rows, want)
	}
	sql, err := compileComparison(t, "MATCH (o:Order) WHERE o.id IN $ids RETURN count(o) AS n", empty)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "WHERE 1 = 0") {
		t.Fatalf("SQL lacks the constant that matches nothing:\n%s", sql)
	}
}

// A WITH that groups by a node has one value of each of its properties per
// group, so a later stage may compute with one and an aggregate together.
func TestArithmeticOverAPropertyOfAGroupedNode(t *testing.T) {
	query := "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(i) AS items " +
		"WITH o.id AS ord, o.amount AS amount, items RETURN ord, amount - items AS rest ORDER BY ord"
	rows, _ := mustRunFake(t, query, nil)
	if want := [][]any{{1, 48}, {2, 69}}; !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	// Without the id beside it, the property is still one value per order.
	query = "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(i) AS items " +
		"WITH o.amount AS amount, items RETURN amount - items AS rest ORDER BY rest"
	rows, _ = mustRunFake(t, query, nil)
	if want := [][]any{{48}, {69}}; !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
	sql, err := compileComparison(t, query, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Grouped by the order's key and the property it reads, so a database that
	// insists on every read column being grouped accepts the statement.
	if !strings.Contains(sql, "GROUP BY t1.`f_id`, t1.`f_amount`") {
		t.Fatalf("SQL does not group by the order and its amount:\n%s", sql)
	}
}

// A property of a node nobody grouped by still has no one value per group.
func TestArithmeticOverAPropertyOfAnUngroupedNode(t *testing.T) {
	query := "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o.region AS region, count(i) AS items " +
		"RETURN o.amount - items AS rest"
	if _, err := compileComparison(t, query, nil); err == nil {
		t.Fatal("compiled, want it refused")
	}
	query = "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) RETURN i.id - count(o) AS n"
	if _, err := compileComparison(t, query, nil); err == nil || !strings.Contains(err.Error(), "outside an aggregate") {
		t.Fatalf("err = %v, want it refused as outside an aggregate", err)
	}
}
