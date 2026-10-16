// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestReadOnlyPinnedTCKUnionUnwindResults(t *testing.T) {
	raw, err := os.ReadFile("testdata/tck_m19_union_unwind.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Source, Scenario, Query, Error string
			Expected                       []map[string]any
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 15 {
		t.Fatalf("unexpected selected coverage: %d", len(fixture.Cases))
	}
	for _, test := range fixture.Cases {
		t.Run(test.Source+"/"+test.Scenario, func(t *testing.T) {
			service, vega, _ := graphService(t)
			request := graphRequest(test.Query)
			request.ResultFormat = ""
			result, err := service.Query(context.Background(), request)
			if test.Error != "" {
				if err == nil || !strings.Contains(err.Error(), test.Error) {
					t.Fatalf("expected %q, got %v", test.Error, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertExpressionRows(t, result.Entries, test.Expected)
			}
			if vega.request != nil {
				t.Fatal("tableless query read facts")
			}
		})
	}
}

func assertExpressionRows(t *testing.T, actual, expected []map[string]any) {
	t.Helper()
	a, err := expressionKey(actual)
	if err != nil {
		t.Fatal(err)
	}
	b, err := expressionKey(expected)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("rows = %#v; expected %#v", actual, expected)
	}
}

func TestUnwindAndUnionValueSemantics(t *testing.T) {
	for _, test := range []struct{ query, rows string }{
		{`UNWIND [1,null,1] AS x RETURN x`, `[{"x":1},{"x":null},{"x":1}]`},
		{`UNWIND [[1,2],[3],4] AS x UNWIND x AS y RETURN y`, `[{"y":1},{"y":2},{"y":3},{"y":4}]`},
		{`UNWIND [1,2] AS x UNWIND [x,x+1] AS y RETURN x,y ORDER BY x,y`, `[{"x":1,"y":1},{"x":1,"y":2},{"x":2,"y":2},{"x":2,"y":3}]`},
		{`UNWIND [{a:1},{a:2}] AS x RETURN x.a AS a`, `[{"a":1},{"a":2}]`},
		{`UNWIND [] AS x RETURN count(*) AS c,collect(x) AS xs`, `[{"c":0,"xs":[]}]`},
		{`UNWIND null AS x RETURN count(*) AS c`, `[{"c":0}]`},
		{`UNWIND 7 AS x RETURN x`, `[{"x":7}]`},
		{`RETURN 1 AS x UNION RETURN 1.0 AS x`, `[{"x":1}]`},
		{`RETURN null AS x UNION RETURN null AS x`, `[{"x":null}]`},
		{`RETURN {a:[1,null]} AS x UNION RETURN {a:[1.0,null]} AS x`, `[{"x":{"a":[1,null]}}]`},
		{`RETURN 1 AS a,2 AS b UNION RETURN 2 AS b,1 AS a`, `[{"a":1,"b":2}]`},
		{`UNWIND [3,1,2] AS x RETURN x ORDER BY x LIMIT 1 UNION ALL RETURN 9 AS x`, `[{"x":1},{"x":9}]`},
		{`UNWIND [2,2,1] AS x RETURN DISTINCT x ORDER BY x DESC SKIP 1 LIMIT 1`, `[{"x":1}]`},
	} {
		t.Run(test.query, func(t *testing.T) {
			service, _, _ := graphService(t)
			result, err := service.Query(context.Background(), graphRequest(test.query))
			if err != nil {
				t.Fatal(err)
			}
			var expected []map[string]any
			if err := json.Unmarshal([]byte(test.rows), &expected); err != nil {
				t.Fatal(err)
			}
			assertExpressionRows(t, result.Entries, expected)
		})
	}
}

func TestUnionCapAppliesAfterCompleteDeduplication(t *testing.T) {
	service, _, _ := graphService(t)
	result, err := service.Query(context.Background(), graphRequest(`UNWIND range(1,1500) AS x RETURN CASE WHEN x < 1400 THEN 0 ELSE x END AS v UNION RETURN -1 AS v`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 103 || result.Execution.ConclusionsLimited {
		t.Fatalf("premature default branch cap: %+v", result.Execution)
	}
	result, err = service.Query(context.Background(), graphRequest(`UNWIND range(1,1500) AS x RETURN x UNION ALL RETURN -1 AS x`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1000 || !result.Execution.ConclusionsLimited || !result.Execution.EvidenceComplete {
		t.Fatalf("result cap: %+v", result.Execution)
	}
}

func TestUnionUnwindBudgetsAndScope(t *testing.T) {
	for _, query := range []string{
		`UNWIND range(1,5000) AS x RETURN x UNION ALL UNWIND range(1,5000) AS x RETURN x`,
		`UNWIND range(1,101) AS x UNWIND range(1,100) AS y RETURN count(*) AS c`,
		`UNWIND [1] AS x UNWIND [2] AS x RETURN x`,
		`UNWIND [y] AS x UNWIND [1] AS y RETURN x`,
		`UNWIND [1] AS x RETURN x UNION RETURN x`,
		`MATCH (p:Person) WHERE x = 1 UNWIND [1] AS x RETURN x`,
		`MATCH (p:Person) UNWIND [1] AS p RETURN p`,
		`RETURN 1 AS x UNION MATCH (p:Missing) RETURN p.id AS x`,
		`RETURN 1 AS x UNION CREATE (p:Person) RETURN p.id AS x`,
		`RETURN 1 AS x UNION ALL RETURN 2 AS x UNION RETURN 3 AS x`,
		`RETURN 1 AS x UNION RETURN 2 AS x LIMIT 10001`,
	} {
		t.Run(query, func(t *testing.T) {
			service, vega, _ := graphService(t)
			result, err := service.Query(context.Background(), graphRequest(query))
			if err == nil || result != nil {
				t.Fatalf("expected all-or-error refusal; got %+v, %v", result, err)
			}
			if vega.request != nil {
				t.Fatal("invalid query executed a branch")
			}
		})
	}
}

// queuedUnionVega supplies one complete observation per SQL branch.
type queuedUnionVega struct {
	*recordingVega
	responsesByStatement map[string]*interfaces.RawQueryResponse
	responses            []*interfaces.RawQueryResponse
	requests             []*interfaces.RawQueryRequest
}

func (v *queuedUnionVega) RawQuery(_ context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	v.requests = append(v.requests, req)
	if v.responsesByStatement != nil {
		response, ok := v.responsesByStatement[req.Query]
		if !ok {
			return nil, fmt.Errorf("unplanned fixture observation")
		}
		return response, nil
	}
	if len(v.responses) == 0 {
		return nil, fmt.Errorf("unexpected observation")
	}
	response := v.responses[0]
	v.responses = v.responses[1:]
	return response, nil
}

func TestUnionAuthorizesEveryBranchBeforeReading(t *testing.T) {
	service, vega, permission := graphService(t)
	permission.levels = map[string]string{interfaces.KNChildResourceID("kn_1", "person") + ".name": interfaces.PROPERTY_ACCESS_MASKED}
	_, err := service.Query(context.Background(), graphRequest(`MATCH (p:Person) RETURN p.id AS x UNION MATCH (q:Person) RETURN q.name AS x`))
	if err == nil || vega.request != nil {
		t.Fatalf("read before authorizing later branch: %v", err)
	}
}

func TestUnionMatchAndUnwindExecuteWithPoliciesAndTrace(t *testing.T) {
	service, base, permission := graphService(t)
	permission.rowFilters = map[string]interfaces.RowFilterPredicate{interfaces.KNChildResourceID("kn_1", "person"): {Kind: "false"}}
	request := graphRequest(`MATCH (p:Person) UNWIND [p.name,p.name] AS x RETURN x UNION ALL MATCH (q:Place) RETURN q.id AS x`)
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	vega := &queuedUnionVega{recordingVega: base, responses: []*interfaces.RawQueryResponse{
		{Entries: []map[string]any{}},
		{Entries: []map[string]any{graphRow(t, compiled.branches[1], map[string]any{"q.id": "l1"})}},
	}}
	service.vba = vega
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertExpressionRows(t, result.Entries, []map[string]any{{"x": "l1"}})
	if len(vega.requests) != 2 || !strings.Contains(vega.requests[0].Query, "1 = 0") {
		t.Fatalf("branch policy missing: %+v", vega.requests)
	}
	trace := string(result.TraceDescriptor)
	for _, expected := range []string{`"union"`, `"unwinds"`, "property:kn_1:person:name", "property:kn_1:place:id"} {
		if !strings.Contains(trace, expected) {
			t.Fatalf("missing trace %q: %s", expected, trace)
		}
	}
	for _, secret := range []string{"person_name", "{{.people}}", "_g0"} {
		if strings.Contains(trace, secret) {
			t.Fatalf("physical trace leak %q", secret)
		}
	}
}

func TestUnionUnwindParametersAndMixedTypes(t *testing.T) {
	service, _, _ := graphService(t)
	request := graphRequest(`UNWIND $items AS x RETURN x.id AS value UNION ALL RETURN 'text' AS value`)
	request.Parameters = map[string]any{"items": []any{map[string]any{"id": json.Number("9007199254740993")}}}
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Entries[0]["value"] != int64(9007199254740993) || result.ValueTypes["value"].Kind != "any" || result.Columns[0].Type != "any" {
		t.Fatalf("number/type lost: %+v", result)
	}
	if strings.Contains(string(result.TraceDescriptor), "9007199254740993") {
		t.Fatal("parameter literal leaked into trace")
	}
}

func TestUnionSharedPayloadBudgetIncludesDuplicates(t *testing.T) {
	service, _, _ := graphService(t)
	request := graphRequest(`RETURN $text AS x UNION RETURN $text AS x`)
	request.Parameters = map[string]any{"text": strings.Repeat("x", (graphPayloadBudget/2)+1)}
	result, err := service.Query(context.Background(), request)
	if err == nil || result != nil {
		t.Fatal("duplicate outputs bypassed shared payload budget")
	}
}

func TestUnionRejectsIncompleteLaterObservation(t *testing.T) {
	service, base, _ := graphService(t)
	request := graphRequest(`MATCH (p:Person) RETURN p.id AS x UNION MATCH (q:Place) RETURN q.id AS x`)
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	service.vba = &queuedUnionVega{recordingVega: base, responses: []*interfaces.RawQueryResponse{
		{Entries: []map[string]any{graphRow(t, compiled.branches[0], map[string]any{"p.id": "p1"})}},
		{Entries: []map[string]any{}, TotalCount: 1},
	}}
	result, err := service.Query(context.Background(), request)
	if err == nil || result != nil {
		t.Fatal("earlier branch escaped as a partial result")
	}
}

func TestUnionGraphSnapshotsCompareOverlappingPropertiesInsideLists(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			service, base, _ := graphService(t)
			request := graphRequest(`MATCH (p:Person) RETURN collect(p) AS objects UNION ALL MATCH (q:Person) RETURN collect(q) AS objects`)
			request.ObjectProperties = map[string][]string{"p": {"name"}}
			second := map[string]any{"q.id": "p1"}
			if conflict {
				request.ObjectProperties["q"] = []string{"name"}
				second["q.name"] = "Bob"
			}
			compiled, err := service.compile(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			service.vba = &queuedUnionVega{recordingVega: base, responses: []*interfaces.RawQueryResponse{
				{Entries: []map[string]any{graphRow(t, compiled.branches[0], map[string]any{"p.id": "p1", "p.name": "Alice"})}},
				{Entries: []map[string]any{graphRow(t, compiled.branches[1], second)}},
			}}
			result, err := service.Query(context.Background(), request)
			if conflict {
				if err == nil || result != nil {
					t.Fatal("conflicting list snapshots accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("compatible partial projections rejected: %v", err)
			}
		})
	}
}

func TestUnwindMatchPredicatePrecedesExpansion(t *testing.T) {
	service, vega, _ := graphService(t)
	request := graphRequest(`MATCH (p:Person) WHERE size(p.name) > 3 UNWIND [p.name,p.name] AS x RETURN count(*) AS n`)
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Al", "Alice"} {
		vega.response.Entries = append(vega.response.Entries, graphRow(t, compiled, map[string]any{"p.name": name}))
	}
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertExpressionRows(t, result.Entries, []map[string]any{{"n": int64(2)}})
	if result.Execution.CandidateRows != 2 || result.Execution.MatchRows != 2 {
		t.Fatalf("row accounting: %+v", result.Execution)
	}
}
