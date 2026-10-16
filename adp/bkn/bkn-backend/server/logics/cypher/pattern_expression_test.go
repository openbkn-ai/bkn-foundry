// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"strings"
	"testing"
)

func TestCorrelatedPatternExpressions(t *testing.T) {
	tests := []struct {
		query string
		rows  []map[string]any
	}{
		{"MATCH (p:Person) RETURN p.id AS id,[(p)-[:FRIEND]->(q:Person) | q.name] AS friends ORDER BY id", []map[string]any{{"id": "p1", "friends": []any{"Bob"}}, {"id": "p2", "friends": []any{"Cara"}}, {"id": "p3", "friends": []any{"Alice"}}}},
		{"MATCH (p:Person) WHERE p:Person AND NOT p:Company RETURN p.id AS id ORDER BY id", []map[string]any{{"id": "p1"}, {"id": "p2"}, {"id": "p3"}}},
		{"MATCH (p:Person) WHERE p:NoSuchType RETURN p.id AS id", []map[string]any{}},
		{"RETURN null:Person AS result", []map[string]any{{"result": nil}}},
		{"MATCH (p:Person) WHERE (p)-[:WORKS_AT]->(:Company) RETURN p.id AS id", []map[string]any{{"id": "p1"}}},
		{"MATCH (p:Person) WHERE EXISTS { (p)-[:WORKS_AT]->(c:Company) WHERE c.id='c1' } RETURN p.id AS id", []map[string]any{{"id": "p1"}}},
		{"MATCH (p:Person) WHERE NOT EXISTS { (p)-[:WORKS_AT]->(:Company) } RETURN p.id AS id ORDER BY id", []map[string]any{{"id": "p2"}, {"id": "p3"}}},
		{"MATCH (p:Person) WHERE EXISTS { MATCH (p)-[:WORKS_AT]->(c:Company) RETURN c.id AS id UNION MATCH (p)-[:FRIEND]->(q:Person) WHERE q.name='Cara' RETURN q.id AS id } RETURN p.id AS id ORDER BY id", []map[string]any{{"id": "p1"}, {"id": "p2"}}},
		{"MATCH (p:Person) WHERE EXISTS { MATCH (p)-[:WORKS_AT]->(c:Company) RETURN c } RETURN p.id AS id", []map[string]any{{"id": "p1"}}},
		{"MATCH (p:Person) WHERE EXISTS { MATCH (p)-[:WORKS_AT]->(c:Company) WITH count(c) AS n WHERE n>0 RETURN n } RETURN p.id AS id", []map[string]any{{"id": "p1"}}},
		{"MATCH (p:Person) WHERE EXISTS { (p)-[:FRIEND]->(q:Person) WHERE EXISTS { (q)-[:WORKS_AT]->(:Company) } } RETURN p.id AS id", []map[string]any{{"id": "p3"}}},
		{"MATCH (p:Person) WHERE EXISTS { MATCH (q:Person) WHERE EXISTS { (p)-[:FRIEND]->(q) WHERE q.name='Bob' } RETURN q } RETURN p.id AS id", []map[string]any{{"id": "p1"}}},
		{"MATCH (p:Person) RETURN DISTINCT EXISTS { (p)-[:WORKS_AT]->(:Company) } AS employed ORDER BY EXISTS { (p)-[:WORKS_AT]->(:Company) }", []map[string]any{{"employed": false}, {"employed": true}}},
		{"RETURN percentileCont(1,CASE WHEN EXISTS { MATCH (p:Person) RETURN p } THEN 0.5 ELSE 0 END) AS result", []map[string]any{{"result": float64(1)}}},
		{"RETURN [(a:Person)-[:WORKS_AT]->(c:Company) | c.id] AS ids", []map[string]any{{"ids": []any{"c1"}}}},
		{"MATCH (p:Person) RETURN p.id AS id,[route=(p)-[:FRIEND*1..2]->(q:Person) WHERE q.name='Cara' | length(route)] AS distances ORDER BY id", []map[string]any{{"id": "p1", "distances": []any{int64(2)}}, {"id": "p2", "distances": []any{int64(1)}}, {"id": "p3", "distances": []any{}}}},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			service, data := patternTestService(t)
			request := graphRequest(test.query)
			request.ResultFormat = ""
			attachPatternFixture(t, service, request, data)
			result, err := service.Query(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			assertExpressionRows(t, result.Entries, test.rows)
		})
	}
}

func TestCorrelatedPatternReadsArePlannedAndCached(t *testing.T) {
	service, data := patternTestService(t)
	request := graphRequest("MATCH (p:Person) RETURN p.id AS id,[(p)-[:FRIEND]->(q:Person) | q.name] AS friends")
	vega := attachPatternFixture(t, service, request, data)
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 3 || len(vega.requests) != 3 {
		t.Fatalf("subquery reread per input row: rows=%d reads=%d", len(result.Entries), len(vega.requests))
	}
	if !strings.Contains(string(result.TraceDescriptor), "subqueries") || !strings.Contains(string(result.TraceDescriptor), "property:kn_1:person:name") {
		t.Fatal("subquery provenance missing")
	}
}

func TestPatternExpressionScopeAndPreReadAuthorization(t *testing.T) {
	for _, query := range []string{
		"MATCH (p:Person) WHERE (p)-[:FRIEND]->(notBound) RETURN p",
		"MATCH (p:Person) RETURN [(p)-[:FRIEND]->(q:Person) | q] AS friends,q",
		"MATCH (p:Person) WHERE EXISTS { MATCH (p)-[:FRIEND]->(q) SET q.name='x' } RETURN p",
		"MATCH (p:Person) RETURN [(p)-[:FRIEND]->(q:Person) | collect(q)] AS friends",
	} {
		service, _, _ := graphService(t)
		if result, err := service.Query(context.Background(), graphRequest(query)); err == nil || result != nil {
			t.Fatal("invalid subquery accepted", query, err)
		}
	}
	for _, query := range []string{
		"MATCH (p:Person) RETURN CASE WHEN false THEN [(p)-[:FRIEND]->(q:Person) | q.name] ELSE [] END AS friends",
		"MATCH (p:Person) WHERE EXISTS { (p)-[:FRIEND]->(q:Person) WHERE q.name='Bob' } RETURN p.id",
		"MATCH (p:Person) WHERE EXISTS { MATCH (q:Person) WHERE q.name='Bob' RETURN q } RETURN p.id",
	} {
		service, _ := patternTestService(t)
		permission := service.ps.(*stubPermission)
		permission.levels = map[string]string{"kn_1/person.name": "masked"}
		original := service.vba.(*recordingVega)
		if result, err := service.Query(context.Background(), graphRequest(query)); err == nil || result != nil || original.request != nil {
			t.Fatal("subquery or lazy branch bypassed authorization", query, err)
		}
	}
}
