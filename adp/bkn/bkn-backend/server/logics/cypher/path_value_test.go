// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestFixedPathValuesAndFunctions(t *testing.T) {
	for _, pattern := range []string{
		"route=(p:Person)-[:WORKS_AT]->(c:Company)",
		"route=(c:Company)<-[:WORKS_AT]-(p:Person)",
	} {
		t.Run(pattern, func(t *testing.T) {
			service, vega, _ := graphService(t)
			request := graphRequest("MATCH " + pattern + " RETURN route,length(route) AS length,[n IN nodes(route) | n.id] AS ids,type(head(relationships(route))) AS relation")
			request.ResultFormat = ""
			compiled, err := service.compile(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			vega.response.Entries = []map[string]any{graphRow(t, compiled, map[string]any{"p.id": "p1", "c.id": "c1"})}
			result, err := service.Query(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			path := result.Entries[0]["route"].(interfaces.CypherPathValue)
			if len(path.Nodes) != 2 || len(path.Relationships) != 1 || path.Relationships[0].Source.ObjectTypeID != "person" || path.Relationships[0].Target.ObjectTypeID != "company" || result.Entries[0]["length"] != int64(1) || result.Entries[0]["relation"] != "works_at" {
				t.Fatalf("invalid traversal/canonical direction: %+v", result)
			}
			if result.ValueTypes["route"].Kind != "path" {
				t.Fatal("path type missing")
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "EvaluationProperties") {
				t.Fatal("internal path fields leaked")
			}
			descriptor := string(result.TraceDescriptor)
			if !strings.Contains(descriptor, "relation:kn_1:works_at") || strings.Contains(descriptor, "res_person") {
				t.Fatal("missing or physical path provenance", descriptor)
			}
		})
	}
}

func TestFixedPathThroughScopeAndZeroLength(t *testing.T) {
	request := graphRequest("MATCH route=(p:Person) WITH route RETURN length(route) AS length,head(nodes(route)).name AS name")
	service, _ := pipelineFixture(t, request, map[int][]map[string]any{0: {{"p.id": "p1", "p.name": "Alice"}}})
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertExpressionRows(t, result.Entries, []map[string]any{{"length": int64(0), "name": "Alice"}})
	for _, query := range []string{
		"MATCH p=(p:Person) RETURN p",
		"MATCH q=(p:Person), q=(c:Company) RETURN q",
		"MATCH r=(p:Person)-[r:WORKS_AT]->(c:Company) RETURN r",
	} {
		service, vega, _ := graphService(t)
		if result, err := service.Query(context.Background(), graphRequest(query)); err == nil || result != nil || vega.request != nil {
			t.Fatal("path variable collision admitted", query, err)
		}
	}
}

func TestAnonymousFixedPathNodes(t *testing.T) {
	for _, query := range []string{
		"MATCH route=(:Person)-[:WORKS_AT]->(:Company) RETURN length(route) AS hops",
		"MATCH route=(:Person)-[:WORKS_AT]->(:Company) WITH route RETURN length(route) AS hops",
	} {
		service, data := patternTestService(t)
		request := graphRequest(query)
		attachPatternFixture(t, service, request, data)
		result, err := service.Query(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		assertExpressionRows(t, result.Entries, []map[string]any{{"hops": int64(1)}})
	}
}
