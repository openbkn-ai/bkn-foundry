// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestReadOnlyExpressionsWithoutMatch(t *testing.T) {
	cases := []struct {
		query string
		want  any
	}{
		{"RETURN 1 + 2 * 3 AS value", int64(7)},
		{"RETURN (1 + 2) * 3 AS value", int64(9)},
		{"RETURN 7 / 2 AS value", int64(3)},
		{"RETURN 1 + null AS value", nil},
		{"RETURN 7.0 / 2 AS value", float64(3.5)},
		{"RETURN -5 % 2 AS value", int64(-1)},
		{"RETURN 2 ^ 3 ^ 2 AS value", float64(64)},
		{"RETURN CASE WHEN null THEN 1 WHEN 2 < 3 THEN 7 ELSE 1/0 END AS value", int64(7)},
		{"RETURN CASE 2 WHEN 1 THEN 3 WHEN 2 THEN 7 ELSE 1/0 END AS value", int64(7)},
		{"RETURN CASE WHEN false THEN 1 END AS value", nil},
		{"RETURN coalesce(null, 7, 1/0) AS value", int64(7)},
		{"RETURN false AND null AS value", false},
		{"RETURN true AND null AS value", nil},
		{"RETURN true OR null AS value", true},
		{"RETURN false OR null AS value", nil},
		{"RETURN true XOR false AS value", true},
		{"RETURN true XOR null AS value", nil},
		{"RETURN NOT null AS value", nil},
		{"RETURN null = null AS value", nil},
		{"RETURN null IS NULL AS value", true},
		{"RETURN 1 < 2 < 3 AS value", true},
		{"RETURN 3 < 2 < 4 AS value", false},
		{"RETURN 1 = '1' AS value", false},
		{"RETURN 1 IN [2,null] AS value", nil},
		{"RETURN null IN [] AS value", false},
		{"RETURN 1 IN [null,1] AS value", true},
		{"RETURN [1,2,3][-1] AS value", int64(3)},
		{"RETURN [1,2,3][3] AS value", nil},
		{"RETURN [1,2,3][1..] AS value", []any{int64(2), int64(3)}},
		{"RETURN [1,2,3][..-1] AS value", []any{int64(1), int64(2)}},
		{"RETURN [1,2,3][2..1] AS value", []any{}},
		{"RETURN [1,2,3][null..2] AS value", nil},
		{"RETURN -9223372036854775808 AS value", int64(math.MinInt64)},
		{"RETURN {a: [1,2]}.a[1] AS value", int64(2)},
		{"RETURN {a: 1}['missing'] AS value", nil},
		{"RETURN [1] + [2] + 3 AS value", []any{int64(1), int64(2), int64(3)}},
		{"RETURN [null] = [null] AS value", nil},
		{"RETURN [1,null] = [2,null] AS value", false},
		{"RETURN toUpper('a张') + toLower('B') AS value", "A张b"},
		{"RETURN size('张😀x') AS value", int64(3)},
		{"RETURN substring('张😀x',1,1) AS value", "😀"},
		{"RETURN reverse('张😀x') AS value", "x😀张"},
		{"RETURN head(tail(range(1,5,2))) AS value", int64(3)},
		{"RETURN range(3,1,-1) AS value", []any{int64(3), int64(2), int64(1)}},
		{"RETURN split('a,b',',')[1] AS value", "b"},
		{"RETURN keys({b:1,a:2}) AS value", []any{"a", "b"}},
		{"RETURN toInteger('not a number') AS value", nil},
		{"RETURN abs(-3) AS value", int64(3)},
		{"RETURN round(-1.5) AS value", float64(-1)},
		{"RETURN pi() AS value", math.Pi},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			service, vega, _ := graphService(t)
			request := graphRequest(tc.query)
			request.ResultFormat = ""
			result, err := service.Query(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if vega.request != nil {
				t.Fatal("tableless expression sent to Vega")
			}
			if len(result.Entries) != 1 || !reflect.DeepEqual(result.Entries[0]["value"], tc.want) {
				t.Fatalf("got %+v (%T), want %+v (%T)", result.Entries, result.Entries[0]["value"], tc.want, tc.want)
			}
		})
	}
}

func TestReadOnlyExpressionsWithParameters(t *testing.T) {
	service, _, _ := graphService(t)
	request := graphRequest("RETURN $input.a[0] AS value, $input.missing AS absent, $flag IS NULL AS is_null")
	request.ResultFormat = ""
	request.Parameters = map[string]any{"input": map[string]any{"a": []any{json.Number("9007199254740993")}}, "flag": nil}
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Entries[0]["value"] != int64(9007199254740993) || result.Entries[0]["absent"] != nil || result.Entries[0]["is_null"] != true {
		t.Fatalf("parameter semantics: %+v", result.Entries)
	}
	descriptor := string(result.TraceDescriptor)
	if strings.Contains(descriptor, "9007199254740993") || !strings.Contains(descriptor, "$.parameters.input") {
		t.Fatalf("parameter trace leaks values or loses provenance: %s", descriptor)
	}
}

func TestReadOnlyRuntimeWhereAndMixedAggregates(t *testing.T) {
	service, vega, _ := graphService(t)
	person := service.schema.(*fakeSchemaSource).objectTypes[0]
	person.DataProperties = append(person.DataProperties, dataProperty("stock", "stock"), dataProperty("demand", "demand"))
	person.DataProperties[2].Type = "integer"
	person.DataProperties[3].Type = "integer"
	request := graphRequest("MATCH (p:Person) WHERE p.stock < p.demand RETURN p.name AS name, count(*) AS n, sum(p.stock) AS total, avg(p.stock) AS average, collect(DISTINCT p.stock) AS evidence ORDER BY n DESC SKIP 1 LIMIT 1")
	request.ResultFormat = ""
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	inputs := []map[string]any{
		{"p.name": "A", "p.stock": int64(2), "p.demand": int64(3)},
		{"p.name": "A", "p.stock": int64(2), "p.demand": int64(3)},
		{"p.name": "B", "p.stock": int64(1), "p.demand": int64(3)},
		{"p.name": "C", "p.stock": int64(9), "p.demand": int64(3)},
	}
	for _, input := range inputs {
		vega.response.Entries = append(vega.response.Entries, graphRow(t, compiled, input))
	}
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || result.Entries[0]["name"] != "B" || result.Entries[0]["n"] != int64(1) || result.Entries[0]["total"] != int64(1) || result.Entries[0]["average"] != float64(1) || result.Execution.MatchRows != 3 {
		t.Fatalf("query operations in wrong order: %+v", result)
	}
	if !result.Execution.ConclusionsLimited || !result.Execution.EvidenceComplete {
		t.Fatalf("wrong completeness: %+v", result.Execution)
	}
	if !strings.Contains(string(result.TraceDescriptor), "residual_predicate") {
		t.Fatal("runtime predicate not traced")
	}
}

func TestReadOnlyDistinctAndNumericGrouping(t *testing.T) {
	service, vega, _ := graphService(t)
	request := graphRequest("MATCH (p:Person) RETURN DISTINCT p.name AS name ORDER BY name SKIP 1 LIMIT 2")
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []any{"B", "A", "A", nil} {
		vega.response.Entries = append(vega.response.Entries, graphRow(t, compiled, map[string]any{"p.name": name}))
	}
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 2 || result.Entries[0]["name"] != "B" || result.Entries[1]["name"] != nil {
		t.Fatalf("DISTINCT/order/null ordering: %+v", result.Entries)
	}
	request.Query = "MATCH (p:Person) RETURN collect(DISTINCT p.id) AS ids"
	compiled, err = service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	vega.response.Entries = nil
	for _, number := range []any{json.Number("1"), float64(1), json.Number("1.0"), int64(2), nil} {
		vega.response.Entries = append(vega.response.Entries, graphRow(t, compiled, map[string]any{"p.id": number}))
	}
	result, err = service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries[0]["ids"].([]any)) != 2 {
		t.Fatalf("numeric equivalence: %+v", result.Entries)
	}
}

func TestReadOnlyExpressionErrorsAndAuthorization(t *testing.T) {
	for _, query := range []string{"RETURN 1 / 0 AS value", "RETURN 9223372036854775807 + 1 AS value", "RETURN true + 1 AS value", "RETURN null OR 1 AS value", "RETURN substring('a',-1) AS value", "RETURN range(1,10,0) AS value", "RETURN size(1) AS value", "RETURN [1]['a'] AS value"} {
		t.Run(query, func(t *testing.T) {
			service, vega, _ := graphService(t)
			result, err := service.Query(context.Background(), graphRequest(query))
			if err == nil || result != nil || statusOf(t, err) != 400 {
				t.Fatalf("runtime error became success or wrong status: %+v %v", result, err)
			}
			if vega.request != nil {
				t.Fatal("constant query executed at Vega")
			}
		})
	}
	service, vega, permission := graphService(t)
	permission.levels = map[string]string{interfaces.KNChildResourceID("kn_1", "person") + ".name": interfaces.PROPERTY_ACCESS_MASKED}
	request := graphRequest("MATCH (p:Person) RETURN CASE WHEN true THEN 1 ELSE size(p.name) END AS value")
	if _, err := service.Query(context.Background(), request); err == nil || vega.request != nil {
		t.Fatal("unselected CASE branch bypassed property authorization")
	}
}

func TestReadOnlyPinnedTCKBooleanResults(t *testing.T) {
	raw, err := os.ReadFile("testdata/tck_m19_boolean.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		SHA256 string `json:"sha256"`
		Cases  []struct {
			Source, Scenario, Query string
			Expected                []map[string]any
		}
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	if len(suite.Cases) != 11 || suite.SHA256 != "93d3ea6dcf386a64754bb561936a7035c3b429f81b9bc091b809061b4ea0d9bd" {
		t.Fatal("TCK baseline changed without updating its provenance")
	}
	for _, tc := range suite.Cases {
		t.Run(tc.Source+"/"+tc.Scenario, func(t *testing.T) {
			service, vega, _ := graphService(t)
			request := graphRequest(tc.Query)
			request.ResultFormat = ""
			result, err := service.Query(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Entries, tc.Expected) {
				t.Fatalf("TCK result mismatch: got %+v want %+v", result.Entries, tc.Expected)
			}
			if vega.request != nil {
				t.Fatal("TCK tableless query used a data source")
			}
		})
	}
}

func TestReadOnlyNullableParametersAndInlineFilters(t *testing.T) {
	service, vega, _ := graphService(t)
	request := graphRequest("MATCH (p:Person {name: 'A'}) WHERE p.id IN $ids RETURN p.id AS id")
	request.ResultFormat = ""
	request.Parameters = map[string]any{"ids": []any{"p1", nil}}
	compiled, err := service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.statement, "t0.`person_name` = 'A'") {
		t.Fatalf("inline filter lost during residual binding: %s", compiled.statement)
	}
	vega.response.Entries = []map[string]any{graphRow(t, compiled, map[string]any{"p.id": "p1"}), graphRow(t, compiled, map[string]any{"p.id": "p2"})}
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || result.Entries[0]["id"] != "p1" || result.Execution.CandidateRows != 2 || result.Execution.MatchRows != 1 {
		t.Fatalf("nullable membership: %+v", result)
	}
	request.Query = "MATCH (p:Person) WHERE p.name CONTAINS $value RETURN p.id AS id"
	request.Parameters = map[string]any{"value": nil}
	compiled, err = service.compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	vega.response.Entries = []map[string]any{graphRow(t, compiled, map[string]any{"p.id": "p1", "p.name": "A"})}
	result, err = service.Query(context.Background(), request)
	if err != nil || len(result.Entries) != 0 {
		t.Fatalf("null string predicate must not match: %+v %v", result, err)
	}
}

func TestReadOnlyEmptyMixedAggregatesAndOverflow(t *testing.T) {
	service, _, _ := graphService(t)
	request := graphRequest("MATCH (p:Person) RETURN count(*) AS n, sum(1) AS total, avg(1) AS average, min(p.id) AS minimum, max(p.id) AS maximum, collect(p.id) AS ids")
	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	row := result.Entries[0]
	if row["n"] != int64(0) || row["total"] != int64(0) || row["average"] != nil || row["minimum"] != nil || row["maximum"] != nil || len(row["ids"].([]any)) != 0 {
		t.Fatalf("empty aggregates: %+v", row)
	}
	mean, err := aggregateExpressionValues("avg", []any{int64(math.MaxInt64), int64(math.MaxInt64)})
	if err != nil || mean != float64(math.MaxInt64) {
		t.Fatalf("representable mean overflowed: %v %v", mean, err)
	}
	if _, err := aggregateExpressionValues("sum", []any{int64(math.MaxInt64), int64(1)}); err == nil {
		t.Fatal("integer sum overflow was silently rounded")
	}
	for _, query := range []string{"CREATE (n:X) RETURN n", "MATCH (n:X) SET n.x=1 RETURN n", "MATCH (n:X) DELETE n", "MERGE (n:X) RETURN n", "MATCH (n:X) REMOVE n.x RETURN n"} {
		tree, err := Parse(query)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Analyze(tree); err == nil {
			t.Fatal("write admitted on default path")
		}
		if _, err := AnalyzeGraph(tree); err == nil {
			t.Fatal("write admitted on value path")
		}
	}
}

func TestReadOnlyIntermediateExpressionBudgets(t *testing.T) {
	service, _, _ := graphService(t)
	request := graphRequest("RETURN $value + $value AS result")
	request.Parameters = map[string]any{"value": strings.Repeat("x", 5<<20)}
	if result, err := service.Query(context.Background(), request); err == nil || result != nil {
		t.Fatal("oversized intermediate string was accepted")
	}
	request = graphRequest("RETURN range(1,9999) + range(1,9999) AS result")
	if result, err := service.Query(context.Background(), request); err == nil || result != nil {
		t.Fatal("oversized generated collection was accepted")
	}
}

// Permission hints must survive composition, including empty observations.
func TestValueEvaluationPreservesRowFilterGuidance(t *testing.T) {
	for _, query := range []string{
		"MATCH (p:Person) RETURN p",
		"MATCH (p:Person) WITH p RETURN p",
		"MATCH (p:Person) RETURN p UNION MATCH (p:Person) RETURN p",
	} {
		t.Run(query, func(t *testing.T) {
			service, _, permission := graphService(t)
			permission.rowFilters = map[string]interfaces.RowFilterPredicate{
				interfaces.KNChildResourceID("kn_1", "person"): {Kind: "false"},
			}
			result, err := service.Query(context.Background(), graphRequest(query))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.PermissionImpacts) != 1 || result.PermissionImpacts[0].ObjectTypeID != "person" || !result.PermissionImpacts[0].RowFilterApplied {
				t.Fatalf("lost or duplicated row-filter guidance: %+v", result.PermissionImpacts)
			}
		})
	}
}
