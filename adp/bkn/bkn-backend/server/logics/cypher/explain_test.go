// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func explainQuery(t *testing.T, service *cypherQueryService, query string) (*interfaces.CypherPlan, *recordingVega, error) {
	t.Helper()
	vega := service.vba.(*recordingVega)
	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: query, Explain: true,
	})
	if err != nil {
		return nil, vega, err
	}
	if len(result.Entries) != 0 || result.Plan == nil {
		t.Fatalf("explain returned rows or no plan: %+v", result)
	}
	return result.Plan, vega, nil
}

// Explain compiles and authorizes as running does, and reads nothing.
func TestExplainDescribesWithoutReading(t *testing.T) {
	service := graphService(&recordingVega{})
	plan, vega, err := explainQuery(t, service,
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WHERE o.amount > 30 "+
			"RETURN c.name AS name, count(o) AS n ORDER BY n DESC")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if vega.request != nil || vega.proxyRequest != nil {
		t.Fatalf("explain sent a statement to vega-backend: %+v %+v", vega.request, vega.proxyRequest)
	}
	if plan.Shape != "aggregate" || plan.Graph {
		t.Errorf("shape = %q, graph = %v", plan.Shape, plan.Graph)
	}
	if len(plan.Objects) != 2 || plan.Objects[0].ObjectTypeID != "ot_order" || plan.Objects[1].ObjectTypeID != "ot_customer" {
		t.Errorf("objects = %+v", plan.Objects)
	}
	if len(plan.Relations) != 1 || plan.Relations[0].RelationTypeID != "rt_placed_by" {
		t.Errorf("relations = %+v", plan.Relations)
	}
	if len(plan.Conditions) != 1 || plan.Conditions[0].Operator != ">" ||
		!slices.Equal(plan.Conditions[0].Properties, []string{"ot_order.amount"}) || plan.Conditions[0].Where != "where" {
		t.Errorf("conditions = %+v", plan.Conditions)
	}
	if len(plan.Columns) != 2 || plan.Columns[1].Name != "n" || plan.Columns[1].Kind != "count" ||
		plan.Columns[1].Type == nil || plan.Columns[1].Type.Kind != "integer" {
		t.Errorf("columns = %+v", plan.Columns)
	}
	if len(plan.Ordering) != 1 || plan.Ordering[0].Column != "n" || !plan.Ordering[0].Descending {
		t.Errorf("ordering = %+v", plan.Ordering)
	}
	if plan.Limit != interfaces.CYPHER_DEFAULT_LIMIT || plan.LimitSource != "default" ||
		!slices.ContainsFunc(plan.Warnings, func(w string) bool { return strings.HasPrefix(w, "no LIMIT") }) {
		t.Errorf("limit = %d %s, warnings = %v", plan.Limit, plan.LimitSource, plan.Warnings)
	}
	if !slices.Contains(plan.Reads, "ot_order.amount") || !slices.Contains(plan.Reads, "ot_customer.name") {
		t.Errorf("reads = %v", plan.Reads)
	}
}

// A query explain accepts runs, and one it refuses is refused with the same
// error, code and hint alike.
func TestExplainRefusesWhatRunningRefuses(t *testing.T) {
	for _, query := range []string{
		"MATCH (o:Order) RETURN o.amount != 3",
		"MATCH (o:Order) WITH collect(o.id) AS a UNWIND a AS x RETURN x",
		"MATCH (o:Nope) RETURN o.id",
		"MATCH (o:Order) RETURN o.nope",
		"MATCH (o:Order) RETURN o.amount / 0",
	} {
		_, _, explained := explainQuery(t, graphService(&recordingVega{}), query)
		_, ran := graphService(&recordingVega{}).Query(callerContext(), interfaces.CypherQuery{
			KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: query,
		})
		var a, b *rest.HTTPError
		if !errors.As(explained, &a) || !errors.As(ran, &b) {
			t.Errorf("%s: explain err = %v, run err = %v", query, explained, ran)
			continue
		}
		if a.HTTPCode != b.HTTPCode || a.BaseError.ErrorCode != b.BaseError.ErrorCode || a.BaseError.ErrorDetails != b.BaseError.ErrorDetails {
			t.Errorf("%s: explain %+v, run %+v", query, a.BaseError, b.BaseError)
		}
	}
}

func TestExplainNamesEachShape(t *testing.T) {
	for _, tc := range []struct{ query, shape string }{
		{"MATCH (o:Order) RETURN o.id", "match"},
		{"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WITH c, count(o) AS n WHERE n > 1 RETURN count(*) AS groups", "totals"},
		{"MATCH (o:Order) WITH DISTINCT o.customer_code AS code MATCH (c:Customer) WHERE c.code = code RETURN c.name", "semi_join"},
		{"MATCH (o:Order) WITH collect(DISTINCT o.customer_code) AS a MATCH (c:Customer) WITH a, collect(DISTINCT c.code) AS b " +
			"RETURN size([x IN a WHERE x IN b]) AS common", "list_comparison"},
		{"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) WITH c, count(o) AS total MATCH (o2:Order)-[:PLACED_BY]->(c) " +
			"RETURN c.name AS name, count(o2) AS n, total", "carried"},
	} {
		plan, _, err := explainQuery(t, graphService(&recordingVega{}), tc.query)
		if err != nil {
			t.Errorf("%s: %v", tc.query, err)
			continue
		}
		if plan.Shape != tc.shape {
			t.Errorf("%s: shape = %q, want %q", tc.query, plan.Shape, tc.shape)
		}
		for _, column := range plan.Columns {
			if column.Type == nil {
				t.Errorf("%s: column %q has no type", tc.query, column.Name)
			}
		}
	}
}

func TestExplainWarns(t *testing.T) {
	plan, _, err := explainQuery(t, graphService(&recordingVega{}), "MATCH (o:Order), (c:Customer) RETURN o.id, c.name LIMIT 5")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !slices.ContainsFunc(plan.Warnings, func(w string) bool { return strings.Contains(w, "share no node") }) ||
		slices.ContainsFunc(plan.Warnings, func(w string) bool { return strings.HasPrefix(w, "no LIMIT") }) {
		t.Errorf("warnings = %v", plan.Warnings)
	}

	plan, _, err = explainQuery(t, graphService(&recordingVega{}),
		"MATCH (o:Order)-[:PLACED_BY]->(c:Customer) RETURN c AS source, 'X' AS relationType, c AS target, collect(o) AS evidence LIMIT 5")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !plan.Graph || !slices.ContainsFunc(plan.Warnings, func(w string) bool { return strings.HasPrefix(w, "collect reads its evidence") }) {
		t.Errorf("graph = %v, warnings = %v", plan.Graph, plan.Warnings)
	}
	if source := plan.Columns[0]; source.Kind != "node" || source.Type.Kind != "object" || source.Type.ObjectTypeID != "ot_customer" {
		t.Errorf("source column = %+v %+v", source, source.Type)
	}

	east := "east"
	service := graphService(&recordingVega{})
	service.ps = &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/ot_order":    {Kind: "in", Property: "region", Values: []interfaces.RowFilterValue{{Type: "string", String: &east}}},
		"kn_1/ot_customer": {Kind: "true"},
	}}
	service.schema.(*fakeSchemaSource).objectTypes[0].DataProperties = append(
		service.schema.(*fakeSchemaSource).objectTypes[0].DataProperties, dataProperty("region", "f_region"))
	plan, _, err = explainQuery(t, service, "MATCH (o:Order) RETURN count(*) AS n")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !slices.Equal(plan.RowFiltered, []string{"ot_order"}) ||
		!slices.ContainsFunc(plan.Warnings, func(w string) bool { return strings.Contains(w, "access policy narrows") }) {
		t.Errorf("row filtered = %v, warnings = %v", plan.RowFiltered, plan.Warnings)
	}
}
