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

// A WITH that passes nodes on becomes a derived table with one row per row
// it passes on, joined to the next MATCH on every primary key column of those
// nodes; what it named is read from that table.
func TestCarriedStageGenerates(t *testing.T) {
	stage := "SELECT COUNT(t0.`f_id`) AS `total`, t1.`f_code` AS `_key_0_0`, t1.`f_region` AS `_key_0_1` " +
		"FROM {{.res_order}} t0 JOIN {{.res_customer}} t1 ON t0.`f_cust` = t1.`f_code` AND t0.`f_region` = t1.`f_region` " +
		"GROUP BY t1.`f_code`, t1.`f_region`"
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name: "a count per node, read beside the next MATCH's own",
			query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WITH c, count(o) AS total " +
				"MATCH (i:Item)-[:BELONGS_TO]->(o2:Order)-[:PLACED_BY]->(c) " +
				"RETURN c.name AS name, count(i) AS items, total, count(i) * 1.0 / total AS share ORDER BY total DESC, name",
			want: "SELECT t2.`f_name` AS `name`, COUNT(t0.`f_id`) AS `items`, w.`total` AS `total`, " +
				"COUNT(t0.`f_id`) * 1E+00 / w.`total` AS `share` FROM {{.res_item}} t0 " +
				"JOIN {{.res_order}} t1 ON t0.`f_order` = t1.`f_id` " +
				"JOIN {{.res_customer}} t2 ON t1.`f_cust` = t2.`f_code` AND t1.`f_region` = t2.`f_region` " +
				"JOIN (" + stage + ") w ON t2.`f_code` = w.`_key_0_0` AND t2.`f_region` = w.`_key_0_1` " +
				"GROUP BY t2.`f_name`, w.`total` ORDER BY `total` DESC, `name` LIMIT 1000",
		},
		{
			name: "the WITH's condition on an aggregate stays in the stage",
			query: "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WITH c, count(o) AS total WHERE total > 2 " +
				"MATCH (o2:Order)-[:PLACED_BY]->(c) WHERE o2.amount > 10 RETURN c.name AS name, o2.id AS id",
			want: "SELECT t1.`f_name` AS `name`, t0.`f_id` AS `id` FROM {{.res_order}} t0 " +
				"JOIN {{.res_customer}} t1 ON t0.`f_cust` = t1.`f_code` AND t0.`f_region` = t1.`f_region` " +
				"JOIN (" + stage + " HAVING COUNT(t0.`f_id`) > 2) w ON t1.`f_code` = w.`_key_0_0` AND t1.`f_region` = w.`_key_0_1` " +
				"WHERE t0.`f_amount` > 10 LIMIT 1000",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			analyzed, err := Analyze(tree)
			if err != nil {
				t.Fatalf("%s: %v", tc.query, err)
			}
			plan, err := Compile(analyzed, bindJoinSchema(t), CompileOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.query, err)
			}
			if !plan.SQLOnly {
				t.Errorf("a plan with a carried stage is not marked SQL-only")
			}
			sql, err := Generate(plan, GenerateOptions{DefaultLimit: 1000})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if sql != tc.want {
				t.Errorf("SQL =\n%s\nwant\n%s", sql, tc.want)
			}
		})
	}
}

func TestCarriedStageRefuses(t *testing.T) {
	stage := "MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WITH c, count(o) AS total "
	for _, tc := range []struct {
		query string
		want  string
	}{
		{stage + "MATCH (i:Item) RETURN count(i) AS n", "a node a WITH passes on and the next MATCH leaves out"},
		{stage + "OPTIONAL MATCH (o2:Order)-[:PLACED_BY]->(c) RETURN count(o2) AS n", "OPTIONAL MATCH"},
		{stage + "MATCH (o2:Order)-[:PLACED_BY]->(c) WHERE o2.amount = total RETURN count(*) AS n",
			"a carried value in the next MATCH's conditions"},
		{stage + "MATCH (o2:Order)-[:PLACED_BY]->(c:Order) RETURN count(*) AS n", "one variable with two labels"},
		{"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WITH c AS k, count(o) AS total MATCH (o2:Order)-[:PLACED_BY]->(k) RETURN total",
			"renaming a node a WITH passes on"},
		{"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WITH c, collect(o.id) AS ids MATCH (o2:Order)-[:PLACED_BY]->(c) RETURN size(ids) AS n",
			"a collected list passed on with nodes"},
		{stage + "MATCH (o2:Order)-[:PLACED_BY]->(c) WITH c, total RETURN total", "a WITH after the MATCH that follows a WITH"},
		{stage + "MATCH (o2:Order)-[:PLACED_BY]->(c) RETURN c.name AS name, count(o2) * 1.0 / total AS share",
			"return total as a column of its own"},
	} {
		_, err := compileWithClause(t, tc.query)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s:\nerr = %v, want it to mention %q", tc.query, err, tc.want)
		}
	}
}

// A node passed on can be returned after the WITH, as a graph value, with or
// without another MATCH; the query then runs on the SQL path whatever the
// executor, and the stage is read under the caller's row filters.
func TestCarriedStageReturnsNodesOnTheSQLPath(t *testing.T) {
	east := "east"
	permission := &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/ot_order":    {Kind: "in", Property: "region", Values: []interfaces.RowFilterValue{{Type: "string", String: &east}}},
		"kn_1/ot_customer": {Kind: "true"},
	}}
	for _, query := range []string{
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WITH c, count(o) AS total WHERE total > 1 " +
			"MATCH (o2:Order)-[:PLACED_BY]->(c) RETURN c AS source, 'PLACED' AS relationType, o2 AS target, total",
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WITH o, c, count(*) AS n WHERE n > 1 " +
			"RETURN c AS source, 'PLACED' AS relationType, o AS target, n",
	} {
		vega := &recordingVega{response: &interfaces.RawQueryResponse{Entries: []map[string]any{}}}
		service := graphService(vega)
		service.ps = permission
		order := service.schema.(*fakeSchemaSource).objectTypes[0]
		order.DataProperties = append(order.DataProperties, dataProperty("region", "f_region"))
		result, err := service.Query(callerContext(), interfaces.CypherQuery{KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: query})
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		statement := vega.request.Query
		// Orders are t0 in each part of the statement that reads them.
		if all, kept := strings.Count(statement, "{{.res_order}} t0"),
			strings.Count(statement, "t0.`f_region` IN ('east')"); all == 0 || kept != all {
			t.Errorf("orders read %d times, filtered %d:\n%s", all, kept, statement)
		}
		if result.ValueTypes["source"] == nil || result.ValueTypes["source"].Kind != "object" {
			t.Errorf("value types = %+v", result.ValueTypes)
		}
		impacted := make([]string, 0, len(result.PermissionImpacts))
		for _, impact := range result.PermissionImpacts {
			impacted = append(impacted, impact.ObjectTypeID)
		}
		if !slices.Contains(impacted, "ot_order") {
			t.Errorf("permission impacts = %+v", result.PermissionImpacts)
		}
		var descriptor SemanticQueryDescriptor
		if err := json.Unmarshal(result.TraceDescriptor, &descriptor); err != nil {
			t.Fatalf("descriptor: %v", err)
		}
	}
}

func TestCarriedStageProxyBindingsCoverTheStage(t *testing.T) {
	plan, _ := compileBindJoin(t, "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WITH o, count(i) AS n "+
		"MATCH (o)-[:PLACED_BY]->(c:Customer) RETURN c.name AS name, n", nil)
	resources := planResourceIDs(plan)
	for _, want := range []string{"res_item", "res_order", "res_customer"} {
		if !slices.Contains(resources, want) {
			t.Errorf("resources = %v, want %s", resources, want)
		}
	}
	var objectTypes []string
	for _, binding := range proxyBindings(plan) {
		objectTypes = append(objectTypes, binding.ChildID)
	}
	if !slices.Contains(objectTypes, "ot_item") {
		t.Errorf("proxy bindings = %v, want the stage's object type", objectTypes)
	}
}
