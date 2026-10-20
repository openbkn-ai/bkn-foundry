// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const (
	eastCustomers = "MATCH (o:Order) WHERE o.region = 'east' WITH collect(DISTINCT o.customer_code) AS a "
	westCustomers = "MATCH (c:Customer) WHERE c.region = 'west' WITH a, collect(DISTINCT c.code) AS b "
)

// Two collected lists compare in one statement: each stage is a derived table
// of its own, nulls left out as collect leaves them out, and the second
// stage's rows decide whether there is a row at all.
func TestIntersectionGenerates(t *testing.T) {
	first := "SELECT t0.`f_cust` AS `value` FROM {{.res_order}} t0 WHERE t0.`f_region` = 'east'"
	second := "SELECT t0.`f_code` AS `value` FROM {{.res_customer}} t0 WHERE t0.`f_region` = 'west'"
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "what the first list shares with the second, and both sizes",
			query: eastCustomers + westCustomers + "RETURN size([x IN a WHERE x IN b]) AS common, size(a) AS na, size(b) AS nb",
			want: "SELECT COUNT(b.`value`) AS `common`, COUNT(a.`value`) AS `na`, MAX(c.`n`) AS `nb`" +
				" FROM (SELECT 1 AS `one` FROM (" + second + ") s LIMIT 1) e" +
				" LEFT JOIN (SELECT DISTINCT s.`value` FROM (" + first + ") s WHERE s.`value` IS NOT NULL) a ON 1 = 1" +
				" LEFT JOIN (SELECT DISTINCT s.`value` FROM (" + second + ") s WHERE s.`value` IS NOT NULL) b ON a.`value` = b.`value`" +
				" CROSS JOIN (SELECT COUNT(DISTINCT s.`value`) AS `n` FROM (" + second + ") s) c" +
				" GROUP BY e.`one`",
		},
		{
			// Without DISTINCT the filtered list keeps its repeats, as the
			// list comprehension does, and its other side counts each value.
			name: "the second list filtered by the first, without DISTINCT",
			query: "MATCH (o:Order) WHERE o.region = 'east' WITH collect(o.customer_code) AS a " +
				"MATCH (c:Customer) WHERE c.region = 'west' WITH a, collect(c.code) AS b " +
				"RETURN size([x IN b WHERE x IN a])",
			want: "SELECT COUNT(b.`value`) AS `size([x IN b WHERE x IN a])`" +
				" FROM (SELECT 1 AS `one` FROM (" + second + ") s LIMIT 1) e" +
				" LEFT JOIN (SELECT s.`value` FROM (" + second + ") s WHERE s.`value` IS NOT NULL) a ON 1 = 1" +
				" LEFT JOIN (SELECT DISTINCT s.`value` FROM (" + first + ") s WHERE s.`value` IS NOT NULL) b ON a.`value` = b.`value`" +
				" CROSS JOIN (SELECT COUNT(s.`value`) AS `n` FROM (" + first + ") s) c" +
				" GROUP BY e.`one`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, err := compileWithClause(t, tc.query)
			if err != nil {
				t.Fatalf("%s: %v", tc.query, err)
			}
			if sql != tc.want {
				t.Errorf("SQL =\n%s\nwant\n%s", sql, tc.want)
			}
		})
	}
}

func TestIntersectionRefuses(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  string
	}{
		{eastCustomers + westCustomers + "RETURN [x IN a WHERE x IN b] AS shared", "returning a list"},
		{eastCustomers + westCustomers + "RETURN a", "returning a list"},
		{eastCustomers + westCustomers + "RETURN size([x IN a WHERE x IN a]) AS n", "this value after two collected lists"},
		{eastCustomers + westCustomers + "RETURN size([x IN a WHERE x IN b]) + 1 AS n", "this value after two collected lists"},
		{eastCustomers + westCustomers + "RETURN size([x IN a WHERE NOT x IN b]) AS n", "this value after two collected lists"},
		{eastCustomers + westCustomers + "RETURN size(a) AS n LIMIT 1", "DISTINCT, ORDER BY, SKIP or LIMIT after two lists"},
		{eastCustomers + westCustomers + "RETURN size(a) AS n, size(b) AS n", "column name \"n\" is returned twice"},
		{eastCustomers + "MATCH (c:Customer) WITH collect(c.code) AS b RETURN size(b) AS n", "a second list without the first"},
		{eastCustomers + "MATCH (c:Customer) WITH a, collect(c.code) AS b WHERE size(b) > 1 RETURN size(b) AS n",
			"WHERE on a WITH that collects a list"},
		{eastCustomers + "MATCH (c:Customer) WITH a, collect(c.code) AS a RETURN size(a) AS n", "named twice"},
		{"MATCH (o:Order) WITH collect(o.amount) AS a " + westCustomers + "RETURN size([x IN a WHERE x IN b]) AS n",
			"the two are never equal"},
		{"MATCH (o:Order) WITH collect(o.customer_code) AS a MATCH (c:Customer) WITH a, collect(c.code) AS b " +
			"RETURN size([x IN a WHERE x IN b]) AS ab, size([x IN b WHERE x IN a]) AS ba", "both ways round"},
		{eastCustomers + "MATCH (c:Customer) WHERE c.code IN a WITH a, collect(c.name) AS b RETURN size(b) AS n",
			"using the first list in the second MATCH"},
		{eastCustomers + westCustomers + "UNWIND a AS x UNWIND b AS y WITH x WHERE x = y RETURN count(DISTINCT x) AS n",
			"size([x IN a WHERE x IN b])"},
	} {
		_, err := compileWithClause(t, tc.query)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s:\nerr = %v, want it to mention %q", tc.query, err, tc.want)
		}
	}
}

// Both lists collected with DISTINCT share the same values whichever way
// round they are counted, so both directions may be asked for.
func TestIntersectionCountsBothWaysOverDistinctLists(t *testing.T) {
	query := eastCustomers + westCustomers + "RETURN size([x IN a WHERE x IN b]) AS ab, size([x IN b WHERE x IN a]) AS ba"
	sql, err := compileWithClause(t, query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if !strings.HasPrefix(sql, "SELECT COUNT(b.`value`) AS `ab`, COUNT(b.`value`) AS `ba` FROM") {
		t.Errorf("SQL = %s", sql)
	}
}

// Other stages after a MATCH that follows a WITH are refused as before.
func TestIntersectionLeavesOtherStagesAlone(t *testing.T) {
	query := "MATCH (o:Order) WITH DISTINCT o.customer_code AS c MATCH (k:Customer) WHERE k.code = c " +
		"WITH k.region AS r, count(*) AS n RETURN r, n"
	if _, err := compileWithClause(t, query); err == nil || !strings.Contains(err.Error(), "a WITH after the MATCH that follows a WITH") {
		t.Errorf("err = %v", err)
	}
}

// The intersection runs on the SQL path whatever executor is configured,
// reads through the bindings of both stages, and records what each list
// collects.
func TestIntersectionRunsOnTheSQLPath(t *testing.T) {
	vega := &recordingVega{response: &interfaces.RawQueryResponse{Entries: []map[string]any{
		{"common": json.Number("1"), "n": json.Number("2")},
	}}}
	service := graphService(vega)
	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order) WITH collect(DISTINCT o.customer_code) AS a " +
			"MATCH (c:Customer) WITH a, collect(DISTINCT c.code) AS b " +
			"RETURN size([x IN a WHERE x IN b]) AS common, size(a) AS n",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if vega.request == nil || !strings.Contains(vega.request.Query, "COUNT(b.`value`) AS `common`") {
		t.Fatalf("the intersection did not run on the SQL path: %+v", vega.request)
	}
	var descriptor SemanticQueryDescriptor
	if err := json.Unmarshal(result.TraceDescriptor, &descriptor); err != nil {
		t.Fatalf("trace descriptor: %v", err)
	}
	if len(descriptor.Objects) != 2 || descriptor.Objects[0].Alias != "list0.o" || descriptor.Objects[1].Alias != "list1.c" {
		t.Errorf("objects = %+v", descriptor.Objects)
	}
	common, n := descriptor.Projections[0], descriptor.Projections[1]
	if common.Expression != "intersection" || common.Aggregate != "count" || len(common.PropertyRefs) != 2 {
		t.Errorf("common = %+v", common)
	}
	if n.PropertyRef != "property:kn_1:ot_order:customer_code" || !n.Distinct {
		t.Errorf("n = %+v", n)
	}
}

// Row filters hold in both stages, in every place the statement repeats a
// stage, and each stage's object type is reported as filtered.
func TestIntersectionAppliesRowFiltersToBothStages(t *testing.T) {
	east, ann := "east", "Ann"
	permission := &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/ot_order":    {Kind: "in", Property: "region", Values: []interfaces.RowFilterValue{{Type: "string", String: &east}}},
		"kn_1/ot_customer": {Kind: "in", Property: "name", Values: []interfaces.RowFilterValue{{Type: "string", String: &ann}}},
	}}
	vega := &recordingVega{}
	service := relationService(vega, permission, nil)
	order := service.schema.(*fakeSchemaSource).objectTypes[0]
	order.DataProperties = append(order.DataProperties, dataProperty("region", "f_region"))

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order) WITH collect(DISTINCT o.customer_code) AS a " +
			"MATCH (c:Customer) WITH a, collect(DISTINCT c.code) AS b " +
			"RETURN size([x IN a WHERE x IN b]) AS common, size(b) AS nb",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	statement := vega.request.Query
	for resource, filtered := range map[string]string{
		"{{.res_order}}":    "{{.res_order}} t0 WHERE t0.`f_region` IN ('east')",
		"{{.res_customer}}": "{{.res_customer}} t0 WHERE t0.`f_name` IN ('Ann')",
	} {
		if all, kept := strings.Count(statement, resource), strings.Count(statement, filtered); all == 0 || all != kept {
			t.Errorf("%s appears %d times, %d of them filtered:\n%s", resource, all, kept, statement)
		}
	}
	impacted := make([]string, 0, len(result.PermissionImpacts))
	for _, impact := range result.PermissionImpacts {
		impacted = append(impacted, impact.ObjectTypeID)
	}
	if !slices.Contains(impacted, "ot_order") || !slices.Contains(impacted, "ot_customer") {
		t.Errorf("permission impacts = %+v, want both stages' row filters reported", result.PermissionImpacts)
	}
}

func TestIntersectionProxyBindingsCoverBothStages(t *testing.T) {
	plan, _ := compileBindJoin(t, "MATCH (o:Order) WITH collect(o.customer_code) AS a "+
		"MATCH (c:Customer) WITH a, collect(c.code) AS b RETURN size([x IN a WHERE x IN b]) AS n", nil)
	resources := planResourceIDs(plan)
	if !slices.Contains(resources, "res_order") || !slices.Contains(resources, "res_customer") {
		t.Fatalf("resources = %v, want both stages", resources)
	}
	var objectTypes []string
	for _, binding := range proxyBindings(plan) {
		objectTypes = append(objectTypes, binding.ChildID)
	}
	if !slices.Contains(objectTypes, "ot_order") || !slices.Contains(objectTypes, "ot_customer") {
		t.Fatalf("proxy bindings = %v, want both stages' object types", objectTypes)
	}
}
