// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A WITH that passes one property's values to the next MATCH is a semi-join:
// each row of the next stage is kept once if its value is among them, however
// often the first stage produced it.
func TestSemiJoinEvaluates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  [][]any
	}{
		{
			name: "WITH DISTINCT and equality keep each row once",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH DISTINCT o.customer_code AS code " +
				"MATCH (c:Customer) WHERE c.code = code RETURN c.name AS name ORDER BY name",
			want: [][]any{{"Alice"}, {"Alicia"}},
		},
		{
			name: "collect and IN",
			query: "MATCH (o:Order) WHERE o.region = 'east' WITH collect(o.id) AS ids " +
				"MATCH (i:Item) WHERE i.order_id IN ids RETURN count(*) AS n",
			want: [][]any{{2}},
		},
		{
			name: "a value in an inline property map",
			query: "MATCH (i:Item) WITH DISTINCT i.order_id AS oid " +
				"MATCH (o:Order {id: oid}) RETURN DISTINCT o.region AS r ORDER BY r",
			want: [][]any{{"east"}, {"west"}},
		},
		{
			name: "without DISTINCT, RETURN DISTINCT, returning the value",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o.customer_code AS code " +
				"MATCH (c:Customer) WHERE c.code = code RETURN DISTINCT code",
			want: [][]any{{"c1"}},
		},
		{
			name: "no values keep no row",
			query: "MATCH (o:Order) WHERE o.region = 'north' WITH collect(o.id) AS ids " +
				"MATCH (i:Item) WHERE i.order_id IN ids RETURN count(*) AS n",
			want: [][]any{{0}},
		},
		{
			// Both uses name one value, so a customer pairs only with its
			// own orders, not with every order whose code is in the set.
			name: "one value used twice pairs the rows that share it",
			query: "MATCH (o:Order) WHERE o.region = 'east' WITH DISTINCT o.customer_code AS code " +
				"MATCH (c:Customer), (o2:Order) WHERE c.code = code AND o2.customer_code = code AND c.region = 'east' " +
				"RETURN c.name AS name, o2.id AS id ORDER BY name, id",
			want: [][]any{{"Alice", 1}, {"Alice", 2}, {"Bob", 3}, {"Bob", 4}},
		},
		{
			name: "inline properties and WHERE together",
			query: "MATCH (o:Order) WHERE o.region = 'east' WITH DISTINCT o.customer_code AS code " +
				"MATCH (c:Customer {region: 'east'}) WHERE c.code = code AND c.name <> 'x' RETURN c.name AS name ORDER BY name",
			want: [][]any{{"Alice"}, {"Bob"}},
		},
		{
			name: "with other conditions on the second stage",
			query: "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH DISTINCT o.customer_code AS code " +
				"MATCH (c:Customer) WHERE c.code = code AND c.region = 'west' RETURN c.name AS name",
			want: [][]any{{"Alicia"}},
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

func TestSemiJoinGenerates(t *testing.T) {
	sql, err := compileWithClause(t, "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WHERE o.amount > 10 "+
		"WITH DISTINCT o.customer_code AS code MATCH (c:Customer) WHERE c.code = code AND c.region = 'west' RETURN c.name")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FROM {{.res_customer}} t0 JOIN (SELECT DISTINCT t1.`f_cust` AS `value` FROM {{.res_item}} t0 JOIN {{.res_order}} t1",
		"WHERE t1.`f_amount` > 10) s0 ON t0.`f_code` = s0.`value`",
		"WHERE t0.`f_region` = 'west'",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL lacks %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "LIMIT") {
		t.Errorf("the earlier stage must not be limited:\n%s", sql)
	}
	if strings.Contains(sql, " IN (SELECT") {
		t.Errorf("a semi-join is a derived-table join, not an IN subquery:\n%s", sql)
	}

	// A second use of the same value compares with the first instead of
	// joining the earlier stage again.
	sql, err = compileWithClause(t, "MATCH (o:Order) WITH DISTINCT o.customer_code AS code "+
		"MATCH (c:Customer), (o2:Order) WHERE c.code = code AND o2.customer_code = code RETURN c.name, o2.id")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(sql, "JOIN (SELECT") != 1 || !strings.Contains(sql, "t1.`f_cust` = t0.`f_code`") {
		t.Errorf("want one semi-join and the second use compared with the first:\n%s", sql)
	}
}

// More values than one membership filter is known to carry leave the
// bind-join executor, so the query falls back to the SQL path.
func TestSemiJoinPastTheValueBoundFallsBack(t *testing.T) {
	data := bindJoinData()
	data["res_order"] = nil
	for i := 0; i <= maxSemiJoinValues; i++ {
		data["res_order"] = append(data["res_order"], map[string]any{"f_id": i, "f_cust": fmt.Sprintf("c%d", i)})
	}
	_, err := runFake(t, newFakeDataAPI(data), defaultBindJoinLimits,
		"MATCH (o:Order) WITH DISTINCT o.customer_code AS code MATCH (c:Customer) WHERE c.code = code RETURN count(*) AS n", nil)
	var budget *ReadBudgetError
	if !errors.As(err, &budget) {
		t.Fatalf("err = %v, want a read budget error the service falls back on", err)
	}
}

func TestSemiJoinRefuses(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"MATCH (o:Order) WITH o.customer_code AS code MATCH (c:Customer) WHERE c.code = code RETURN c.name",
			"a WITH that repeats values before MATCH"},
		{"MATCH (o:Order) WITH DISTINCT o.customer_code AS code MATCH (c:Customer) WHERE c.code = code OR c.region = 'x' RETURN c.name",
			"under OR or NOT"},
		{"MATCH (o:Order) WITH collect(o.id) AS ids MATCH (i:Item) WHERE NOT i.order_id IN ids RETURN i.id",
			"NOT IN over a WITH's values"},
		{"MATCH (o:Order) WITH collect(o.id) AS ids MATCH (i:Item) WHERE i.order_id = ids RETURN i.id",
			"comparing with a collected list"},
		{"MATCH (o:Order) WITH DISTINCT o.id AS oid MATCH (i:Item) WHERE i.order_id > oid RETURN i.id",
			"only equality"},
		{"MATCH (o:Order) WITH DISTINCT o.id AS oid MATCH (i:Item) RETURN i.id",
			"whose value the next MATCH does not use"},
		{"MATCH (o:Order) WITH DISTINCT o.id AS oid WHERE oid > 1 MATCH (i:Item) WHERE i.order_id = oid RETURN i.id",
			"WHERE, ORDER BY, SKIP or LIMIT on a WITH before MATCH"},
		{"MATCH (o:Order) WITH o.id AS oid, o.region AS r MATCH (i:Item) WHERE i.order_id = oid RETURN i.id",
			"pass one property to the next MATCH"},
		{"MATCH (o:Order) WITH DISTINCT o.region AS r MATCH (i:Item) WHERE i.id = r RETURN i.id",
			"never equal"},
		{"MATCH (o:Order) WHERE o.id = oid RETURN o.id", "referring to a node as a value"},
		{"MATCH (o:Order) RETURN o.id AS id, o.region IN regions AS flagged", "IN over something other than a list"},
		{"MATCH (o:Order) RETURN o.id AS id, o.region = r AS flagged", "referring to a node as a value"},
		{"MATCH (o:Order) WITH collect(o.id) AS ids MATCH (i:Item) WHERE i.order_id IN ids RETURN i.order_id IN ids AS f",
			"IN over something other than a list"},
		{"MATCH (o:Order) WITH collect(o.id) AS ids MATCH (i:Item) WHERE i.order_id IN ids[0..2] RETURN i.id",
			"IN over something other than a list"},
		{"MATCH (o:Order) WITH DISTINCT o.id AS a MATCH (i:Item) WHERE i.order_id = a WITH DISTINCT i.id AS b " +
			"MATCH (x:Item) WHERE x.id = b RETURN x.id", "more than one WITH"},
	} {
		_, err := compileWithClause(t, tc.query)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.query, err, tc.want)
		}
	}
}

// The earlier stage reads tables of its own; the caller's row policy holds
// there as it does in the stage it feeds.
func TestSemiJoinAppliesRowFiltersToItsStage(t *testing.T) {
	east := "east"
	permission := &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/ot_order":    {Kind: "in", Property: "region", Values: []interfaces.RowFilterValue{{Type: "string", String: &east}}},
		"kn_1/ot_customer": {Kind: "true"},
	}}
	vega := &recordingVega{}
	service := relationService(vega, permission, nil)
	order := service.schema.(*fakeSchemaSource).objectTypes[0]
	order.DataProperties = append(order.DataProperties, dataProperty("region", "f_region"))

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order) WITH DISTINCT o.customer_code AS code MATCH (c:Customer) WHERE c.code = code RETURN c.name",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !strings.Contains(vega.request.Query, "JOIN (SELECT DISTINCT t0.`f_cust_code` AS `value` FROM {{.res_order}} t0 WHERE t0.`f_region` IN ('east')) s0") {
		t.Fatalf("statement = %s, want the row filter inside the earlier stage", vega.request.Query)
	}
	impacted := make([]string, 0, len(result.PermissionImpacts))
	for _, impact := range result.PermissionImpacts {
		impacted = append(impacted, impact.ObjectTypeID)
	}
	if !slices.Contains(impacted, "ot_order") {
		t.Fatalf("permission impacts = %+v, want the earlier stage's row filter reported", result.PermissionImpacts)
	}
	if !strings.Contains(string(result.TraceDescriptor), semanticSemiJoin) {
		t.Fatalf("descriptor = %s, want the semi-join recorded", result.TraceDescriptor)
	}
}

func TestSemiJoinProxyBindingsCoverItsStage(t *testing.T) {
	plan, _ := compileBindJoin(t, "MATCH (o:Order) WITH DISTINCT o.customer_code AS code "+
		"MATCH (c:Customer) WHERE c.code = code RETURN c.name", nil)
	resources := planResourceIDs(plan)
	if !slices.Contains(resources, "res_order") || !slices.Contains(resources, "res_customer") {
		t.Fatalf("resources = %v, want both stages", resources)
	}
	var objectTypes []string
	for _, binding := range proxyBindings(plan) {
		objectTypes = append(objectTypes, binding.ChildID)
	}
	if !slices.Contains(objectTypes, "ot_order") {
		t.Fatalf("proxy bindings = %v, want the earlier stage's object type", objectTypes)
	}
}
