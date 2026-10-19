// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// runGraph compiles a query against the typed test model with the given
// object_properties and runs it through the bind-join executor.
func runGraph(t *testing.T, data map[string][]map[string]any, query string,
	objectProperties map[string][]string) ([]map[string]any, *Plan, error) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		return nil, nil, err
	}
	schema := bindJoinSchema(t)
	plan, err := Compile(analyzed, schema, CompileOptions{ObjectProperties: objectProperties})
	if err != nil {
		return nil, nil, err
	}
	_, entries, err := runBindJoin(context.Background(), plan, schema, newFakeDataAPI(data).read,
		defaultBindJoinLimits, interfaces.CYPHER_DEFAULT_LIMIT)
	return entries, plan, err
}

func mustRunGraph(t *testing.T, query string, objectProperties map[string][]string) []map[string]any {
	t.Helper()
	entries, _, err := runGraph(t, bindJoinData(), query, objectProperties)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return entries
}

// asJSON renders a value the way the response carries it.
func asJSON(t *testing.T, value any) string {
	t.Helper()
	text, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func key(object any) string {
	value := object.(interfaces.CypherObjectValue)
	parts := make([]string, 0, len(value.Key))
	for property, key := range value.Key {
		parts = append(parts, property+"="+key.Value)
	}
	return value.ObjectTypeID + "(" + strings.Join(sortedStrings(parts), ",") + ")"
}

func sortedStrings(values []string) []string {
	for i := range values {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
	return values
}

// The target query of the design (bkn-docs#220 §3.1), on the test model: a
// customer reached through several items of one order is one conclusion that
// keeps every path as evidence; another customer is its own conclusion.
func TestGraphTargetQueryGroupsEvidenceByIdentity(t *testing.T) {
	entries := mustRunGraph(t, "MATCH (i:Item)-[e:BELONGS_TO]->(o:Order)-[r:PLACED_BY]->(c:Customer) "+
		"RETURN c AS source, 'BOUGHT' AS relationType, o AS target, collect({item: i, belongs: e, placed: r}) AS evidence "+
		"ORDER BY relationType", map[string][]string{"c": {"name"}})
	if len(entries) != 2 {
		t.Fatalf("entries = %s, want two conclusions", asJSON(t, entries))
	}
	byTarget := map[string]map[string]any{}
	for _, entry := range entries {
		byTarget[key(entry["target"])] = entry
		if entry["relationType"] != "BOUGHT" {
			t.Fatalf("relationType = %v", entry["relationType"])
		}
	}
	first := byTarget["ot_order(id=1)"]
	if first == nil || key(first["source"]) != "ot_customer(code=c1,region=east)" {
		t.Fatalf("entries = %s", asJSON(t, entries))
	}
	if props := first["source"].(interfaces.CypherObjectValue).Properties; props["name"] != "Alice" {
		t.Fatalf("snapshot = %v, want the requested name", props)
	}
	evidence := first["evidence"].([]any)
	if len(evidence) != 2 {
		t.Fatalf("evidence = %s, want both items of order 1", asJSON(t, evidence))
	}
	path := evidence[0].(map[string]any)
	belongs := path["belongs"].(interfaces.CypherRelationValue)
	if belongs.RelationTypeID != "rt_belongs_to" || key(belongs.Source)[:7] != "ot_item" ||
		key(belongs.Target) != "ot_order(id=1)" || !strings.HasPrefix(belongs.BindingDigest, "sha256:") {
		t.Fatalf("relation = %s", asJSON(t, belongs))
	}
	if len(belongs.Basis) != 1 || belongs.Basis[0].SourceProperty != "order_id" || belongs.Basis[0].TargetProperty != "id" {
		t.Fatalf("basis = %s", asJSON(t, belongs.Basis))
	}
	placed := path["placed"].(interfaces.CypherRelationValue)
	if len(placed.Basis) != 2 {
		t.Fatalf("composite relation basis = %s, want both key pairs", asJSON(t, placed.Basis))
	}
	second := byTarget["ot_order(id=2)"]
	if second == nil || key(second["source"]) != "ot_customer(code=c1,region=west)" || len(second["evidence"].([]any)) != 1 {
		t.Fatalf("second conclusion = %s", asJSON(t, second))
	}
}

func TestGraphRelationKeepsTheModelDirection(t *testing.T) {
	entries := mustRunGraph(t, "MATCH (o:Order)<-[e:BELONGS_TO]-(i:Item) WHERE o.id = 2 RETURN e", nil)
	relation := entries[0]["e"].(interfaces.CypherRelationValue)
	if key(relation.Source) != "ot_item(id=13)" || key(relation.Target) != "ot_order(id=2)" {
		t.Fatalf("relation = %s, want the item as the source however the pattern is written", asJSON(t, relation))
	}
}

func TestGraphCollectSemantics(t *testing.T) {
	// Duplicates are kept.
	entries := mustRunGraph(t, "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) WHERE o.id = 1 RETURN o.id AS id, collect(o.region) AS regions", nil)
	if got := asJSON(t, entries); got != `[{"id":1,"regions":["east","east"]}]` {
		t.Fatalf("entries = %s", got)
	}
	// Collect alone over nothing is one row holding an empty list.
	entries = mustRunGraph(t, "MATCH (o:Order) WHERE o.region = 'north' RETURN collect(o) AS orders", nil)
	if got := asJSON(t, entries); got != `[{"orders":[]}]` {
		t.Fatalf("entries = %s", got)
	}
	// With a grouping column, nothing matched is no conclusion at all.
	entries = mustRunGraph(t, "MATCH (o:Order) WHERE o.region = 'north' RETURN o.region AS r, collect(o) AS orders", nil)
	if len(entries) != 0 {
		t.Fatalf("entries = %s, want none", asJSON(t, entries))
	}
	// NULL is skipped.
	entries = mustRunGraph(t, "MATCH (o:Order) RETURN collect(o.region) AS regions", nil)
	if got := len(entries[0]["regions"].([]any)); got != 3 {
		t.Fatalf("regions = %v, want the three non-null ones", entries[0]["regions"])
	}
}

func TestGraphOptionalMatchGivesNull(t *testing.T) {
	entries := mustRunGraph(t, "MATCH (o:Order) WHERE o.id = 3 OPTIONAL MATCH (o)<-[e:BELONGS_TO]-(i:Item) RETURN o, i, e", nil)
	if entries[0]["i"] != nil || entries[0]["e"] != nil || entries[0]["o"] == nil {
		t.Fatalf("entries = %s, want the order with no item and no relation", asJSON(t, entries))
	}
}

func TestGraphKeysStayExactAndMustExist(t *testing.T) {
	data := bindJoinData()
	data["res_order"] = []map[string]any{{"f_id": json.Number("9007199254740993"), "f_region": "east"}}
	entries, _, err := runGraph(t, data, "MATCH (o:Order) RETURN o", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := entries[0]["o"].(interfaces.CypherObjectValue).Key["id"]; got.Value != "9007199254740993" || got.Type != "integer" {
		t.Fatalf("key = %+v, want the exact digits", got)
	}

	data["res_order"] = []map[string]any{{"f_id": nil, "f_region": "east"}}
	if _, _, err := runGraph(t, data, "MATCH (o:Order) RETURN o", nil); err == nil || !strings.Contains(err.Error(), "no identity") {
		t.Fatalf("err = %v, want a row without a key refused", err)
	}
}

// LIMIT cuts conclusions, never the evidence inside one, and evidence past the
// default row limit is complete.
func TestGraphLimitAppliesToConclusions(t *testing.T) {
	data := bindJoinData()
	data["res_item"] = nil
	for i := 0; i < 1500; i++ {
		data["res_item"] = append(data["res_item"], map[string]any{"f_id": 1000 + i, "f_order": 1})
	}
	data["res_item"] = append(data["res_item"], map[string]any{"f_id": 9, "f_order": 2})
	entries, _, err := runGraph(t, data, "MATCH (i:Item)-[:BELONGS_TO]->(o:Order) RETURN o.id AS id, o AS ord, collect(i) AS items ORDER BY id LIMIT 1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || len(entries[0]["items"].([]any)) != 1500 {
		t.Fatalf("got %d conclusions, first with %d items; want one conclusion with all 1500", len(entries),
			len(entries[0]["items"].([]any)))
	}
}

func TestGraphConstantsAndMaps(t *testing.T) {
	entries := mustRunGraph(t, "MATCH (o:Order) RETURN 'orders' AS kind, count(*) AS n", nil)
	if got := asJSON(t, entries); got != `[{"kind":"orders","n":4}]` {
		t.Fatalf("entries = %s", got)
	}
	entries = mustRunGraph(t, "MATCH (o:Order) WHERE o.id = 1 RETURN {id: o.id, region: o.region, nested: {flag: true}} AS m", nil)
	if got := asJSON(t, entries); got != `[{"m":{"id":1,"nested":{"flag":true},"region":"east"}}]` {
		t.Fatalf("entries = %s", got)
	}
}

func TestGraphRefusals(t *testing.T) {
	for _, tc := range []struct {
		query      string
		properties map[string][]string
		want       string
	}{
		{query: "MATCH (o:Order) RETURN collect(DISTINCT o) AS os", want: "collect(DISTINCT"},
		{query: "MATCH (o:Order) RETURN collect(o) AS os, count(*) AS n", want: "line 1:23: collect cannot be returned beside count"},
		{query: "MATCH (o:Order) RETURN DISTINCT o", want: "line 1:32: DISTINCT over graph values"},
		{query: "MATCH (o:Order) RETURN o AS x ORDER BY x", want: "line 1:23: sorting by the graph value"},
		{query: "MATCH (o:Order) RETURN {a: o.id, a: o.region} AS m", want: "repeated key"},
		{query: "MATCH (o:Order)-[r:PLACED_BY]->(c:Customer) RETURN r.since", want: "has no properties"},
		{query: "MATCH (o:Order)-[r:PLACED_BY]->(c:Customer) WHERE r.since = 1 RETURN o.id", want: "has no properties"},
		{query: "MATCH (a:Order)-[f:FOLLOWS]-(b:Order) RETURN f", want: "either way"},
		{query: "MATCH (o:Order)-[o:PLACED_BY]->(c:Customer) RETURN o", want: "names a node already"},
		{query: "MATCH (o:Order) RETURN {n: count(*)} AS m", want: "aggregate inside a map"},
		{query: "MATCH (o:Order) RETURN collect(1) AS ones", want: "collect over a constant"},
		{query: "MATCH (o:Order) RETURN {o: collect(o)} AS m", want: "aggregate inside a map"},
		{query: "MATCH (o:Order) RETURN o", properties: map[string][]string{"o": {"nope"}}, want: "no property"},
		{query: "MATCH (o:Order) RETURN o.id", properties: map[string][]string{"o": {"region"}}, want: "does not return that node"},
		{query: "MATCH (o:Order) RETURN o", properties: map[string][]string{"x": {"region"}}, want: "not a node"},
	} {
		_, _, err := runGraph(t, bindJoinData(), tc.query, tc.properties)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.query, err, tc.want)
		}
	}
}

// object_properties is not part of the query text, so a refusal of it points
// at no position rather than at line 0.
func TestGraphObjectPropertiesRefusalHasNoPosition(t *testing.T) {
	_, _, err := runGraph(t, bindJoinData(), "MATCH (o:Order) RETURN o.id", map[string][]string{"o": {"region"}})
	if err == nil || !strings.HasPrefix(err.Error(), "object_properties names") {
		t.Fatalf("err = %v, want the message without a position", err)
	}
}

func TestGraphSnapshotPropertiesAreBounded(t *testing.T) {
	properties := make([]string, MaxSnapshotProperties+1)
	for i := range properties {
		properties[i] = "region"
	}
	_, _, err := runGraph(t, bindJoinData(), "MATCH (o:Order) RETURN o", map[string][]string{"o": properties})
	if err == nil || !strings.Contains(err.Error(), "at most "+strconv.Itoa(MaxSnapshotProperties)) {
		t.Fatalf("err = %v, want the bound reported", err)
	}
}
