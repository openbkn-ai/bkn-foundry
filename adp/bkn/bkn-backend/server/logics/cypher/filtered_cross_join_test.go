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

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// The model of the probe network on the test environment: sales channels and
// orders, every offline channel linked to every refunding order. Columns are
// renamed away from the property names, as they are there.
func filteredObjectTypes() []*interfaces.ObjectType {
	typed := func(name, column, kind string) *interfaces.DataProperty {
		property := dataProperty(name, column)
		property.Type = kind
		return property
	}
	channel := objectType("probe_channel", "channel", resource("res_channel", "sales_channels"),
		typed("c_key", "channel_id", "integer"),
		typed("c_kind", "channel_type", "string"),
		typed("c_label", "channel_name", "string"),
		typed("c_opened", "opened_at", "datetime"),
	)
	channel.PrimaryKeys = []string{"c_key"}
	order := objectType("probe_order", "order", resource("res_order", "orders"),
		typed("o_key", "order_id", "integer"),
		typed("o_state", "order_status", "string"),
		typed("o_amount", "actual_amount", "decimal"),
		typed("o_paid", "is_paid", "boolean"),
	)
	order.PrimaryKeys = []string{"o_key"}
	return []*interfaces.ObjectType{channel, order}
}

func condition(t *testing.T, text string) *cond.CondCfg {
	t.Helper()
	var parsed cond.CondCfg
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("condition %s: %v", text, err)
	}
	return &parsed
}

func filteredRelation(t *testing.T, id, source, target string) *interfaces.RelationType {
	t.Helper()
	relation := relationType(id, id)
	relation.Type = interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN
	relation.SourceObjectTypeID = "probe_channel"
	relation.TargetObjectTypeID = "probe_order"
	relation.MappingRules = &interfaces.FilteredCrossJoinMapping{
		SourceCondition: condition(t, source),
		TargetCondition: condition(t, target),
	}
	return relation
}

const (
	offline   = `{"field":"c_kind","operation":"==","value_from":"const","value":"offline"}`
	refunding = `{"field":"o_state","operation":"==","value_from":"const","value":"refunding"}`
)

func filteredSchema(t *testing.T, relations ...*interfaces.RelationType) *Schema {
	t.Helper()
	if len(relations) == 0 {
		relations = []*interfaces.RelationType{filteredRelation(t, "probe_fcj", offline, refunding)}
	}
	return testSchema(t, &fakeSchemaSource{objectTypes: filteredObjectTypes(), relationTypes: relations})
}

func compileFiltered(t *testing.T, schema *Schema, query string, options CompileOptions) (*Plan, error) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		return nil, err
	}
	return Compile(analyzed, schema, options)
}

func mustGenerateFiltered(t *testing.T, schema *Schema, query string) string {
	t.Helper()
	plan, err := compileFiltered(t, schema, query, CompileOptions{})
	if err != nil {
		t.Fatalf("Compile(%q): %v", query, err)
	}
	if !plan.SQLOnly {
		t.Fatalf("a plan over a filtered_cross_join relation must stay on the SQL path")
	}
	statement, err := Generate(plan, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate(%q): %v", query, err)
	}
	return statement
}

const filteredLink = "JOIN {{.res_order}} t1 ON (t0.`channel_type` = 'offline' AND t1.`order_status` = 'refunding')"

func TestFilteredCrossJoinLinksEveryPairThatPasses(t *testing.T) {
	schema := filteredSchema(t)
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "forward",
			query: "MATCH (c:probe_channel)-[:probe_fcj]->(o:probe_order) RETURN c.c_key, o.o_key",
			want:  "SELECT t0.`channel_id` AS `c.c_key`, t1.`order_id` AS `o.o_key` FROM {{.res_channel}} t0 " + filteredLink,
		},
		{
			name:  "written from the target",
			query: "MATCH (o:probe_order)<-[:probe_fcj]-(c:probe_channel) RETURN count(*) AS n",
			want: "SELECT COUNT(*) AS `n` FROM {{.res_order}} t0 " +
				"JOIN {{.res_channel}} t1 ON (t1.`channel_type` = 'offline' AND t0.`order_status` = 'refunding')",
		},
		{
			name:  "undirected between different object types",
			query: "MATCH (c:probe_channel)-[:probe_fcj]-(o:probe_order) RETURN count(*) AS n",
			want:  "SELECT COUNT(*) AS `n` FROM {{.res_channel}} t0 " + filteredLink,
		},
		{
			name:  "with a condition of the query's own",
			query: "MATCH (c:probe_channel)-[:probe_fcj]->(o:probe_order) WHERE o.o_amount > 100 RETURN o.o_key",
			want: "SELECT t1.`order_id` AS `o.o_key` FROM {{.res_channel}} t0 " + filteredLink +
				" WHERE t1.`actual_amount` > 100",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustGenerateFiltered(t, schema, tc.query); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// Each operation becomes the predicate vega-backend writes for it on a SQL
// source, so Cypher links exactly the rows the subgraph query links.
func TestFilteredCrossJoinTranslatesEachOperation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		condition string
		want      string
	}{
		{"equal", `{"field":"c_kind","operation":"==","value":"offline"}`, "t0.`channel_type` = 'offline'"},
		{"equal, filter spelling", `{"field":"c_kind","operation":"=","value":"offline"}`, "t0.`channel_type` = 'offline'"},
		{"not equal", `{"field":"c_kind","operation":"!=","value":"web"}`, "t0.`channel_type` <> 'web'"},
		{"greater", `{"field":"c_key","operation":">","value":3}`, "t0.`channel_id` > 3"},
		{"at least", `{"field":"c_key","operation":">=","value":3}`, "t0.`channel_id` >= 3"},
		{"less", `{"field":"c_key","operation":"<","value":3}`, "t0.`channel_id` < 3"},
		{"at most", `{"field":"c_key","operation":"<=","value":3.5}`, "t0.`channel_id` <= 3.5"},
		{"in", `{"field":"c_kind","operation":"in","value":["web","app"]}`, "t0.`channel_type` IN ('web', 'app')"},
		{"not in", `{"field":"c_key","operation":"not_in","value":[1,2]}`, "t0.`channel_id` NOT IN (1, 2)"},
		{"in nothing", `{"field":"c_key","operation":"in","value":[]}`, "1 = 0"},
		{"like is a literal substring", `{"field":"c_label","operation":"like","value":"50_off"}`,
			"t0.`channel_name` LIKE '%50!_off%' ESCAPE '!'"},
		{"like with escaped percent", `{"field":"c_label","operation":"like","value":"50\\%"}`,
			"t0.`channel_name` LIKE '%50!%%' ESCAPE '!'"},
		{"not like", `{"field":"c_label","operation":"not_like","value":"test"}`,
			"NOT t0.`channel_name` LIKE '%test%' ESCAPE '!'"},
		{"prefix", `{"field":"c_label","operation":"prefix","value":"North"}`,
			"t0.`channel_name` LIKE 'North%' ESCAPE '!'"},
		{"not prefix", `{"field":"c_label","operation":"not_prefix","value":"North"}`,
			"NOT t0.`channel_name` LIKE 'North%' ESCAPE '!'"},
		{"range is inclusive", `{"field":"c_key","operation":"range","value":[2,5]}`,
			"t0.`channel_id` >= 2 AND t0.`channel_id` <= 5"},
		{"between is inclusive", `{"field":"c_key","operation":"between","value":[2,5]}`,
			"t0.`channel_id` >= 2 AND t0.`channel_id` <= 5"},
		{"out of range", `{"field":"c_key","operation":"out_range","value":[2,5]}`,
			"(t0.`channel_id` < 2 OR t0.`channel_id` > 5)"},
		{"null", `{"field":"c_label","operation":"null"}`, "t0.`channel_name` IS NULL"},
		{"not exist", `{"field":"c_label","operation":"not_exist"}`, "t0.`channel_name` IS NULL"},
		{"not null", `{"field":"c_label","operation":"not_null"}`, "t0.`channel_name` IS NOT NULL"},
		{"exist", `{"field":"c_label","operation":"exist"}`, "t0.`channel_name` IS NOT NULL"},
		{"empty", `{"field":"c_label","operation":"empty"}`, "t0.`channel_name` = ''"},
		{"not empty", `{"field":"c_label","operation":"not_empty"}`, "t0.`channel_name` <> ''"},
		{"and and or", `{"operation":"and","sub_conditions":[` +
			`{"field":"c_kind","operation":"==","value":"offline"},` +
			`{"operation":"or","sub_conditions":[{"field":"c_key","operation":"<","value":3},{"field":"c_label","operation":"null"}]}]}`,
			"t0.`channel_type` = 'offline' AND (t0.`channel_id` < 3 OR t0.`channel_name` IS NULL)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := filteredSchema(t, filteredRelation(t, "probe_fcj", tc.condition, refunding))
			got := mustGenerateFiltered(t, schema, "MATCH (c:probe_channel)-[:probe_fcj]->(o:probe_order) RETURN count(*) AS n")
			prefix := "SELECT COUNT(*) AS `n` FROM {{.res_channel}} t0 JOIN {{.res_order}} t1 ON ("
			suffix := " AND t1.`order_status` = 'refunding')"
			if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) ||
				strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix) != tc.want {
				t.Fatalf("got  %s\nwant %s%s%s", got, prefix, tc.want, suffix)
			}
		})
	}
}

func TestFilteredCrossJoinRefusals(t *testing.T) {
	for _, tc := range []struct {
		name      string
		condition string
		want      string
	}{
		{"full-text match", `{"field":"c_label","operation":"match","value":"north"}`, `"match" condition on "c_label" is not supported`},
		{"regex", `{"field":"c_label","operation":"regex","value":"^n"}`, `"regex" condition on "c_label" is not supported`},
		{"bare percent in like", `{"field":"c_label","operation":"like","value":"50%"}`, "wildcard '%' is not supported"},
		{"comma-separated contain", `{"field":"c_label","operation":"contain","value":["a"]}`, `"contain" condition on "c_label" is not supported`},
		{"date property", `{"field":"c_opened","operation":">","value":"2026-01-01"}`, "date or time property"},
		{"value from another field", `{"field":"c_kind","operation":"==","value_from":"field","value":"c_label"}`, "only constant values"},
		{"value from the user", `{"field":"c_kind","operation":"==","value_from":"user","value":"id"}`, "only constant values"},
		{"value of the wrong type", `{"field":"c_key","operation":"==","value":"north"}`, "not a number"},
		{"string match on a number", `{"field":"c_key","operation":"like","value":"1"}`, "needs a string property"},
		{"range with one bound", `{"field":"c_key","operation":"range","value":[1]}`, "needs two values"},
		{"unknown property", `{"field":"c_missing","operation":"==","value":"x"}`, "has no property"},
		{"no property", `{"operation":"==","value":"x"}`, "names no property"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := filteredSchema(t, filteredRelation(t, "probe_fcj", tc.condition, refunding))
			_, err := compileFiltered(t, schema, "MATCH (c:probe_channel)-[:probe_fcj]->(o:probe_order) RETURN count(*) AS n", CompileOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}

	self := relationType("probe_peer", "probe_peer")
	self.Type = interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN
	self.SourceObjectTypeID, self.TargetObjectTypeID = "probe_channel", "probe_channel"
	self.MappingRules = &interfaces.FilteredCrossJoinMapping{
		SourceCondition: condition(t, offline), TargetCondition: condition(t, `{"field":"c_kind","operation":"==","value":"web"}`),
	}
	_, err := compileFiltered(t, filteredSchema(t, self), "MATCH (a:probe_channel)-[:probe_peer]-(b:probe_channel) RETURN count(*) AS n", CompileOptions{})
	if err == nil || !strings.Contains(err.Error(), "write its direction") {
		t.Fatalf("err = %v, want the undirected self relation refused", err)
	}
}

// An anchor that has no partner keeps its row with nulls; the source
// condition decides whether it has partners at all, so it belongs to the join.
func TestFilteredCrossJoinOptionalMatch(t *testing.T) {
	got := mustGenerateFiltered(t, filteredSchema(t),
		"MATCH (c:probe_channel) OPTIONAL MATCH (c)-[:probe_fcj]->(o:probe_order) RETURN c.c_key, o.o_key")
	want := "SELECT t0.`channel_id` AS `c.c_key`, t1.`order_id` AS `o.o_key` FROM {{.res_channel}} t0 " +
		"LEFT " + filteredLink
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestFilteredCrossJoinClosingACycleKeepsItsConditions(t *testing.T) {
	self := relationType("probe_peer", "probe_peer")
	self.Type = interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN
	self.SourceObjectTypeID, self.TargetObjectTypeID = "probe_channel", "probe_channel"
	self.MappingRules = &interfaces.FilteredCrossJoinMapping{
		SourceCondition: condition(t, offline), TargetCondition: condition(t, `{"field":"c_key","operation":">","value":1}`),
	}
	got := mustGenerateFiltered(t, filteredSchema(t, self), "MATCH (a:probe_channel)-[:probe_peer]->(a) RETURN a.c_key")
	want := "SELECT t0.`channel_id` AS `a.c_key` FROM {{.res_channel}} t0 " +
		"WHERE (t0.`channel_type` = 'offline' AND t0.`channel_id` > 1)"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestFilteredCrossJoinRelationshipValue(t *testing.T) {
	plan, err := compileFiltered(t, filteredSchema(t), "MATCH (c:probe_channel)-[r:probe_fcj]->(o:probe_order) RETURN c, r, o LIMIT 25",
		CompileOptions{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	read, err := planGraphRead(plan, DialectMySQL, interfaces.CYPHER_DEFAULT_LIMIT)
	if err != nil {
		t.Fatalf("planGraphRead: %v", err)
	}
	entry := map[string]any{}
	for _, column := range read.columns {
		switch column.table {
		case 0:
			entry[column.alias] = 10
		case 1:
			entry[column.alias] = 10387
		}
	}
	rows, _, err := assembleGraphRows(plan, read, []map[string]any{entry})
	if err != nil {
		t.Fatalf("assembleGraphRows: %v", err)
	}
	relation, ok := rows[0]["r"].(interfaces.CypherRelationValue)
	if !ok {
		t.Fatalf("r = %#v, want a relation value", rows[0]["r"])
	}
	if relation.RelationTypeID != "probe_fcj" || relation.Source.ObjectTypeID != "probe_channel" ||
		relation.Target.ObjectTypeID != "probe_order" {
		t.Fatalf("relation = %+v", relation)
	}
	encoded, _ := json.Marshal(relation)
	if !strings.Contains(string(encoded), `"basis":[]`) {
		t.Fatalf("a relation with no key pairs has an empty basis: %s", encoded)
	}
	if !strings.HasPrefix(relation.BindingDigest, "sha256:") {
		t.Fatalf("binding digest = %q", relation.BindingDigest)
	}
}

func filteredService(permission *stubPermission, vega *recordingVega) *cypherQueryService {
	return &cypherQueryService{
		ps: permission,
		schema: &fakeSchemaSource{objectTypes: filteredObjectTypes(), relationTypes: []*interfaces.RelationType{
			{
				RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
					RTID: "probe_fcj", RTName: "probe_fcj", Type: interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN,
					SourceObjectTypeID: "probe_channel", TargetObjectTypeID: "probe_order",
					MappingRules: &interfaces.FilteredCrossJoinMapping{
						SourceCondition: &cond.CondCfg{Field: "c_kind", Operation: "==", ValueOptCfg: cond.ValueOptCfg{ValueFrom: "const", Value: "offline"}},
						TargetCondition: &cond.CondCfg{Field: "o_state", Operation: "==", ValueOptCfg: cond.ValueOptCfg{ValueFrom: "const", Value: "refunding"}},
					},
				},
			},
		}},
		vba: vega,
	}
}

// Both ends are asked for their row policy, and both apply.
func TestFilteredCrossJoinRowFiltersApplyToBothEnds(t *testing.T) {
	app, paid := "app", true
	permission := &stubPermission{rowFilters: map[string]interfaces.RowFilterPredicate{
		"kn_1/probe_channel": {Kind: "in", Property: "c_kind", Values: []interfaces.RowFilterValue{{Type: "string", String: &app}}},
		"kn_1/probe_order":   {Kind: "in", Property: "o_paid", Values: []interfaces.RowFilterValue{{Type: "boolean", Boolean: &paid}}},
	}}
	vega := &recordingVega{}
	_, err := filteredService(permission, vega).Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: "main", Query: "MATCH (c:probe_channel)-[:probe_fcj]->(o:probe_order) RETURN count(*) AS n",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got, want := permission.rowFilterCalls[0], []string{"kn_1/probe_channel", "kn_1/probe_order"}; !slices.Equal(got, want) {
		t.Fatalf("row-filter refs = %v, want %v", got, want)
	}
	want := "SELECT COUNT(*) AS `n` FROM {{.res_channel}} t0 " + filteredLink +
		" WHERE t0.`channel_type` IN ('app') AND t1.`is_paid` IN (TRUE) LIMIT 1000"
	if got := vega.request.Query; got != want {
		t.Fatalf("statement = %s\nwant      = %s", got, want)
	}
}

func TestFilteredCrossJoinDeniedRelationIsUnknown(t *testing.T) {
	vega := &recordingVega{}
	permission := &stubPermission{denied: map[string]bool{interfaces.KNChildResourceID("kn_1", "probe_fcj"): true}}
	_, err := filteredService(permission, vega).Query(callerContext(), interfaces.CypherQuery{
		KNID: "kn_1", Branch: "main", Query: "MATCH (c:probe_channel)-[:probe_fcj]->(o:probe_order) RETURN count(*) AS n",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown relationship type") {
		t.Fatalf("err = %v, want the relation to read as unknown", err)
	}
	if vega.request != nil {
		t.Fatal("a denied relation still reached vega-backend")
	}
}

func TestFilteredCrossJoinProxyBindingsCoverBothEnds(t *testing.T) {
	plan, err := compileFiltered(t, filteredSchema(t), "MATCH (c:probe_channel)-[:probe_fcj]->(o:probe_order) RETURN count(*) AS n",
		CompileOptions{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var got []string
	for _, binding := range proxyBindings(plan) {
		got = append(got, binding.ChildType+":"+binding.ChildID+"->"+binding.TargetID)
	}
	want := []string{
		interfaces.MODULE_TYPE_OBJECT_TYPE + ":probe_channel->res_channel",
		interfaces.MODULE_TYPE_OBJECT_TYPE + ":probe_order->res_order",
		interfaces.MODULE_TYPE_RELATION_TYPE + ":probe_fcj->res_channel",
		interfaces.MODULE_TYPE_RELATION_TYPE + ":probe_fcj->res_order",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("bindings = %v\nwant       %v", got, want)
	}
}
