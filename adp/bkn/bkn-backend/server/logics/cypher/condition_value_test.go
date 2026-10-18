// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A condition returned as a value is true, false or null, by the same rules
// WHERE decides it by, and groups like any other value.
func TestConditionValuesEvaluate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
		want       [][]any
	}{
		{
			name: "grouping by a condition",
			query: "MATCH (o:Order) RETURN o.region STARTS WITH 'e' AS east, count(*) AS n " +
				"ORDER BY n DESC, east",
			want: [][]any{{true, 2}, {nil, 1}, {false, 1}},
		},
		{
			name: "three-valued logic per row",
			query: "MATCH (o:Order) RETURN o.id AS id, o.amount > 30 AS big, o.amount IS NULL AS missing, " +
				"NOT (o.region = 'east') AS elsewhere, o.region IN ['east'] OR o.amount > 60 AS either ORDER BY id",
			want: [][]any{
				{1, true, false, false, true},
				{2, true, false, true, true},
				{3, false, false, false, true},
				{4, nil, true, nil, nil},
			},
		},
		{
			name: "a condition over an optional node",
			query: "MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) " +
				"RETURN o.id AS ord, i.id IS NULL AS lonely, i.id > 11 AS later ORDER BY ord, later",
			want: [][]any{{1, false, false}, {1, false, true}, {2, false, true}, {3, true, nil}, {4, true, nil}},
		},
		{
			name:  "a date compares as a wall-clock value, as it does in WHERE",
			query: "MATCH (o:Order) RETURN o.id AS id, o.placed_at >= '2024-02-01' AS february ORDER BY id",
			want:  [][]any{{1, false}, {2, false}, {3, true}, {4, nil}},
		},
		{
			name:       "a parameter",
			query:      "MATCH (o:Order) WHERE o.id < 3 RETURN o.id AS id, o.region = $region AS mine ORDER BY id",
			parameters: map[string]any{"region": "west"},
			want:       [][]any{{1, false}, {2, true}},
		},
		{
			name:  "DISTINCT over a condition",
			query: "MATCH (o:Order) RETURN DISTINCT o.amount > 30 AS big ORDER BY big",
			want:  [][]any{{nil}, {false}, {true}},
		},
		{
			name:  "a condition across a relationship",
			query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN o.id AS id, c.name CONTAINS 'li' AS li ORDER BY id",
			want:  [][]any{{1, true}, {2, true}, {3, false}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, _ := mustRunFake(t, tc.query, tc.parameters)
			if !reflect.DeepEqual(rows, tc.want) {
				t.Fatalf("rows = %v, want %v", rows, tc.want)
			}
		})
	}
}

func TestConditionValueColumns(t *testing.T) {
	query := "MATCH (o:Order) RETURN o.region = 'east', count(*) AS n"
	plan, schema := compileBindJoin(t, query, nil)
	columns, _, err := runBindJoin(context.Background(), plan, schema, newFakeDataAPI(bindJoinData()).read,
		defaultBindJoinLimits, interfaces.CYPHER_DEFAULT_LIMIT)
	if err != nil {
		t.Fatal(err)
	}
	want := []interfaces.RawQueryColumn{{Name: "o.region = 'east'", Type: "boolean"}, {Name: "n", Type: "integer"}}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("columns = %v, want %v", columns, want)
	}
	if len(plan.GroupBy) != 1 {
		t.Fatalf("GroupBy = %v, want the condition", plan.GroupBy)
	}
}

// The SQL path writes the condition with the code that writes WHERE, and
// groups by it.
func TestConditionValueSQL(t *testing.T) {
	plan, _ := compileBindJoin(t,
		"MATCH (o:Order) WHERE o.amount > 0 RETURN o.region CONTAINS 'a_' AS a, o.amount IN [50, 70] AS listed, count(*) AS n ORDER BY n DESC", nil)
	sql, err := Generate(plan, GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"(t0.`f_region` LIKE '%a!_%' ESCAPE '!') AS `a`",
		"(t0.`f_amount` IN (50, 70)) AS `listed`",
		"GROUP BY (t0.`f_region` LIKE '%a!_%' ESCAPE '!'), (t0.`f_amount` IN (50, 70))",
		"ORDER BY `n` DESC",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL lacks %q:\n%s", want, sql)
		}
	}
}

// MySQL returns 1, 0 and NULL for a condition; the result says true, false and
// null, as the executor does.
func TestConditionsAsBooleans(t *testing.T) {
	plan, _ := compileBindJoin(t, "MATCH (o:Order) RETURN o.id AS id, o.amount > 30 AS big", nil)
	columns := []interfaces.RawQueryColumn{{Name: "id", Type: "integer"}, {Name: "big", Type: "integer"}}
	entries := []map[string]any{
		{"id": json.Number("1"), "big": json.Number("1")},
		{"id": json.Number("2"), "big": float64(0)},
		{"id": json.Number("3"), "big": nil},
		{"id": json.Number("4"), "big": int64(1)},
	}
	conditionsAsBooleans(plan, columns, entries)
	if columns[1].Type != "boolean" || columns[0].Type != "integer" {
		t.Fatalf("columns = %v", columns)
	}
	var got []any
	for _, entry := range entries {
		got = append(got, entry["big"])
	}
	if want := []any{true, false, nil, true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
	if entries[0]["id"] != json.Number("1") {
		t.Fatalf("a plain column changed: %v", entries[0]["id"])
	}
}

func TestConditionValueDescriptor(t *testing.T) {
	query := "MATCH (o:Order) RETURN o.region = 'east' OR o.amount > 60 AS busy, count(*) AS n"
	plan, _ := compileBindJoin(t, query, nil)
	descriptor, err := BuildSemanticQueryDescriptor(plan, query)
	if err != nil {
		t.Fatal(err)
	}
	projection := descriptor.Projections[0]
	if projection.Expression != semanticCondition || len(projection.PropertyRefs) != 2 {
		t.Fatalf("projection = %+v", projection)
	}
	if len(descriptor.Grouping) != 2 {
		t.Fatalf("grouping = %v", descriptor.Grouping)
	}
}
