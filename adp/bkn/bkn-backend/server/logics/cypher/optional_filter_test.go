// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A statement saved as a function makes a filter optional with
// ($x IS NULL OR condition): the caller passes a value to filter by it, or null
// or nothing to read every row.
func TestOptionalFilterRows(t *testing.T) {
	byRegion := "MATCH (o:Order) WHERE ($region IS NULL OR o.region = $region) RETURN o.id AS id ORDER BY id"
	guarded := "MATCH (o:Order) WHERE ($region IS NOT NULL AND o.region = $region) RETURN o.id AS id ORDER BY id"
	optionalItem := "MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) WHERE ($item IS NULL OR i.id = $item) " +
		"RETURN o.id AS ord, i.id AS item ORDER BY ord, item"
	guardedItem := "MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) WHERE ($item IS NOT NULL AND i.id = $item) " +
		"RETURN o.id AS ord, i.id AS item ORDER BY ord, item"
	minItems := "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(i) AS n WHERE ($min IS NULL OR n >= $min) " +
		"RETURN o.id AS id, n ORDER BY id"
	guardedMin := "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(i) AS n WHERE ($min IS NOT NULL AND n >= $min) " +
		"RETURN o.id AS id, n ORDER BY id"
	twoFilters := "MATCH (o:Order) WHERE ($ids IS NULL OR o.id IN $ids) AND ($region IS NULL OR o.region = $region) " +
		"RETURN o.id AS id ORDER BY id"
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
		want       [][]any
	}{
		{"a value filters", byRegion, map[string]any{"region": "east"}, [][]any{{1}, {3}}},
		{"null reads every row", byRegion, map[string]any{"region": nil}, [][]any{{1}, {2}, {3}, {4}}},
		{"left out reads every row", byRegion, nil, [][]any{{1}, {2}, {3}, {4}}},
		{"a guard that fails matches nothing", guarded, nil, [][]any{}},
		{"a guard that holds filters", guarded, map[string]any{"region": "west"}, [][]any{{2}}},
		{"NOT over the test", "MATCH (o:Order) WHERE NOT ($region IS NULL) AND o.region = $region RETURN o.id AS id",
			map[string]any{"region": "west"}, [][]any{{2}}},
		{"OPTIONAL MATCH with a value", optionalItem, map[string]any{"item": 12},
			[][]any{{1, 12}, {2, nil}, {3, nil}, {4, nil}}},
		{"OPTIONAL MATCH without one", optionalItem, nil,
			[][]any{{1, 11}, {1, 12}, {2, 13}, {3, nil}, {4, nil}}},
		{"OPTIONAL MATCH guard that fails finds no node and keeps the rows", guardedItem, nil,
			[][]any{{1, nil}, {2, nil}, {3, nil}, {4, nil}}},
		{"after a WITH with a value", minItems, map[string]any{"min": 2}, [][]any{{1, 2}}},
		{"after a WITH without one", minItems, nil, [][]any{{1, 2}, {2, 1}}},
		{"after a WITH a guard that fails", guardedMin, nil, [][]any{}},
		{"after a WITH a guard that holds", guardedMin, map[string]any{"min": 2}, [][]any{{1, 2}}},
		{"two optional filters, one given", twoFilters, map[string]any{"ids": []any{1, 2, 3}}, [][]any{{1}, {2}, {3}}},
		{"two optional filters, both given", twoFilters, map[string]any{"ids": []any{1, 2, 3}, "region": "east"},
			[][]any{{1}, {3}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, _ := mustRunFake(t, tc.query, tc.parameters)
			if !reflect.DeepEqual(rows, tc.want) {
				t.Fatalf("rows = %v, want %v", rows, tc.want)
			}
		})
	}
}

// The test is decided before planning, so the data source sees the plain
// condition or none, never an OR around a constant.
func TestOptionalFilterSQL(t *testing.T) {
	byRegion := "MATCH (o:Order) WHERE ($region IS NULL OR o.region = $region) RETURN o.id"
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
		contains   []string
		lacks      []string
	}{
		{"a value", byRegion, map[string]any{"region": "east"},
			[]string{"WHERE t0.`f_region` = 'east'"}, []string{" OR ", "IS NULL"}},
		{"null", byRegion, map[string]any{"region": nil}, nil, []string{"WHERE"}},
		{"a guard that fails", "MATCH (o:Order) WHERE ($region IS NOT NULL AND o.region = $region) RETURN o.id", nil,
			[]string{"WHERE 1 = 0"}, nil},
		{"OPTIONAL MATCH guard that fails",
			"MATCH (o:Order) OPTIONAL MATCH (o)<-[:BELONGS_TO]-(i:Item) WHERE ($item IS NOT NULL AND i.id = $item) RETURN o.id, i.id",
			nil, []string{"LEFT JOIN", "AND 1 = 0"}, []string{"WHERE"}},
		{"after a WITH, a guard that fails",
			"MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(i) AS n WHERE ($min IS NOT NULL AND n >= $min) RETURN o.id, n",
			nil, []string{"HAVING 1 = 0"}, nil},
		{"after a WITH, no value",
			"MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(i) AS n WHERE ($min IS NULL OR n >= $min) RETURN o.id, n",
			nil, nil, []string{"HAVING"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, err := compileComparison(t, tc.query, tc.parameters)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(sql, want) {
					t.Errorf("SQL lacks %q:\n%s", want, sql)
				}
			}
			for _, unwanted := range tc.lacks {
				if strings.Contains(sql, unwanted) {
					t.Errorf("SQL has %q:\n%s", unwanted, sql)
				}
			}
		})
	}
}

func TestOptionalFilterRejections(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{"only parameter tests", "MATCH (o:Order) WHERE $region IS NULL RETURN o.id",
			"a WHERE that only tests whether parameters are null"},
		{"in RETURN", "MATCH (o:Order) RETURN $region IS NULL AS b", "testing a parameter for null outside WHERE"},
		{"in CASE", "MATCH (o:Order) RETURN CASE WHEN $region IS NULL THEN 1 ELSE 2 END AS b",
			"testing a parameter for null outside WHERE"},
		{"in CASE after a WITH",
			"MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(i) AS n RETURN o.id AS id, CASE WHEN $x IS NULL THEN n ELSE 0 END AS v",
			"testing a parameter for null outside WHERE"},
		{"over a date conversion", "MATCH (o:Order) WHERE (date($d) IS NULL OR o.region = $d) RETURN o.id",
			"IS NULL to variable.property or $parameter"},
		// A value the caller left out is still an error where the statement
		// compares with it unguarded.
		{"used unguarded", "MATCH (o:Order) WHERE ($region IS NULL OR o.region = $region) AND o.id = $id RETURN o.id",
			`parameter "id" was not supplied`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileComparison(t, tc.query, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// Through the service, each part of a UNION is decided on its own.
func TestOptionalFilterThroughTheService(t *testing.T) {
	for _, tc := range []struct {
		name       string
		parameters map[string]any
		lacks      string
		contains   string
	}{
		{"value", map[string]any{"floor": 10}, " OR ", "`f_total` >= 10"},
		{"no value", nil, "f_total", "res_order"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vega := &recordingVega{}
			service := testService(t, vega, &stubPermission{})
			_, err := service.Query(context.Background(), interfaces.CypherQuery{
				KNID: "kn_1",
				Query: "MATCH (o:Order) WHERE ($floor IS NULL OR o.amount >= $floor) RETURN o.id AS id " +
					"UNION MATCH (o:Order) WHERE ($floor IS NULL OR o.amount >= $floor) RETURN o.id AS id",
				Parameters: tc.parameters,
			})
			if vega.request == nil {
				t.Fatalf("the query never reached vega-backend: %v", err)
			}
			if !strings.Contains(vega.request.Query, tc.contains) || strings.Contains(vega.request.Query, tc.lacks) {
				t.Fatalf("query = %s, want %q and not %q", vega.request.Query, tc.contains, tc.lacks)
			}
		})
	}
}
