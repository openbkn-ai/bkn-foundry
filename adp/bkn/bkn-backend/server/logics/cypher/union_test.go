// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// partsVega records every statement and answers each from a queue, so a test
// can see a UNION run part by part.
type partsVega struct {
	recordingVega
	statements []*interfaces.RawQueryRequest
	answers    []*interfaces.RawQueryResponse
}

func (v *partsVega) RawQuery(_ context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	v.statements = append(v.statements, req)
	v.request = req
	if len(v.answers) == 0 {
		return &interfaces.RawQueryResponse{}, nil
	}
	answer := v.answers[0]
	v.answers = v.answers[1:]
	return answer, nil
}

func codes(values ...string) *interfaces.RawQueryResponse {
	entries := make([]map[string]any, 0, len(values))
	for _, value := range values {
		entries = append(entries, map[string]any{"code": value})
	}
	return &interfaces.RawQueryResponse{Columns: []interfaces.RawQueryColumn{{Name: "code"}}, Entries: entries}
}

func unionService(vega interfaces.VegaBackendAccess, permission *stubPermission) *cypherQueryService {
	service := relationService(nil, permission, nil)
	service.vba = vega
	return service
}

// vega-backend runs one SELECT at a time, so each part runs as its own
// statement, keeping its ORDER BY and LIMIT, and the rows are put together:
// all of them for UNION ALL, each distinct row once for UNION.
func TestUnionRunsEachPartAsItsOwnStatement(t *testing.T) {
	for all, want := range map[bool][]string{true: {"c1", "c2", "c2", "c3"}, false: {"c1", "c2", "c3"}} {
		keyword := "UNION"
		if all {
			keyword = "UNION ALL"
		}
		vega := &partsVega{answers: []*interfaces.RawQueryResponse{codes("c1", "c2"), codes("c2", "c3")}}
		result, err := unionService(vega, &stubPermission{}).Query(callerContext(), interfaces.CypherQuery{
			KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
			Query: "MATCH (o:Order) RETURN o.customer_code AS code ORDER BY code LIMIT 5 " +
				keyword + " MATCH (c:Customer) RETURN c.code AS code",
		})
		if err != nil {
			t.Fatalf("%s: %v", keyword, err)
		}
		if len(vega.statements) != 2 || strings.Contains(vega.statements[0].Query, "UNION") ||
			!strings.Contains(vega.statements[0].Query, "LIMIT 5") || !strings.Contains(vega.statements[1].Query, "LIMIT 1000") {
			t.Fatalf("%s: statements = %+v", keyword, vega.statements)
		}
		got := []string{}
		for _, entry := range result.Entries {
			got = append(got, entry["code"].(string))
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: rows = %v, want %v", keyword, got, want)
		}
		if result.Limited {
			t.Errorf("%s: a UNION is not marked as cut at the default limit", keyword)
		}
		var descriptor SemanticQueryDescriptor
		if err := json.Unmarshal(result.TraceDescriptor, &descriptor); err != nil {
			t.Fatalf("trace descriptor: %v", err)
		}
		if len(descriptor.Objects) != 2 || descriptor.Objects[1].Alias != "union1.c" {
			t.Errorf("objects = %+v", descriptor.Objects)
		}
	}
}

// A constant labels a part, as in 'large' AS kind.
func TestUnionOfPartsLabelledByConstants(t *testing.T) {
	vega := &partsVega{}
	_, err := unionService(vega, &stubPermission{}).Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order) WHERE o.amount > 3 RETURN 'large' AS kind, count(*) AS n " +
			"UNION ALL MATCH (o:Order) WHERE o.amount <= 3 RETURN 'small' AS kind, count(*) AS n",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(vega.statements) != 2 {
		t.Errorf("statements = %d", len(vega.statements))
	}
}

func TestUnionRefuses(t *testing.T) {
	for query, want := range map[string]string{
		"MATCH (o:Order) RETURN o.id AS id UNION MATCH (c:Customer) RETURN c.code AS code":                                           "the same columns in the same order",
		"MATCH (o:Order) RETURN o.id AS a, o.amount AS b UNION MATCH (o:Order) RETURN o.amount AS b, o.id AS a":                      "the same columns in the same order",
		"MATCH (o:Order) RETURN o AS n UNION MATCH (o:Order) RETURN o AS n":                                                          "UNION of nodes",
		"MATCH (o:Order) RETURN o.id AS id UNION MATCH (c:Customer) RETURN c.code AS id UNION ALL MATCH (o:Order) RETURN o.id AS id": "mixing UNION and UNION ALL",
	} {
		// Nodes need primary keys to be returned at all.
		vega := &recordingVega{}
		_, err := graphService(vega).Query(callerContext(), interfaces.CypherQuery{
			KNID: "kn_1", Branch: interfaces.MAIN_BRANCH, Query: query,
		})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to mention %q", query, err, want)
		}
		if vega.request != nil {
			t.Errorf("%s: a refused query reached vega-backend", query)
		}
	}
}

// Row filters hold in every part, and each part's object type is reported.
func TestUnionAppliesRowFiltersToEveryPart(t *testing.T) {
	east, ann := "east", "Ann"
	permission := &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/ot_order":    {Kind: "in", Property: "region", Values: []interfaces.RowFilterValue{{Type: "string", String: &east}}},
		"kn_1/ot_customer": {Kind: "in", Property: "name", Values: []interfaces.RowFilterValue{{Type: "string", String: &ann}}},
	}}
	vega := &partsVega{}
	service := unionService(vega, permission)
	order := service.schema.(*fakeSchemaSource).objectTypes[0]
	order.DataProperties = append(order.DataProperties, dataProperty("region", "f_region"))

	result, err := service.Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order) RETURN o.customer_code AS code UNION ALL MATCH (c:Customer) RETURN c.code AS code",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(vega.statements) != 2 ||
		!strings.Contains(vega.statements[0].Query, "{{.res_order}} t0 WHERE t0.`f_region` IN ('east')") ||
		!strings.Contains(vega.statements[1].Query, "{{.res_customer}} t0 WHERE t0.`f_name` IN ('Ann')") {
		t.Errorf("statements = %+v", vega.statements)
	}
	impacted := make([]string, 0, len(result.PermissionImpacts))
	for _, impact := range result.PermissionImpacts {
		impacted = append(impacted, impact.ObjectTypeID)
	}
	if !slices.Contains(impacted, "ot_order") || !slices.Contains(impacted, "ot_customer") {
		t.Errorf("permission impacts = %+v", result.PermissionImpacts)
	}
}

// Explain names the shape and the first part's columns.
func TestExplainUnion(t *testing.T) {
	plan, _, err := explainQuery(t, graphService(&recordingVega{}),
		"MATCH (o:Order) RETURN o.customer_code AS code, count(*) AS n UNION ALL MATCH (c:Customer) RETURN c.code AS code, count(*) AS n")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if plan.Shape != "union" || len(plan.Columns) != 2 || plan.Columns[1].Name != "n" || len(plan.Objects) != 2 {
		t.Errorf("plan = %+v", plan)
	}
}

// Over datasets and indexes each part runs through the data API, as it would
// on its own.
func TestUnionOutsideSQL(t *testing.T) {
	vega := &datasetVega{accountDataAPI: accountDataAPI{api: newFakeDataAPI(relationServiceData())}}
	result, err := datasetService(vega).Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: interfaces.MAIN_BRANCH,
		Query: "MATCH (o:Order) RETURN o.id AS id UNION ALL MATCH (o:Order) RETURN o.id AS id",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Entries) != 4 || len(vega.readers) == 0 {
		t.Errorf("entries = %v, readers = %v", result.Entries, vega.readers)
	}
}
