// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestReviewConnectorNumericIdentity(t *testing.T) {
	for _, raw := range []string{"19.99", "1.999e1", "-19.99", "0.1", "1e0", "-0.0", "1e-300"} {
		t.Run(raw, func(t *testing.T) {
			source := json.Number(raw)
			literal, err := source.Float64()
			if err != nil {
				t.Fatal(err)
			}
			equal, err := expressionEquality(source, literal)
			if err != nil || equal != true {
				t.Fatalf("source/literal equality: %v, %v", equal, err)
			}
			order, err := expressionCompare(source, literal)
			if err != nil || order != 0 {
				t.Fatalf("source/literal order: %d, %v", order, err)
			}
			a, err := expressionKey(map[string]any{"n": []any{source}})
			if err != nil {
				t.Fatal(err)
			}
			b, err := expressionKey(map[string]any{"n": []any{literal}})
			if err != nil || a != b {
				t.Fatalf("source/literal grouping keys: %s != %s, %v", a, b, err)
			}
		})
	}
	for _, pair := range [][2]any{
		{json.Number("9007199254740993"), float64(9007199254740992)},
		{json.Number("9223372036854775807"), float64(math.MaxInt64)},
		{json.Number("19.99"), math.Nextafter(19.99, math.Inf(1))},
	} {
		equal, err := expressionEquality(pair[0], pair[1])
		if err != nil || equal != false {
			t.Fatalf("distinct numeric values collapsed: %v, %v", pair, err)
		}
	}
}

// Execute generated SQL, with UseNumber decoding as in the real Vega adapter.
func TestReviewConnectorDecimalsAcrossExecutionPaths(t *testing.T) {
	sqlite := startM19SQLite(t)
	_, err := sqlite.exchange(map[string]any{"tables": []map[string]any{{
		"name": "people", "columns": []string{"person_id", "person_name", "price"},
		"rows": []map[string]any{{"person_id": "p1", "person_name": "Alice", "price": 19.99}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"MATCH (p:Person) WHERE p.price = 19.99 RETURN p.name AS name",
		"MATCH (p:Person) WHERE p.price = 19.99 WITH p RETURN p.name AS name",
		"MATCH (p:Person) WHERE p.price = 19.99 AND toLower(p.name) = 'alice' RETURN p.name AS name",
		"MATCH (p:Person) WHERE p.price = $price WITH p RETURN p.name AS name",
		"MATCH (p:Person) WHERE p.price IN [19.99] WITH p RETURN p.name AS name",
		"MATCH (p:Person) WHERE p.price >= 19.99 AND p.price <= 19.99 WITH p RETURN p.name AS name",
		"MATCH (p:Person) UNWIND [p.price,19.99] AS x RETURN DISTINCT x AS price",
		"MATCH (p:Person) UNWIND [p.price,19.99] AS x RETURN x AS price,count(*) AS n",
		"MATCH (p:Person) RETURN p.price AS price UNION RETURN 19.99 AS price",
	} {
		t.Run(query, func(t *testing.T) {
			service, _, _ := graphService(t)
			person := service.schema.(*fakeSchemaSource).objectTypes[0]
			price := dataProperty("price", "price")
			price.Type = "float64"
			person.DataProperties = append(person.DataProperties, price)
			service.vba = sqlite
			request := graphRequest(query)
			request.ResultFormat = ""
			request.Parameters["price"] = 19.99
			result, err := service.Query(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			want := `[{"name":"Alice"}]`
			if strings.Contains(query, "AS price") {
				want = `[{"price":19.99}]`
				if strings.Contains(query, "count(*)") {
					want = `[{"n":2,"price":19.99}]`
				}
			}
			actual, err := json.Marshal(result.Entries)
			if err != nil || string(actual) != want {
				t.Fatalf("got %s, want %s: %v", actual, want, err)
			}
		})
	}
}

func TestReviewAnonymousCorrelatedScope(t *testing.T) {
	for _, test := range []struct {
		query string
		rows  []map[string]any
	}{
		{"MATCH (:Person {name:'Cara'})-[:FRIEND]->(f) WHERE (f)-[:WORKS_AT]->({id:'c1'}) RETURN f.name AS name", []map[string]any{{"name": "Alice"}}},
		{"MATCH (:Person {name:'Cara'})-[:FRIEND]->(f:Person) WHERE (f)-[:WORKS_AT]->({id:'c1'}) RETURN f.name AS name", []map[string]any{{"name": "Alice"}}},
		{"MATCH (:Person {name:'Cara'})-[:FRIEND]->(f) WHERE NOT (f)-[:WORKS_AT]->({id:'c1'}) RETURN f.name AS name", []map[string]any{}},
		{"MATCH (:Person {name:'Cara'})-[:FRIEND]->(f) WHERE EXISTS { MATCH (f)-[:WORKS_AT]->({id:'c1'}) RETURN f } RETURN f.name AS name", []map[string]any{{"name": "Alice"}}},
		{"MATCH (:Person {name:'Cara'})-[:FRIEND]->(f) RETURN [(f)-[:WORKS_AT]->({id:'c1'}) | f.name] AS names", []map[string]any{{"names": []any{"Alice"}}}},
		{"MATCH (:Person {name:'Cara'})-[:FRIEND]->(f) WHERE EXISTS { MATCH (f)-[:WORKS_AT]->({id:'c1'}) WHERE EXISTS { MATCH (:Person {name:'Bob'}) RETURN 1 AS n } RETURN f } RETURN f.name AS name", []map[string]any{{"name": "Alice"}}},
	} {
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

func TestReviewProjectionRebinding(t *testing.T) {
	for _, test := range []struct {
		query string
		rows  []map[string]any
	}{
		{"MATCH (a:Person)-[:FRIEND]->(b:Person) WITH b AS a,a.name AS oldName WHERE a.name='Bob' RETURN oldName", []map[string]any{{"oldName": "Alice"}}},
		{"MATCH (a:Person)-[:FRIEND]->(b:Person) WITH DISTINCT b AS a,a.name AS oldName WHERE a.name='Bob' RETURN oldName", []map[string]any{{"oldName": "Alice"}}},
		{"MATCH (a:Person)-[:FRIEND]->(b:Person) WITH b AS a,toLower(a.name) AS oldName WHERE toLower(a.name)='bob' RETURN oldName", []map[string]any{{"oldName": "alice"}}},
		{"MATCH (a:Person)-[:FRIEND]->(b:Person) WITH b AS a,a.name AS oldName ORDER BY a.name RETURN oldName", []map[string]any{{"oldName": "Cara"}, {"oldName": "Alice"}, {"oldName": "Bob"}}},
		{"MATCH (a:Person)-[:FRIEND]->(b:Person) WITH DISTINCT a AS a,a.name AS oldName WHERE a.name='Bob' RETURN oldName", []map[string]any{{"oldName": "Bob"}}},
		{"MATCH (p:Person) WITH DISTINCT (p)-[:WORKS_AT]->(:Company) AS employed ORDER BY (p)-[:WORKS_AT]->(:Company) RETURN employed", []map[string]any{{"employed": false}, {"employed": true}}},
		{"MATCH (a:Person)-[:FRIEND]->(b:Person) WITH DISTINCT b AS a,(a)-[:WORKS_AT]->(:Company) AS was ORDER BY (a)-[:WORKS_AT]->(:Company),a.name RETURN a.name AS name,was", []map[string]any{{"name": "Bob", "was": true}, {"name": "Cara", "was": false}, {"name": "Alice", "was": false}}},
	} {
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

func TestReviewConstantProjectionUsesVisibleMappedColumn(t *testing.T) {
	for _, level := range []string{"", "none"} {
		t.Run(level, func(t *testing.T) {
			service, vega, permission := graphService(t)
			person := service.schema.(*fakeSchemaSource).objectTypes[0]
			if level == "" {
				person.DataProperties[0].MappedField = nil
			} else {
				permission.levels = map[string]string{"kn_1/person.id": level}
			}
			vega.response.Entries = []map[string]any{{"_g0": "Alice"}, {"_g0": "Bob"}}
			result, err := service.Query(context.Background(), graphRequest("MATCH (p:Person) RETURN 1 AS value"))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Entries) != 2 {
				t.Fatal(result.Entries)
			}
			for _, row := range result.Entries {
				if len(row) != 1 || row["value"] != int64(1) {
					t.Fatal(result.Entries)
				}
			}
			if vega.request == nil || strings.Contains(vega.request.Query, "person_id") || !strings.Contains(vega.request.Query, "person_name") {
				t.Fatalf("unexpected physical query: %+v", vega.request)
			}
		})
	}
	service, vega, permission := graphService(t)
	permission.levels = map[string]string{"kn_1/person.id": "none", "kn_1/person.name": "none"}
	result, err := service.Query(context.Background(), graphRequest("MATCH (p:Person) RETURN 1 AS value"))
	if err == nil || result != nil || vega.request != nil {
		t.Fatalf("no visible fact column must fail before reading: %v, %v", result, err)
	}
}

func TestReviewSemanticProjectionRejectsMissingExpression(t *testing.T) {
	plan := &Plan{NetworkID: "kn", Branch: "main", Tables: []PlanTable{{Variable: "n", ObjectTypeID: "person"}}, Select: []PlanColumn{{Alias: "missing", Table: -1}}}
	if _, err := BuildSemanticQueryDescriptor(plan, "MATCH (n:Person) RETURN n.id"); err == nil {
		t.Fatal("incomplete projection accepted")
	}
}
