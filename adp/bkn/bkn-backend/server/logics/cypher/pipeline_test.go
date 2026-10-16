// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestPipelineWithValueSemantics(t *testing.T) {
	for _, test := range []struct{ query, rows string }{
		{`WITH 1 AS x RETURN x`, `[{"x":1}]`},
		{`WITH 1 AS x WITH x+1 AS y RETURN y`, `[{"y":2}]`},
		{`WITH 1 AS x WITH x+1 AS x RETURN x`, `[{"x":2}]`},
		{`UNWIND [1,1,2] AS x WITH x,count(*) AS n WHERE n>1 RETURN x,n`, `[{"x":1,"n":2}]`},
		{`WITH [1,2,3] AS xs UNWIND xs AS x WITH sum(x) AS n RETURN n`, `[{"n":6}]`},
		{`UNWIND range(1,1500) AS x WITH x RETURN count(*) AS n`, `[{"n":1500}]`},
		{`UNWIND [3,1,2] AS x WITH x ORDER BY x LIMIT 2 RETURN collect(x) AS xs`, `[{"xs":[1,2]}]`},
		{`UNWIND [3,1,2] AS x WITH x AS y ORDER BY y+1 DESC SKIP 1 LIMIT 1 RETURN y`, `[{"y":2}]`},
		{`UNWIND [1,1,2] AS x WITH x,count(*) AS n ORDER BY n+1 DESC RETURN collect(x) AS xs`, `[{"xs":[1,2]}]`},
		{`UNWIND [1,1,2] AS x WITH x,count(*) AS n ORDER BY count(*) DESC RETURN collect(x) AS xs`, `[{"xs":[1,2]}]`},
		{`UNWIND [3,1,2] AS x WITH DISTINCT x+1 AS y ORDER BY x+1 RETURN collect(y) AS ys`, `[{"ys":[2,3,4]}]`},
		{`UNWIND [3,1,2] AS x RETURN x AS y ORDER BY y+1`, `[{"y":1},{"y":2},{"y":3}]`},
		{`WITH [[1,2],[3]] AS xs UNWIND xs AS x UNWIND x AS y RETURN y`, `[{"y":1},{"y":2},{"y":3}]`},
		{`WITH 1 AS x,2 AS y WITH *,x+y AS z RETURN *`, `[{"x":1,"y":2,"z":3}]`},
		{`WITH 1 AS x WHERE false RETURN count(*) AS n`, `[{"n":0}]`},
		{`WITH 1 AS x RETURN x UNION ALL WITH 2 AS y RETURN y AS x`, `[{"x":1},{"x":2}]`},
		{`UNWIND [] AS x WITH count(*) AS n WHERE n=0 RETURN n`, `[{"n":0}]`},
		{`WITH {a:{b:2}} AS x RETURN x.a.b AS b`, `[{"b":2}]`},
		{`UNWIND [1,2,3] AS x WITH x ORDER BY x LIMIT 2 WHERE x>1 RETURN x`, `[{"x":2}]`},
		{`WITH 1 AS x,2 AS y WITH x AS z WHERE y>1 RETURN z`, `[{"z":1}]`},
	} {
		t.Run(test.query, func(t *testing.T) {
			service, vega, _ := graphService(t)
			request := graphRequest(test.query)
			request.ResultFormat = ""
			result, err := service.Query(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			var expected []map[string]any
			if err := json.Unmarshal([]byte(test.rows), &expected); err != nil {
				t.Fatal(err)
			}
			assertExpressionRows(t, result.Entries, expected)
			if vega.request != nil {
				t.Fatal("tableless stage queried Vega")
			}
		})
	}
}

func TestReadOnlyPinnedTCKWithResults(t *testing.T) {
	raw, err := os.ReadFile("testdata/tck_m19_with.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []struct {
			Source, Scenario, Query string
			Expected                []map[string]any
		}
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	if len(suite.Cases) != 14 {
		t.Fatal("pinned WITH coverage changed")
	}
	for _, test := range suite.Cases {
		t.Run(test.Source+"/"+test.Scenario, func(t *testing.T) {
			service, vega, _ := graphService(t)
			result, err := service.Query(context.Background(), graphRequest(test.Query))
			if err != nil {
				t.Fatal(err)
			}
			assertExpressionRows(t, result.Entries, test.Expected)
			if vega.request != nil {
				t.Fatal("tableless WITH read facts")
			}
		})
	}
}

func TestPipelineScopeErrorsPrecedeReads(t *testing.T) {
	for _, query := range []string{
		`MATCH (p:Person) WITH p.id AS id RETURN p.name`,
		`MATCH (p:Person) WITH p.id AS id,id+1 AS other RETURN other`,
		`WITH 1 AS x WITH x+1 RETURN x`,
		`WITH 1 AS x UNWIND [2] AS x RETURN x`,
		`WITH 1 AS x MATCH (x:Person) RETURN x`,
		`WITH 1 AS x MATCH (p:Missing) RETURN x`,
		`MATCH (p:Person) WITH p AS q RETURN q.unknown`,
		`MATCH (p:Person) WITH p CREATE (q:Person) RETURN q`,
		`MATCH (p:Person) WITH p LIMIT 10001 RETURN p`,
	} {
		t.Run(query, func(t *testing.T) {
			service, vega, _ := graphService(t)
			if result, err := service.Query(context.Background(), graphRequest(query)); err == nil || result != nil || vega.request != nil {
				t.Fatalf("invalid stage executed: %+v, %v", result, err)
			}
		})
	}
}

func pipelineFixture(t *testing.T, request interfaces.CypherQuery, observations map[int][]map[string]any) (*cypherQueryService, *queuedUnionVega) {
	t.Helper()
	service, base, _ := graphService(t)
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	vega := &queuedUnionVega{recordingVega: base}
	for index, stage := range compiled.plan.Stages {
		if stage.Kind != "match" {
			continue
		}
		response := &interfaces.RawQueryResponse{Entries: []map[string]any{}}
		for _, values := range observations[index] {
			response.Entries = append(response.Entries, graphRow(t, compiled.stages[index], values))
		}
		vega.responses = append(vega.responses, response)
	}
	service.vba = vega
	return service, vega
}

func TestPipelineNodeAliasPropertiesAreAuthorizedAndPrivate(t *testing.T) {
	request := graphRequest(`MATCH (p:Person) WITH p AS q RETURN q.name AS name,q AS node`)
	service, _ := pipelineFixture(t, request, map[int][]map[string]any{0: {{"p.id": "p1", "p.name": "Alice"}}})
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Entries[0]["name"] != "Alice" {
		t.Fatalf("alias property lost: %+v", result.Entries)
	}
	raw, _ := json.Marshal(result.Entries[0]["node"])
	if strings.Contains(string(raw), "Alice") {
		t.Fatalf("implicit snapshot expansion: %s", raw)
	}
	for _, level := range []string{interfaces.PROPERTY_ACCESS_MASKED, interfaces.PROPERTY_ACCESS_NONE} {
		service, vega, permission := graphService(t)
		permission.levels = map[string]string{interfaces.KNChildResourceID("kn_1", "person") + ".name": level}
		if _, err := service.Query(context.Background(), request); err == nil || vega.request != nil {
			t.Fatal("alias bypassed property access")
		}
	}
}

func TestPipelineCollectUnwindPreservesNodeOrigin(t *testing.T) {
	request := graphRequest(`MATCH (p:Person) WITH collect(p) AS people UNWIND people AS q RETURN q.name AS name`)
	service, _ := pipelineFixture(t, request, map[int][]map[string]any{0: {{"p.id": "p1", "p.name": "Alice"}, {"p.id": "p2", "p.name": "Bob"}}})
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertExpressionRows(t, result.Entries, []map[string]any{{"name": "Alice"}, {"name": "Bob"}})
}

func TestPipelineWithAliasCorrelatesLaterMatch(t *testing.T) {
	request := graphRequest(`MATCH (p:Person) WITH p AS q MATCH (q)-[:WORKS_AT]->(c:Company) RETURN q.name AS name,c.id AS company`)
	service, vega := pipelineFixture(t, request, map[int][]map[string]any{
		0: {{"p.id": "p1", "p.name": "Alice"}},
		2: {{"q.id": "p1", "q.name": "Alice", "c.id": "c1"}, {"q.id": "p2", "q.name": "Bob", "c.id": "c2"}},
	})
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertExpressionRows(t, result.Entries, []map[string]any{{"name": "Alice", "company": "c1"}})
	if len(vega.requests) != 2 {
		t.Fatal("staged reads missing")
	}
}

func TestPipelineUnwindScalarCorrelatesMatchPredicate(t *testing.T) {
	request := graphRequest(`UNWIND ['p1','p2'] AS id MATCH (p:Person) WHERE p.id=id RETURN id,p.name AS name`)
	service, _ := pipelineFixture(t, request, map[int][]map[string]any{1: {{"p.id": "p2", "p.name": "Bob"}, {"p.id": "p3", "p.name": "Carol"}}})
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertExpressionRows(t, result.Entries, []map[string]any{{"id": "p2", "name": "Bob"}})
}

func TestPipelineOptionalMatchNullExtendsAfterPredicate(t *testing.T) {
	request := graphRequest(`MATCH (p:Person) OPTIONAL MATCH (p)-[:WORKS_AT]->(c:Company) WHERE c.id='c1' RETURN p.id AS person,c.id AS company`)
	service, _ := pipelineFixture(t, request, map[int][]map[string]any{
		0: {{"p.id": "p1"}, {"p.id": "p2"}},
		1: {{"p.id": "p1", "c.id": "c1"}, {"p.id": "p2", "c.id": "c2"}},
	})
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertExpressionRows(t, result.Entries, []map[string]any{{"person": "p1", "company": "c1"}, {"person": "p2", "company": nil}})
}

func TestPipelineInitialOptionalAndMandatoryAfterNull(t *testing.T) {
	for _, test := range []struct {
		query string
		count int
	}{
		{`OPTIONAL MATCH (p:Person) RETURN count(*) AS n`, 1},
		{`OPTIONAL MATCH (p:Person) MATCH (p)-[:WORKS_AT]->(c:Company) RETURN count(*) AS n`, 0},
	} {
		t.Run(test.query, func(t *testing.T) {
			request := graphRequest(test.query)
			service, _ := pipelineFixture(t, request, nil)
			result, err := service.Query(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			assertExpressionRows(t, result.Entries, []map[string]any{{"n": int64(test.count)}})
		})
	}
}

func TestPipelineOptionalRowPolicyIsAppliedBeforeNullExtension(t *testing.T) {
	request := graphRequest(`MATCH (p:Person) OPTIONAL MATCH (p)-[:WORKS_AT]->(c:Company) RETURN p.id AS id,c AS company`)
	service, vega := pipelineFixture(t, request, map[int][]map[string]any{0: {{"p.id": "p1"}}})
	permission := service.ps.(*stubPermission)
	permission.rowFilters = map[string]interfaces.RowFilterPredicate{interfaces.KNChildResourceID("kn_1", "company"): {Kind: "false"}}
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertExpressionRows(t, result.Entries, []map[string]any{{"id": "p1", "company": nil}})
	if len(vega.requests) != 2 || !strings.Contains(vega.requests[1].Query, "1 = 0") {
		t.Fatal("optional row policy was not pushed into the observation")
	}
}

func TestPipelineEmptyInputSkipsLaterReadsButNotAuthorization(t *testing.T) {
	request := graphRequest(`MATCH (p:Person) WITH p WHERE false MATCH (q:Person) RETURN q.name`)
	service, vega := pipelineFixture(t, request, map[int][]map[string]any{0: {{"p.id": "p1"}}})
	result, err := service.Query(context.Background(), request)
	if err != nil || len(result.Entries) != 0 || len(vega.requests) != 1 {
		t.Fatalf("empty stage: %+v, %v, %d", result, err, len(vega.requests))
	}
	service, raw, permission := graphService(t)
	permission.levels = map[string]string{interfaces.KNChildResourceID("kn_1", "person") + ".name": interfaces.PROPERTY_ACCESS_MASKED}
	if _, err := service.Query(context.Background(), request); err == nil || raw.request != nil {
		t.Fatal("empty intermediate result bypassed pre-read authorization")
	}
}

func TestPipelineSharedBudgetCountsIntermediateStages(t *testing.T) {
	service, _, _ := graphService(t)
	request := graphRequest(`UNWIND range(1,4000) AS x WITH x RETURN count(*) AS n`)
	result, err := service.Query(context.Background(), request)
	if err == nil || result != nil {
		t.Fatal("small aggregate output bypassed cumulative stage work budget")
	}
}

func TestPipelineUnwindNodeListPropertyUsesOriginalObservation(t *testing.T) {
	request := graphRequest(`MATCH (p:Person) UNWIND [p] AS q RETURN q.name AS name`)
	service, vega, _ := graphService(t)
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	vega.response.Entries = []map[string]any{graphRow(t, compiled, map[string]any{"p.id": "p1", "p.name": "Alice"})}
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertExpressionRows(t, result.Entries, []map[string]any{{"name": "Alice"}})
}
